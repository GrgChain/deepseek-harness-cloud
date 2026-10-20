package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/runtimeapproval"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/webapi"
)

// This explicit maintenance migration publishes new immutable versions for the
// label change. It never rewrites old template bytes, builds or audit records.
func migrateWorkspaceNames(ctx context.Context, pool *pgxpool.Pool, operator string) error {
	if operator == "" || os.Getenv("HC_MAINTENANCE") != "true" {
		return errWorkspace
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		return errWorkspace
	}
	defer tx.Rollback(context.Background())
	if _, err = tx.Exec(ctx, `SET LOCAL lock_timeout='2s'; SELECT pg_advisory_xact_lock(731804529); LOCK TABLE instances,operations,workspace_workspaces,workspace_builds,workspace_templates,workspace_template_versions,approved_templates,organization_defaults IN SHARE ROW EXCLUSIVE MODE`); err != nil {
		return errWorkspace
	}
	var safe bool
	if err = tx.QueryRow(ctx, `SELECT NOT EXISTS(SELECT 1 FROM instances WHERE desired_state NOT IN ('STOPPED','DELETED') OR observed_state NOT IN ('STOPPED','DELETED')) AND NOT EXISTS(SELECT 1 FROM operations WHERE status IN ('ACCEPTED','RUNNING','UNCERTAIN')) AND NOT EXISTS(SELECT 1 FROM workspace_builds WHERE job_status IN ('pending','running')) AND NOT EXISTS(SELECT 1 FROM access_routes WHERE enabled) AND NOT EXISTS(SELECT 1 FROM quota_reservations WHERE status IN ('HELD','USED') AND resource_type IN ('CPU','MEMORY','RUNNING','CONCURRENT'))`).Scan(&safe); err != nil || !safe {
		return errWorkspace
	}
	var organization, control string
	if err = tx.QueryRow(ctx, `SELECT organization_id::text,control_user_id::text FROM workspace_authority WHERE singleton`).Scan(&organization, &control); err != nil {
		return errWorkspace
	}
	rows, err := tx.Query(ctx, `SELECT a.id::text,a.version::text,a.runtime_manifest,a.enabled,v.source FROM approved_templates a JOIN workspace_template_versions v ON v.id=a.version AND v.template_id=a.id WHERE NOT v.archived ORDER BY a.id,a.version`)
	if err != nil {
		return errWorkspace
	}
	type entry struct {
		template, version string
		manifest, source  []byte
		enabled           bool
	}
	var entries []entry
	for rows.Next() {
		var e entry
		if err = rows.Scan(&e.template, &e.version, &e.manifest, &e.enabled, &e.source); err != nil {
			break
		}
		entries = append(entries, e)
	}
	rows.Close()
	if err != nil || rows.Err() != nil || len(entries) > 16 {
		return errWorkspace
	}
	mappings := map[string]string{}
	for _, e := range entries {
		var source map[string][]byte
		if json.Unmarshal(e.source, &source) != nil || len(source) != 3 {
			return errWorkspace
		}
		main := source["main.tf"]
		if !bytes.Contains(main, []byte("hc.coder_")) {
			if !bytes.Contains(main, []byte("hc.workspace_owner_id")) || !bytes.Contains(main, []byte("hc.workspace_id")) {
				return errWorkspace
			}
			continue
		}
		if bytes.Count(main, []byte("hc.coder_owner_id")) != 1 || bytes.Count(main, []byte("hc.coder_workspace_id")) != 1 {
			return errWorkspace
		}
		source["main.tf"] = bytes.ReplaceAll(bytes.ReplaceAll(main, []byte("hc.coder_owner_id"), []byte("hc.workspace_owner_id")), []byte("hc.coder_workspace_id"), []byte("hc.workspace_id"))
		if bytes.Contains(source["main.tf"], []byte("hc.coder_")) {
			return errWorkspace
		}
		var variables map[string]string
		if json.Unmarshal(source["variables.json"], &variables) != nil || len(variables) != 2 {
			return errWorkspace
		}
		image, _ := json.Marshal(variables["runtime_image"])
		root, _ := json.Marshal(variables["runtime_root"])
		canonical := []byte(`{"runtime_image": ` + string(image) + `, "runtime_root": ` + string(root) + `}`)
		content := append(append(append([]byte{}, source["main.tf"]...), source[".terraform.lock.hcl"]...), canonical...)
		name := "v0-" + runtimeapproval.SHA(content)[:20]
		version, file := webapi.ID(), webapi.ID()
		encoded, _ := json.Marshal(source)
		if _, err = tx.Exec(ctx, `INSERT INTO workspace_template_versions(id,template_id,organization_id,name,file_id,source,source_sha256,created_by) VALUES($1,$2,$3,$4,$5,$6,$7,$8)`, version, e.template, organization, name, file, encoded, runtimeapproval.SHA(encoded), control); err != nil {
			return errWorkspace
		}
		manifest, parseErr := runtimeapproval.Parse(e.manifest)
		if parseErr != nil || manifest.TemplateVersion != e.version || manifest.Image != variables["runtime_image"] {
			return errWorkspace
		}
		manifest.TemplateVersion = version
		raw, _ := json.Marshal(manifest)
		if _, err = tx.Exec(ctx, `UPDATE approved_templates SET enabled=false WHERE id=$1 AND version=$2;`, e.template, e.version); err != nil {
			return errWorkspace
		}
		if _, err = tx.Exec(ctx, `INSERT INTO approved_templates(id,version,name,image_digest,runtime_manifest,enabled) VALUES($1,$2,'hc-dsh-initial',$3,$4,$5)`, e.template, version, manifest.Image, raw, e.enabled); err != nil {
			return errWorkspace
		}
		if _, err = tx.Exec(ctx, `UPDATE instances SET config_snapshot=jsonb_set(config_snapshot,'{template_version}',to_jsonb($2::text)),version=version+1,access_generation=access_generation+1,updated_at=clock_timestamp() WHERE config_snapshot->>'template_version'=$1`, e.version, version); err != nil {
			return errWorkspace
		}
		if _, err = tx.Exec(ctx, `UPDATE organization_defaults SET template_version=$2,version=version+1 WHERE template_version=$1`, e.version, version); err != nil {
			return errWorkspace
		}
		if _, err = tx.Exec(ctx, `UPDATE workspace_templates SET active_version_id=$2 WHERE id=$1 AND active_version_id=$3`, e.template, version, e.version); err != nil {
			return errWorkspace
		}
		if _, err = tx.Exec(ctx, `UPDATE workspace_template_versions SET archived=true WHERE id=$1`, e.version); err != nil {
			return errWorkspace
		}
		mappings[e.version] = version
	}
	if len(mappings) > 0 {
		var org string
		if err = tx.QueryRow(ctx, `SELECT id::text FROM organizations`).Scan(&org); err != nil {
			return errWorkspace
		}
		raw, _ := json.Marshal(mappings)
		if _, err = tx.Exec(ctx, `INSERT INTO audit_events(id,org_id,resource_id,action,outcome,trace_id,details) VALUES($1::uuid,$2,$2,'WORKSPACE_NAMES_MIGRATE','SUCCEEDED',$1::text,jsonb_build_object('schema_version',1,'operator_ref',$3::text,'version_mapping',$4::jsonb,'reason_code','WORKSPACE_LABEL_NAMES'))`, webapi.ID(), org, operator, raw); err != nil {
			return errWorkspace
		}
	}
	return tx.Commit(ctx)
}

