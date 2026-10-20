package main

import (
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/loafoe/go-marstek/pkg/marstek"
)

// DeviceIdentifier describes a device on the LAN.
type DeviceIdentifier struct {
	IP      string
	Device  string
	WifiMAC string
}

type DeviceState struct {
	mu         sync.RWMutex
	LastUpdate map[string]time.Time

	ESStatus *marstek.ESStatus
	ESMode   *marstek.ESModeStatus
	Battery  *marstek.BatteryStatus
	PV       *marstek.PVStatus
	EM       *marstek.EMStatus
}

func NewDeviceState() *DeviceState {
	return &DeviceState{
		LastUpdate: make(map[string]time.Time),
	}
}

type APIStats struct {
	Calls    atomic.Uint64
	Errors   atomic.Uint64
	Timeouts atomic.Uint64
}

type DeviceStats struct {
	ESStatus APIStats
	ESMode   APIStats
	Battery  APIStats
	PV       APIStats
	EM       APIStats
}

type EndpointConfig struct {
	Name     string
	Interval time.Duration
}

var DefaultEndpointIntervals = []EndpointConfig{
	{"ESStatus", 1 * time.Minute}, // SOC, power values - high frequency
	{"PV", 1 * time.Minute},       // PV power/voltage - high frequency
	{"ESMode", 5 * time.Minute},   // Operating mode, phase power - medium frequency
	{"Battery", 5 * time.Minute},  // Temperature, capacity - medium frequency
	{"EM", 15 * time.Minute},      // CT state, energy counters - low frequency
}

// StateManagerConfig holds the runtime configuration of a StateManager.
type StateManagerConfig struct {
	InstanceID int
	EnablePV   bool

	// AutoDiscover enables LAN discovery. It is used to populate the device
	// list at startup and to recover devices whose address went stale.
	AutoDiscover bool

	DevicePort      int
	RequestTimeout  time.Duration
	DiscoverTimeout time.Duration

	// MaxConsecutiveTimeouts is the number of consecutive failed calls
	// (timeouts and unreachable hosts) on a device address that is tolerated
	// before the address is considered stale. When it is exceeded the device
	// is looked up on the LAN again, because the device may have been assigned
	// a new IP address. Values of zero or less disable the check.
	MaxConsecutiveTimeouts int

	// ExitOnUnreachable makes the exporter exit when a device is unreachable
	// and cannot be recovered by rediscovering it on the LAN.
	ExitOnUnreachable bool
}

// deviceRuntime holds the mutable runtime state of a single device.
type deviceRuntime struct {
	mu sync.RWMutex

	ip     string
	info   DeviceIdentifier
	client *marstek.Client
	state  *DeviceState
	stats  *DeviceStats

	// consecutiveFailures counts the failed calls since the last successful
	// one, a device that stops answering may have been assigned a new address.
	consecutiveFailures int

	// reschedule asks the device loop to poll all endpoints immediately, it is
	// set when the device address changed.
	reschedule bool
}

func (d *deviceRuntime) address() string {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.ip
}

func (d *deviceRuntime) identifier() DeviceIdentifier {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.info
}

func (d *deviceRuntime) connection() (*marstek.Client, *DeviceState) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.client, d.state
}

func (d *deviceRuntime) takeReschedule() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	reschedule := d.reschedule
	d.reschedule = false
	return reschedule
}

type StateManager struct {
	config  StateManagerConfig
	devices []*deviceRuntime

	// recoveryMu serializes the rediscovery of devices, discovery broadcasts
	// on the LAN and must not run for multiple devices at once.
	recoveryMu sync.Mutex

	stopCh  chan struct{}
	wg      sync.WaitGroup
	fatalCh chan error
}

func NewStateManager(clients []*marstek.Client, deviceInfos []DeviceIdentifier, config StateManagerConfig) *StateManager {
	sm := &StateManager{
		config:  config,
		devices: make([]*deviceRuntime, 0, len(clients)),
		stopCh:  make(chan struct{}),
		fatalCh: make(chan error, 1),
	}

	for i, client := range clients {
		info := deviceInfos[i]
		sm.devices = append(sm.devices, &deviceRuntime{
			ip:     info.IP,
			info:   info,
			client: client,
			state:  NewDeviceState(),
			stats:  &DeviceStats{},
		})
	}

	return sm
}

