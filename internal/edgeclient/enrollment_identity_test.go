package edgeclient

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/orrn/spool/internal/cache"
	"github.com/orrn/spool/internal/db"
	"github.com/orrn/spool/internal/jobs"
	"github.com/orrn/spool/internal/node"
)

func TestEnrollDifferentDeploymentRequiresExplicitStateReset(t *testing.T) {
	ctx := context.Background()
	sqlDB, err := db.Open(filepath.Join(t.TempDir(), "edge.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer sqlDB.Close()
	jobStore, err := jobs.NewStore(ctx, sqlDB)
	if err != nil {
		t.Fatal(err)
	}
	cacheStore, err := cache.NewStore(ctx, sqlDB)
	if err != nil {
		t.Fatal(err)
	}

	const newDeployment = "019a0000-0000-7000-8000-000000000201"
	const newInstance = "019a0000-0000-7000-8000-000000000202"
	fixture := newSnapshotSyncFixture(t, "forbidden-rebind")
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/edge/v1/enroll":
			_ = json.NewEncoder(w).Encode(EnrollResponse{
				ProtocolVersion: 1, DeploymentID: newDeployment, InstanceID: newInstance,
				NodeName: "Other site", SiteLabel: "Other", APIOrigin: srv.URL,
				NodeSecret: "secret-v1-2222222222222222222222222222", CredentialVersion: 1, ConfigVersion: 1,
			})
		case "/api/edge/v1/config":
			_ = json.NewEncoder(w).Encode(ConfigResponse{
				ProtocolVersion: 1, ConfigSchemaVersion: 1, DeploymentID: newDeployment, InstanceID: newInstance,
				ConfigVersion: 1, Printers: []json.RawMessage{}, Profiles: []json.RawMessage{}, Templates: []json.RawMessage{},
			})
		default:
			fixture.serveHTTP(t, w, r)
		}
	}))
	defer srv.Close()

	nodeStore := node.NewStore(filepath.Join(t.TempDir(), "node.json"))
	if err := nodeStore.Save(node.State{
		ProtocolVersion: 1, DeploymentID: "old-deployment", InstanceID: "old-instance", InstallID: "old-install", APIOrigin: srv.URL,
		Active: node.Credential{Version: 1, Secret: "secret-v1-0000000000000000000000000000"},
	}); err != nil {
		t.Fatal(err)
	}
	client := New(nodeStore, "1.0.0", srv.Client()).WithStores(jobStore, cacheStore)
	if _, err := client.Enroll(ctx, srv.URL, "enr_new_token_1234567890", "new-install"); err == nil {
		t.Fatal("different-node enrollment silently rebound local state")
	}
	state, err := nodeStore.Load()
	if err != nil || state.DeploymentID != "old-deployment" || state.InstanceID != "old-instance" {
		t.Fatalf("refused enrollment changed node identity: %+v %v", state, err)
	}
}

func TestEnrollRejectsPlainHTTPRemoteOrigin(t *testing.T) {
	store := node.NewStore(filepath.Join(t.TempDir(), "node.json"))
	client := New(store, "1.0.0", http.DefaultClient)
	if _, err := client.Enroll(context.Background(), "http://api.orrn.in", "enr_test_12345678901234567890", "install"); err == nil {
		t.Fatal("plain HTTP remote enrollment was accepted")
	}
}
