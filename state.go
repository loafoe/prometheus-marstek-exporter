package main

import (
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/loafoe/go-marstek/pkg/marstek"
)

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

type StateManager struct {
	clients     []*marstek.Client
	deviceInfos []DeviceIdentifier
	instanceID  int
	cyclePeriod time.Duration

	mu     sync.RWMutex
	states map[string]*DeviceState
	stats  map[string]*DeviceStats

	stopCh chan struct{}
	wg     sync.WaitGroup
}

func NewStateManager(clients []*marstek.Client, deviceInfos []DeviceIdentifier, instanceID int, cyclePeriod time.Duration) *StateManager {
	sm := &StateManager{
		clients:     clients,
		deviceInfos: deviceInfos,
		instanceID:  instanceID,
		cyclePeriod: cyclePeriod,
		states:      make(map[string]*DeviceState),
		stats:       make(map[string]*DeviceStats),
		stopCh:      make(chan struct{}),
	}

	for _, info := range deviceInfos {
		sm.states[info.IP] = NewDeviceState()
		sm.stats[info.IP] = &DeviceStats{}
	}

	return sm
}

func (sm *StateManager) Start() {
	for i := range sm.clients {
		sm.wg.Add(1)
		go sm.deviceLoop(i)
	}
}

func (sm *StateManager) Stop() {
	close(sm.stopCh)
	sm.wg.Wait()
}

type apiCall struct {
	name   string
	stats  *APIStats
	update func(client *marstek.Client, state *DeviceState) error
}

func (sm *StateManager) deviceLoop(idx int) {
	defer sm.wg.Done()

	client := sm.clients[idx]
	info := sm.deviceInfos[idx]
	state := sm.states[info.IP]
	stats := sm.stats[info.IP]

	calls := []apiCall{
		{
			name:  "ESStatus",
			stats: &stats.ESStatus,
			update: func(c *marstek.Client, s *DeviceState) error {
				es, err := c.GetESStatus(sm.instanceID)
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
			name:  "ESMode",
			stats: &stats.ESMode,
			update: func(c *marstek.Client, s *DeviceState) error {
				mode, err := c.GetESMode(sm.instanceID)
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
			name:  "Battery",
			stats: &stats.Battery,
			update: func(c *marstek.Client, s *DeviceState) error {
				bat, err := c.GetBatteryStatus(sm.instanceID)
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
			name:  "PV",
			stats: &stats.PV,
			update: func(c *marstek.Client, s *DeviceState) error {
				pv, err := c.GetPVStatus(sm.instanceID)
				if err != nil {
					return err
				}
				s.mu.Lock()
				s.PV = pv
				s.LastUpdate["PV"] = time.Now()
				s.mu.Unlock()
				return nil
			},
		},
		{
			name:  "EM",
			stats: &stats.EM,
			update: func(c *marstek.Client, s *DeviceState) error {
				em, err := c.GetEMStatus(sm.instanceID)
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

	callInterval := sm.cyclePeriod / time.Duration(len(calls))
	ticker := time.NewTicker(callInterval)
	defer ticker.Stop()

	callIdx := 0

	sm.executeCall(calls[callIdx], client, state, info.IP)
	callIdx = (callIdx + 1) % len(calls)

	for {
		select {
		case <-ticker.C:
			sm.executeCall(calls[callIdx], client, state, info.IP)
			callIdx = (callIdx + 1) % len(calls)
		case <-sm.stopCh:
			return
		}
	}
}

func (sm *StateManager) executeCall(call apiCall, client *marstek.Client, state *DeviceState, deviceIP string) {
	call.stats.Calls.Add(1)

	err := call.update(client, state)
	if err != nil {
		if isRetryable(err) {
			time.Sleep(2 * time.Second)
			err = call.update(client, state)
			if err == nil {
				slog.Debug("API call succeeded on retry", "device", deviceIP, "call", call.name)
				return
			}
		}
		call.stats.Errors.Add(1)
		if isTimeout(err) {
			call.stats.Timeouts.Add(1)
		}
		slog.Warn("API call failed", "device", deviceIP, "call", call.name, "error", err)
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

func (sm *StateManager) GetState(deviceIP string) *DeviceState {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	return sm.states[deviceIP]
}

func (sm *StateManager) GetStats(deviceIP string) *DeviceStats {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	return sm.stats[deviceIP]
}

func (sm *StateManager) GetDeviceInfos() []DeviceIdentifier {
	return sm.deviceInfos
}