func (sm *StateManager) Start() {
	for _, d := range sm.devices {
		sm.wg.Add(1)
		go sm.deviceLoop(d)
	}
}

func (sm *StateManager) Stop() {
	close(sm.stopCh)
	sm.wg.Wait()
}

// FatalErrors reports errors the exporter cannot recover from, the process
// should exit once one is received.
func (sm *StateManager) FatalErrors() <-chan error {
	return sm.fatalCh
}

// DeviceSnapshot is a view of a device used by the collector.
type DeviceSnapshot struct {
	Info  DeviceIdentifier
	State *DeviceState
	Stats *DeviceStats
}

// Snapshots returns the current view of all known devices.
func (sm *StateManager) Snapshots() []DeviceSnapshot {
	snapshots := make([]DeviceSnapshot, 0, len(sm.devices))
	for _, d := range sm.devices {
		snapshots = append(snapshots, DeviceSnapshot{
			Info:  d.identifier(),
			State: d.state,
			Stats: d.stats,
		})
	}
	return snapshots
}

type apiCall struct {
	name     string
	interval time.Duration
	lastCall time.Time
	stats    *APIStats
	update   func(client *marstek.Client, state *DeviceState) error
}

func (sm *StateManager) deviceLoop(d *deviceRuntime) {
	defer sm.wg.Done()

	stats := d.stats

	intervalFor := func(name string) time.Duration {
		for _, cfg := range DefaultEndpointIntervals {
			if cfg.Name == name {
				return cfg.Interval
			}
		}
		return 1 * time.Minute
	}

	calls := []apiCall{
		{
			name:     "ESStatus",
			interval: intervalFor("ESStatus"),
			stats:    &stats.ESStatus,
			update: func(c *marstek.Client, s *DeviceState) error {
				es, err := c.GetESStatus(sm.config.InstanceID)
				if err != nil {
					return err
				}
				s.mu.Lock()
				s.ESStatus = es
				s.LastUpdate["ESStatus"] = time.Now()
				s.mu.Unlock()
				return nil
			},
		},
		{
			name:     "ESMode",
			interval: intervalFor("ESMode"),
			stats:    &stats.ESMode,
			update: func(c *marstek.Client, s *DeviceState) error {
				mode, err := c.GetESMode(sm.config.InstanceID)
				if err != nil {
					return err
				}
				s.mu.Lock()
				s.ESMode = mode
				s.LastUpdate["ESMode"] = time.Now()
				s.mu.Unlock()
				return nil
			},
		},
		{
			name:     "Battery",
			interval: intervalFor("Battery"),
			stats:    &stats.Battery,
			update: func(c *marstek.Client, s *DeviceState) error {
				bat, err := c.GetBatteryStatus(sm.config.InstanceID)
				if err != nil {
					return err
				}
				s.mu.Lock()
				s.Battery = bat
				s.LastUpdate["Battery"] = time.Now()
				s.mu.Unlock()
				return nil
			},
		},
		{
			name:     "EM",
			interval: intervalFor("EM"),
			stats:    &stats.EM,
			update: func(c *marstek.Client, s *DeviceState) error {
				em, err := c.GetEMStatus(sm.config.InstanceID)
				if err != nil {
					return err
				}
				s.mu.Lock()
				s.EM = em
				s.LastUpdate["EM"] = time.Now()
				s.mu.Unlock()
				return nil
			},
		},
	}

	if sm.config.EnablePV {
		calls = append(calls, apiCall{
			name:     "PV",
			interval: intervalFor("PV"),
			stats:    &stats.PV,
			update: func(c *marstek.Client, s *DeviceState) error {
				pv, err := c.GetPVStatus(sm.config.InstanceID)
				if err != nil {
					return err
				}
				s.mu.Lock()
				s.PV = pv
				s.LastUpdate["PV"] = time.Now()
				s.mu.Unlock()
				return nil
			},
		})
	}

	for i := range calls {
		sm.runCall(&calls[i], d)
		calls[i].lastCall = time.Now()
		time.Sleep(2 * time.Second)
	}

	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			now := time.Now()
			if d.takeReschedule() {
				// The device moved to a new address, poll everything right away.
				for i := range calls {
					calls[i].lastCall = time.Time{}
				}
			}
			for i := range calls {
				if now.Sub(calls[i].lastCall) >= calls[i].interval {
					sm.runCall(&calls[i], d)
					calls[i].lastCall = now
				}
			}
		case <-sm.stopCh:
			return
		}
	}
}

