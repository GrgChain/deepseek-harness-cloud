package instances

import (
	"context"
	"encoding/json"
	"errors"
	"regexp"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/identity"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/pagination"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/webapi"
)

var idPattern = regexp.MustCompile(`^[a-f0-9]{8}-[a-f0-9]{4}-4[a-f0-9]{3}-[89ab][a-f0-9]{3}-[a-f0-9]{12}$`)
var prefixPattern = regexp.MustCompile(`^[\p{L}\p{N}_-]{0,32}$`)
var digestPattern = regexp.MustCompile(`^sha256:[a-f0-9]{64}$`)

type QueryService struct {
	pool   *pgxpool.Pool
	cursor *pagination.Codec
}

func NewQueryService(pool *pgxpool.Pool, cursor *pagination.Codec) *QueryService {
	return &QueryService{pool: pool, cursor: cursor}
}
func (s *QueryService) begin(ctx context.Context, p identity.Principal, permission string) (pgx.Tx, time.Time, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, time.Time{}, err
	}
	if err = identity.RequirePermission(ctx, tx, p, permission); err != nil {
		_ = tx.Rollback(context.Background())
		return nil, time.Time{}, err
	}
	var now time.Time
	if err = tx.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&now); err != nil {
		_ = tx.Rollback(context.Background())
		return nil, time.Time{}, err
	}
	return tx, now.UTC(), nil
}
func invalidQuery() error  { return webapi.Fail(422, "INVALID_SPEC", "查询参数不合法") }
func queryNotFound() error { return webapi.Fail(404, "NOT_FOUND", "资源不存在或不可访问") }
func invalidCursor() error {
	return webapi.Fail(422, "INVALID_CURSOR", "分页游标无效或已过期，请刷新列表")
}
func validState(state string) bool {
	switch state {
	case "", "CREATING", "STARTING", "RUNNING", "STOPPING", "STOPPED", "ERROR", "DELETING", "DELETED":
		return true
	}
	return false
}

const selectInstance = `SELECT i.id::text,i.org_id::text,i.owner_id::text,i.name,i.backend_type,i.workspace_id::text,i.storage_asset_id::text,i.desired_state,i.observed_state,i.version,i.model_config_id::text,i.model_config_version,i.config_snapshot,i.created_at,i.updated_at,i.deleted_at,
 a.id::text,a.status,a.capacity_bytes,a.retention_until,COALESCE(a.init_version>=1 AND a.filesystem_id IS NOT NULL,false),r.host,COALESCE(r.enabled,false),
 (SELECT op.id::text FROM operations op WHERE op.instance_id=i.id AND op.status IN ('ACCEPTED','RUNNING','UNCERTAIN')),
 (SELECT op.id::text FROM operations op WHERE op.instance_id=i.id ORDER BY op.created_at DESC,op.id DESC LIMIT 1),
 COALESCE((SELECT jsonb_object_agg(h.dimension,jsonb_build_object('status',h.status,'checked_at',h.checked_at,'expires_at',h.expires_at,'reason_code',h.reason_code)) FROM instance_health h WHERE h.instance_id=i.id),'{}'::jsonb),
 COALESCE(c.revoked_at IS NULL AND sec.revoked_at IS NULL AND c.status<>'REVOKED',false),COALESCE(c.status='VALID' AND c.revoked_at IS NULL AND sec.revoked_at IS NULL AND mv.status='HEALTHY' AND mv.checked_at<=clock_timestamp() AND mv.expires_at>clock_timestamp(),false),
 CASE WHEN mv.status='CHECKING' AND mv.lease_expires_at>clock_timestamp() THEN 'CHECKING' WHEN mv.status='CHECKING' THEN 'UNKNOWN' ELSE COALESCE(mv.status,'UNKNOWN') END,
 CASE WHEN mv.status='CHECKING' THEN mv.lease_expires_at-interval '45 seconds' ELSE mv.checked_at END,
 CASE WHEN mv.status='CHECKING' THEN mv.lease_expires_at ELSE mv.expires_at END,
 CASE WHEN mv.status='CHECKING' AND mv.lease_expires_at>clock_timestamp() THEN '' WHEN mv.status='CHECKING' THEN 'HEALTH_EXPIRED' ELSE COALESCE(mv.reason_code,'HEALTH_REQUIRED') END,
 COALESCE(NULLIF(pv.manifest->'catalog'->>'model',''),CASE WHEN c.provider='deepseek-official' THEN c.model ELSE '' END),
 COALESCE(CASE WHEN mp.id IN ('custom-openai','custom-openai-responses','custom-anthropic') THEN '自定义' ELSE NULLIF(mp.name,'') END,CASE WHEN c.provider='deepseek-official' THEN 'DeepSeek' ELSE '' END)
 FROM instances i LEFT JOIN storage_assets a ON a.id=i.storage_asset_id LEFT JOIN access_routes r ON r.instance_id=i.id
 JOIN model_configurations c ON c.org_id=i.org_id AND c.id=i.model_config_id AND c.version=i.model_config_version
 JOIN secrets sec ON sec.org_id=c.org_id AND sec.id=c.credential_ref
 LEFT JOIN model_validations mv ON mv.config_id=c.id AND mv.config_version=c.version
 LEFT JOIN user_model_connection_versions pv ON pv.org_id=i.org_id AND pv.owner_id=i.owner_id AND pv.model_configuration_id=i.model_config_id AND pv.model_configuration_version=i.model_config_version
 LEFT JOIN model_providers mp ON mp.id=pv.manifest->'catalog'->>'provider'`

