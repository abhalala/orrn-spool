package printer

import (
	"context"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"time"
)

const maxCapabilityResponse = 64 * 1024

// Capabilities is evidence returned by a printer. None of these fields change
// desired print configuration; CUTTER and media values are observations only.
type Capabilities struct {
	MACAddress    string
	Host          string
	Port          int
	Features      []string
	MediaType     string
	MediaWidthMM  float64
	MediaHeightMM float64
	GapMM         float64
	DPI           int
	SpeedInSec    int
	Density       int
	Cutter        *bool
	VerifiedAt    time.Time
}

// Discover performs the read-only TSPL ~!I information query. VerifiedAt is
// set only after a syntactically valid response from the configured endpoint.
func Discover(ctx context.Context, target Target) (Capabilities, error) {
	cfg := target.Config()
	if cfg.Timeout <= 0 {
		cfg.Timeout = 5 * time.Second
	}
	d := net.Dialer{Timeout: cfg.Timeout}
	conn, err := d.DialContext(ctx, "tcp", net.JoinHostPort(cfg.Host, cfg.Port))
	if err != nil {
		return Capabilities{}, err
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(cfg.Timeout))
	if _, err := io.WriteString(conn, "~!I\r\n"); err != nil {
		return Capabilities{}, err
	}
	raw, readErr := io.ReadAll(io.LimitReader(conn, maxCapabilityResponse))
	if readErr != nil && len(raw) == 0 {
		return Capabilities{}, readErr
	}
	capabilities, recognized := parseCapabilities(string(raw))
	if recognized == 0 {
		if readErr != nil {
			return Capabilities{}, readErr
		}
		return Capabilities{}, fmt.Errorf("printer returned no recognizable capability fields")
	}
	capabilities.Host = target.Host
	capabilities.Port = target.Port
	if capabilities.Port == 0 {
		capabilities.Port = 9100
	}
	capabilities.VerifiedAt = time.Now().UTC()
	return capabilities, nil
}

// NormalizeMAC accepts colon, hyphen, dot, or bare hexadecimal notation and
// returns lower-case colon notation. Invalid identifiers return an empty string.
func NormalizeMAC(raw string) string {
	replacer := strings.NewReplacer(":", "", "-", "", ".", "", " ", "")
	hex := replacer.Replace(strings.TrimSpace(raw))
	if len(hex) != 12 {
		return ""
	}
	if _, err := strconv.ParseUint(hex, 16, 64); err != nil {
		return ""
	}
	hex = strings.ToLower(hex)
	return strings.Join([]string{hex[0:2], hex[2:4], hex[4:6], hex[6:8], hex[8:10], hex[10:12]}, ":")
}

func parseCapabilities(raw string) (Capabilities, int) {
	var out Capabilities
	recognized := 0
	for _, line := range strings.Split(strings.ReplaceAll(raw, "\x00", ""), "\n") {
		key, value, ok := strings.Cut(strings.TrimSpace(line), ":")
		if !ok {
			key, value, ok = strings.Cut(strings.TrimSpace(line), "=")
		}
		if !ok {
			continue
		}
		key = strings.NewReplacer(" ", "", "_", "", "-", "").Replace(strings.ToUpper(strings.TrimSpace(key)))
		value = strings.TrimSpace(value)
		switch key {
		case "MAC", "MACADDRESS":
			out.MACAddress = NormalizeMAC(value)
			recognized++
		case "INSTALLEDFEATURES":
			for _, feature := range strings.FieldsFunc(value, func(r rune) bool { return r == ',' || r == ';' }) {
				feature = strings.ToLower(strings.TrimSpace(feature))
				if feature != "" {
					out.Features = append(out.Features, feature)
					if feature == "cutter" && out.Cutter == nil {
						installed := true
						out.Cutter = &installed
					}
				}
			}
			recognized++
		case "MEDIA", "MEDIATYPE":
			out.MediaType = strings.ToLower(strings.ReplaceAll(value, " ", "_"))
			recognized++
		case "MEDIAWIDTH", "LABELWIDTH":
			out.MediaWidthMM = capabilityFloat(value, &recognized)
		case "MEDIAHEIGHT", "LABELHEIGHT":
			out.MediaHeightMM = capabilityFloat(value, &recognized)
		case "GAP", "GAPMM":
			out.GapMM = capabilityFloat(value, &recognized)
		case "DPI", "RESOLUTION":
			out.DPI = capabilityInt(value, &recognized)
		case "SPEED", "SPEEDINSEC":
			out.SpeedInSec = capabilityInt(value, &recognized)
		case "DENSITY":
			out.Density = capabilityInt(value, &recognized)
		case "CUTTER":
			if enabled, ok := capabilityBool(value); ok {
				out.Cutter = &enabled
				recognized++
			}
		}
	}
	return out, recognized
}

func capabilityFloat(raw string, recognized *int) float64 {
	fields := strings.Fields(strings.ReplaceAll(raw, ",", " "))
	if len(fields) == 0 {
		return 0
	}
	value, err := strconv.ParseFloat(fields[0], 64)
	if err != nil {
		return 0
	}
	*recognized++
	return value
}

func capabilityInt(raw string, recognized *int) int {
	return int(capabilityFloat(raw, recognized))
}

func capabilityBool(raw string) (bool, bool) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "1", "on", "yes", "true", "installed", "enabled":
		return true, true
	case "0", "off", "no", "false", "not installed", "disabled":
		return false, true
	default:
		return false, false
	}
}
