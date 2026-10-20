package identity

import (
	"context"
	"errors"
	"sync"
	"testing"

	"golang.org/x/crypto/bcrypt"
)

func localResetFixture(t *testing.T) (*managementFixture, Principal, *passwordClient, ManagedUser) {
	t.Helper()
	f := managementDatabase(t)
	admin := f.login(t, "admin-a")
	client := &passwordClient{provider: NewAccounts(f.app, f.org)}
	f.service.local = client
	var role string
	if err := f.root.QueryRow(f.ctx, `SELECT id::text FROM roles WHERE org_id=$1 AND code='MEMBER'`, f.org).Scan(&role); err != nil {
		t.Fatal(err)
	}
	user, err := f.service.CreateUser(f.ctx, admin, "local-reset-fixture-create", UserCreateInput{DisplayName: "Local member", Email: "reset@example.com", IdentitySource: "local", RoleID: role, InitialPassword: "synthetic-initial-password"}, "create")
	if err != nil || user.Status != "ACTIVE" {
		t.Fatal("local account not provisioned", err, client.Ensure(f.ctx, user.ID, user.LoginEmail, user.DisplayName, "synthetic-initial-password"))
	}
	return f, admin, client, user
}

func TestPasswordResetConcurrentIntentAcceptsOnePassword(t *testing.T) {
	f, first, client, user := localResetFixture(t)
	second := f.login(t, "admin-b")
	var wg sync.WaitGroup
	results := make(chan error, 2)
	start := make(chan struct{})
	for i, admin := range []Principal{first, second} {
		wg.Add(1)
		go func(i int, admin Principal) {
			defer wg.Done()
			<-start
			password := "synthetic-first-password"
			if i == 1 {
				password = "synthetic-second-password"
			}
			_, err := f.service.UserAction(f.ctx, admin, user.ID, "USER_PASSWORD_RESET", UserActionInput{ExpectedVersion: user.Version, Password: password}, "concurrent-reset")
			results <- err
		}(i, admin)
	}
	close(start)
	wg.Wait()
	close(results)
	success := 0
	for err := range results {
		if err == nil {
			success++
		} else if problemCode(err) != "VERSION_CONFLICT" {
			t.Fatal("unexpected reset conflict", err)
		}
	}
	if success != 1 || client.updates != 1 {
		t.Fatal("concurrent administrators reset two different passwords")
	}
}

func TestPasswordResetSelfConfirmationSurvivesOwnSessionRevocation(t *testing.T) {
	f, admin, client, user := localResetFixture(t)
	var role string
	if err := f.root.QueryRow(f.ctx, `SELECT id::text FROM roles WHERE org_id=$1 AND code='ADMIN'`, f.org).Scan(&role); err != nil {
		t.Fatal(err)
	}
	user, err := f.service.UserAction(f.ctx, admin, user.ID, "USER_ROLE", UserActionInput{ExpectedVersion: user.Version, RoleID: role}, "assign-admin")
	if err != nil {
		t.Fatal(err)
	}
	self := f.login(t, user.Subject)
	if _, err = f.service.UserAction(f.ctx, self, user.ID, "USER_PASSWORD_RESET", UserActionInput{ExpectedVersion: user.Version, Password: "synthetic-self-reset"}, "reset-self"); !errors.Is(err, ErrUnauthenticated) {
		t.Fatal("self reset did not require a new session", err)
	}
	after, err := f.service.GetUser(f.ctx, admin, user.ID)
	if err != nil || after.PasswordReset == nil || after.PasswordReset.Status != "SUCCEEDED" || client.updates != 1 {
		t.Fatal("self session revocation prevented durable confirmation", err)
	}
	if fresh := f.login(t, user.Subject); fresh.Generation <= self.Generation {
		t.Fatal("self reset restored the old authorization generation")
	}
}

type passwordResetHook struct {
	LocalAccountProvider
	after func()
}

func (h passwordResetHook) ResetHash(ctx context.Context, id, email string, hash []byte) error {
	if err := h.LocalAccountProvider.ResetHash(ctx, id, email, hash); err != nil {
		return err
	}
	h.after()
	return nil
}

