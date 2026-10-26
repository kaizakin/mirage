package provisioner

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/kaizakin/mirage/internal/secretbox"
	"github.com/kaizakin/mirage/internal/store"
)

type fakeHostedDB struct{ rows []store.Hosted }

func (f *fakeHostedDB) HostedServers(context.Context) ([]store.Hosted, error) {
	return slices.Clone(f.rows), nil
}

func (f *fakeHostedDB) row(id int64) *store.Hosted {
	for i := range f.rows {
		if f.rows[i].ServerID == id {
			return &f.rows[i]
		}
	}
	return nil
}

func (f *fakeHostedDB) MarkHostedDeployed(_ context.Context, id int64, gen int) error {
	f.row(id).DeployedGeneration = gen
	return nil
}

func (f *fakeHostedDB) MarkHostedRunning(_ context.Context, id int64, gen int) error {
	if r := f.row(id); r.Generation == gen && r.Status == store.HostedDeploying {
		r.Status = store.HostedRunning
	}
	return nil
}

func (f *fakeHostedDB) MarkHostedFailed(_ context.Context, id int64, gen int, reason string) error {
	if r := f.row(id); r.Generation == gen {
		r.Status, r.Reason = store.HostedFailed, reason
	}
	return nil
}

// fakeHostedCluster keeps each namespace's release values and reports each one's health.
type fakeHostedCluster struct {
	namespaces map[string]bool
	releases   map[string]map[string]any
	upgrades   int
	health     map[string]Health
}

func (c *fakeHostedCluster) EnsureHostedNamespace(_ context.Context, ns, _ string) error {
	c.namespaces[ns] = true
	return nil
}

func (c *fakeHostedCluster) DeleteNamespace(_ context.Context, ns string) (bool, error) {
	delete(c.namespaces, ns)
	delete(c.releases, ns)
	return true, nil
}

func (c *fakeHostedCluster) HostedNamespaces(context.Context) ([]string, error) {
	var out []string
	for ns := range c.namespaces {
		out = append(out, ns)
	}
	return out, nil
}

func (c *fakeHostedCluster) HostedRelease(_ context.Context, ns string) (ReleaseState, error) {
	if _, ok := c.releases[ns]; ok {
		return ReleaseDeployed, nil
	}
	return ReleaseAbsent, nil
}

func (c *fakeHostedCluster) InstallHosted(_ context.Context, ns string, values map[string]any) error {
	c.releases[ns] = values
	return nil
}

func (c *fakeHostedCluster) UpgradeHosted(_ context.Context, ns string, values map[string]any) error {
	c.upgrades++
	c.releases[ns] = values
	return nil
}

func (c *fakeHostedCluster) UninstallHosted(_ context.Context, ns string) error {
	delete(c.releases, ns)
	return nil
}

func (c *fakeHostedCluster) HostedHealth(_ context.Context, ns string) (Health, error) {
	return c.health[ns], nil
}

func newHosting(t *testing.T) (*Hosting, *fakeHostedDB, *fakeHostedCluster, *secretbox.Box) {
	t.Helper()
	box, err := secretbox.New(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	db := &fakeHostedDB{}
	cluster := &fakeHostedCluster{namespaces: map[string]bool{}, releases: map[string]map[string]any{}, health: map[string]Health{}}
	return NewHosting(db, cluster, box, HostingConfig{GatewayNamespace: "mirage"}), db, cluster, box
}

func TestHostedServerDeploysRunsUpgradesAndIsCollected(t *testing.T) {
	h, db, cluster, _ := newHosting(t)
	ctx := t.Context()
	db.rows = []store.Hosted{{ServerID: 1, Slug: "fx", Image: "fx:1", Port: 9000, Status: store.HostedDeploying, Generation: 1,
		DeployingSince: time.Now()}}

	h.Pass(ctx)
	if got := cluster.releases["mcp-fx"]; got["image"] != "fx:1" || got["port"] != 9000 || got["gatewayNamespace"] != "mirage" {
		t.Fatalf("installed values = %v", got)
	}
	cluster.health["mcp-fx"] = Health{Waiting: "the server is starting"}
	h.Pass(ctx)
	if db.rows[0].Status != store.HostedDeploying {
		t.Fatalf("status = %s while the server starts", db.rows[0].Status)
	}
	cluster.health["mcp-fx"] = Health{Ready: true}
	h.Pass(ctx)
	if db.rows[0].Status != store.HostedRunning {
		t.Fatalf("status = %s once ready, want running", db.rows[0].Status)
	}

	// An admin sets a new image: the row moves back to deploying under a new generation.
	db.rows[0].Image, db.rows[0].Generation, db.rows[0].Status = "fx:2", 2, store.HostedDeploying
	h.Pass(ctx)
	if cluster.upgrades != 1 || cluster.releases["mcp-fx"]["image"] != "fx:2" {
		t.Fatalf("after the image changed: %d upgrades, values %v", cluster.upgrades, cluster.releases["mcp-fx"])
	}
	h.Pass(ctx)
	h.Pass(ctx)
	if cluster.upgrades != 1 || db.rows[0].Status != store.HostedRunning {
		t.Fatalf("%d upgrades, status %s; want one upgrade, then running", cluster.upgrades, db.rows[0].Status)
	}

	// The server row is deleted: its namespace goes on the next pass.
	db.rows = nil
	h.Pass(ctx)
	if len(cluster.namespaces) != 0 {
		t.Fatalf("namespaces left after the server was deleted: %v", cluster.namespaces)
	}
}

func TestBrokenOrSlowHostedServersFailWithAReasonAndKeepTheirResources(t *testing.T) {
	h, db, cluster, box := newHosting(t)
	ctx := t.Context()
	env, err := box.Encrypt([]byte(`{"API_KEY":"k"}`), []byte("hosted-env:bad"))
	if err != nil {
		t.Fatal(err)
	}
	db.rows = []store.Hosted{
		{ServerID: 1, Slug: "bad", Image: "bad:1", EnvEnc: env, Status: store.HostedDeploying, Generation: 1, DeployingSince: time.Now()},
		{ServerID: 2, Slug: "slow", Image: "slow:1", Status: store.HostedDeploying, Generation: 1, DeployingSince: time.Now().Add(-time.Hour)},
	}
	h.Pass(ctx)
	if got := cluster.releases["mcp-bad"]["env"]; !mapsEqual(got, map[string]string{"API_KEY": "k"}) {
		t.Fatalf("the release got env %v, want the opened environment", got)
	}

	broken := "CreateContainerConfigError: container has runAsNonRoot and image will run as root"
	cluster.health["mcp-bad"] = Health{Broken: broken}
	cluster.health["mcp-slow"] = Health{Waiting: "ImagePullBackOff: not found"}
	h.Pass(ctx)
	if r := db.row(1); r.Status != store.HostedFailed || r.Reason != broken {
		t.Fatalf("broken server = %s %q", r.Status, r.Reason)
	}
	if r := db.row(2); r.Status != store.HostedFailed || r.Reason != "not running after 3m0s: ImagePullBackOff: not found" {
		t.Fatalf("slow server = %s %q", r.Status, r.Reason)
	}
	if len(cluster.releases) != 2 {
		t.Fatalf("a failure removed resources: %v", cluster.releases)
	}
}

func mapsEqual(got any, want map[string]string) bool {
	m, ok := got.(map[string]string)
	if !ok || len(m) != len(want) {
		return false
	}
	for k, v := range want {
		if m[k] != v {
			return false
		}
	}
	return true
}
