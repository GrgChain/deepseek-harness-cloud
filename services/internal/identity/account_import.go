package identity

import (
	"context"
	"errors"
	"net/url"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/webapi"
	"golang.org/x/crypto/bcrypt"
)

type AccountImport struct {
	SchemaVersion  int               `json:"schema_version"`
	OrganizationID string            `json:"organization_id"`
	PreviousIssuer string            `json:"previous_issuer"`
	Accounts       []ImportedAccount `json:"accounts"`
}
type ImportedAccount struct {
	ID               string `json:"id"`
	Email            string `json:"email"`
	Name             string `json:"name"`
	PasswordHash     string `json:"password_hash"`
	PreviousSubject  string `json:"previous_subject"`
	Role             string `json:"role"`
	WorkspaceOwnerID string `json:"workspace_owner_id"`
}

func ParseAccountImport(raw []byte, organization string) (AccountImport, error) {
	var in AccountImport
	invalid := errors.New("private account import is invalid")
	if webapi.StrictUnmarshal(raw, &in) != nil || in.SchemaVersion != 1 || in.OrganizationID != organization || !uuidPattern.MatchString(organization) || len(in.Accounts) < 1 || len(in.Accounts) > 5000 {
		return in, invalid
	}
	issuer, err := url.Parse(in.PreviousIssuer)
	if err != nil || issuer.Scheme != "https" || issuer.Host == "" || issuer.User != nil || issuer.RawQuery != "" || issuer.Fragment != "" {
		return in, invalid
	}
	ids, emails, subjects := map[string]bool{}, map[string]bool{}, map[string]bool{}
	for _, a := range in.Accounts {
		email := strings.ToLower(a.Email)
		cost, err := bcrypt.Cost([]byte(a.PasswordHash))
		if !uuidPattern.MatchString(a.ID) || validateUserProfile(a.Name, a.Email) != nil || err != nil || cost < 10 || cost > 14 || len(a.PasswordHash) != 60 || ids[a.ID] || emails[email] {
			return in, invalid
		}
		if a.Role != "" && (a.Role != "ADMIN" && a.Role != "MEMBER" || !uuidPattern.MatchString(a.WorkspaceOwnerID) || a.PreviousSubject == "" || len(a.PreviousSubject) > 1024 || subjects[a.PreviousSubject]) {
			return in, invalid
		}
		if a.Role == "" && (a.WorkspaceOwnerID != "" || a.PreviousSubject != "") {
			return in, invalid
		}
		ids[a.ID] = true
		emails[email] = true
		if a.PreviousSubject != "" {
			subjects[a.PreviousSubject] = true
		}
	}
	return in, nil
}

