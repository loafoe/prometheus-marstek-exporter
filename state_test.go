package main

import (
	"encoding/json"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/loafoe/go-marstek/pkg/marstek"
)

func newTestManager(t *testing.T, config StateManagerConfig, devices []DeviceIdentifier) *StateManager {
	t.Helper()

	clients := make([]*marstek.Client, 0, len(devices))
	for _, dev := range devices {
		clients = append(clients, marstek.NewClient(dev.IP,
			marstek.WithPort(config.DevicePort),
			marstek.WithTimeout(config.RequestTimeout),
		))
	}

	return NewStateManager(clients, devices, config)
}

// startFakeDevice starts a UDP server that answers like a Marstek device and
// returns its address.
func startFakeDevice(t *testing.T, wifiMAC string) (string, int) {
	t.Helper()

	conn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 0})
	if err != nil {
		t.Fatalf("could not start the fake device: %v", err)
	}
	t.Cleanup(func() { conn.Close() })

	go func() {
		buf := make([]byte, 4096)
		for {
			_, addr, err := conn.ReadFromUDP(buf)
			if err != nil {
				return
			}

			result := map[string]any{
				"device":    "FakeDevice",
				"ver":       1,
				"ble_mac":   "00:11:22:33:44:55",
				"wifi_mac":  wifiMAC,
				"wifi_name": "fake-wifi",
				"ip":        addr.IP.String(),
			}
			data, err := json.Marshal(map[string]any{"id": 1, "result": result})
			if err != nil {
				return
			}
			if _, err := conn.WriteToUDP(data, addr); err != nil {
				return
			}
		}
	}()

	local := conn.LocalAddr().(*net.UDPAddr)
	return local.IP.String(), local.Port
}

func TestTimeoutStreakBelowThresholdKeepsRunning(t *testing.T) {
	sm := newTestManager(t, StateManagerConfig{
		AutoDiscover:           false,
		MaxConsecutiveTimeouts: 3,
		ExitOnUnreachable:      true,
	}, []DeviceIdentifier{{IP: "192.0.2.1"}})

	for i := 0; i < 3; i++ {
		sm.noteFailure(sm.devices[0])
	}

	select {
	case err := <-sm.FatalErrors():
		t.Fatalf("unexpected fatal error: %v", err)
	default:
	}
}

