package main

import (
	"flag"
	"testing"
	"time"
)

type testFlags struct {
	set          *flag.FlagSet
	listenAddr   *string
	deviceIPs    *string
	autoDiscover *bool
	timeout      *time.Duration
	maxTimeouts  *int
}

func newTestFlags() *testFlags {
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	return &testFlags{
		set:          fs,
		listenAddr:   fs.String("listen-address", ":9141", "listen address"),
		deviceIPs:    fs.String("device-ips", "", "device IPs"),
		autoDiscover: fs.Bool("auto-discover", true, "auto discover"),
		timeout:      fs.Duration("timeout", 10*time.Second, "request timeout"),
		maxTimeouts:  fs.Int("max-consecutive-timeouts", 10, "max consecutive timeouts"),
	}
}

func TestEnvName(t *testing.T) {
	tests := map[string]string{
		"auto-discover":            "MARSTEK_AUTO_DISCOVER",
		"device-ips":               "MARSTEK_DEVICE_IPS",
		"max-consecutive-timeouts": "MARSTEK_MAX_CONSECUTIVE_TIMEOUTS",
		"enable-pv":                "MARSTEK_ENABLE_PV",
		"metrics-path":             "MARSTEK_METRICS_PATH",
	}
	for flagName, want := range tests {
		if got := envName(flagName); got != want {
			t.Errorf("envName(%q) = %q, want %q", flagName, got, want)
		}
	}
}

func TestApplyEnvDefaults(t *testing.T) {
	t.Run("auto-discover can be disabled through the environment", func(t *testing.T) {
		flags := newTestFlags()
		t.Setenv("MARSTEK_AUTO_DISCOVER", "false")

		applyEnvDefaults(flags.set)

		if *flags.autoDiscover {
			t.Fatal("expected auto-discover to be false")
		}
	})

	t.Run("flags win over the environment", func(t *testing.T) {
		flags := newTestFlags()
		t.Setenv("MARSTEK_AUTO_DISCOVER", "false")
		t.Setenv("MARSTEK_TIMEOUT", "1s")
		if err := flags.set.Parse([]string{"-auto-discover=true", "-timeout=30s"}); err != nil {
			t.Fatal(err)
		}

		applyEnvDefaults(flags.set)

		if !*flags.autoDiscover {
			t.Fatal("expected the command line value to win")
		}
		if *flags.timeout != 30*time.Second {
			t.Fatalf("timeout = %s, want 30s", *flags.timeout)
		}
	})

	t.Run("durations and numbers are parsed like flags", func(t *testing.T) {
		flags := newTestFlags()
		t.Setenv("MARSTEK_TIMEOUT", "8s")
		t.Setenv("MARSTEK_MAX_CONSECUTIVE_TIMEOUTS", "3")

		applyEnvDefaults(flags.set)

		if *flags.timeout != 8*time.Second {
			t.Fatalf("timeout = %s, want 8s", *flags.timeout)
		}
		if *flags.maxTimeouts != 3 {
			t.Fatalf("max-consecutive-timeouts = %d, want 3", *flags.maxTimeouts)
		}
	})

	t.Run("legacy listen address variable is honoured", func(t *testing.T) {
		flags := newTestFlags()
		t.Setenv("LISTEN_ADDRESS", ":9999")

		applyEnvDefaults(flags.set)

		if *flags.listenAddr != ":9999" {
			t.Fatalf("listen-address = %q, want :9999", *flags.listenAddr)
		}
	})

	t.Run("invalid values are ignored", func(t *testing.T) {
		flags := newTestFlags()
		t.Setenv("MARSTEK_TIMEOUT", "not-a-duration")

		applyEnvDefaults(flags.set)

		if *flags.timeout != 10*time.Second {
			t.Fatalf("timeout = %s, want the default 10s", *flags.timeout)
		}
	})

	t.Run("empty values keep the default", func(t *testing.T) {
		flags := newTestFlags()
		t.Setenv("MARSTEK_DEVICE_IPS", "")

		applyEnvDefaults(flags.set)

		if *flags.deviceIPs != "" {
			t.Fatalf("device-ips = %q, want empty", *flags.deviceIPs)
		}
	})
}