func scanInstance(row pgx.Row, now time.Time) (InstanceView, error) {
	var view InstanceView
	var snapshot, health []byte
	var assetID, assetStatus *string
	var capacity *int64
	var retained *time.Time
	var routeEnabled, credentialUsable, modelUsable, assetInitialized bool
	var modelHealth HealthView
	err := row.Scan(&view.ID, &view.OrganizationID, &view.OwnerID, &view.Name, &view.Backend, &view.WorkspaceID, &view.AssetID, &view.Desired, &view.Observed, &view.Version, &view.ModelID, &view.ModelVersion, &snapshot, &view.CreatedAt, &view.UpdatedAt, &view.DeletedAt, &assetID, &assetStatus, &capacity, &retained, &assetInitialized, &view.AccessHost, &routeEnabled, &view.ActiveOperationID, &view.LatestOperationID, &health, &credentialUsable, &modelUsable, &modelHealth.Status, &modelHealth.CheckedAt, &modelHealth.ExpiresAt, &modelHealth.Reason, &view.ModelDisplayName, &view.ModelProviderName)
	if err != nil {
		return view, err
	}
	view.Snapshot, err = DecodeSnapshot(snapshot)
	if err != nil {
		return view, err
	}
	if view.Snapshot.ModelID != view.ModelID || view.Snapshot.ModelVersion != view.ModelVersion {
		return view, errors.New("instance configuration binding mismatched")
	}
	view.TemplateVersion = view.Snapshot.TemplateVersion
	view.CreatedAt = view.CreatedAt.UTC()
	view.UpdatedAt = view.UpdatedAt.UTC()
	if view.DeletedAt != nil {
		value := view.DeletedAt.UTC()
		view.DeletedAt = &value
	}
	if retained != nil {
		value := retained.UTC()
		retained = &value
	}
	if assetID != nil && assetStatus != nil && capacity != nil {
		view.Storage = &StorageView{AssetID: *assetID, Status: *assetStatus, Capacity: *capacity, RetentionUntil: retained}
		view.RetentionUntil = retained
	}
	if err = json.Unmarshal(health, &view.Health); err != nil {
		return view, err
	}
	if !credentialUsable {
		view.Health[Configuration] = HealthView{Status: "UNHEALTHY", Reason: "CREDENTIAL_REVOKED"}
		modelHealth = HealthView{Status: "UNHEALTHY", Reason: "CREDENTIAL_REVOKED"}
	}
	if !routeEnabled {
		view.Health[Access] = HealthView{Status: "UNHEALTHY", Reason: "ACCESS_CLOSED"}
	}
	if !assetInitialized || view.Storage == nil || view.Storage.Status != "READY" || view.Storage.Capacity != view.Snapshot.Spec.Storage {
		view.Health[Storage] = HealthView{Status: "UNHEALTHY", Reason: "STORAGE_MISSING"}
	}
	view.Health[Model] = modelHealth
	checks := map[Dimension]Check{}
	for _, dimension := range Dimensions() {
		item := view.Health[dimension].normalize(now)
		view.Health[dimension] = item
		check := Check{Status: Status(item.Status)}
		if item.CheckedAt != nil {
			check.CheckedAt = *item.CheckedAt
		}
		if item.ExpiresAt != nil {
			check.ExpiresAt = *item.ExpiresAt
		}
		checks[dimension] = check
	}
	healthSet := NewHealthSet(checks)
	view.Ready = healthSet.Ready(view.Observed, now)
	view.CanEnter = healthSet.CanEnter(view.Observed, true, now)
	view.AllowedActions = []string{}
	if view.ActiveOperationID == nil {
		switch view.Observed {
		case "ERROR":
			view.AllowedActions = append(view.AllowedActions, "delete")
		case "RUNNING":
			view.AllowedActions = append(view.AllowedActions, "stop", "delete", "configure")
		case "STOPPED":
			if assetInitialized && modelUsable && view.Health[Model].Status == "HEALTHY" && view.Storage != nil && view.Storage.Status == "READY" && view.Storage.Capacity == view.Snapshot.Spec.Storage {
				view.AllowedActions = append(view.AllowedActions, "start")
			}
			view.AllowedActions = append(view.AllowedActions, "delete", "configure")
		}
	}
	if view.Storage != nil && (view.Storage.Status == "RESTORING" || view.Storage.Status == "RECLAIM_PENDING") {
		view.Ready = false
		view.CanEnter = false
		filtered := []string{}
		for _, action := range view.AllowedActions {
			if action == "stop" {
				filtered = append(filtered, action)
			}
		}
		view.AllowedActions = filtered
	}
	if view.CanEnter {
		view.AllowedActions = append(view.AllowedActions, "access")
	}
	return view, nil
}