func TestPasswordResetRechecksAdministratorAfterProviderWrite(t *testing.T) {
	f, first, client, user := localResetFixture(t)
	second := f.login(t, "admin-b")
	provider := f.service.local
	var revokeError error
	f.service.local = passwordResetHook{provider, func() {
		_, revokeError = f.service.UserAction(f.ctx, second, first.UserID, "USER_DISABLE", UserActionInput{ExpectedVersion: 1}, "revoke-reset-actor")
	}}
	if _, err := f.service.UserAction(f.ctx, first, user.ID, "USER_PASSWORD_RESET", UserActionInput{ExpectedVersion: user.Version, Password: "synthetic-actor-reset"}, "reset"); !errors.Is(err, ErrForbidden) || revokeError != nil {
		t.Fatal("revoked administrator published completion", err, revokeError)
	}
	pending, err := f.service.GetUser(f.ctx, second, user.ID)
	if err != nil || pending.PasswordReset == nil || pending.PasswordReset.Status != "PENDING" {
		t.Fatal("accepted reset was lost when administrator was revoked", err)
	}
	if _, err = f.root.Exec(f.ctx, `UPDATE identity_password_resets SET lease_expires_at=clock_timestamp()-interval '1 second' WHERE id=$1`, pending.PasswordReset.ID); err != nil {
		t.Fatal(err)
	}
	f.service.local = provider
	done, err := f.service.UserAction(f.ctx, second, user.ID, "USER_PASSWORD_RESET_RETRY", UserActionInput{ExpectedVersion: pending.Version}, "recover-reset")
	if err != nil || done.PasswordReset.Status != "SUCCEEDED" || client.updates != 2 {
		t.Fatal("active administrator could not reconcile the original password", err)
	}
}

func TestPasswordResetDoesNotChangeProviderWhenIntentAuditFails(t *testing.T) {
	f, admin, client, user := localResetFixture(t)
	var err error
	var bypass bool
	if err = f.app.QueryRow(f.ctx, `SELECT has_function_privilege(current_user,'public.hc_iam_profile_change(uuid,uuid,uuid,bigint,text,uuid,bigint,jsonb,bytea,bytea,text)','EXECUTE')`).Scan(&bypass); err != nil || bypass {
		t.Fatal("API can bypass the durable password reset boundary", err)
	}
	if _, err = f.root.Exec(f.ctx, `CREATE FUNCTION reject_password_audit() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.action LIKE 'USER_PASSWORD_RESET%' THEN RAISE EXCEPTION 'test audit failure'; END IF; RETURN NEW; END $$;
 CREATE TRIGGER reject_password_audit BEFORE INSERT ON audit_events FOR EACH ROW EXECUTE FUNCTION reject_password_audit()`); err != nil {
		t.Fatal(err)
	}
	if _, err = f.service.UserAction(f.ctx, admin, user.ID, "USER_PASSWORD_RESET", UserActionInput{ExpectedVersion: user.Version, Password: "synthetic-new-password"}, "reset"); err == nil {
		t.Fatal("audit failure was hidden")
	}
	if client.updates != 0 || bcrypt.CompareHashAndPassword(client.account.Hash, []byte("synthetic-initial-password")) != nil {
		t.Fatal("identity provider changed before the platform intent committed")
	}
	after, err := f.service.GetUser(f.ctx, admin, user.ID)
	if err != nil || after.Version != user.Version {
		t.Fatal("failed intent changed the user version", err)
	}
}

