package workspace

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"reflect"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/webapi"
)

const EmbeddedEngine = "harness-workspace-v1"

// Client shares the platform database, not Coder's server or migrations.
// Authorization remains in platform membership; the control ID is a service
// identity, never an extra password-login account.
type Client struct {
	pool                  *pgxpool.Pool
	organization, control string
}

func NewEmbedded(pool *pgxpool.Pool, organization, control string) (*Client, error) {
	if pool == nil || !instanceUUID.MatchString(organization) || !instanceUUID.MatchString(control) {
		return nil, ErrUnavailable
	}
	return &Client{pool: pool, organization: organization, control: control}, nil
}
func storeError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	var p *pgconn.PgError
	if errors.As(err, &p) && (p.Code == "23505" || p.Code == "23503" || p.Code == "23514") {
		return ErrConflict
	}
	return ErrUnavailable
}
func (c *Client) BuildInfo(ctx context.Context) (BuildInfo, error) {
	var valid bool
	err := c.pool.QueryRow(ctx, `SELECT organization_id=$1 AND control_user_id=$2 FROM workspace_authority WHERE singleton`, c.organization, c.control).Scan(&valid)
	if err != nil || !valid {
		return BuildInfo{}, ErrUnavailable
	}
	return BuildInfo{Version: Version, Engine: EmbeddedEngine}, nil
}
func (c *Client) User(ctx context.Context, id string) (User, error) {
	if id == "me" {
		if _, err := c.BuildInfo(ctx); err != nil {
			return User{}, err
		}
		return User{ID: c.control, Username: "hc-control", LoginType: "platform", Status: "active", OrganizationIDs: []string{c.organization}, Roles: []Role{{Name: "owner"}}}, nil
	}
	if !instanceUUID.MatchString(id) {
		return User{}, ErrNotFound
	}
	var status string
	err := c.pool.QueryRow(ctx, `SELECT CASE WHEN u.status='ACTIVE' AND m.status='ACTIVE' AND o.status='ACTIVE' THEN 'active' ELSE 'suspended' END FROM users u JOIN memberships m ON m.user_id=u.id JOIN organizations o ON o.id=m.org_id WHERE u.workspace_owner_id=$1`, id).Scan(&status)
	if err != nil {
		return User{}, storeError(err)
	}
	return User{ID: id, Username: "hc-" + id, LoginType: "platform", Status: status, OrganizationIDs: []string{c.organization}, Roles: []Role{{Name: "member"}}}, nil
}

const buildSelect = `SELECT b.id::text,b.workspace_id::text,b.template_version_id::text,b.build_number,b.transition,b.job_id::text,b.job_status,b.error_code,w.owner_id::text,b.initiator_id::text,b.created_at,coalesce(b.completed_at,b.started_at,b.created_at) FROM workspace_builds b JOIN workspace_workspaces w ON w.id=b.workspace_id `

type rowQuerier interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

