package admission

import (
	"context"
	"regexp"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/access"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/hostcapacity"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/identity"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/runtimeapproval"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/settings"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/webapi"
)

var digest = regexp.MustCompile(`^sha256:[a-f0-9]{64}$`)

type Gate struct {
	pool    *pgxpool.Pool
	runtime *access.Runtime
	http    *httpChecks
}

func NewGate(pool *pgxpool.Pool, runtime *access.Runtime, caPath string) (*Gate, error) {
	if runtime == nil {
		return nil, errObservation
	}
	checks, err := newHTTPChecks(runtime.Rules, runtime.Options.Environment, "http://127.0.0.1:18081", "http://127.0.0.1:18082", caPath)
	if err != nil {
		return nil, err
	}
	return &Gate{pool, runtime, checks}, nil
}
func (g *Gate) Close() { g.http.Close() }
func (g *Gate) Check(ctx context.Context, p identity.Principal) error {
	checks, err := g.Checks(ctx, p)
	if err != nil {
		return err
	}
	for _, check := range checks {
		if check.Health.Status != "HEALTHY" {
			return webapi.Fail(503, "DEPENDENCY_UNAVAILABLE", "运行环境尚未通过创建或启动检查")
		}
	}
	return nil
}

// No network runs while the principal or configuration rows are locked. The
// acceptance transaction rechecks authority and the immutable snapshot later.
func (g *Gate) Checks(ctx context.Context, p identity.Principal) ([]settings.Check, error) {
	return g.checks(ctx, p, true)
}

func (g *Gate) ChecksForResources(ctx context.Context, p identity.Principal, spec settings.Spec) ([]settings.Check, error) {
	return g.checks(ctx, p, true, spec)
}

func (g *Gate) PlatformChecks(ctx context.Context, p identity.Principal) ([]settings.Check, error) {
	return g.checks(ctx, p, false)
}

func (g *Gate) checks(ctx context.Context, p identity.Principal, checkOwner bool, specs ...settings.Spec) ([]settings.Check, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if p.OrganizationID != g.runtime.Options.OrganizationID {
		return nil, identity.ErrForbidden
	}
	tx, err := g.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(context.Background())
	if _, err = identity.LockPrincipal(ctx, tx, p); err != nil {
		return nil, err
	}
	var owner string
	if checkOwner {
		if err = tx.QueryRow(ctx, `SELECT workspace_owner_id::text FROM users WHERE id=$1`, p.UserID).Scan(&owner); err != nil {
			return nil, err
		}
	}
	var expected, version string
	var manifest []byte
	rows, err := tx.Query(ctx, `SELECT image_digest,version::text,runtime_manifest FROM approved_templates WHERE enabled ORDER BY id,version LIMIT 2`)
	if err != nil {
		return nil, err
	}
	count := 0
	for rows.Next() {
		count++
		if err = rows.Scan(&expected, &version, &manifest); err != nil {
			rows.Close()
			return nil, err
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	approved := count == 1 && manifestApproved(manifest, expected, version)
	started := time.Now().UTC()
	expiry := started.Add(15 * time.Second)
	var facts workerFacts
	var workerErr, ownerErr, gatewayErr, ingressErr error
	var group sync.WaitGroup
	group.Add(4)
	go func() { defer group.Done(); facts, workerErr = g.http.worker(ctx) }()
	go func() {
		defer group.Done()
		if checkOwner {
			ownerErr = g.runtime.CheckOwner(ctx, owner)
		}
	}()
	go func() { defer group.Done(); gatewayErr = g.http.gateway(ctx) }()
	go func() { defer group.Done(); ingressErr = g.http.ingress(ctx) }()
	group.Wait()
	add := func(name string, healthy bool, reason string) settings.Check {
		status := "UNHEALTHY"
		if healthy {
			status = "HEALTHY"
			reason = ""
		}
		return settings.Check{Name: name, Health: settings.Health{Status: status, CheckedAt: &started, ExpiresAt: &expiry, Reason: reason}}
	}
	imageMatch := approved && facts.RuntimeImage == expected
	if parsed, e := runtimeapproval.Parse(manifest); e == nil {
		imageMatch = imageMatch && facts.StorageImage == parsed.StorageImage && facts.NetworkImage == parsed.NetworkImage
	}
	capacity := workerErr == nil && facts.Capacity
	if len(specs) > 0 {
		capacity = capacity && (hostcapacity.Facts{CPUs: facts.HostCPUCount, AvailableMemory: facts.HostAvailableMemory}).Fits(specs[0].CPU, specs[0].Memory)
	}
	return []settings.Check{
		add("workspace", workerErr == nil && facts.Workspace && ownerErr == nil, "WORKSPACE_UNAVAILABLE"),
		add("docker", workerErr == nil && facts.Docker, "DEPENDENCY_UNAVAILABLE"),
		add("runtime_manifest", workerErr == nil && imageMatch, "RUNTIME_NOT_APPROVED"),
		add("storage", workerErr == nil && facts.Storage, "STORAGE_UNAVAILABLE"),
		add("capacity", capacity, "CAPACITY_UNAVAILABLE"),
		add("access", gatewayErr == nil && ingressErr == nil, "ACCESS_UNAVAILABLE"),
	}, nil
}

// The approved row is an operator-controlled artifact, not browser input. Even
// an enabled row with an empty/partial manifest must not open command acceptance.
func manifestApproved(raw []byte, image, version string) bool {
	manifest, err := runtimeapproval.Parse(raw)
	return err == nil && manifest.Matches(image, version)
}

var _ settings.RuntimeChecks = (*Gate)(nil)