func (s *QueryService) List(ctx context.Context, p identity.Principal, input ListInput) (InstancePage, error) {
	page := InstancePage{Items: []InstanceView{}}
	tx, now, err := s.begin(ctx, p, "instances:read")
	if err != nil {
		return page, err
	}
	defer tx.Rollback(context.Background())
	p, err = queryPermissions(ctx, tx, p)
	if err != nil {
		return page, err
	}
	if input.Limit == 0 {
		input.Limit = 20
	}
	offset, err := pagination.Offset(input.Page, input.Limit, input.Cursor)
	if err != nil {
		return page, invalidQuery()
	}
	if input.Limit < 1 || input.Limit > 100 || !validState(input.State) || !prefixPattern.MatchString(input.Query) {
		return page, invalidQuery()
	}
	filters, _ := json.Marshal(struct {
		State, Query   string
		IncludeDeleted bool
	}{input.State, input.Query, input.IncludeDeleted})
	scope := pagination.Scope{Subject: p.UserID, Organization: p.OrganizationID, Resource: "instances", Filters: string(filters)}
	var timestamp *time.Time
	var id *string
	if input.Cursor != "" {
		position, err := s.cursor.Decode(input.Cursor, scope, now)
		if err != nil {
			return page, invalidCursor()
		}
		timestamp = &position.Timestamp
		id = &position.ID
	}
	// The underscore in a display name is literal, not a LIKE wildcard.
	prefix := strings.ReplaceAll(input.Query, "_", `\_`)
	if err = tx.QueryRow(ctx, `SELECT count(*) FROM instances i WHERE i.org_id=$1 AND i.owner_id=$2 AND ($3 OR i.deleted_at IS NULL) AND ($4='' OR i.observed_state=$4) AND i.name LIKE $5||'%'`, p.OrganizationID, p.UserID, input.IncludeDeleted, input.State, prefix).Scan(&page.Total); err != nil {
		return page, err
	}
	rows, err := tx.Query(ctx, selectInstance+` WHERE i.org_id=$1 AND i.owner_id=$2 AND ($3 OR i.deleted_at IS NULL) AND ($4='' OR i.observed_state=$4) AND i.name LIKE $5||'%' AND ($6::timestamptz IS NULL OR (i.created_at,i.id)<($6,$7::uuid)) ORDER BY i.created_at DESC,i.id DESC LIMIT $8 OFFSET $9`, p.OrganizationID, p.UserID, input.IncludeDeleted, input.State, prefix, timestamp, id, input.Limit+1, offset)
	if err != nil {
		return page, err
	}
	for rows.Next() {
		view, err := scanInstance(rows, now)
		if err != nil {
			rows.Close()
			return page, err
		}
		filterCapabilities(p, &view)
		page.Items = append(page.Items, view)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return page, err
	}
	if len(page.Items) > input.Limit {
		page.Items = page.Items[:input.Limit]
		last := page.Items[len(page.Items)-1]
		token, err := s.cursor.Encode(scope, pagination.Position{Timestamp: last.CreatedAt, ID: last.ID}, now)
		if err != nil {
			return page, err
		}
		page.NextCursor = &token
	}
	return page, tx.Commit(ctx)
}
func (s *QueryService) Get(ctx context.Context, p identity.Principal, id string) (InstanceView, error) {
	tx, now, err := s.begin(ctx, p, "instances:read")
	if err != nil {
		return InstanceView{}, err
	}
	defer tx.Rollback(context.Background())
	p, err = queryPermissions(ctx, tx, p)
	if err != nil {
		return InstanceView{}, err
	}
	if !idPattern.MatchString(id) {
		return InstanceView{}, invalidQuery()
	}
	result, err := scanInstance(tx.QueryRow(ctx, selectInstance+` WHERE i.org_id=$1 AND i.owner_id=$2 AND i.id=$3`, p.OrganizationID, p.UserID, id), now)
	if errors.Is(err, pgx.ErrNoRows) {
		return InstanceView{}, queryNotFound()
	}
	if err != nil {
		return InstanceView{}, err
	}
	filterCapabilities(p, &result)
	return result, tx.Commit(ctx)
}