// runCall performs an API call and keeps track of the consecutive failures of
// the device, a device that stops answering may have been assigned a new IP
// address.
func (sm *StateManager) runCall(call *apiCall, d *deviceRuntime) {
	err := sm.executeCall(call, d)
	switch {
	case err == nil:
		sm.noteReachable(d)
	case isUnreachable(err):
		sm.noteFailure(d)
	}
}

func (sm *StateManager) executeCall(call *apiCall, d *deviceRuntime) error {
	call.stats.Calls.Add(1)

	client, state := d.connection()

	err := call.update(client, state)
	if err != nil {
		if isRetryable(err) {
			time.Sleep(2 * time.Second)
			err = call.update(client, state)
			if err == nil {
				slog.Debug("API call succeeded on retry", "device", d.address(), "call", call.name)
				return nil
			}
		}
		call.stats.Errors.Add(1)
		if isTimeout(err) {
			call.stats.Timeouts.Add(1)
		}
		slog.Warn("API call failed", "device", d.address(), "call", call.name, "error", err)
	}
	return err
}

// noteReachable resets the failure streak of a device that answered again.
func (sm *StateManager) noteReachable(d *deviceRuntime) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.consecutiveFailures > 0 {
		slog.Info("device is answering again, resetting the failure streak",
			"device", d.ip, "failures", d.consecutiveFailures)
		d.consecutiveFailures = 0
	}
}

// noteFailure counts a failed call and looks the device up on the LAN once the
// configured threshold is exceeded, its IP address may have changed.
func (sm *StateManager) noteFailure(d *deviceRuntime) {
	d.mu.Lock()
	d.consecutiveFailures++
	streak := d.consecutiveFailures
	d.mu.Unlock()

	if sm.config.MaxConsecutiveTimeouts <= 0 || streak <= sm.config.MaxConsecutiveTimeouts {
		return
	}

	slog.Warn("consecutive failures exceeded the threshold, the device address may have changed",
		"device", d.address(),
		"consecutive_failures", streak,
		"threshold", sm.config.MaxConsecutiveTimeouts)

	sm.recoverDevice(d)
}

// recoverDevice looks the device up on the LAN again. When the device is found
// at a different address the exporter switches over to it, otherwise the device
// is reported as unreachable.
func (sm *StateManager) recoverDevice(d *deviceRuntime) {
	sm.recoveryMu.Lock()
	defer sm.recoveryMu.Unlock()

	staleIP := d.address()

	if !sm.config.AutoDiscover {
		sm.handleUnreachable(d, fmt.Sprintf(
			"discovery is disabled, the device at %s may have been assigned a new address", staleIP))
		return
	}

	slog.Info("discovering devices on LAN after repeated timeouts", "device", staleIP)
	devices, err := marstek.DiscoverDevices(sm.config.DevicePort, sm.config.DiscoverTimeout)
	if err != nil {
		sm.handleUnreachable(d, fmt.Sprintf("discovery failed: %v", err))
		return
	}

	match, byMAC := matchDiscoveredDevice(d, devices)
	if match == nil {
		sm.handleUnreachable(d, fmt.Sprintf(
			"the device does not answer on the LAN any more, the address %s may have changed", staleIP))
		return
	}

	if match.IP == "" {
		sm.handleUnreachable(d, fmt.Sprintf(
			"the discovered device did not report an address, the address %s may have changed", staleIP))
		return
	}

	if match.IP == staleIP {
		sm.handleUnreachable(d, fmt.Sprintf(
			"the device answers discovery at %s but does not answer API calls", staleIP))
		return
	}

	if !byMAC {
		slog.Warn("the identity of the device is unknown, assuming the only device on the LAN is the same one",
			"device", staleIP, "new_ip", match.IP)
	}

	sm.switchDevice(d, *match)
}