// ImportAccounts is operator-only and atomic. A replay verifies the existing
// account; it never overwrites a password, role, status or Workspace Owner.
func ImportAccounts(ctx context.Context, pool *pgxpool.Pool, in AccountImport, issuer, operator string) error {
	if operator == "" || issuer == "" {
		return errors.New("identity import requires an operator and target issuer")
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(context.Background())
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(731804523)`); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('hc-iam:'||$1::text,0))`, in.OrganizationID); err != nil {
		return err
	}
	var org string
	err = tx.QueryRow(ctx, `SELECT id::text FROM organizations FOR UPDATE`).Scan(&org)
	if errors.Is(err, pgx.ErrNoRows) {
		admin := false
		for _, a := range in.Accounts {
			admin = admin || a.Role == "ADMIN"
		}
		if !admin {
			return errors.New("initial account import requires an administrator")
		}
		if _, err = tx.Exec(ctx, `INSERT INTO organizations(id,name,status) VALUES($1,'Harness Cloud','ACTIVE')`, in.OrganizationID); err != nil {
			return err
		}
	} else if err != nil || org != in.OrganizationID {
		return errors.New("account import organization mismatch")
	}
	if _, err = tx.Exec(ctx, `SELECT user_id FROM memberships WHERE org_id=$1 ORDER BY user_id FOR UPDATE`, in.OrganizationID); err != nil {
		return err
	}
	for _, a := range in.Accounts {
		var user, owner, previousIssuer, previousSubject string
		if a.Role != "" {
			err = tx.QueryRow(ctx, `SELECT u.id::text,u.workspace_owner_id::text,u.identity_issuer,u.identity_subject FROM users u JOIN memberships m ON m.user_id=u.id
    WHERE m.org_id=$1 AND ((u.identity_issuer=$2 AND u.identity_subject=$3) OR (u.identity_issuer=$4 AND u.identity_subject=$5)) FOR UPDATE OF u`, in.OrganizationID, in.PreviousIssuer, a.PreviousSubject, issuer, a.ID).Scan(&user, &owner, &previousIssuer, &previousSubject)
			if errors.Is(err, pgx.ErrNoRows) {
				if in.PreviousIssuer != issuer {
					return errors.New("account migration requires an existing trusted issuer and subject binding")
				}
				user = newID()
				owner = a.WorkspaceOwnerID
				_, err = tx.Exec(ctx, `INSERT INTO users(id,identity_issuer,identity_subject,workspace_owner_id,status,identity_source,display_name,email) VALUES($1,$2,$3,$4,'ACTIVE','local',$5,$6)`, user, issuer, a.ID, owner, a.Name, a.Email)
				if err == nil {
					_, err = tx.Exec(ctx, `INSERT INTO memberships(org_id,user_id,role,status) VALUES($1,$2,$3,'ACTIVE')`, in.OrganizationID, user, a.Role)
				}
			} else if err == nil {
				if owner != a.WorkspaceOwnerID {
					return errors.New("account import cannot change Workspace Owner")
				}
				if previousIssuer != issuer || previousSubject != a.ID {
					var pending bool
					if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM identity_password_resets WHERE org_id=$1 AND user_id=$2 AND status='PENDING')`, in.OrganizationID, user).Scan(&pending); err != nil {
						return err
					}
					if pending {
						return errors.New("finish pending password reset before changing issuer")
					}
					_, err = tx.Exec(ctx, `UPDATE users SET identity_issuer=$2,identity_subject=$3,identity_source='local',version=version+1,updated_at=clock_timestamp(),email=CASE WHEN email='' THEN $4 ELSE email END,display_name=CASE WHEN display_name='' OR display_name=identity_subject THEN $5 ELSE display_name END WHERE id=$1`, user, issuer, a.ID, a.Email, a.Name)
					if err == nil {
						_, err = tx.Exec(ctx, `SELECT public.hc_iam_revoke($1,ARRAY[$2::uuid])`, in.OrganizationID, user)
					}
				}
			}
			if err != nil {
				return errors.Join(errors.New("account import identity conflict"), err)
			}
		}
		var userID any
		if user != "" {
			userID = user
		}
		result, err := tx.Exec(ctx, `INSERT INTO identity_accounts(id,org_id,user_id,login_email,display_name,password_hash) VALUES($1,$2,$3,$4,$5,$6) ON CONFLICT(id) DO NOTHING`, a.ID, in.OrganizationID, userID, strings.ToLower(a.Email), a.Name, []byte(a.PasswordHash))
		if err != nil {
			return errors.New("account import credential conflict")
		}
		var same bool
		err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM identity_accounts WHERE id=$1 AND org_id=$2 AND user_id IS NOT DISTINCT FROM $3::uuid AND login_email=$4)`, a.ID, in.OrganizationID, userID, strings.ToLower(a.Email)).Scan(&same)
		if err != nil || !same {
			return errors.New("account import conflicts with an existing identity")
		}
		if user != "" {
			_, err = tx.Exec(ctx, `INSERT INTO identity_provisioning_operations(id,org_id,user_id,provider_account_id,login_email,status) VALUES($1,$2,$3,$4,$5,'SUCCEEDED') ON CONFLICT(user_id) DO NOTHING`, newID(), in.OrganizationID, user, a.ID, strings.ToLower(a.Email))
			if err != nil {
				return err
			}
			if result.RowsAffected() > 0 {
				_, err = tx.Exec(ctx, `INSERT INTO audit_events(id,org_id,owner_id,resource_id,action,outcome,trace_id,details) VALUES($1,$2,$3,$3,'IDENTITY_IMPORT','SUCCEEDED',$4,jsonb_build_object('operator_ref',$5::text))`, newID(), in.OrganizationID, user, newID(), operator)
				if err != nil {
					return err
				}
			}
		}
	}
	var admins int
	if err = tx.QueryRow(ctx, `SELECT count(*) FROM memberships m JOIN users u ON u.id=m.user_id JOIN identity_accounts a ON a.user_id=u.id AND a.org_id=m.org_id
 WHERE m.org_id=$1 AND m.role='ADMIN' AND m.status='ACTIVE' AND u.status='ACTIVE' AND a.enabled`, in.OrganizationID).Scan(&admins); err != nil {
		return err
	}
	if admins < 1 {
		return errors.New("account import must retain an active platform administrator")
	}
	return tx.Commit(ctx)
}
