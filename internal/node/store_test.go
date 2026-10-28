package node

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestNodeStorePersistsPendingRotationAndPromotesAtomically(t *testing.T) {
	path := filepath.Join(t.TempDir(), "node.json")
	store := NewStore(path)

	initial := State{
		ProtocolVersion: 1,
		DeploymentID:    "019a0000-0000-7000-8000-000000000001",
		InstanceID:      "019a0000-0000-7000-8000-000000000002",
		NodeName:        "Fourcubes",
		SiteLabel:       "Surat",
		APIOrigin:       "https://api.orrn.in",
		Active: Credential{
			Version: 1,
			Secret:  "secret-v1-0000000000000000000000000000",
		},
	}
	if err := store.Save(initial); err != nil {
		t.Fatalf("Save initial: %v", err)
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatalf("stat: %v", err)
		}
		if got := info.Mode().Perm(); got != 0o600 {
			t.Fatalf("mode = %o, want 0600", got)
		}
	}

	if err := store.StagePending(Credential{Version: 2, Secret: "secret-v2-1111111111111111111111111111"}); err != nil {
		t.Fatalf("StagePending: %v", err)
	}

	reloaded, err := store.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if reloaded.Active.Version != 1 || reloaded.Pending == nil || reloaded.Pending.Version != 2 {
		t.Fatalf("unexpected state before confirm: %+v", reloaded)
	}

	if err := store.PromotePending(2); err != nil {
		t.Fatalf("PromotePending: %v", err)
	}
	after, err := store.Load()
	if err != nil {
		t.Fatalf("Load after promote: %v", err)
	}
	if after.Active.Version != 2 || after.Pending != nil {
		t.Fatalf("unexpected state after promote: %+v", after)
	}

	if err := store.MarkRevoked(); err != nil {
		t.Fatalf("MarkRevoked: %v", err)
	}
	revoked, err := store.Load()
	if err != nil {
		t.Fatalf("Load after revoke: %v", err)
	}
	if !revoked.Revoked || revoked.Active.Secret != "" || revoked.Pending != nil {
		t.Fatalf("expected revoked state with wiped secrets, got %+v", revoked)
	}
}
