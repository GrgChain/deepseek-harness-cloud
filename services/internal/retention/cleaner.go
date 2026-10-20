package retention

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

type CleanupResult struct{ Tickets, Challenges, Sessions, Native, Portal, OIDC, Idempotency, IdentityTokens, IdentityCodes, IdentityAuthorizations int }

func (r CleanupResult) Total() int {
	return r.Tickets + r.Challenges + r.Sessions + r.Native + r.Portal + r.OIDC + r.Idempotency + r.IdentityTokens + r.IdentityCodes + r.IdentityAuthorizations
}

type Cleaner struct{ pool *pgxpool.Pool }

func NewCleaner(pool *pgxpool.Pool) *Cleaner { return &Cleaner{pool} }
func (c *Cleaner) Once(ctx context.Context, limit int) (CleanupResult, error) {
	if limit < 1 || limit > 500 {
		return CleanupResult{}, errors.New("invalid cleanup bound")
	}
	var raw []byte
	if err := c.pool.QueryRow(ctx, `SELECT public.hc_cleanup_expired($1)`, limit).Scan(&raw); err != nil {
		return CleanupResult{}, err
	}
	var result CleanupResult
	err := json.Unmarshal(raw, &result)
	return result, err
}
func (c *Cleaner) Preview(ctx context.Context, limit int) (CleanupResult, error) {
	if limit < 1 || limit > 500 {
		return CleanupResult{}, errors.New("invalid cleanup bound")
	}
	tx, err := c.pool.Begin(ctx)
	if err != nil {
		return CleanupResult{}, err
	}
	defer tx.Rollback(context.Background())
	var raw []byte
	if err = tx.QueryRow(ctx, `SELECT public.hc_cleanup_expired($1)`, limit).Scan(&raw); err != nil {
		return CleanupResult{}, err
	}
	var result CleanupResult
	err = json.Unmarshal(raw, &result)
	return result, err
}
func (c *Cleaner) Run(ctx context.Context) error {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()
	for {
		bounded, cancel := context.WithTimeout(ctx, 10*time.Second)
		result, err := c.Once(bounded, 500)
		cancel()
		if err != nil && ctx.Err() == nil {
			slog.Warn("Expired authentication record cleanup temporarily unavailable")
		} else if result.Total() > 0 {
			slog.Info("Expired authentication records cleaned", "records", result.Total())
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}
