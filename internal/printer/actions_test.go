package printer

import (
	"strings"
	"testing"
	"time"
)

func TestActions(t *testing.T) {
	capabilities := Capabilities{Model: "TA220", Features: []string{"cutter", "rtc", "font_download"}}
	tests := []struct {
		name   string
		action DeviceAction
		want   string
	}{
		{name: "calibrate", action: DeviceAction{Action: "calibrate"}, want: "GAPDETECT\r\n"},
		{name: "cutter on", action: DeviceAction{Action: "set_cutter", Enabled: true}, want: "SET CUTTER ON\r\n"},
		{name: "cutter off", action: DeviceAction{Action: "set_cutter"}, want: "SET CUTTER OFF\r\n"},
		{name: "rtc", action: DeviceAction{Action: "set_rtc", RTC: time.Date(2026, 10, 27, 12, 34, 56, 0, time.UTC)}, want: "SET RTC 2026,10,27,12,34,56\r\n"},
		{name: "font", action: DeviceAction{Action: "download_font", Font: []byte("FONT")}, want: "DOWNLOAD \"ORRN.TTF\",4,\r\nFONT"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := BuildDeviceAction(test.action, capabilities)
			if err != nil || got != test.want {
				t.Fatalf("BuildDeviceAction = %q, %v; want %q", got, err, test.want)
			}
			for _, forbidden := range []string{"AUTO.BAS", "RESET", "IgnorePrinterResponse"} {
				if strings.Contains(got, forbidden) {
					t.Fatalf("implicit operation %q in %q", forbidden, got)
				}
			}
		})
	}
}

func TestActionsRejectUnsupportedFeatures(t *testing.T) {
	for _, action := range []DeviceAction{{Action: "set_cutter"}, {Action: "set_rtc", RTC: time.Now()}, {Action: "download_font", Font: []byte("FONT")}} {
		if _, err := BuildDeviceAction(action, Capabilities{Model: "TA220"}); err == nil {
			t.Fatalf("unsupported action accepted: %+v", action)
		}
	}
	if _, err := BuildDeviceAction(DeviceAction{Action: "calibrate"}, Capabilities{Model: "OTHER"}); err == nil {
		t.Fatal("unsupported model accepted")
	}
	if _, err := BuildDeviceAction(DeviceAction{Action: "download_font", Font: make([]byte, maxFontDownloadBytes+1)}, Capabilities{Model: "TA220", Features: []string{"font_download"}}); err == nil {
		t.Fatal("oversized font accepted")
	}
	if _, err := BuildDeviceAction(DeviceAction{Action: "firmware_update"}, Capabilities{Model: "TA220"}); err == nil {
		t.Fatal("firmware update accepted")
	}
}
