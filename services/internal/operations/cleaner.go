package operations

import (
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/retention"
)

type CleanupResult = retention.CleanupResult
type Cleaner = retention.Cleaner

func NewCleaner(pool *pgxpool.Pool) *Cleaner { return retention.NewCleaner(pool) }
