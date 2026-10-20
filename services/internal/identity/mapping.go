package identity

import (
	"context"
	"errors"
	"sort"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/webapi"
)

// MemberMapping binds an OIDC identity to the platform Workspace Owner.
type MemberMapping struct {
	Issuer           string `json:"issuer"`
	Subject          string `json:"subject"`
	Role             string `json:"role"`
	WorkspaceOwnerID string `json:"workspace_owner_id"`
}
type Mapping struct {
	SchemaVersion  int             `json:"schema_version"`
	OrganizationID string          `json:"organization_id"`
	Members        []MemberMapping `json:"members"`
}

func ParseMapping(data []byte, issuer, organization string) (Mapping, error) {
	var m Mapping
	if err := webapi.StrictUnmarshal(data, &m); err != nil {
		return m, errors.New("identity mapping is invalid")
	}
	if m.SchemaVersion != 1 || m.OrganizationID != organization || !uuidPattern.MatchString(organization) || len(m.Members) == 0 || len(m.Members) > 1000 {
		return m, errors.New("identity mapping is invalid")
	}
	identities, owners := map[string]bool{}, map[string]bool{}
	for _, member := range m.Members {
		key := member.Issuer + "\x00" + member.Subject
		if member.Issuer != issuer || member.Subject == "" || len(member.Subject) > 1024 || (member.Role != "ADMIN" && member.Role != "MEMBER") || !uuidPattern.MatchString(member.WorkspaceOwnerID) || identities[key] || owners[member.WorkspaceOwnerID] {
			return m, errors.New("identity mapping is invalid or duplicated")
		}
		identities[key] = true
		owners[member.WorkspaceOwnerID] = true
	}
	return m, nil
}

// ApplyMapping runs only under the controlled operations account, never from HTTP.
func ApplyMapping(ctx context.Context, pool *pgxpool.Pool, m Mapping, operator string) error {
	if operator == "" {
		return errors.New("operator reference is required")
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(context.Background())
	if _, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock(731804523)"); err != nil {
		return err
	}
	var existing int
	if err := tx.QueryRow(ctx, "SELECT count(*) FROM organizations").Scan(&existing); err != nil {
		return err
	}
	if existing == 0 {
		admin := false
		for _, member := range m.Members {
			admin = admin || member.Role == "ADMIN"
		}
		if !admin {
			return errors.New("initial mapping requires an administrator")
		}
		if _, err := tx.Exec(ctx, "INSERT INTO organizations(id,name,status) VALUES ($1,'Harness Cloud','ACTIVE')", m.OrganizationID); err != nil {
			return err
		}
	}
	// Sorting makes membership locks deterministic for parallel operations.
	members := append([]MemberMapping(nil), m.Members...)
	sort.Slice(members, func(i, j int) bool { return members[i].Subject < members[j].Subject })
	for _, member := range members {
		var id string
		err := tx.QueryRow(ctx, `INSERT INTO users(id,identity_issuer,identity_subject,workspace_owner_id,status) VALUES ($1,$2,$3,$4,'ACTIVE')
 ON CONFLICT (identity_issuer,identity_subject) DO UPDATE SET identity_subject=EXCLUDED.identity_subject
 RETURNING id::text`, newID(), member.Issuer, member.Subject, member.WorkspaceOwnerID).Scan(&id)
		if err != nil {
			return errors.New("identity mapping conflicts with an existing account")
		}
		// Serialize against all commands authenticated under this membership.
		if _, err := tx.Exec(ctx, `SELECT user_id FROM memberships WHERE user_id=$1 ORDER BY org_id FOR UPDATE`, id); err != nil {
			return err
		}
		var oldOwner string
		if err := tx.QueryRow(ctx, `SELECT workspace_owner_id::text FROM users WHERE id=$1`, id).Scan(&oldOwner); err != nil {
			return err
		}
		if oldOwner != member.WorkspaceOwnerID {
			var hasAssets bool
			if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM instances i LEFT JOIN storage_assets a ON a.instance_id=i.id WHERE i.owner_id=$1 AND (i.deleted_at IS NULL OR (a.id IS NOT NULL AND a.status<>'DELETED')))`, id).Scan(&hasAssets); err != nil {
				return err
			}
			if hasAssets {
				return errors.New("Workspace Owner identity cannot change while active or retained assets exist")
			}
			if _, err := tx.Exec(ctx, `UPDATE users SET workspace_owner_id=$2 WHERE id=$1`, id, member.WorkspaceOwnerID); err != nil {
				return errors.New("Workspace Owner identity is already assigned")
			}
			if _, err := tx.Exec(ctx, `UPDATE memberships SET auth_generation=auth_generation+1 WHERE user_id=$1`, id); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `UPDATE portal_sessions SET revoked_at=COALESCE(revoked_at,now()) WHERE user_id=$1`, id); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `UPDATE access_sessions a SET revoked_at=COALESCE(a.revoked_at,now()) FROM portal_sessions p WHERE a.portal_session_id=p.id AND p.user_id=$1`, id); err != nil {
				return err
			}
		}
		if _, err := tx.Exec(ctx, `INSERT INTO memberships(org_id,user_id,role,status) VALUES ($1,$2,$3,'ACTIVE')
 ON CONFLICT (org_id,user_id) DO UPDATE SET role=EXCLUDED.role,status='ACTIVE',auth_generation=memberships.auth_generation+1
 WHERE memberships.role<>EXCLUDED.role OR memberships.status<>'ACTIVE'`, m.OrganizationID, id, member.Role); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO audit_events(id,org_id,actor_id,owner_id,resource_id,action,outcome,trace_id,details)
 VALUES ($1,$2,NULL,$3,$3,'IDENTITY_APPLY','SUCCEEDED',$4,jsonb_build_object('operator_ref',$5::text))`, newID(), m.OrganizationID, id, newID(), operator); err != nil {
			return err
		}
	}
	var admins int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM memberships m JOIN users u ON u.id=m.user_id WHERE m.org_id=$1 AND m.role='ADMIN' AND m.status='ACTIVE' AND u.status='ACTIVE'`, m.OrganizationID).Scan(&admins); err != nil {
		return err
	}
	if admins == 0 {
		return errors.New("identity mapping must preserve an active administrator")
	}
	return tx.Commit(ctx)
}
