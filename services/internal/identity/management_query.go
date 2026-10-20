package identity

import (
	"context"
	"encoding/json"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/jonluo94/deepseek-harness-cloud/services/internal/pagination"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/webapi"
)

func (s *ManagementService) position(p Principal, resource string, in ManagementListInput) (pagination.Scope, pagination.Position, error) {
	filters, _ := json.Marshal([]string{in.Query, in.Status, in.RoleID})
	scope := pagination.Scope{Subject: p.UserID, Organization: p.OrganizationID, Resource: resource, Filters: string(filters)}
	if _, err := pagination.Offset(in.Page, in.Limit, in.Cursor); err != nil {
		return scope, pagination.Position{}, managementInvalid("页码不合法，不能同时指定page和cursor")
	}
	if in.Limit < 1 || in.Limit > 100 || !utf8.ValidString(in.Query) || utf8.RuneCountInString(in.Query) > 100 || strings.ContainsFunc(in.Query, unicode.IsControl) || (in.RoleID != "" && !uuidPattern.MatchString(in.RoleID)) {
		return scope, pagination.Position{}, managementInvalid("查询参数不合法")
	}
	if resource == "roles" && (in.Status != "" || in.RoleID != "") {
		return scope, pagination.Position{}, managementInvalid("角色查询不接受用户筛选")
	}
	if in.Status != "" && in.Status != "PENDING_IDENTITY" && in.Status != "ACTIVE" && in.Status != "DISABLED" && in.Status != "ARCHIVED" {
		return scope, pagination.Position{}, managementInvalid("用户状态不合法")
	}
	if in.Cursor != "" {
		position, err := s.cursor.Decode(in.Cursor, scope, time.Now())
		if err != nil {
			return scope, position, webapi.Fail(422, "INVALID_CURSOR", "分页游标无效、已过期或筛选已变化")
		}
		return scope, position, nil
	}
	return scope, pagination.Position{}, nil
}
func (s *ManagementService) next(scope pagination.Scope, position pagination.Position) (*string, error) {
	token, err := s.cursor.Encode(scope, position, time.Now())
	if err != nil {
		return nil, err
	}
	return &token, nil
}
func (s *ManagementService) ListRoles(ctx context.Context, p Principal, in ManagementListInput) (ManagementPage[Role], error) {
	page := ManagementPage[Role]{Items: []Role{}}
	scope, pos, err := s.position(p, "roles", in)
	if err != nil {
		return page, err
	}
	tx, err := s.begin(ctx, p, "roles:read")
	if err != nil {
		return page, err
	}
	defer tx.Rollback(context.Background())
	if err = tx.QueryRow(ctx, `SELECT count(*) FROM roles r WHERE r.org_id=$1 AND ($2='' OR position(lower($2) in lower(r.name||' '||r.code))>0)`, p.OrganizationID, in.Query).Scan(&page.Total); err != nil {
		return page, err
	}
	offset, _ := pagination.Offset(in.Page, in.Limit, in.Cursor)
	rows, err := tx.Query(ctx, selectRole+` WHERE r.org_id=$1 AND ($2='' OR position(lower($2) in lower(r.name||' '||r.code))>0)
 AND ($3::timestamptz IS NULL OR (r.created_at,r.id)<($3,$4::uuid)) ORDER BY r.created_at DESC,r.id DESC LIMIT $5 OFFSET $6`, p.OrganizationID, in.Query, nullableTime(pos.Timestamp), nullableString(pos.ID), in.Limit+1, offset)
	if err != nil {
		return page, err
	}
	defer rows.Close()
	for rows.Next() {
		role, err := scanRole(rows)
		if err != nil {
			return page, err
		}
		page.Items = append(page.Items, role)
	}
	if err = rows.Err(); err != nil {
		return page, err
	}
	if len(page.Items) > in.Limit {
		page.Items = page.Items[:in.Limit]
		last := page.Items[len(page.Items)-1]
		page.NextCursor, err = s.next(scope, pagination.Position{Timestamp: last.CreatedAt, ID: last.ID})
	}
	return page, err
}
func (s *ManagementService) ListUsers(ctx context.Context, p Principal, in ManagementListInput) (ManagementPage[ManagedUser], error) {
	page := ManagementPage[ManagedUser]{Items: []ManagedUser{}, LocalAccounts: s.local != nil}
	scope, pos, err := s.position(p, "users", in)
	if err != nil {
		return page, err
	}
	tx, err := s.begin(ctx, p, "users:read")
	if err != nil {
		return page, err
	}
	defer tx.Rollback(context.Background())
	if err = tx.QueryRow(ctx, `SELECT count(*) FROM users u JOIN memberships m ON m.user_id=u.id JOIN roles r ON r.org_id=m.org_id AND r.code=m.role
 WHERE m.org_id=$1 AND ($2='' OR position(lower($2) in lower(u.display_name||' '||u.email))>0)
 AND (($3='' AND u.status<>'ARCHIVED') OR u.status=$3) AND ($4::uuid IS NULL OR r.id=$4)`, p.OrganizationID, in.Query, in.Status, nullableString(in.RoleID)).Scan(&page.Total); err != nil {
		return page, err
	}
	offset, _ := pagination.Offset(in.Page, in.Limit, in.Cursor)
	rows, err := tx.Query(ctx, selectUser+` WHERE m.org_id=$1 AND ($2='' OR position(lower($2) in lower(u.display_name||' '||u.email))>0)
 AND (($3='' AND u.status<>'ARCHIVED') OR u.status=$3) AND ($4::uuid IS NULL OR r.id=$4)
 AND ($5::timestamptz IS NULL OR (u.created_at,u.id)<($5,$6::uuid)) ORDER BY u.created_at DESC,u.id DESC LIMIT $7 OFFSET $8`, p.OrganizationID, in.Query, in.Status, nullableString(in.RoleID), nullableTime(pos.Timestamp), nullableString(pos.ID), in.Limit+1, offset)
	if err != nil {
		return page, err
	}
	defer rows.Close()
	for rows.Next() {
		u, err := scanManagedUser(rows)
		if err != nil {
			return page, err
		}
		page.Items = append(page.Items, u)
	}
	if err = rows.Err(); err != nil {
		return page, err
	}
	if len(page.Items) > in.Limit {
		page.Items = page.Items[:in.Limit]
		last := page.Items[len(page.Items)-1]
		page.NextCursor, err = s.next(scope, pagination.Position{Timestamp: last.CreatedAt, ID: last.ID})
	}
	return page, err
}
func nullableTime(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return t
}
func nullableString(s string) any {
	if s == "" {
		return nil
	}
	return s
}
