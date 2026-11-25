package retention

import (
	"context"
	"testing"
)

func TestCleanupResultTotal(t *testing.T) {
	r := CleanupResult{
		Tickets:                1,
		Challenges:             2,
		Sessions:               3,
		Native:                 4,
		Portal:                 5,
		OIDC:                   6,
		Idempotency:            7,
		IdentityTokens:         8,
		IdentityCodes:          9,
		IdentityAuthorizations: 10,
	}
	if got := r.Total(); got != 55 {
		t.Fatalf("Total() = %d, want 55", got)
	}
	if got := (CleanupResult{}).Total(); got != 0 {
		t.Fatalf("empty Total() = %d, want 0", got)
	}
}

func TestCleanerRejectsInvalidBounds(t *testing.T) {
	cleaner := NewCleaner(nil)
	ctx := context.Background()
	for _, limit := range []int{-10, 0, 501, 10000} {
		if _, err := cleaner.Once(ctx, limit); err == nil {
			t.Errorf("Once(%d) accepted invalid bound", limit)
		}
		if _, err := cleaner.Preview(ctx, limit); err == nil {
			t.Errorf("Preview(%d) accepted invalid bound", limit)
		}
	}
}
