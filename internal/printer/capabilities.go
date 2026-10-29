package printer

import (
	"context"
	"errors"
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
	Model         string
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

// discoveryIdle is how long Discover keeps reading after the first reply byte.
// The TA220 answers ~!T without a line ending and never closes the socket, so
// the reply is complete once the printer goes quiet.
const discoveryIdle = 750 * time.Millisecond

// neighborLookup reads the OS neighbor (ARP) table; replaceable in tests.
var neighborLookup = lookupPlatformNeighbors

// SetNeighborLookupForTesting replaces the OS neighbor table lookup and returns
// a function restoring the previous one. Tests only; not safe for concurrent use
// with running discovery.
func SetNeighborLookupForTesting(lookup func(context.Context) ([]Neighbor, error)) (restore func()) {
	previous := neighborLookup
	neighborLookup = lookup
	return func() { neighborLookup = previous }
}

// Discover asks the printer for its model with the read-only TSPL ~!T query,
// then resolves the MAC from the OS neighbor table for the endpoint's IPv4.
// The TA220 does not report its MAC or media over port 9100, so MACAddress is
// empty when the neighbor table has no entry, and media fields stay unset.
// VerifiedAt is set only after the configured endpoint returned a model.
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
	deadline := time.Now().Add(cfg.Timeout)
	if ctxDeadline, ok := ctx.Deadline(); ok && ctxDeadline.Before(deadline) {
		deadline = ctxDeadline
	}
	stop := context.AfterFunc(ctx, func() { _ = conn.SetDeadline(time.Now()) })
	defer stop()
	_ = conn.SetDeadline(deadline)
	if _, err := io.WriteString(conn, "~!T"); err != nil {
		return Capabilities{}, err
	}
	raw, readErr := readUntilIdle(conn, deadline)
	model := strings.ToUpper(strings.Trim(string(raw), "\x00\r\n \t"))
	if model == "" {
		if ctx.Err() != nil {
			return Capabilities{}, ctx.Err()
		}
		if readErr != nil {
			return Capabilities{}, fmt.Errorf("printer model query: %w", readErr)
		}
		return Capabilities{}, fmt.Errorf("printer returned no model")
	}
	capabilities := Capabilities{Model: model, Host: target.Host, Port: target.Port}
	if capabilities.Port == 0 {
		capabilities.Port = 9100
	}
	capabilities.MACAddress = neighborMAC(ctx, cfg.Host)
	capabilities.VerifiedAt = time.Now().UTC()
	return capabilities, nil
}

// readUntilIdle reads until discoveryIdle passes without data after the first
// byte, the overall deadline expires, or the peer closes. It never waits for EOF.
func readUntilIdle(conn net.Conn, deadline time.Time) ([]byte, error) {
	var raw []byte
	buf := make([]byte, 256)
	for len(raw) < maxCapabilityResponse {
		readDeadline := deadline
		if len(raw) > 0 {
			if idle := time.Now().Add(discoveryIdle); idle.Before(readDeadline) {
				readDeadline = idle
			}
		}
		_ = conn.SetReadDeadline(readDeadline)
		n, err := conn.Read(buf)
		raw = append(raw, buf[:n]...)
		if err != nil {
			var netErr net.Error
			if len(raw) > 0 && (err == io.EOF || (errors.As(err, &netErr) && netErr.Timeout())) {
				return raw, nil
			}
			return raw, err
		}
	}
	return raw, nil
}

// neighborMAC returns the normalized MAC the OS neighbor table holds for host,
// or "" when the host does not resolve to an IPv4 or has no entry.
func neighborMAC(ctx context.Context, host string) string {
	ip := net.ParseIP(host)
	if ip == nil {
		addrs, err := net.DefaultResolver.LookupIP(ctx, "ip4", host)
		if err != nil || len(addrs) == 0 {
			return ""
		}
		ip = addrs[0]
	}
	neighbors, err := neighborLookup(ctx)
	if err != nil {
		return ""
	}
	for _, neighbor := range neighbors {
		if candidate := net.ParseIP(neighbor.IPAddress); candidate != nil && candidate.Equal(ip) {
			if mac := NormalizeMAC(neighbor.MACAddress); mac != "" {
				return mac
			}
		}
	}
	return ""
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
