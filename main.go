package main

import (
	"flag"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/loafoe/go-marstek/pkg/marstek"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

func main() {
	var (
		listenAddr   = flag.String("listen-address", ":9141", "Address to listen on for metrics")
		deviceIPs    = flag.String("device-ips", "", "Comma-separated list of Marstek device IPs (auto-discovered if not set)")
		devicePort   = flag.Int("device-port", 30000, "Marstek device UDP port")
		timeout      = flag.Duration("timeout", 10*time.Second, "Timeout for device communication")
		instanceID   = flag.Int("instance-id", 0, "Device instance ID")
		metricsPath  = flag.String("metrics-path", "/metrics", "Path under which to expose metrics")
		autoDiscover = flag.Bool("auto-discover", true,
			"Auto-discover devices on LAN if IPs not specified, also used to recover devices that were assigned a new IP address")
		discoverTimeout = flag.Duration("discover-timeout", 5*time.Second, "Timeout for device discovery")
		enablePV        = flag.Bool("enable-pv", false, "Enable PV (solar) status polling")
		maxTimeouts     = flag.Int("max-consecutive-timeouts", 10,
			"Consecutive API timeouts on a device address before the address is considered stale and the device is rediscovered (0 disables the check)")
		exitUnreachable = flag.Bool("exit-on-unreachable", true,
			"Exit the exporter when a device is unreachable and cannot be recovered by rediscovering it on the LAN")
	)
	flag.Parse()

	if envAddr := os.Getenv("MARSTEK_DEVICE_IPS"); envAddr != "" && *deviceIPs == "" {
		*deviceIPs = envAddr
	}
	if envPort := os.Getenv("MARSTEK_DEVICE_PORT"); envPort != "" {
		if p, err := strconv.Atoi(envPort); err == nil {
			*devicePort = p
		}
	}
	if envInstance := os.Getenv("MARSTEK_INSTANCE_ID"); envInstance != "" {
		if i, err := strconv.Atoi(envInstance); err == nil {
			*instanceID = i
		}
	}
	if envListen := os.Getenv("LISTEN_ADDRESS"); envListen != "" {
		*listenAddr = envListen
	}
	if envPV := os.Getenv("MARSTEK_ENABLE_PV"); envPV == "true" || envPV == "1" {
		*enablePV = true
	}

	var clients []*marstek.Client
	var deviceInfos []DeviceIdentifier

	if *deviceIPs != "" {
		ips := strings.Split(*deviceIPs, ",")
		for _, ip := range ips {
			ip = strings.TrimSpace(ip)
			if ip == "" {
				continue
			}
			client := marstek.NewClient(ip,
				marstek.WithPort(*devicePort),
				marstek.WithTimeout(*timeout),
			)
			info := DeviceIdentifier{IP: ip}
			if dev := probeDevice(ip, *devicePort, *timeout); dev != nil {
				info.Device = dev.Device
				info.WifiMAC = dev.WifiMAC
			}
			clients = append(clients, client)
			deviceInfos = append(deviceInfos, info)
			slog.Info("added device", "ip", ip, "device", info.Device, "mac", info.WifiMAC)
		}
	} else if *autoDiscover {
		slog.Info("auto-discovering Marstek devices on LAN...")
		devices, err := marstek.DiscoverDevices(*devicePort, *discoverTimeout)
		if err != nil {
			slog.Error("failed to discover devices", "error", err)
			os.Exit(1)
		}
		if len(devices) == 0 {
			slog.Error("no Marstek devices found on LAN")
			os.Exit(1)
		}
		for _, dev := range devices {
			client := marstek.NewClient(dev.IP,
				marstek.WithPort(*devicePort),
				marstek.WithTimeout(*timeout),
			)
			clients = append(clients, client)
			deviceInfos = append(deviceInfos, DeviceIdentifier{
				IP:      dev.IP,
				Device:  dev.Device,
				WifiMAC: dev.WifiMAC,
			})
			slog.Info("discovered device", "ip", dev.IP, "device", dev.Device, "mac", dev.WifiMAC)
		}
	}

	if len(clients) == 0 {
		slog.Error("no devices configured (use -device-ips flag, MARSTEK_DEVICE_IPS env var, or -auto-discover)")
		os.Exit(1)
	}

	stateManager := NewStateManager(clients, deviceInfos, StateManagerConfig{
		InstanceID:             *instanceID,
		EnablePV:               *enablePV,
		AutoDiscover:           *autoDiscover,
		DevicePort:             *devicePort,
		RequestTimeout:         *timeout,
		DiscoverTimeout:        *discoverTimeout,
		MaxConsecutiveTimeouts: *maxTimeouts,
		ExitOnUnreachable:      *exitUnreachable,
	})
	stateManager.Start()

	go func() {
		if err := <-stateManager.FatalErrors(); err != nil {
			slog.Error("exiting due to an unrecoverable error", "error", err)
			os.Exit(1)
		}
	}()

	collector := NewCachedCollector(stateManager)
	prometheus.MustRegister(collector)

	http.Handle(*metricsPath, promhttp.Handler())
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`<html>
<head><title>Marstek Exporter</title></head>
<body>
<h1>Marstek Exporter</h1>
<p><a href="` + *metricsPath + `">Metrics</a></p>
<h2>Devices</h2>
<ul>`))
		for _, dev := range stateManager.Snapshots() {
			w.Write([]byte("<li>" + dev.Info.IP))
			if dev.Info.Device != "" {
				w.Write([]byte(" - " + dev.Info.Device))
			}
			w.Write([]byte("</li>"))
		}
		w.Write([]byte(`</ul>
</body>
</html>`))
	})
	http.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	})

	slog.Info("starting marstek exporter", "address", *listenAddr, "devices", len(clients))
	if err := http.ListenAndServe(*listenAddr, nil); err != nil {
		slog.Error("failed to start server", "error", err)
		os.Exit(1)
	}
}

// probeDevice asks a device for its identity. The identity is used to find the
// device again when it was assigned a new IP address. Failures are ignored, the
// device is then polled without a known identity.
func probeDevice(ip string, port int, timeout time.Duration) *marstek.DeviceInfo {
	probeTimeout := timeout
	if probeTimeout > 3*time.Second {
		probeTimeout = 3 * time.Second
	}

	client := marstek.NewClient(ip,
		marstek.WithPort(port),
		marstek.WithTimeout(probeTimeout),
		marstek.WithRetries(0),
	)

	dev, err := client.GetDevice("0")
	if err != nil {
		slog.Debug("could not read the identity of the device", "ip", ip, "error", err)
		return nil
	}
	return dev
}
