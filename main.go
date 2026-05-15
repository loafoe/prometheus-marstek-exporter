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
		listenAddr      = flag.String("listen-address", ":9141", "Address to listen on for metrics")
		deviceIPs       = flag.String("device-ips", "", "Comma-separated list of Marstek device IPs (auto-discovered if not set)")
		devicePort      = flag.Int("device-port", 30000, "Marstek device UDP port")
		timeout         = flag.Duration("timeout", 10*time.Second, "Timeout for device communication")
		instanceID      = flag.Int("instance-id", 0, "Device instance ID")
		metricsPath     = flag.String("metrics-path", "/metrics", "Path under which to expose metrics")
		autoDiscover    = flag.Bool("auto-discover", true, "Auto-discover devices on LAN if IPs not specified")
		discoverTimeout = flag.Duration("discover-timeout", 5*time.Second, "Timeout for device discovery")
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
			clients = append(clients, client)
			deviceInfos = append(deviceInfos, DeviceIdentifier{IP: ip})
			slog.Info("added device", "ip", ip)
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

	collector := NewMultiDeviceCollector(clients, deviceInfos, *instanceID)
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
		for _, info := range deviceInfos {
			w.Write([]byte("<li>" + info.IP))
			if info.Device != "" {
				w.Write([]byte(" - " + info.Device))
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

type DeviceIdentifier struct {
	IP      string
	Device  string
	WifiMAC string
}