func TestPasswordResetUnknownWriteAndFailedConfirmationRecoverSameIntent(t *testing.T) {
	for _, failure := range []string{"provider response lost", "confirmation audit fails"} {
		t.Run(failure, func(t *testing.T) {
			f, admin, client, user := localResetFixture(t)
			oldSession := f.login(t, user.Subject)
			client.failUpdate = failure == "provider response lost"
			if failure == "confirmation audit fails" {
				if _, err := f.root.Exec(f.ctx, `CREATE FUNCTION reject_reset_confirmation() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.action='USER_PASSWORD_RESET' THEN RAISE EXCEPTION 'test audit failure'; END IF; RETURN NEW; END $$;
 CREATE TRIGGER reject_reset_confirmation BEFORE INSERT ON audit_events FOR EACH ROW EXECUTE FUNCTION reject_reset_confirmation()`); err != nil {
					t.Fatal(err)
				}
			}
			_, resetErr := f.service.UserAction(f.ctx, admin, user.ID, "USER_PASSWORD_RESET", UserActionInput{ExpectedVersion: user.Version, Password: "synthetic-new-password"}, "reset")
			if failure == "confirmation audit fails" && resetErr == nil || failure == "provider response lost" && resetErr != nil {
				t.Fatal("incorrect failure boundary", resetErr)
			}
			pending, err := f.service.GetUser(f.ctx, admin, user.ID)
			if err != nil || pending.PasswordReset == nil || pending.PasswordReset.Status != "PENDING" || pending.Version != user.Version+1 || client.updates != 1 {
				t.Fatal("uncertain reset lost its durable intent", err)
			}
			tx, _ := f.app.Begin(f.ctx)
			err = RequirePermission(f.ctx, tx, oldSession, "instances:read")
			tx.Rollback(f.ctx)
			if !errors.Is(err, ErrUnauthenticated) {
				t.Fatal("committed reset intent retained old session", err)
			}
			if _, err = f.service.UserAction(f.ctx, admin, user.ID, "USER_PASSWORD_RESET", UserActionInput{ExpectedVersion: pending.Version, Password: "synthetic-different-password"}, "replace"); problemCode(err) != "PASSWORD_RESET_PENDING" {
				t.Fatal("new password superseded an uncertain write", err)
			}
			if _, err = f.service.UserAction(f.ctx, admin, user.ID, "USER_PASSWORD_RESET_RETRY", UserActionInput{ExpectedVersion: pending.Version}, "early-retry"); problemCode(err) != "STATE_CONFLICT" {
				t.Fatal("active provider lease was stolen", err)
			}
			if _, err = f.service.UserAction(f.ctx, admin, user.ID, "USER_ARCHIVE", UserActionInput{ExpectedVersion: pending.Version}, "archive-pending"); problemCode(err) != "STATE_CONFLICT" {
				t.Fatal("pending reset was orphaned by archive", err)
			}
			if _, err = f.app.Exec(f.ctx, `UPDATE identity_password_resets SET status='SUCCEEDED'`); err == nil {
				t.Fatal("runtime role bypassed reset state transitions")
			}
			if failure == "confirmation audit fails" {
				if _, err = f.root.Exec(f.ctx, `DROP TRIGGER reject_reset_confirmation ON audit_events`); err != nil {
					t.Fatal(err)
				}
			}
			if _, err = f.root.Exec(f.ctx, `UPDATE identity_password_resets SET lease_expires_at=clock_timestamp()-interval '1 second' WHERE id=$1`, pending.PasswordReset.ID); err != nil {
				t.Fatal(err)
			}
			client.failUpdate = false
			// A new service object must recover using only database state.
			fresh := NewManagementService(f.app, f.service.cursor, f.service.issuer, f.service.local, f.cipher, "test")
			done, err := fresh.UserAction(f.ctx, admin, user.ID, "USER_PASSWORD_RESET_RETRY", UserActionInput{ExpectedVersion: pending.Version}, "retry")
			if err != nil || done.PasswordReset.ID != pending.PasswordReset.ID || done.PasswordReset.Status != "SUCCEEDED" || done.Version != pending.Version+1 || client.updates != 2 {
				t.Fatal("recovery changed intent or did not acknowledge the original hash", err)
			}
			if bcrypt.CompareHashAndPassword(client.account.Hash, []byte("synthetic-new-password")) != nil {
				t.Fatal("recovery selected a different password")
			}
			var wiped bool
			if err = f.root.QueryRow(f.ctx, `SELECT key_id IS NULL AND nonce IS NULL AND ciphertext IS NULL FROM identity_password_resets WHERE id=$1`, done.PasswordReset.ID).Scan(&wiped); err != nil || !wiped {
				t.Fatal("confirmed intent retained its encrypted provider hash", err)
			}
		})
	}
}
