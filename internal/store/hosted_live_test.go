package store

import (
	"errors"
	"testing"

	"github.com/kaizakin/mirage/internal/secretbox"
)

func TestLiveHostedServersAreInternalRedeployOnChangeAndGoWithTheirServer(t *testing.T) {
	s := migrated(t)
	ctx := t.Context()
	box, _ := secretbox.New(make([]byte, 32))
	spec := HostedSpec{Image: "ghcr.io/acme/fx:1", Port: 9000, Path: "/mcp", Env: map[string]string{"API_KEY": "k1"}}
	if err := s.CreateHostedServer(ctx, box, ServerSeed{Slug: "fx", AuthType: "none"}, spec); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateHostedServer(ctx, box, ServerSeed{Slug: "fx", AuthType: "none"}, spec); !errors.Is(err, ErrConflict) {
		t.Fatalf("second fx: err = %v, want ErrConflict", err)
	}
	if err := s.CreateHostedServer(ctx, box, ServerSeed{Slug: "bad", AuthType: "none"}, HostedSpec{Image: "x", Port: 70000, Path: "/"}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("port 70000: err = %v, want ErrInvalid", err)
	}

	enabled, _ := s.EnabledServers(ctx)
	if len(enabled) != 1 || !enabled[0].Internal || enabled[0].URL != "http://mcp.mcp-fx.svc.cluster.local:9000/mcp" {
		t.Fatalf("enabled servers = %+v; want fx, internal, at its Service", enabled)
	}
	if _, hosted, err := s.ServerOwner(ctx, "fx"); err != nil || !hosted {
		t.Fatalf("owner of fx: hosted %v, %v", hosted, err)
	}
	rows, _ := s.Servers(ctx)
	if h := rows[0].Hosted; h == nil || h.Image != spec.Image || h.Status != HostedDeploying {
		t.Fatalf("listed hosted state = %+v", h)
	}

	list, err := s.HostedServers(ctx)
	if err != nil || len(list) != 1 {
		t.Fatalf("hosted = %+v, %v", list, err)
	}
	h := list[0]
	if env, err := h.Env(box); err != nil || env["API_KEY"] != "k1" {
		t.Fatalf("env = %v, %v", env, err)
	}
	if err := s.MarkHostedDeployed(ctx, h.ServerID, 1); err != nil {
		t.Fatal(err)
	}
	if err := s.MarkHostedRunning(ctx, h.ServerID, 1); err != nil {
		t.Fatal(err)
	}

	image := "ghcr.io/acme/fx:2"
	if err := s.UpdateHostedServer(ctx, box, "fx", HostedPatch{Image: &image}); err != nil {
		t.Fatal(err)
	}
	list, _ = s.HostedServers(ctx)
	h = list[0]
	if h.Image != image || h.Generation != 2 || h.DeployedGeneration != 1 || h.Status != HostedDeploying {
		t.Fatalf("after the image changed: %+v", h)
	}
	if env, _ := h.Env(box); env["API_KEY"] != "k1" {
		t.Fatalf("changing the image lost the environment: %v", env)
	}
	// A stale generation's verdict must not land on the new deployment.
	if err := s.MarkHostedFailed(ctx, h.ServerID, 1, "old"); err != nil {
		t.Fatal(err)
	}
	if list, _ = s.HostedServers(ctx); list[0].Status != HostedDeploying {
		t.Fatalf("generation 1's failure marked generation 2: %+v", list[0])
	}
	if err := s.UpdateHostedServer(ctx, box, "nope", HostedPatch{Image: &image}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown hosted server: err = %v", err)
	}

	if err := s.DeleteServer(ctx, "fx"); err != nil {
		t.Fatal(err)
	}
	if list, _ = s.HostedServers(ctx); len(list) != 0 {
		t.Fatalf("hosted rows outlived their server: %+v", list)
	}
}
