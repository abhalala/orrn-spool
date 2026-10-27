//go:build windows

package printer

import (
	"context"
	"fmt"
	"os/exec"
)

func lookupPlatformNeighbors(ctx context.Context) ([]Neighbor, error) {
	output, err := exec.CommandContext(ctx, "arp", "-a").CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("read ARP table: %w", err)
	}
	return parseNeighbors(string(output)), nil
}