// matchDiscoveredDevice finds the device that belongs to a stale address. The
// WiFi MAC is preferred because it survives an address change. It is only used
// when the identity of the device is unknown, otherwise only a device with the
// same MAC can be the same device.
func matchDiscoveredDevice(d *deviceRuntime, devices []marstek.DeviceInfo) (*marstek.DeviceInfo, bool) {
	current := d.identifier()

	if current.WifiMAC != "" {
		for i := range devices {
			if devices[i].WifiMAC == current.WifiMAC {
				return &devices[i], true
			}
		}
		return nil, false
	}

	if len(devices) == 1 {
		return &devices[0], false
	}

	return nil, false
}

// switchDevice moves a device to a new address and rebuilds its client.
func (sm *StateManager) switchDevice(d *deviceRuntime, dev marstek.DeviceInfo) {
	client := marstek.NewClient(dev.IP,
		marstek.WithPort(sm.config.DevicePort),
		marstek.WithTimeout(sm.config.RequestTimeout),
	)

	// Check that the device answers at its new address before it is used.
	probe := marstek.NewClient(dev.IP,
		marstek.WithPort(sm.config.DevicePort),
		marstek.WithTimeout(sm.probeTimeout()),
		marstek.WithRetries(1),
	)
	if _, err := probe.GetDevice("0"); err != nil {
		sm.handleUnreachable(d, fmt.Sprintf(
			"the device does not answer API calls at its new address %s: %v", dev.IP, err))
		return
	}

	d.mu.Lock()
	oldIP := d.ip
	d.ip = dev.IP
	d.info = DeviceIdentifier{
		IP:      dev.IP,
		Device:  dev.Device,
		WifiMAC: dev.WifiMAC,
	}
	d.client = client
	d.consecutiveFailures = 0
	d.reschedule = true
	d.mu.Unlock()

	slog.Warn("device address changed, switched to the rediscovered address",
		"old_ip", oldIP,
		"new_ip", dev.IP,
		"device", dev.Device,
		"mac", dev.WifiMAC)
}

// probeTimeout is the timeout used to check that a device answers at an
// address, the device just answered a discovery broadcast so it should reply
// quickly.
func (sm *StateManager) probeTimeout() time.Duration {
	timeout := sm.config.RequestTimeout
	if timeout <= 0 || timeout > 5*time.Second {
		timeout = 5 * time.Second
	}
	return timeout
}

// handleUnreachable reports a device that cannot be reached. The exporter
// exits unless it is configured to keep running.
func (sm *StateManager) handleUnreachable(d *deviceRuntime, reason string) {
	ip := d.address()

	if sm.config.ExitOnUnreachable {
		slog.Error("device is unreachable, exiting", "device", ip, "reason", reason)
		sm.fail(fmt.Errorf("device %s is unreachable: %s", ip, reason))
		return
	}

	slog.Error("device is unreachable", "device", ip, "reason", reason)

	d.mu.Lock()
	d.consecutiveFailures = 0
	d.mu.Unlock()
}

// fail reports an unrecoverable error, the exporter exits once it is received.
func (sm *StateManager) fail(err error) {
	select {
	case sm.fatalCh <- err:
	default:
	}
}

func isRetryable(err error) bool {
	return isParseError(err) || isTimeout(err)
}

func isTimeout(err error) bool {
	if err == nil {
		return false
	}
	errStr := err.Error()
	return contains(errStr, "timeout") || contains(errStr, "i/o timeout")
}

// isUnreachable reports whether the error means that nothing answers at the
// address of the device, this is the case for timeouts and for hosts that do
// not exist any more.
func isUnreachable(err error) bool {
	if err == nil {
		return false
	}
	if isTimeout(err) {
		return true
	}

	errStr := strings.ToLower(err.Error())
	for _, needle := range []string{
		"connection refused",
		"network is unreachable",
		"host is unreachable",
		"no route to host",
		"no such host",
	} {
		if contains(errStr, needle) {
			return true
		}
	}

	return false
}

func isParseError(err error) bool {
	if err == nil {
		return false
	}
	return contains(err.Error(), "Parse error")
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsAt(s, substr))
}

func containsAt(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