func runWorkspaceNames(ctx context.Context, pool *pgxpool.Pool, root string) error {
	manifestPath := os.Getenv("HC_RUNTIME_MANIFEST_FILE")
	if !filepath.IsAbs(manifestPath) || filepath.Clean(manifestPath) != manifestPath {
		return errWorkspace
	}
	for _, dir := range []string{root, filepath.Dir(manifestPath)} {
		info, err := os.Lstat(dir)
		if err != nil || !info.IsDir() || info.Mode().Perm()&0077 != 0 || info.Mode()&os.ModeSymlink != 0 {
			return errWorkspace
		}
	}

	if err := migrateWorkspaceNames(ctx, pool, os.Getenv("HC_OPERATOR_REF")); err != nil {
		return err
	}
	var template, version string
	var raw, sourceRaw []byte
	if err := pool.QueryRow(ctx, `SELECT a.id::text,a.version::text,a.runtime_manifest,v.source FROM approved_templates a JOIN workspace_template_versions v ON v.id=a.version WHERE a.enabled`).Scan(&template, &version, &raw, &sourceRaw); err != nil {
		return errWorkspace
	}
	m, err := runtimeapproval.Parse(raw)
	if err != nil {
		return errWorkspace
	}
	var source map[string][]byte
	var variables map[string]string
	if json.Unmarshal(sourceRaw, &source) != nil || json.Unmarshal(source["variables.json"], &variables) != nil {
		return errWorkspace
	}
	image, _ := json.Marshal(variables["runtime_image"])
	runtimeRoot, _ := json.Marshal(variables["runtime_root"])
	canonical := []byte(`{"runtime_image": ` + string(image) + `, "runtime_root": ` + string(runtimeRoot) + `}`)
	content := append(append(append([]byte{}, source["main.tf"]...), source[".terraform.lock.hcl"]...), canonical...)
	var organization string
	if err = pool.QueryRow(ctx, `SELECT organization_id::text FROM workspace_authority`).Scan(&organization); err != nil {
		return errWorkspace
	}
	c := runtimeapproval.Contract{TemplateID: template, TemplateVersion: version, OrganizationID: organization, WorkspaceSourceVersion: m.WorkspaceSourceVersion, TerraformVersion: m.TerraformVersion, WorkspaceProviderVersion: "2.19.0", DockerProvider: "4.6.0", SourceSHA: runtimeapproval.SHA(content), Image: m.Image}
	contract, _ := json.MarshalIndent(c, "", "  ")
	if err = writeWorkspaceFile(root+"/template-contract.json", contract); err != nil {
		return err
	}
	return writeWorkspaceFile(manifestPath, raw)
}
