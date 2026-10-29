package printer

import (
	"context"
	"fmt"
	"net"
	"strings"
	"time"
)

// Neighbor is an address learned from the operating system's ARP/neighbor
// table. Recovery never scans an address range or uses the public Internet.
type Neighbor struct {
	IPAddress  string
	MACAddress string
}

// EndpointEvidence proves that a supported printer answered a status query and
// returned the expected MAC at one endpoint.
type EndpointEvidence struct {
	PrinterID  string
	Host       string
	Port       int
	MACAddress string
	Model      string
	VerifiedAt time.Time
}

// VerifyEndpoint requires both a genuine status response and matching identity
// evidence. A reachable host alone is never sufficient.
func VerifyEndpoint(ctx context.Context, target Target) (EndpointEvidence, error) {
	expectedMAC := NormalizeMAC(target.MACAddress)
	if expectedMAC == "" {
		return EndpointEvidence{}, fmt.Errorf("verified MAC is required")
	}
	status, err := NewSession(target.Config()).ProbeStatus(ctx)
	if err != nil {
		return EndpointEvidence{}, fmt.Errorf("endpoint status unavailable: %w", err)
	}
	if !status.Online {
		return EndpointEvidence{}, fmt.Errorf("endpoint status unavailable")
	}
	capabilities, err := Discover(ctx, target)
	if err != nil {
		return EndpointEvidence{}, fmt.Errorf("endpoint identity unavailable: %w", err)
	}
	actualMAC := NormalizeMAC(capabilities.MACAddress)
	if actualMAC == "" || actualMAC != expectedMAC {
		return EndpointEvidence{}, fmt.Errorf("MAC mismatch: expected %s, got %s", expectedMAC, actualMAC)
	}
	model := strings.ToUpper(strings.TrimSpace(capabilities.Model))
	if model != "TA220" && model != "TE200" {
		return EndpointEvidence{}, fmt.Errorf("unsupported model %q", capabilities.Model)
	}
	return EndpointEvidence{
		PrinterID:  target.PrinterID,
		Host:       target.Host,
		Port:       capabilities.Port,
		MACAddress: actualMAC,
		Model:      model,
		VerifiedAt: capabilities.VerifiedAt,
	}, nil
}

func parseNeighbors(raw string) []Neighbor {
	var neighbors []Neighbor
	for _, line := range strings.Split(raw, "\n") {
		fields := strings.Fields(line)
		ip := ""
		mac := ""
		for _, field := range fields {
			candidate := strings.Trim(field, "()")
			if ip == "" && net.ParseIP(candidate) != nil {
				ip = candidate
			}
			if mac == "" {
				mac = NormalizeMAC(field)
			}
		}
		if ip != "" && mac != "" {
			neighbors = append(neighbors, Neighbor{IPAddress: ip, MACAddress: mac})
		}
	}
	return neighbors
}

// RecoverEndpoint checks only addresses already present in the OS neighbor
// table and only probes entries whose MAC matches the verified printer.
func RecoverEndpoint(ctx context.Context, target Target) (EndpointEvidence, error) {
	return recoverEndpoint(ctx, target, neighborLookup)
}

func recoverEndpoint(ctx context.Context, target Target, lookup func(context.Context) ([]Neighbor, error)) (EndpointEvidence, error) {
	expectedMAC := NormalizeMAC(target.MACAddress)
	if expectedMAC == "" {
		return EndpointEvidence{}, fmt.Errorf("verified MAC is required for DHCP recovery")
	}
	neighbors, err := lookup(ctx)
	if err != nil {
		return EndpointEvidence{}, err
	}
	var lastErr error
	for _, neighbor := range neighbors {
		if NormalizeMAC(neighbor.MACAddress) != expectedMAC {
			continue
		}
		candidate := target
		candidate.Host = neighbor.IPAddress
		evidence, verifyErr := VerifyEndpoint(ctx, candidate)
		if verifyErr == nil {
			return evidence, nil
		}
		lastErr = verifyErr
	}
	if lastErr != nil {
		return EndpointEvidence{}, lastErr
	}
	return EndpointEvidence{}, fmt.Errorf("no neighbor with verified MAC %s", expectedMAC)
}