func TestTimeoutStreakAboveThresholdIsFatal(t *testing.T) {
	sm := newTestManager(t, StateManagerConfig{
		AutoDiscover:           false,
		MaxConsecutiveTimeouts: 3,
		ExitOnUnreachable:      true,
	}, []DeviceIdentifier{{IP: "192.0.2.1"}})

	for i := 0; i < 4; i++ {
		sm.noteFailure(sm.devices[0])
	}

	select {
	case err := <-sm.FatalErrors():
		if !strings.Contains(err.Error(), "192.0.2.1") {
			t.Fatalf("the fatal error does not mention the device: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("expected a fatal error for a device that stays unreachable")
	}
}

func TestUnreachableDeviceKeepsRunningWhenExitIsDisabled(t *testing.T) {
	sm := newTestManager(t, StateManagerConfig{
		AutoDiscover:           false,
		MaxConsecutiveTimeouts: 1,
		ExitOnUnreachable:      false,
	}, []DeviceIdentifier{{IP: "192.0.2.1"}})

	sm.noteFailure(sm.devices[0])
	sm.noteFailure(sm.devices[0])

	select {
	case err := <-sm.FatalErrors():
		t.Fatalf("unexpected fatal error: %v", err)
	default:
	}

	sm.devices[0].mu.RLock()
	streak := sm.devices[0].consecutiveFailures
	sm.devices[0].mu.RUnlock()
	if streak != 0 {
		t.Fatalf("expected the timeout streak to be reset, got %d", streak)
	}
}

func TestSuccessfulCallResetsTimeoutStreak(t *testing.T) {
	sm := newTestManager(t, StateManagerConfig{
		AutoDiscover:           false,
		MaxConsecutiveTimeouts: 2,
		ExitOnUnreachable:      false,
	}, []DeviceIdentifier{{IP: "192.0.2.1"}})

	device := sm.devices[0]
	sm.noteFailure(device)
	sm.noteFailure(device)
	sm.noteReachable(device)
	sm.noteFailure(device)

	device.mu.RLock()
	streak := device.consecutiveFailures
	device.mu.RUnlock()

	if streak != 1 {
		t.Fatalf("expected the timeout streak to restart, got %d", streak)
	}
}

func TestMatchDiscoveredDevice(t *testing.T) {
	tests := []struct {
		name    string
		current DeviceIdentifier
		devices []marstek.DeviceInfo
		wantIP  string
		wantMAC bool
	}{
		{
			name:    "match by mac",
			current: DeviceIdentifier{IP: "192.0.2.1", WifiMAC: "aa:bb:cc:dd:ee:ff"},
			devices: []marstek.DeviceInfo{
				{IP: "192.0.2.9", WifiMAC: "11:22:33:44:55:66"},
				{IP: "192.0.2.10", WifiMAC: "aa:bb:cc:dd:ee:ff"},
			},
			wantIP:  "192.0.2.10",
			wantMAC: true,
		},
		{
			name:    "a known device is never matched by another one",
			current: DeviceIdentifier{IP: "192.0.2.1", WifiMAC: "aa:bb:cc:dd:ee:ff"},
			devices: []marstek.DeviceInfo{{IP: "192.0.2.9", WifiMAC: "11:22:33:44:55:66"}},
			wantIP:  "",
			wantMAC: false,
		},
		{
			name:    "unknown device without a mac falls back to a single device",
			current: DeviceIdentifier{IP: "192.0.2.1"},
			devices: []marstek.DeviceInfo{{IP: "192.0.2.9", WifiMAC: "11:22:33:44:55:66"}},
			wantIP:  "192.0.2.9",
			wantMAC: false,
		},
		{
			name:    "several devices without a mac match",
			current: DeviceIdentifier{IP: "192.0.2.1", WifiMAC: "aa:bb:cc:dd:ee:ff"},
			devices: []marstek.DeviceInfo{
				{IP: "192.0.2.9", WifiMAC: "11:22:33:44:55:66"},
				{IP: "192.0.2.10", WifiMAC: "22:33:44:55:66:77"},
			},
			wantIP:  "",
			wantMAC: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sm := newTestManager(t, StateManagerConfig{}, []DeviceIdentifier{tt.current})

			match, byMAC := matchDiscoveredDevice(sm.devices[0], tt.devices)

			if tt.wantIP == "" {
				if match != nil {
					t.Fatalf("expected no match, got %s", match.IP)
				}
				return
			}

			if match == nil {
				t.Fatal("expected a match, got none")
			}
			if match.IP != tt.wantIP {
				t.Fatalf("expected the match %s, got %s", tt.wantIP, match.IP)
			}
			if byMAC != tt.wantMAC {
				t.Fatalf("expected a mac match to be %v, got %v", tt.wantMAC, byMAC)
			}
		})
	}
}

func TestSwitchDeviceMovesToTheNewAddress(t *testing.T) {
	ip, port := startFakeDevice(t, "aa:bb:cc:dd:ee:ff")

	sm := newTestManager(t, StateManagerConfig{
		DevicePort:     port,
		RequestTimeout: 2 * time.Second,
	}, []DeviceIdentifier{{IP: "192.0.2.1", WifiMAC: "aa:bb:cc:dd:ee:ff"}})

	device := sm.devices[0]
	sm.switchDevice(device, marstek.DeviceInfo{
		IP:      ip,
		Device:  "FakeDevice",
		WifiMAC: "aa:bb:cc:dd:ee:ff",
	})

	if got := device.address(); got != ip {
		t.Fatalf("expected the device to move to %s, got %s", ip, got)
	}

	info := device.identifier()
	if info.Device != "FakeDevice" {
		t.Fatalf("expected the device name to be updated, got %q", info.Device)
	}

	select {
	case err := <-sm.FatalErrors():
		t.Fatalf("unexpected fatal error: %v", err)
	default:
	}
}

func TestSwitchDeviceKeepsTheOldAddressWhenTheDeviceDoesNotAnswer(t *testing.T) {
	sm := newTestManager(t, StateManagerConfig{
		DevicePort:        1, // nothing is listening here
		RequestTimeout:    200 * time.Millisecond,
		ExitOnUnreachable: true,
	}, []DeviceIdentifier{{IP: "192.0.2.1", WifiMAC: "aa:bb:cc:dd:ee:ff"}})

	device := sm.devices[0]
	sm.switchDevice(device, marstek.DeviceInfo{
		IP:      "127.0.0.1",
		Device:  "FakeDevice",
		WifiMAC: "aa:bb:cc:dd:ee:ff",
	})

	if got := device.address(); got != "192.0.2.1" {
		t.Fatalf("expected the device to stay at 192.0.2.1, got %s", got)
	}

	select {
	case err := <-sm.FatalErrors():
		if !strings.Contains(err.Error(), "unreachable") {
			t.Fatalf("unexpected fatal error: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("expected a fatal error for a device that does not answer at its new address")
	}
}