func readBuild(ctx context.Context, q rowQuerier, where string, args ...any) (Build, error) {
	var b Build
	err := q.QueryRow(ctx, buildSelect+where, args...).Scan(&b.ID, &b.WorkspaceID, &b.TemplateVersionID, &b.BuildNumber, &b.Transition, &b.Job.ID, &b.Job.Status, &b.Job.ErrorCode, &b.OwnerID, &b.InitiatorID, &b.CreatedAt, &b.UpdatedAt)
	if err != nil {
		return b, storeError(err)
	}
	b.Status = string(ConvertWorkspaceStatus(ProvisionerJobStatus(b.Job.Status), WorkspaceTransition(b.Transition)))
	return b, nil
}
func (c *Client) Build(ctx context.Context, id string) (Build, error) {
	if !instanceUUID.MatchString(id) {
		return Build{}, ErrNotFound
	}
	return readBuild(ctx, c.pool, `WHERE b.id=$1`, id)
}
func (c *Client) BuildParameters(ctx context.Context, id string) ([]Parameter, error) {
	var raw []byte
	var p []Parameter
	if !instanceUUID.MatchString(id) {
		return nil, ErrNotFound
	}
	if err := c.pool.QueryRow(ctx, `SELECT parameters FROM workspace_builds WHERE id=$1`, id).Scan(&raw); err != nil {
		return nil, storeError(err)
	}
	if json.Unmarshal(raw, &p) != nil {
		return nil, ErrUnavailable
	}
	return p, nil
}
func readWorkspace(ctx context.Context, q rowQuerier, where string, args ...any) (Workspace, error) {
	var w Workspace
	if err := q.QueryRow(ctx, `SELECT id::text,name,owner_id::text,organization_id::text,template_id::text FROM workspace_workspaces `+where, args...).Scan(&w.ID, &w.Name, &w.OwnerID, &w.OrganizationID, &w.TemplateID); err != nil {
		return w, storeError(err)
	}
	var err error
	w.LatestBuild, err = readBuild(ctx, q, `WHERE b.workspace_id=$1 ORDER BY b.build_number DESC LIMIT 1`, w.ID)
	w.AutomaticUpdates = "never"
	zero := int64(0)
	w.TTLMillis = &zero
	return w, err
}
func (c *Client) Workspace(ctx context.Context, id string) (Workspace, error) {
	if !instanceUUID.MatchString(id) {
		return Workspace{}, ErrNotFound
	}
	return readWorkspace(ctx, c.pool, `WHERE id=$1 AND NOT deleted`, id)
}
func (c *Client) WorkspaceByName(ctx context.Context, owner, name string) (Workspace, error) {
	return c.byName(ctx, owner, name, false)
}
func (c *Client) WorkspaceByNameIncludingDeleted(ctx context.Context, owner, name string) (Workspace, error) {
	return c.byName(ctx, owner, name, true)
}
func (c *Client) byName(ctx context.Context, owner, name string, deleted bool) (Workspace, error) {
	if !instanceUUID.MatchString(owner) {
		return Workspace{}, ErrNotFound
	}
	return readWorkspace(ctx, c.pool, `WHERE owner_id=$1 AND name=$2 AND (NOT deleted OR $3)`, owner, name, deleted)
}
func (c *Client) WorkspaceInventory(ctx context.Context, organization string) ([]Workspace, error) {
	if organization != c.organization {
		return nil, ErrUnavailable
	}
	rows, err := c.pool.Query(ctx, `SELECT id::text FROM workspace_workspaces WHERE organization_id=$1 AND NOT deleted ORDER BY id LIMIT 1001`, organization)
	if err != nil {
		return nil, ErrUnavailable
	}
	var ids []string
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			break
		}
		ids = append(ids, id)
	}
	rows.Close()
	if err != nil || rows.Err() != nil || len(ids) > 1000 {
		return nil, ErrUnavailable
	}
	result := make([]Workspace, 0, len(ids))
	for _, id := range ids {
		w, err := c.Workspace(ctx, id)
		if err != nil {
			return nil, err
		}
		result = append(result, w)
	}
	return result, nil
}
func validParameters(p []Parameter) bool {
	if len(p) != 2 {
		return false
	}
	values := map[string]string{}
	for _, v := range p {
		if (v.Name != "instance_id" && v.Name != "asset_id") || !instanceUUID.MatchString(v.Value) || values[v.Name] != "" {
			return false
		}
		values[v.Name] = v.Value
	}
	return len(values) == 2
}
func parametersEqual(a, b []Parameter) bool {
	if !validParameters(a) || !validParameters(b) {
		return false
	}
	for _, x := range a {
		found := false
		for _, y := range b {
			found = found || x == y
		}
		if !found {
			return false
		}
	}
	return true
}
func (c *Client) CreateWorkspace(ctx context.Context, owner string, in CreateWorkspaceRequest) (Workspace, error) {
	if !validParameters(in.RichParameterValues) || !instanceUUID.MatchString(in.TemplateVersionID) {
		return Workspace{}, ErrConflict
	}
	var instance string
	for _, p := range in.RichParameterValues {
		if p.Name == "instance_id" {
			instance = p.Value
		}
	}
	name, err := WorkspaceName(instance)
	if err != nil || in.Name != name {
		return Workspace{}, ErrConflict
	}
	u, err := c.User(ctx, owner)
	if err != nil || !OwnerStatusUsable(u.Status) {
		return Workspace{}, ErrConflict
	}
	tx, err := c.pool.Begin(ctx)
	if err != nil {
		return Workspace{}, ErrUnavailable
	}
	defer tx.Rollback(context.Background())
	var template string
	if err = tx.QueryRow(ctx, `SELECT template_id::text FROM workspace_template_versions WHERE id=$1 AND organization_id=$2 AND NOT archived`, in.TemplateVersionID, c.organization).Scan(&template); err != nil {
		return Workspace{}, storeError(err)
	}
	id, build, job := webapi.ID(), webapi.ID(), webapi.ID()
	parameters, _ := json.Marshal(in.RichParameterValues)
	if _, err = tx.Exec(ctx, `INSERT INTO workspace_workspaces(id,name,owner_id,organization_id,template_id) VALUES($1,$2,$3,$4,$5)`, id, in.Name, owner, c.organization, template); err != nil {
		return Workspace{}, storeError(err)
	}
	if _, err = tx.Exec(ctx, `INSERT INTO workspace_builds(id,workspace_id,template_version_id,build_number,transition,initiator_id,job_id,parameters) VALUES($1,$2,$3,1,'start',$4,$5,$6)`, build, id, in.TemplateVersionID, c.control, job, parameters); err != nil {
		return Workspace{}, storeError(err)
	}
	w, err := readWorkspace(ctx, tx, `WHERE id=$1`, id)
	if err != nil {
		return w, err
	}
	if err = tx.Commit(ctx); err != nil {
		return Workspace{}, ErrUncertain
	}
	return w, nil
}
func (c *Client) CreateBuild(ctx context.Context, id, transition, version string, parameters []Parameter) (Build, error) {
	if !instanceUUID.MatchString(id) || !instanceUUID.MatchString(version) || (transition != "start" && transition != "stop" && transition != "delete") {
		return Build{}, ErrConflict
	}
	tx, err := c.pool.Begin(ctx)
	if err != nil {
		return Build{}, ErrUnavailable
	}
	defer tx.Rollback(context.Background())
	w, err := readWorkspace(ctx, tx, `WHERE id=$1 AND NOT deleted FOR UPDATE`, id)
	if err != nil {
		return Build{}, err
	}
	if transition == "start" {
		u, err := c.User(ctx, w.OwnerID)
		if err != nil || !OwnerStatusUsable(u.Status) {
			return Build{}, ErrConflict
		}
	}
	var valid bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM workspace_template_versions WHERE id=$1 AND template_id=$2 AND organization_id=$3 AND (NOT archived OR $4))`, version, w.TemplateID, c.organization, transition != "start").Scan(&valid); err != nil || !valid {
		return Build{}, ErrConflict
	}
	if w.LatestBuild.Job.Status == "pending" || w.LatestBuild.Job.Status == "running" {
		return Build{}, ErrConflict
	}
	var previous []byte
	var old []Parameter
	if err = tx.QueryRow(ctx, `SELECT parameters FROM workspace_builds WHERE id=$1`, w.LatestBuild.ID).Scan(&previous); err != nil || json.Unmarshal(previous, &old) != nil {
		return Build{}, ErrUnavailable
	}
	if len(parameters) > 0 && !parametersEqual(parameters, old) {
		return Build{}, ErrConflict
	}
	build, job := webapi.ID(), webapi.ID()
	if _, err = tx.Exec(ctx, `INSERT INTO workspace_builds(id,workspace_id,template_version_id,build_number,transition,initiator_id,job_id,parameters) VALUES($1,$2,$3,$4,$5,$6,$7,$8)`, build, id, version, w.LatestBuild.BuildNumber+1, transition, c.control, job, previous); err != nil {
		return Build{}, storeError(err)
	}
	b, err := readBuild(ctx, tx, `WHERE b.id=$1`, build)
	if err != nil {
		return b, err
	}
	if err = tx.Commit(ctx); err != nil {
		return Build{}, ErrUncertain
	}
	return b, nil
}

func (c *Client) Template(ctx context.Context, id string) (Template, error) {
	var t Template
	if !instanceUUID.MatchString(id) {
		return t, ErrNotFound
	}
	err := c.pool.QueryRow(ctx, `SELECT id::text,organization_id::text,name,active_version_id::text,deleted FROM workspace_templates WHERE id=$1`, id).Scan(&t.ID, &t.OrganizationID, &t.Name, &t.ActiveVersion, &t.Deleted)
	return t, storeError(err)
}
func (c *Client) TemplateVersion(ctx context.Context, id string) (TemplateVersion, error) {
	var v TemplateVersion
	if !instanceUUID.MatchString(id) {
		return v, ErrNotFound
	}
	err := c.pool.QueryRow(ctx, `SELECT id::text,template_id::text,organization_id::text,name,archived,file_id::text,created_by::text FROM workspace_template_versions WHERE id=$1`, id).Scan(&v.ID, &v.TemplateID, &v.OrganizationID, &v.Name, &v.Archived, &v.Job.FileID, &v.Job.InitiatorID)
	v.Job.ID = v.ID
	v.Job.Status = "succeeded"
	v.Job.OrganizationID = v.OrganizationID
	return v, storeError(err)
}
func (c *Client) TemplateSource(ctx context.Context, id string) (map[string][]byte, error) {
	var raw []byte
	var out map[string][]byte
	if !instanceUUID.MatchString(id) {
		return nil, ErrNotFound
	}
	if err := c.pool.QueryRow(ctx, `SELECT source FROM workspace_template_versions WHERE file_id=$1`, id).Scan(&raw); err != nil {
		return nil, storeError(err)
	}
	if json.Unmarshal(raw, &out) != nil {
		return nil, ErrUnavailable
	}
	return out, nil
}
func (c *Client) TemplateVariables(ctx context.Context, id string) ([]Variable, error) {
	v, err := c.TemplateVersion(ctx, id)
	if err != nil {
		return nil, err
	}
	source, err := c.TemplateSource(ctx, v.Job.FileID)
	if err != nil {
		return nil, err
	}
	var values map[string]string
	if json.Unmarshal(source["variables.json"], &values) != nil || len(values) != 2 {
		return nil, ErrUnavailable
	}
	return []Variable{{Name: "runtime_image", Value: values["runtime_image"]}, {Name: "runtime_root", Value: values["runtime_root"]}}, nil
}

// PublishTemplate is operator-only. Runtime roles cannot write template tables.
// The version ID, bytes and name are immutable; replay of identical input is safe.
func (c *Client) PublishTemplate(ctx context.Context, template, version, file, name string, source map[string][]byte) error {
	if c.pool == nil || !instanceUUID.MatchString(template) || !instanceUUID.MatchString(version) || !instanceUUID.MatchString(file) || len(name) < 1 || len(name) > 128 || len(source) != 3 {
		return ErrConflict
	}
	for _, k := range []string{"main.tf", ".terraform.lock.hcl", "variables.json"} {
		if len(source[k]) == 0 || len(source[k]) > 1<<20 {
			return ErrConflict
		}
	}
	var vars map[string]string
	if json.Unmarshal(source["variables.json"], &vars) != nil || len(vars) != 2 || vars["runtime_image"] == "" || vars["runtime_root"] == "" {
		return ErrConflict
	}
	raw, _ := json.Marshal(source)
	sum := sha256.Sum256(raw)
	sha := hex.EncodeToString(sum[:])
	tx, err := c.pool.Begin(ctx)
	if err != nil {
		return ErrUnavailable
	}
	defer tx.Rollback(context.Background())
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(731804529)`); err != nil {
		return ErrUnavailable
	}
	var existing []byte
	var existingName, existingTemplate, existingFile string
	err = tx.QueryRow(ctx, `SELECT source,name,template_id::text,file_id::text FROM workspace_template_versions WHERE id=$1`, version).Scan(&existing, &existingName, &existingTemplate, &existingFile)
	if err == nil {
		var prior map[string][]byte
		_ = json.Unmarshal(existing, &prior)
		if !reflect.DeepEqual(prior, source) || existingName != name || existingTemplate != template || existingFile != file {
			return ErrConflict
		}
		return tx.Commit(ctx)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return ErrUnavailable
	}
	if _, err = tx.Exec(ctx, `INSERT INTO workspace_templates(id,organization_id,name,active_version_id) VALUES($1,$2,'hc-dsh-initial',$3) ON CONFLICT(id) DO NOTHING`, template, c.organization, version); err != nil {
		return storeError(err)
	}
	if _, err = tx.Exec(ctx, `INSERT INTO workspace_template_versions(id,template_id,organization_id,name,file_id,source,source_sha256,created_by) VALUES($1,$2,$3,$4,$5,$6,$7,$8)`, version, template, c.organization, name, file, raw, sha, c.control); err != nil {
		return storeError(err)
	}
	if _, err = tx.Exec(ctx, `UPDATE workspace_templates SET active_version_id=$2 WHERE id=$1 AND organization_id=$3`, template, version, c.organization); err != nil {
		return storeError(err)
	}
	return storeError(tx.Commit(ctx))
}
