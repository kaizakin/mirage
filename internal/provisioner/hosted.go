package provisioner

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/kaizakin/mirage/internal/secretbox"
	"github.com/kaizakin/mirage/internal/store"
)

// HostedStore is the part of the database the hosting reconciler works from.
type HostedStore interface {
	HostedServers(ctx context.Context) ([]store.Hosted, error)
	MarkHostedDeployed(ctx context.Context, serverID int64, generation int) error
	MarkHostedRunning(ctx context.Context, serverID int64, generation int) error
	MarkHostedFailed(ctx context.Context, serverID int64, generation int, reason string) error
}

// HostedCluster is what hosting does to Kubernetes. All of it is idempotent.
type HostedCluster interface {
	EnsureHostedNamespace(ctx context.Context, namespace, slug string) error
	DeleteNamespace(ctx context.Context, namespace string) (gone bool, err error)
	// HostedNamespaces lists the namespaces created for hosted servers.
	HostedNamespaces(ctx context.Context) ([]string, error)
	HostedRelease(ctx context.Context, namespace string) (ReleaseState, error)
	InstallHosted(ctx context.Context, namespace string, values map[string]any) error
	UpgradeHosted(ctx context.Context, namespace string, values map[string]any) error
	UninstallHosted(ctx context.Context, namespace string) error
	HostedHealth(ctx context.Context, namespace string) (Health, error)
}

// hostedRelease is the Helm release name in every hosted namespace; its Deployment and
// Service are named after it, and store.HostedSpec.URL points at that Service.
const hostedRelease = "mcp"

// HostingConfig tunes a Hosting.
type HostingConfig struct {
	// Timeout bounds one deployment, from when it was asked for. Default 3m.
	Timeout time.Duration
	// GatewayNamespace is where the gateway runs; only it may call a hosted server.
	GatewayNamespace string
	// Interval between passes; default 3s.
	Interval time.Duration
}

// Hosting reconciles hosted servers, level triggered like the store Provisioner: each pass
// moves every server one step toward its row, and removes the namespaces of servers that
// are gone. A failed deployment leaves its resources alone: after a bad upgrade the previous
// version keeps serving until an admin sets an image that works.
type Hosting struct {
	db      HostedStore
	cluster HostedCluster
	box     *secretbox.Box
	cfg     HostingConfig
	now     func() time.Time
}

// NewHosting returns a Hosting.
func NewHosting(db HostedStore, cluster HostedCluster, box *secretbox.Box, cfg HostingConfig) *Hosting {
	if cfg.Timeout <= 0 {
		cfg.Timeout = 3 * time.Minute
	}
	if cfg.Interval <= 0 {
		cfg.Interval = 3 * time.Second
	}
	return &Hosting{db: db, cluster: cluster, box: box, cfg: cfg, now: time.Now}
}

// Run reconciles on every interval until ctx ends.
func (h *Hosting) Run(ctx context.Context) error {
	ticker := time.NewTicker(h.cfg.Interval)
	defer ticker.Stop()
	for {
		passCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
		h.Pass(passCtx)
		cancel()
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}

// Pass takes one step for every hosted server and collects the namespaces of deleted ones.
func (h *Hosting) Pass(ctx context.Context) {
	servers, err := h.db.HostedServers(ctx)
	if err != nil {
		slog.Error("listing hosted servers", "err", err)
		return
	}
	wanted := map[string]bool{}
	for _, srv := range servers {
		wanted[srv.Namespace()] = true
		if err := h.Step(ctx, srv); err != nil && ctx.Err() == nil {
			slog.Warn("hosted server step failed; retrying", "server", srv.Slug, "err", err)
		}
	}
	namespaces, err := h.cluster.HostedNamespaces(ctx)
	if err != nil {
		slog.Error("listing hosted namespaces", "err", err)
		return
	}
	for _, ns := range namespaces {
		if wanted[ns] {
			continue
		}
		if gone, err := h.cluster.DeleteNamespace(ctx, ns); err != nil {
			slog.Warn("deleting the namespace of a removed hosted server", "namespace", ns, "err", err)
		} else if gone {
			slog.Info("hosted server removed", "namespace", ns)
		}
	}
}

// Step takes the next step toward srv's row.
func (h *Hosting) Step(ctx context.Context, srv store.Hosted) error {
	if srv.Status == store.HostedFailed {
		return nil
	}
	ns := srv.Namespace()
	if err := h.cluster.EnsureHostedNamespace(ctx, ns, srv.Slug); err != nil {
		return err
	}
	state, err := h.cluster.HostedRelease(ctx, ns)
	if err != nil {
		return err
	}
	switch {
	case state == ReleaseStuck:
		return h.cluster.UninstallHosted(ctx, ns)
	case state == ReleaseAbsent:
		// Also puts back a running server whose namespace was deleted from outside.
		return h.deploy(ctx, srv, h.cluster.InstallHosted)
	case srv.DeployedGeneration < srv.Generation:
		return h.deploy(ctx, srv, h.cluster.UpgradeHosted)
	case srv.Status == store.HostedRunning:
		return nil
	}
	health, err := h.cluster.HostedHealth(ctx, ns)
	switch {
	case err != nil:
		return err
	case health.Ready:
		slog.Info("hosted server running", "server", srv.Slug, "image", srv.Image)
		return h.db.MarkHostedRunning(ctx, srv.ServerID, srv.Generation)
	case health.Broken != "":
		return h.fail(ctx, srv, health.Broken)
	case h.now().Sub(srv.DeployingSince) > h.cfg.Timeout:
		return h.fail(ctx, srv, fmt.Sprintf("not running after %s: %s", h.cfg.Timeout, health.Waiting))
	}
	return nil
}

func (h *Hosting) deploy(ctx context.Context, srv store.Hosted, apply func(context.Context, string, map[string]any) error) error {
	env, err := srv.Env(h.box)
	if err != nil {
		return err
	}
	values := map[string]any{"image": srv.Image, "port": srv.Port, "env": env, "gatewayNamespace": h.cfg.GatewayNamespace}
	if err := apply(ctx, srv.Namespace(), values); err != nil {
		return err
	}
	return h.db.MarkHostedDeployed(ctx, srv.ServerID, srv.Generation)
}

func (h *Hosting) fail(ctx context.Context, srv store.Hosted, reason string) error {
	slog.Warn("hosted server failed", "server", srv.Slug, "image", srv.Image, "reason", reason)
	return h.db.MarkHostedFailed(ctx, srv.ServerID, srv.Generation, reason)
}
