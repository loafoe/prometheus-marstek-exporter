package main

import (
	"log/slog"
	"sync"
	"time"

	"github.com/loafoe/go-marstek/pkg/marstek"
)

type DeviceState struct {
	LastUpdate time.Time
	ESStatus   *marstek.ESStatus
	ESMode     *marstek.ESModeStatus
	Battery    *marstek.BatteryStatus
	PV         *marstek.PVStatus
	EM         *marstek.EMStatus
}

type StateManager struct {
	clients     []*marstek.Client
	deviceInfos []DeviceIdentifier
	instanceID  int
	interval    time.Duration

	mu     sync.RWMutex
	states map[string]*DeviceState

	stopCh chan struct{}
}

func NewStateManager(clients []*marstek.Client, deviceInfos []DeviceIdentifier, instanceID int, interval time.Duration) *StateManager {
	return &StateManager{
		clients:     clients,
		deviceInfos: deviceInfos,
		instanceID:  instanceID,
		interval:    interval,
		states:      make(map[string]*DeviceState),
		stopCh:      make(chan struct{}),
	}
}

func (sm *StateManager) Start() {
	sm.updateAll()
	go sm.loop()
}

func (sm *StateManager) Stop() {
	close(sm.stopCh)
}

func (sm *StateManager) loop() {
	ticker := time.NewTicker(sm.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			sm.updateAll()
		case <-sm.stopCh:
			return
		}
	}
}

func (sm *StateManager) updateAll() {
	for i, client := range sm.clients {
		info := sm.deviceInfos[i]
		sm.updateDevice(client, info.IP)
	}
}

func (sm *StateManager) updateDevice(client *marstek.Client, deviceIP string) {
	state := &DeviceState{
		LastUpdate: time.Now(),
	}

	if es, err := client.GetESStatus(sm.instanceID); err != nil {
		slog.Warn("failed to get ES status", "device", deviceIP, "error", err)
	} else {
		state.ESStatus = es
	}

	if mode, err := client.GetESMode(sm.instanceID); err != nil {
		slog.Warn("failed to get ES mode", "device", deviceIP, "error", err)
	} else {
		state.ESMode = mode
	}

	if bat, err := client.GetBatteryStatus(sm.instanceID); err != nil {
		slog.Warn("failed to get battery status", "device", deviceIP, "error", err)
	} else {
		state.Battery = bat
	}

	if pv, err := client.GetPVStatus(sm.instanceID); err != nil {
		slog.Warn("failed to get PV status", "device", deviceIP, "error", err)
	} else {
		state.PV = pv
	}

	if em, err := client.GetEMStatus(sm.instanceID); err != nil {
		slog.Warn("failed to get EM status", "device", deviceIP, "error", err)
	} else {
		state.EM = em
	}

	sm.mu.Lock()
	sm.states[deviceIP] = state
	sm.mu.Unlock()

	slog.Debug("updated device state", "device", deviceIP)
}

func (sm *StateManager) GetState(deviceIP string) *DeviceState {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	return sm.states[deviceIP]
}

func (sm *StateManager) GetDeviceInfos() []DeviceIdentifier {
	return sm.deviceInfos
}
