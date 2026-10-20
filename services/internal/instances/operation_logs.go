package instances

import (
	"context"
	"encoding/json"
	"regexp"
	"time"

	"github.com/jonluo94/deepseek-harness-cloud/services/internal/identity"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/pagination"
)

var actionPattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_.:-]{0,127}$`)

type OperationLogInput struct {
	Limit                               int
	Page                                int
	Cursor, Action, ResourceID, Outcome string
	From, To                            *time.Time
}
type OperationLogView struct {
	OwnerID        *string        `json:"owner_id"`
	ActorName      string         `json:"actor_name"`
	ResourceType   string         `json:"resource_type"`
	ResourceName   string         `json:"resource_name"`
	Details        map[string]any `json:"details"`
	ID             string         `json:"id"`
	OrganizationID string         `json:"org_id"`
	ActorID        *string        `json:"actor_id"`
	Action         string         `json:"action"`
	ResourceID     *string        `json:"resource_id"`
	Outcome        string         `json:"outcome"`
	TraceID        string         `json:"trace_id"`
	Timestamp      time.Time      `json:"timestamp"`
}
type OperationLogPage struct {
	Items      []OperationLogView `json:"items"`
	Total      int64              `json:"total"`
	NextCursor *string            `json:"next_cursor"`
}

func (s *QueryService) OperationLogs(ctx context.Context, p identity.Principal, input OperationLogInput) (OperationLogPage, error) {
	page := OperationLogPage{Items: []OperationLogView{}}
	tx, now, err := s.begin(ctx, p, "audit:read")
	if err != nil {
		return page, err
	}
	defer tx.Rollback(context.Background())
	var organizationLogs bool
	if err := tx.QueryRow(ctx, `SELECT public.hc_has_permission($1,$2,'audit:organization')`, p.OrganizationID, p.UserID).Scan(&organizationLogs); err != nil {
		return page, err
	}
	if input.Limit == 0 {
		input.Limit = 20
	}
	offset, err := pagination.Offset(input.Page, input.Limit, input.Cursor)
	if err != nil {
		return page, invalidQuery()
	}
	if input.Limit < 1 || input.Limit > 100 || (input.Action != "" && !actionPattern.MatchString(input.Action)) || (input.ResourceID != "" && !idPattern.MatchString(input.ResourceID)) || (input.Outcome != "" && input.Outcome != "ACCEPTED" && input.Outcome != "SUCCEEDED" && input.Outcome != "FAILED" && input.Outcome != "DENIED") {
		return page, invalidQuery()
	}
	to := now
	if input.To != nil {
		to = input.To.UTC()
	}
	from := to.Add(-24 * time.Hour)
	if input.From != nil {
		from = input.From.UTC()
	}
	if !to.After(from) || to.Sub(from) > 90*24*time.Hour {
		return page, invalidQuery()
	}
	// Hash explicit filters; omitted relative time bounds are resolved per page.
	// Like the instance list this is keyset pagination, not a frozen snapshot.
	filters, _ := json.Marshal(struct {
		Action, ResourceID, Outcome string
		From, To                    *time.Time
	}{input.Action, input.ResourceID, input.Outcome, input.From, input.To})
	scope := pagination.Scope{Subject: p.UserID, Organization: p.OrganizationID, Resource: "audit-events", Filters: string(filters)}
	var timestamp *time.Time
	var id *string
	var resource *string
	if input.ResourceID != "" {
		resource = &input.ResourceID
	}
	if input.Cursor != "" {
		position, err := s.cursor.Decode(input.Cursor, scope, now)
		if err != nil {
			return page, invalidCursor()
		}
		timestamp = &position.Timestamp
		id = &position.ID
	}
	if err = tx.QueryRow(ctx, `SELECT count(*) FROM audit_events WHERE org_id=$1 AND ($3 OR actor_id=$2 OR owner_id=$2) AND ($4='' OR action=$4) AND ($5::uuid IS NULL OR resource_id=$5) AND timestamp>=$6 AND timestamp<=$7 AND ($8='' OR outcome=$8)`, p.OrganizationID, p.UserID, organizationLogs, input.Action, resource, from, to, input.Outcome).Scan(&page.Total); err != nil {
		return page, err
	}
	rows, err := tx.Query(ctx, `SELECT a.id::text,a.org_id::text,a.actor_id::text,a.action,a.resource_id::text,a.outcome,a.trace_id,a.timestamp,a.owner_id::text,
 COALESCE(NULLIF(actor.display_name,''),actor.email,''),COALESCE(NULLIF(target.display_name,''),target.email,i.name,r.name,g.name,h.name,''),a.details
 FROM audit_events a
 LEFT JOIN users actor ON actor.id=a.actor_id AND EXISTS(SELECT 1 FROM memberships m WHERE m.org_id=a.org_id AND m.user_id=actor.id)
 LEFT JOIN users target ON target.id=a.resource_id AND (a.action LIKE 'USER\_%' ESCAPE '\' OR a.action LIKE 'IDENTITY\_%' ESCAPE '\') AND EXISTS(SELECT 1 FROM memberships m WHERE m.org_id=a.org_id AND m.user_id=target.id)
 LEFT JOIN instances i ON i.id=a.resource_id AND i.org_id=a.org_id
 LEFT JOIN roles r ON r.id=a.resource_id AND r.org_id=a.org_id
 LEFT JOIN personal_provider_accounts g ON g.id=a.resource_id AND g.org_id=a.org_id AND a.action LIKE 'PERSONAL_PROVIDER_ACCOUNT\_%' ESCAPE '\'
 LEFT JOIN user_model_connections h ON h.id=a.resource_id AND h.org_id=a.org_id AND a.action NOT LIKE 'PERSONAL_PROVIDER_ACCOUNT\_%' ESCAPE '\'
 WHERE a.org_id=$1 AND ($3 OR a.actor_id=$2 OR a.owner_id=$2) AND ($4='' OR a.action=$4) AND ($5::uuid IS NULL OR a.resource_id=$5) AND a.timestamp>=$6 AND a.timestamp<=$7 AND ($8::timestamptz IS NULL OR (a.timestamp,a.id)<($8,$9::uuid)) AND ($12='' OR a.outcome=$12) ORDER BY a.timestamp DESC,a.id DESC LIMIT $10 OFFSET $11`, p.OrganizationID, p.UserID, organizationLogs, input.Action, resource, from, to, timestamp, id, input.Limit+1, offset, input.Outcome)
	if err != nil {
		return page, err
	}
	for rows.Next() {
		var view OperationLogView
		var details []byte
		if err = rows.Scan(&view.ID, &view.OrganizationID, &view.ActorID, &view.Action, &view.ResourceID, &view.Outcome, &view.TraceID, &view.Timestamp, &view.OwnerID, &view.ActorName, &view.ResourceName, &details); err != nil {
			rows.Close()
			return page, err
		}
		view.Timestamp = view.Timestamp.UTC()
		view.ResourceType = operationLogResourceType(view.Action)
		view.Details = safeOperationLogDetails(details)
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
		token, err := s.cursor.Encode(scope, pagination.Position{Timestamp: last.Timestamp, ID: last.ID}, now)
		if err != nil {
			return page, err
		}
		page.NextCursor = &token
	}
	return page, tx.Commit(ctx)
}
