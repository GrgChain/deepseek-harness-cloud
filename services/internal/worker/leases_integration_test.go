package worker

import (
	"bytes"
	"context"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jonluo94/deepseek-harness-cloud/db"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/devlocal"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/webapi"
)

func TestRealLeasesFenceConcurrentWorkersAndExpiredWriters(t *testing.T) {
	path := os.Getenv("HC_TEST_DB_DSN_FILE")
	if path == "" {
		t.Skip("isolated PostgreSQL lease database required")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal("test connection unavailable")
	}
	connection, _ := url.Parse(strings.TrimSpace(string(raw)))
	connection.Path = "/hc_test_worker"
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := db.Apply(ctx, connection.String()); err != nil {
		t.Fatal(err)
	}
	admin, err := pgxpool.New(ctx, connection.String())
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	must := func(sql string, args ...any) {
		t.Helper()
		if _, err := admin.Exec(ctx, sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	tx, err := admin.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err = devlocal.GrantWorker(ctx, tx); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	workerRaw, err := os.ReadFile(filepath.Join(filepath.Dir(path), "worker-dsn"))
	if err != nil {
		t.Fatal("restricted worker connection unavailable")
	}
	workerURL, _ := url.Parse(strings.TrimSpace(string(workerRaw)))
	workerURL.Path = connection.Path
	pool, err := pgxpool.New(ctx, workerURL.String())
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	org := "00000000-0000-4000-8000-000000000061"
	user := webapi.ID()
	model := webapi.ID()
	secret := webapi.ID()
	must(`INSERT INTO organizations(id,name,status) VALUES($1,'Lease fixture','ACTIVE') ON CONFLICT(id) DO NOTHING`, org)
	must(`UPDATE outbox_events SET status='DONE' WHERE status='PENDING'`)
	must(`UPDATE operations SET status='FAILED',lease_owner=NULL,lease_expires_at=NULL WHERE status IN ('ACCEPTED','RUNNING','UNCERTAIN')`)
	must(`INSERT INTO users(id,identity_issuer,identity_subject,workspace_owner_id,status) VALUES($1,'https://lease.test',$2,$1,'ACTIVE')`, user, user)
	must(`INSERT INTO memberships(org_id,user_id,role,status) VALUES($1,$2,'MEMBER','ACTIVE')`, org, user)
	must(`INSERT INTO secrets(id,org_id,key_id,nonce,ciphertext) VALUES($1,$2,'test',$3,'x')`, secret, org, bytes.Repeat([]byte{0}, 12))
	must(`INSERT INTO model_configuration_heads(id,org_id) VALUES($1,$2) ON CONFLICT(org_id) DO NOTHING`, model, org)
	if err := admin.QueryRow(ctx, `SELECT id::text FROM model_configuration_heads WHERE org_id=$1`, org).Scan(&model); err != nil {
		t.Fatal(err)
	}
	must(`INSERT INTO model_configurations(org_id,id,version,provider,endpoint,protocol,model,credential_ref,status) VALUES($1,$2,1,'deepseek-official','https://api.deepseek.com/anthropic','deepseek-messages','fixture',$3,'VALID') ON CONFLICT(id,version) DO NOTHING`, org, model, secret)
	ids := map[string]bool{}
	for range 2 {
		instance, operation := webapi.ID(), webapi.ID()
		ids[operation] = true
		must(`INSERT INTO instances(id,org_id,owner_id,name,external_create_key,workspace_name,desired_state,observed_state,model_config_id,model_config_version,config_snapshot) VALUES($1,$2,$3,$4,$1,$4,'RUNNING','CREATING',$5,1,'{}')`, instance, org, user, "dsh-"+instance[:12], model)
		must(`INSERT INTO operations(id,instance_id,actor_id,type,status,stage,intent,trace_id) VALUES($1,$2,$3,'CREATE','ACCEPTED','QUEUED','{"schema_version":1}','lease-test')`, operation, instance, user)
		must(`INSERT INTO outbox_events(id,operation_id,status) VALUES($1,$2,'PENDING')`, webapi.ID(), operation)
	}
	store := NewLeaseStore(pool)
	var wg sync.WaitGroup
	results := make(chan []Lease, 2)
	failures := make(chan error, 2)
	for _, name := range []string{"worker-a", "worker-b"} {
		wg.Add(1)
		go func(name string) {
			defer wg.Done()
			leases, err := store.Claim(ctx, name, 1, false)
			results <- leases
			failures <- err
		}(name)
	}
	wg.Wait()
	close(results)
	close(failures)
	for err := range failures {
		if err != nil {
			t.Fatal(err)
		}
	}
	var held []Lease
	seen := map[string]bool{}
	for leases := range results {
		for _, lease := range leases {
			if !ids[lease.ID] || seen[lease.ID] || lease.Token != 1 {
				t.Fatalf("duplicate/invalid claim: %+v", lease)
			}
			seen[lease.ID] = true
			held = append(held, lease)
		}
	}
	if len(held) != 2 {
		t.Fatalf("workers did not claim distinct work: %d", len(held))
	}
	if leases, err := store.Claim(ctx, "worker-c", 2, false); err != nil || len(leases) != 0 {
		t.Fatalf("active work was reclaimed: %+v %v", leases, err)
	}
	original := held[0]
	forged := original
	forged.Owner = "someone-else"
	if err := store.Renew(ctx, forged); !errors.Is(err, ErrLostLease) {
		t.Fatalf("wrong worker renewed: %v", err)
	}
	if err := store.Renew(ctx, original); err != nil {
		t.Fatal(err)
	}
	must(`UPDATE operations SET lease_expires_at=clock_timestamp()-interval '1 second' WHERE id=$1`, original.ID)
	if err := store.Stage(ctx, original, "BUILDING"); !errors.Is(err, ErrLostLease) {
		t.Fatalf("expired writer published: %v", err)
	}
	leases, err := store.Claim(ctx, "worker-c", 1, false)
	if err != nil || len(leases) != 1 || leases[0].ID != original.ID || leases[0].Token != 2 {
		t.Fatalf("expired work was not fenced: %+v %v", leases, err)
	}
	current := leases[0]
	if err := store.Renew(ctx, original); !errors.Is(err, ErrLostLease) {
		t.Fatalf("old token renewed: %v", err)
	}
	if err := store.Stage(ctx, current, "RECONCILING"); err != nil {
		t.Fatal(err)
	}
	if err := store.Defer(ctx, current, true); err != nil {
		t.Fatal(err)
	}
	var status string
	var attempts int
	if err := admin.QueryRow(ctx, `SELECT o.status,e.attempts FROM operations o JOIN outbox_events e ON e.operation_id=o.id WHERE o.id=$1`, current.ID).Scan(&status, &attempts); err != nil || status != "UNCERTAIN" || attempts != 1 {
		t.Fatalf("unknown result lost durable retry: %s %d %v", status, attempts, err)
	}
	if leases, err := store.Claim(ctx, "worker-d", 1, false); err != nil || len(leases) != 0 {
		t.Fatalf("deferred retry ran early: %+v %v", leases, err)
	}
	// A statement blocked on a row must use actual clock time after the wait,
	// rather than the transaction start timestamp to accept an expired lease.
	locked, err := admin.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	_, err = locked.Exec(ctx, `UPDATE operations SET lease_expires_at=clock_timestamp()-interval '1 second' WHERE id=$1`, held[1].ID)
	if err != nil {
		t.Fatal(err)
	}
	wrote := make(chan error, 1)
	go func() { wrote <- store.Stage(ctx, held[1], "BUILDING") }()
	if err := locked.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err := <-wrote; !errors.Is(err, ErrLostLease) {
		t.Fatalf("lease expired during row wait was published: %v", err)
	}
	t.Run("maintenance pauses new compute but permits cleanup and reconciliation", func(t *testing.T) {
		must(`UPDATE operations SET status='ACCEPTED',lease_owner=NULL,lease_expires_at=NULL WHERE id=ANY($1::uuid[])`, []string{held[0].ID, held[1].ID})
		must(`UPDATE operations SET type='STOP' WHERE id=$1`, held[1].ID)
		must(`UPDATE outbox_events SET retry_at=clock_timestamp(),status='PENDING' WHERE operation_id=ANY($1::uuid[])`, []string{held[0].ID, held[1].ID})
		paused := NewLeaseStore(pool).WithMaintenance(true)
		leases, err := paused.Claim(ctx, "maintenance-worker", 2, false)
		if err != nil || len(leases) != 1 || leases[0].ID != held[1].ID {
			t.Fatalf("maintenance cleanup lane: %d %v", len(leases), err)
		}
		must(`UPDATE operations SET status='UNCERTAIN' WHERE id=$1`, held[0].ID)
		leases, err = paused.Claim(ctx, "maintenance-worker", 2, false)
		if err != nil || len(leases) != 1 || leases[0].ID != held[0].ID {
			t.Fatalf("maintenance failed to reconcile unknown external work: %d %v", len(leases), err)
		}
	})
	if _, err := pool.Exec(ctx, `UPDATE memberships SET role='ADMIN' WHERE org_id=$1 AND user_id=$2`, org, user); err == nil {
		t.Fatal("worker can modify user roles")
	}
}
