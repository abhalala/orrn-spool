package printer

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

const maxFontDownloadBytes = 1024 * 1024

// DeviceAction is an explicit administrator request. Font bytes are accepted
// only for download_font and are never written as an AUTO.BAS or executable.
type DeviceAction struct {
	Action  string
	Enabled bool
	RTC     time.Time
	Font    []byte
}

// BuildDeviceAction validates discovered support before returning exact TSPL.
func BuildDeviceAction(action DeviceAction, capabilities Capabilities) (string, error) {
	model := strings.ToUpper(strings.TrimSpace(capabilities.Model))
	if model != "TA220" && model != "TE200" {
		return "", fmt.Errorf("unsupported model %q", capabilities.Model)
	}
	switch action.Action {
	case "calibrate":
		return "GAPDETECT\r\n", nil
	case "set_cutter":
		if !hasFeature(capabilities.Features, "cutter") {
			return "", fmt.Errorf("cutter is not installed")
		}
		if action.Enabled {
			return "SET CUTTER ON\r\n", nil
		}
		return "SET CUTTER OFF\r\n", nil
	case "set_rtc":
		if !hasFeature(capabilities.Features, "rtc") {
			return "", fmt.Errorf("RTC is not installed")
		}
		if action.RTC.IsZero() {
			return "", fmt.Errorf("RTC time is required")
		}
		rtc := action.RTC.UTC()
		return fmt.Sprintf("SET RTC %04d,%02d,%02d,%02d,%02d,%02d\r\n", rtc.Year(), rtc.Month(), rtc.Day(), rtc.Hour(), rtc.Minute(), rtc.Second()), nil
	case "download_font":
		if !hasFeature(capabilities.Features, "font_download") {
			return "", fmt.Errorf("font download is not supported")
		}
		if len(action.Font) == 0 || len(action.Font) > maxFontDownloadBytes {
			return "", fmt.Errorf("font size must be between 1 and %d bytes", maxFontDownloadBytes)
		}
		var payload strings.Builder
		payload.Grow(len(action.Font) + 40)
		payload.WriteString("DOWNLOAD \"ORRN.TTF\",")
		payload.WriteString(strconv.Itoa(len(action.Font)))
		payload.WriteString(",\r\n")
		payload.Write(action.Font)
		return payload.String(), nil
	default:
		return "", fmt.Errorf("unsupported device action %q", action.Action)
	}
}

func hasFeature(features []string, wanted string) bool {
	for _, feature := range features {
		if strings.EqualFold(strings.TrimSpace(feature), wanted) {
			return true
		}
	}
	return false
}
