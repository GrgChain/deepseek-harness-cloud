package identity

import (
	"context"
	"encoding/json"
	"slices"

	"github.com/jackc/pgx/v5"
)

const selectRole = `SELECT r.id::text,r.code,r.name,r.description,r.builtin,r.version,
 COALESCE((SELECT array_agg(permission_id ORDER BY permission_id) FROM role_permissions WHERE role_id=r.id),'{}'::text[]),
 (SELECT count(*) FROM memberships WHERE org_id=r.org_id AND role=r.code),r.created_at,r.updated_at FROM roles r`

func scanRole(row pgx.Row) (Role, error) {
	var r Role
	err := row.Scan(&r.ID, &r.Code, &r.Name, &r.Description, &r.Builtin, &r.Version, &r.Permissions, &r.UserCount, &r.CreatedAt, &r.UpdatedAt)
	return r, managementError(err)
}

func (s *ManagementService) GetRole(ctx context.Context, p Principal, id string) (Role, error) {
	if !uuidPattern.MatchString(id) {
		return Role{}, managementInvalid("角色标识不合法")
	}
	tx, err := s.begin(ctx, p, "roles:read")
	if err != nil {
		return Role{}, err
	}
	defer tx.Rollback(context.Background())
	return scanRole(tx.QueryRow(ctx, selectRole+` WHERE r.org_id=$1 AND r.id=$2`, p.OrganizationID, id))
}

func (s *ManagementService) Permissions(ctx context.Context, p Principal) ([]Permission, error) {
	tx, err := s.begin(ctx, p, "roles:read")
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(context.Background())
	return append([]Permission{}, permissionCatalog...), nil
}

func (s *ManagementService) SaveRole(ctx context.Context, p Principal, id, key string, in RoleInput, trace string) (Role, error) {
	if err := validateRoleInput(in); err != nil {
		return Role{}, err
	}
	in.Permissions = append([]string{}, in.Permissions...)
	slices.Sort(in.Permissions)
	var fingerprint []byte
	action := "ROLE_UPDATE"
	if id == "" {
		if !managementKeyPattern.MatchString(key) || in.ExpectedVersion != 0 {
			return Role{}, managementInvalid("新增角色需要稳定请求标识及初始版本0")
		}
		id, action = newID(), "ROLE_CREATE"
		data, _ := json.Marshal(in)
		fingerprint = digest(string(data))
	} else if !uuidPattern.MatchString(id) || in.ExpectedVersion < 1 {
		return Role{}, managementInvalid("角色标识或版本不合法")
	}
	result, err := s.change(ctx, p, action, id, in.ExpectedVersion, in, key, fingerprint, trace)
	if err != nil {
		return Role{}, err
	}
	return s.GetRole(ctx, p, result)
}

func (s *ManagementService) DeleteRole(ctx context.Context, p Principal, id string, version int64, trace string) error {
	if !uuidPattern.MatchString(id) || version < 1 {
		return managementInvalid("角色标识或版本不合法")
	}
	_, err := s.change(ctx, p, "ROLE_DELETE", id, version, map[string]any{}, "", nil, trace)
	return err
}
