package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestScaleAndORRNKeysFromYAML(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	yaml := "scale:\n  device: /dev/ttyUSB0\n  baud: 4800\n  frame: '^W:(?P<grams>\\d+)g$'\norrn:\n  instance_id: abc\n  heartbeat_interval: 30s\n"
	if err := os.WriteFile(path, []byte(yaml), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Scale.Device != "/dev/ttyUSB0" || cfg.Scale.Baud != 4800 || cfg.Scale.Frame != `^W:(?P<grams>\d+)g$` {
		t.Fatalf("scale %+v", cfg.Scale)
	}
	if cfg.ORRN.InstanceID != "abc" || cfg.ORRN.HeartbeatInterval.Seconds() != 30 {
		t.Fatalf("orrn %+v", cfg.ORRN)
	}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestEnvOverridesScaleAndORRN(t *testing.T) {
	cfg := defaults()
	cfg.Scale.Device = "COM1"
	env := map[string]string{
		"SPOOL_SCALE_DEVICE":       "COM3",
		"SPOOL_SCALE_BAUD":         "2400",
		"SPOOL_SCALE_SIMULATE":     "true",
		"ORRN_SPOOL_SHARED_SECRET": "s",
		"ORRN_SPOOL_SERVER_URL":    "https://api.orrn.in",
		"ORRN_SPOOL_INSTANCE_ID":   "i",
		"ORRN_SPOOL_SUBDOMAIN":     "fc",
	}
	cfg.ApplyDeviceEnv(func(k string) string { return env[k] })
	if cfg.Scale.Device != "COM3" || cfg.Scale.Baud != 2400 || !cfg.Scale.Simulate {
		t.Fatalf("scale %+v", cfg.Scale)
	}
	if cfg.ORRN.SharedSecret != "s" || cfg.ORRN.ServerURL != "https://api.orrn.in" || cfg.ORRN.InstanceID != "i" || cfg.ORRN.Subdomain != "fc" {
		t.Fatalf("orrn %+v", cfg.ORRN)
	}
}

func TestValidateRejectsBadScaleFrame(t *testing.T) {
	cfg := defaults()
	cfg.Scale.Device = "/dev/ttyS0"
	cfg.Scale.Frame = `^(\d+)$`
	if err := cfg.Validate(); err == nil {
		t.Fatal("a frame pattern without grams/kg group must fail validation")
	}
	cfg.Scale.Frame = ""
	cfg.Scale.Parity = "mark"
	if err := cfg.Validate(); err == nil {
		t.Fatal("unsupported parity must fail validation")
	}
}

func TestNoScaleIsValid(t *testing.T) {
	if err := defaults().Validate(); err != nil {
		t.Fatal(err)
	}
}
