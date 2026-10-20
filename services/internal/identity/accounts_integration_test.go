package identity

import (
	"context"
	"errors"
	"sync"
	"testing"

	"golang.org/x/crypto/bcrypt"
)

// Failure injection wraps real PostgreSQL credential writes, including lost
// acknowledgements after a committed write. It contains no remote IdP adapter.
type passwordClient struct {
	provider   *Accounts
	account    struct{ Hash []byte }
	updates    int
	failUpdate bool
	mu         sync.Mutex
}

func (c *passwordClient) Subject(id string) string { return c.provider.Subject(id) }
func (c *passwordClient) Ensure(ctx context.Context, id, email, name, password string) error {
	if err := c.provider.Ensure(ctx, id, email, name, password); err != nil {
		return err
	}
	return c.provider.pool.QueryRow(ctx, `SELECT password_hash FROM identity_accounts WHERE id=$1`, id).Scan(&c.account.Hash)
}
func (c *passwordClient) ResetHash(ctx context.Context, id, email string, hash []byte) error {
	if err := c.provider.ResetHash(ctx, id, email, hash); err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.updates++
	c.account.Hash = append([]byte{}, hash...)
	if c.failUpdate {
		return errors.New("synthetic database write acknowledgement lost")
	}
	return nil
}
func TestDatabaseAccountsDoNotAdoptAnotherIdentityOrResetOnReplay(t *testing.T) {
	f, _, client, user := localResetFixture(t)
	if user.Subject != user.ID {
		t.Fatal("new account subject must be its stable UUID")
	}
	if err := client.Ensure(f.ctx, user.ID, user.LoginEmail, user.DisplayName, "synthetic-other-password"); err != nil {
		t.Fatal(err)
	}
	if bcrypt.CompareHashAndPassword(client.account.Hash, []byte("synthetic-initial-password")) != nil {
		t.Fatal("provisioning replay changed the initial password")
	}
	if err := client.Ensure(f.ctx, newID(), user.LoginEmail, "Other User", "synthetic-other-password"); err == nil {
		t.Fatal("adopted another identity by email")
	}
	hash, _ := bcrypt.GenerateFromPassword([]byte("synthetic-unaccepted-reset"), 12)
	defer clear(hash)
	if err := client.ResetHash(f.ctx, user.ID, user.LoginEmail, hash); err == nil {
		t.Fatal("changed a password without a committed reset intent")
	}
}
