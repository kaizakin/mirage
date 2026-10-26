package store

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/kaizakin/mirage/internal/secretbox"
)

// HostedStatus is where a hosted server's deployment is:
//
//	deploying ──available──▶ running
//	    │  ▲                    │
//	    │  └──image or env changed──┘
//	    └──timeout or crash──▶ failed ──image or env changed──▶ deploying
type HostedStatus string

// Hosted server statuses.
const (
	HostedDeploying HostedStatus = "deploying"
	HostedRunning   HostedStatus = "running"
	HostedFailed    HostedStatus = "failed"
)

// HostedPrefix names a hosted server's namespace.
const HostedPrefix = "mcp-"

// HostedSpec is what the platform runs for a hosted server.
type HostedSpec struct {
	Image string
	Port  int
	Path  string
	Env   map[string]string
}

// hostedService is the Service the hosted chart creates, named after its release.
const hostedService = "mcp"

// HostedNamespace is the namespace that holds the hosted server with slug.
func HostedNamespace(slug string) string { return HostedPrefix + slug }

// URL is where the gateway reaches the hosted server with slug.
func (h HostedSpec) URL(slug string) string {
	return fmt.Sprintf("http://%s.%s.svc.cluster.local:%d%s", hostedService, HostedNamespace(slug), h.Port, h.Path)
}

// Hosted is a hosted server as the provisioner reconciles it.
type Hosted struct {
	ServerID           int64
	Slug               string
	Image              string
	Port               int
	Path               string
	EnvEnc             []byte
	Status             HostedStatus
	Reason             string
	Generation         int
	DeployedGeneration int
	DeployingSince     time.Time
}

// Namespace is the Kubernetes namespace that holds the server.
func (h Hosted) Namespace() string { return HostedNamespace(h.Slug) }

// Env opens the server's environment.
func (h Hosted) Env(box *secretbox.Box) (map[string]string, error) {
	env := map[string]string{}
	if h.EnvEnc == nil {
		return env, nil
	}
	plain, err := box.Decrypt(h.EnvEnc, hostedEnvAD(h.Slug))
	if err != nil {
		return nil, fmt.Errorf("opening the environment of %s: %w", h.Slug, err)
	}
	return env, json.Unmarshal(plain, &env)
}

func hostedEnvAD(slug string) []byte { return []byte("hosted-env:" + slug) }

func sealEnv(box *secretbox.Box, slug string, env map[string]string) ([]byte, error) {
	if len(env) == 0 {
		return nil, nil
	}
	plain, err := json.Marshal(env)
	if err != nil {
		return nil, err
	}
	return box.Encrypt(plain, hostedEnvAD(slug))
}

// CreateHostedServer adds an enabled server that the platform runs from spec. The platform
// chooses its address, inside the cluster, so it is internal. It returns ErrConflict for a
// taken slug and ErrInvalid when the fields break a rule.
func (s *Store) CreateHostedServer(ctx context.Context, box *secretbox.Box, seed ServerSeed, spec HostedSpec) error {
	credential, err := seal(box, seed.Slug, seed.Credential)
	if err != nil {
		return err
	}
	env, err := sealEnv(box, seed.Slug, spec.Env)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `
		WITH srv AS (
			INSERT INTO servers (slug, url, auth_type, auth_header, credential_enc, internal)
			VALUES ($1, $2, $3, nullif($4, ''), $5, true) RETURNING id
		)
		INSERT INTO hosted_servers (server_id, image, port, path, env_enc)
		SELECT id, $6, $7, $8, $9 FROM srv`,
		seed.Slug, spec.URL(seed.Slug), seed.AuthType, seed.AuthHeader, credential, spec.Image, spec.Port, spec.Path, env)
	return classify(err)
}

// HostedPatch changes a hosted server's image or environment; nil fields stay as they are.
type HostedPatch struct {
	Image *string
	Env   map[string]string // nil keeps the environment; empty clears it
}

// UpdateHostedServer applies p and asks the provisioner to deploy again. It returns
// ErrNotFound when slug is not a hosted server.
func (s *Store) UpdateHostedServer(ctx context.Context, box *secretbox.Box, slug string, p HostedPatch) error {
	var env []byte
	if p.Env != nil {
		var err error
		if env, err = sealEnv(box, slug, p.Env); err != nil {
			return err
		}
	}
	res, err := s.db.ExecContext(ctx, `
		UPDATE hosted_servers h SET
			image = coalesce($2, h.image),
			env_enc = CASE WHEN $3 THEN $4 ELSE h.env_enc END,
			generation = h.generation + 1,
			status = 'deploying', reason = NULL, deploying_since = now()
		FROM servers s WHERE s.id = h.server_id AND s.slug = $1`,
		slug, p.Image, p.Env != nil, env)
	return affected(res, classify(err))
}

// HostedServers lists every hosted server.
func (s *Store) HostedServers(ctx context.Context) ([]Hosted, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT h.server_id, s.slug, h.image, h.port, h.path, h.env_enc, h.status, coalesce(h.reason, ''),
			h.generation, h.deployed_generation, h.deploying_since
		FROM hosted_servers h JOIN servers s ON s.id = h.server_id ORDER BY s.slug`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Hosted
	for rows.Next() {
		var h Hosted
		if err := rows.Scan(&h.ServerID, &h.Slug, &h.Image, &h.Port, &h.Path, &h.EnvEnc, &h.Status, &h.Reason,
			&h.Generation, &h.DeployedGeneration, &h.DeployingSince); err != nil {
			return nil, err
		}
		out = append(out, h)
	}
	return out, rows.Err()
}

// MarkHostedDeployed records that generation was handed to the cluster.
func (s *Store) MarkHostedDeployed(ctx context.Context, serverID int64, generation int) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE hosted_servers SET deployed_generation = $2 WHERE server_id = $1 AND deployed_generation < $2`, serverID, generation)
	return err
}

// MarkHostedRunning moves a deploying server to running, unless it changed meanwhile.
func (s *Store) MarkHostedRunning(ctx context.Context, serverID int64, generation int) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE hosted_servers SET status = 'running', reason = NULL
		WHERE server_id = $1 AND generation = $2 AND status = 'deploying'`, serverID, generation)
	return err
}

// MarkHostedFailed records why generation cannot run, unless the server changed meanwhile.
func (s *Store) MarkHostedFailed(ctx context.Context, serverID int64, generation int, reason string) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE hosted_servers SET status = 'failed', reason = $3
		WHERE server_id = $1 AND generation = $2 AND status <> 'failed'`, serverID, generation, reason)
	return err
}
