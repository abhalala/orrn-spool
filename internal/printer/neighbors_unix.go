//go:build !windows

package printer

import (
	"context"
	"fmt"
	"os/exec"
	"runtime"
)

func lookupPlatformNeighbors(ctx context.Context) ([]Neighbor, error) {
	var output []byte
	var err error
	if runtime.GOOS == "linux" {
		output, err = exec.CommandContext(ctx, "ip", "neigh", "show").CombinedOutput()
	} else {
		output, err = exec.CommandContext(ctx, "arp", "-n").CombinedOutput()
	}
	if err != nil {
		return nil, fmt.Errorf("read neighbor table: %w", err)
	}
	return parseNeighbors(string(output)), nil
}
