package main

import (
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

type CachedCollector struct {
	stateManager *StateManager

	batterySOC           *prometheus.Desc
	batteryTemperature   *prometheus.Desc
	batteryCapacity      *prometheus.Desc
	batteryRatedCapacity *prometheus.Desc
	batteryCharging      *prometheus.Desc
	batteryDischarging   *prometheus.Desc
	batteryPower         *prometheus.Desc

	pvPower   *prometheus.Desc
	pvVoltage *prometheus.Desc
	pvCurrent *prometheus.Desc

	gridPower    *prometheus.Desc
	offgridPower *prometheus.Desc
	phasePower   *prometheus.Desc
	totalPower   *prometheus.Desc
	ctClampState *prometheus.Desc

	totalPVEnergy         *prometheus.Desc
	totalGridInputEnergy  *prometheus.Desc
	totalGridOutputEnergy *prometheus.Desc
	totalLoadEnergy       *prometheus.Desc
	inputEnergy           *prometheus.Desc
	outputEnergy          *prometheus.Desc

	operatingMode  *prometheus.Desc
	stateAge       *prometheus.Desc
}

func NewCachedCollector(stateManager *StateManager) *CachedCollector {
	namespace := "marstek"
	deviceLabels := []string{"device_ip", "device_name", "instance"}

	return &CachedCollector{
		stateManager: stateManager,

		batterySOC: prometheus.NewDesc(
			prometheus.BuildFQName(namespace, "battery", "soc_percent"),
			"Battery state of charge percentage",
			deviceLabels, nil,
		),
		batteryTemperature: prometheus.NewDesc(
			prometheus.BuildFQName(namespace, "battery", "temperature_celsius"),
			"Battery temperature in Celsius",
			deviceLabels, nil,
		),
		batteryCapacity: prometheus.NewDesc(
			prometheus.BuildFQName(namespace, "battery", "capacity_wh"),
			"Current battery capacity in Wh",
			deviceLabels, nil,
		),
		batteryRatedCapacity: prometheus.NewDesc(
			prometheus.BuildFQName(namespace, "battery", "rated_capacity_wh"),
			"Rated battery capacity in Wh",
			deviceLabels, nil,
		),
		batteryCharging: prometheus.NewDesc(
			prometheus.BuildFQName(namespace, "battery", "charging"),
			"Battery charging state (1=charging, 0=not charging)",
			deviceLabels, nil,
		),
		batteryDischarging: prometheus.NewDesc(
			prometheus.BuildFQName(namespace, "battery", "discharging"),
			"Battery discharging state (1=discharging, 0=not discharging)",
			deviceLabels, nil,
		),
		batteryPower: prometheus.NewDesc(
			prometheus.BuildFQName(namespace, "battery", "power_watts"),
			"Battery power in watts (positive=charging, negative=discharging)",
			deviceLabels, nil,
		),

		pvPower: prometheus.NewDesc(
			prometheus.BuildFQName(namespace, "pv", "power_watts"),
			"Solar PV power in watts",
			append(deviceLabels, "input"), nil,
		),
		pvVoltage: prometheus.NewDesc(
			prometheus.BuildFQName(namespace, "pv", "voltage_volts"),
			"Solar PV voltage",
			append(deviceLabels, "input"), nil,
		),
		pvCurrent: prometheus.NewDesc(
			prometheus.BuildFQName(namespace, "pv", "current_amps"),
			"Solar PV current in amps",
			append(deviceLabels, "input"), nil,
		),

		gridPower: prometheus.NewDesc(
			prometheus.BuildFQName(namespace, "grid", "power_watts"),
			"Grid power in watts (positive=importing, negative=exporting)",
			deviceLabels, nil,
		),
		offgridPower: prometheus.NewDesc(
			prometheus.BuildFQName(namespace, "offgrid", "power_watts"),
			"Offgrid load power in watts",
			deviceLabels, nil,
		),
		phasePower: prometheus.NewDesc(
			prometheus.BuildFQName(namespace, "phase", "power_watts"),
			"Per-phase power in watts",
			append(deviceLabels, "phase"), nil,
		),
		totalPower: prometheus.NewDesc(
			prometheus.BuildFQName(namespace, "total", "power_watts"),
			"Total power in watts",
			deviceLabels, nil,
		),
		ctClampState: prometheus.NewDesc(
			prometheus.BuildFQName(namespace, "ct_clamp", "connected"),
			"CT clamp connection state (1=connected, 0=disconnected)",
			deviceLabels, nil,
		),

		totalPVEnergy: prometheus.NewDesc(
			prometheus.BuildFQName(namespace, "pv", "energy_kwh_total"),
			"Total solar energy produced in kWh",
			deviceLabels, nil,
		),
		totalGridInputEnergy: prometheus.NewDesc(
			prometheus.BuildFQName(namespace, "grid", "input_energy_kwh_total"),
			"Total energy imported from grid in kWh",
			deviceLabels, nil,
		),
		totalGridOutputEnergy: prometheus.NewDesc(
			prometheus.BuildFQName(namespace, "grid", "output_energy_kwh_total"),
			"Total energy exported to grid in kWh",
			deviceLabels, nil,
		),
		totalLoadEnergy: prometheus.NewDesc(
			prometheus.BuildFQName(namespace, "load", "energy_kwh_total"),
			"Total load energy consumed in kWh",
			deviceLabels, nil,
		),
		inputEnergy: prometheus.NewDesc(
			prometheus.BuildFQName(namespace, "meter", "input_energy_kwh_total"),
			"Energy meter input energy in kWh",
			deviceLabels, nil,
		),
		outputEnergy: prometheus.NewDesc(
			prometheus.BuildFQName(namespace, "meter", "output_energy_kwh_total"),
			"Energy meter output energy in kWh",
			deviceLabels, nil,
		),

		operatingMode: prometheus.NewDesc(
			prometheus.BuildFQName(namespace, "operating", "mode"),
			"Current operating mode (1=active for the labeled mode)",
			append(deviceLabels, "mode"), nil,
		),
		stateAge: prometheus.NewDesc(
			prometheus.BuildFQName(namespace, "state", "age_seconds"),
			"Age of cached state in seconds",
			[]string{"device_ip", "device_name"}, nil,
		),
	}
}

func (c *CachedCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- c.batterySOC
	ch <- c.batteryTemperature
	ch <- c.batteryCapacity
	ch <- c.batteryRatedCapacity
	ch <- c.batteryCharging
	ch <- c.batteryDischarging
	ch <- c.batteryPower
	ch <- c.pvPower
	ch <- c.pvVoltage
	ch <- c.pvCurrent
	ch <- c.gridPower
	ch <- c.offgridPower
	ch <- c.phasePower
	ch <- c.totalPower
	ch <- c.ctClampState
	ch <- c.totalPVEnergy
	ch <- c.totalGridInputEnergy
	ch <- c.totalGridOutputEnergy
	ch <- c.totalLoadEnergy
	ch <- c.inputEnergy
	ch <- c.outputEnergy
	ch <- c.operatingMode
	ch <- c.stateAge
}

func (c *CachedCollector) Collect(ch chan<- prometheus.Metric) {
	for _, info := range c.stateManager.GetDeviceInfos() {
		state := c.stateManager.GetState(info.IP)
		if state == nil {
			continue
		}

		deviceName := info.Device
		if deviceName == "" {
			deviceName = "unknown"
		}
		instanceLabel := strconv.Itoa(c.stateManager.instanceID)

		ch <- prometheus.MustNewConstMetric(c.stateAge, prometheus.GaugeValue,
			time.Since(state.LastUpdate).Seconds(), info.IP, deviceName)

		c.collectESStatus(ch, state, info.IP, deviceName, instanceLabel)
		c.collectESMode(ch, state, info.IP, deviceName, instanceLabel)
		c.collectBatteryStatus(ch, state, info.IP, deviceName, instanceLabel)
		c.collectPVStatus(ch, state, info.IP, deviceName, instanceLabel)
		c.collectEMStatus(ch, state, info.IP, deviceName, instanceLabel)
	}
}

func (c *CachedCollector) collectESStatus(ch chan<- prometheus.Metric, state *DeviceState, deviceIP, deviceName, instanceLabel string) {
	status := state.ESStatus
	if status == nil {
		return
	}

	if status.BatSOC != nil {
		ch <- prometheus.MustNewConstMetric(c.batterySOC, prometheus.GaugeValue, float64(*status.BatSOC), deviceIP, deviceName, instanceLabel)
	}
	if status.BatCap != nil {
		ch <- prometheus.MustNewConstMetric(c.batteryCapacity, prometheus.GaugeValue, *status.BatCap, deviceIP, deviceName, instanceLabel)
	}
	if status.PVPower != nil {
		ch <- prometheus.MustNewConstMetric(c.pvPower, prometheus.GaugeValue, *status.PVPower, deviceIP, deviceName, instanceLabel, "total")
	}
	if status.OngridPower != nil {
		ch <- prometheus.MustNewConstMetric(c.gridPower, prometheus.GaugeValue, *status.OngridPower, deviceIP, deviceName, instanceLabel)
	}
	if status.OffgridPower != nil {
		ch <- prometheus.MustNewConstMetric(c.offgridPower, prometheus.GaugeValue, *status.OffgridPower, deviceIP, deviceName, instanceLabel)
	}
	if status.BatPower != nil {
		ch <- prometheus.MustNewConstMetric(c.batteryPower, prometheus.GaugeValue, *status.BatPower, deviceIP, deviceName, instanceLabel)
	}
	if status.TotalPVEnergy != nil {
		ch <- prometheus.MustNewConstMetric(c.totalPVEnergy, prometheus.CounterValue, *status.TotalPVEnergy, deviceIP, deviceName, instanceLabel)
	}
	if status.TotalGridInputEnergy != nil {
		ch <- prometheus.MustNewConstMetric(c.totalGridInputEnergy, prometheus.CounterValue, *status.TotalGridInputEnergy, deviceIP, deviceName, instanceLabel)
	}
	if status.TotalGridOutputEnergy != nil {
		ch <- prometheus.MustNewConstMetric(c.totalGridOutputEnergy, prometheus.CounterValue, *status.TotalGridOutputEnergy, deviceIP, deviceName, instanceLabel)
	}
	if status.TotalLoadEnergy != nil {
		ch <- prometheus.MustNewConstMetric(c.totalLoadEnergy, prometheus.CounterValue, *status.TotalLoadEnergy, deviceIP, deviceName, instanceLabel)
	}
}

func (c *CachedCollector) collectESMode(ch chan<- prometheus.Metric, state *DeviceState, deviceIP, deviceName, instanceLabel string) {
	mode := state.ESMode
	if mode == nil {
		return
	}

	if mode.Mode != "" {
		ch <- prometheus.MustNewConstMetric(c.operatingMode, prometheus.GaugeValue, 1, deviceIP, deviceName, instanceLabel, mode.Mode)
	}
	if mode.APower != nil {
		ch <- prometheus.MustNewConstMetric(c.phasePower, prometheus.GaugeValue, *mode.APower, deviceIP, deviceName, instanceLabel, "A")
	}
	if mode.BPower != nil {
		ch <- prometheus.MustNewConstMetric(c.phasePower, prometheus.GaugeValue, *mode.BPower, deviceIP, deviceName, instanceLabel, "B")
	}
	if mode.CPower != nil {
		ch <- prometheus.MustNewConstMetric(c.phasePower, prometheus.GaugeValue, *mode.CPower, deviceIP, deviceName, instanceLabel, "C")
	}
	if mode.TotalPower != nil {
		ch <- prometheus.MustNewConstMetric(c.totalPower, prometheus.GaugeValue, *mode.TotalPower, deviceIP, deviceName, instanceLabel)
	}
}

func (c *CachedCollector) collectBatteryStatus(ch chan<- prometheus.Metric, state *DeviceState, deviceIP, deviceName, instanceLabel string) {
	status := state.Battery
	if status == nil {
		return
	}

	if status.BatTemp != nil {
		ch <- prometheus.MustNewConstMetric(c.batteryTemperature, prometheus.GaugeValue, *status.BatTemp, deviceIP, deviceName, instanceLabel)
	}
	if status.RatedCapacity != nil {
		ch <- prometheus.MustNewConstMetric(c.batteryRatedCapacity, prometheus.GaugeValue, *status.RatedCapacity, deviceIP, deviceName, instanceLabel)
	}

	charging := 0.0
	if status.ChargFlag {
		charging = 1.0
	}
	ch <- prometheus.MustNewConstMetric(c.batteryCharging, prometheus.GaugeValue, charging, deviceIP, deviceName, instanceLabel)

	discharging := 0.0
	if status.DischrgFlag {
		discharging = 1.0
	}
	ch <- prometheus.MustNewConstMetric(c.batteryDischarging, prometheus.GaugeValue, discharging, deviceIP, deviceName, instanceLabel)
}

func (c *CachedCollector) collectPVStatus(ch chan<- prometheus.Metric, state *DeviceState, deviceIP, deviceName, instanceLabel string) {
	status := state.PV
	if status == nil {
		return
	}

	ch <- prometheus.MustNewConstMetric(c.pvVoltage, prometheus.GaugeValue, status.PVVoltage, deviceIP, deviceName, instanceLabel, "total")
	ch <- prometheus.MustNewConstMetric(c.pvCurrent, prometheus.GaugeValue, status.PVCurrent, deviceIP, deviceName, instanceLabel, "total")

	if status.PV1Power != nil {
		ch <- prometheus.MustNewConstMetric(c.pvPower, prometheus.GaugeValue, *status.PV1Power, deviceIP, deviceName, instanceLabel, "1")
	}
	if status.PV1Voltage != nil {
		ch <- prometheus.MustNewConstMetric(c.pvVoltage, prometheus.GaugeValue, *status.PV1Voltage, deviceIP, deviceName, instanceLabel, "1")
	}
	if status.PV1Current != nil {
		ch <- prometheus.MustNewConstMetric(c.pvCurrent, prometheus.GaugeValue, *status.PV1Current, deviceIP, deviceName, instanceLabel, "1")
	}
	if status.PV2Power != nil {
		ch <- prometheus.MustNewConstMetric(c.pvPower, prometheus.GaugeValue, *status.PV2Power, deviceIP, deviceName, instanceLabel, "2")
	}
	if status.PV2Voltage != nil {
		ch <- prometheus.MustNewConstMetric(c.pvVoltage, prometheus.GaugeValue, *status.PV2Voltage, deviceIP, deviceName, instanceLabel, "2")
	}
	if status.PV2Current != nil {
		ch <- prometheus.MustNewConstMetric(c.pvCurrent, prometheus.GaugeValue, *status.PV2Current, deviceIP, deviceName, instanceLabel, "2")
	}
}

func (c *CachedCollector) collectEMStatus(ch chan<- prometheus.Metric, state *DeviceState, deviceIP, deviceName, instanceLabel string) {
	status := state.EM
	if status == nil {
		return
	}

	if status.CTState != nil {
		ctConnected := 0.0
		if *status.CTState == 1 {
			ctConnected = 1.0
		}
		ch <- prometheus.MustNewConstMetric(c.ctClampState, prometheus.GaugeValue, ctConnected, deviceIP, deviceName, instanceLabel)
	}
	if status.InputEnergy != nil {
		ch <- prometheus.MustNewConstMetric(c.inputEnergy, prometheus.CounterValue, *status.InputEnergy, deviceIP, deviceName, instanceLabel)
	}
	if status.OutputEnergy != nil {
		ch <- prometheus.MustNewConstMetric(c.outputEnergy, prometheus.CounterValue, *status.OutputEnergy, deviceIP, deviceName, instanceLabel)
	}
}
