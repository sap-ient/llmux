package keys

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/llmux/llmux/core/config"
)

// DefaultSchema is the Postgres schema llmux uses when none is configured. A
// dedicated schema lets llmux share one database (e.g. a single Neon database)
// with the other Vulos products without table-name collisions.
const DefaultSchema = "llmux"

// PGStore is a Postgres-backed Store: key definitions + cumulative spend live in
// Postgres, so budgets are correct across replicas. Rate limiting is delegated
// to a Limiter (Redis-backed for cross-replica correctness, or in-memory).
// Key definitions are seeded from config and cached in memory for fast lookup.
type PGStore struct {
	pool    *pgxpool.Pool
	limiter Limiter

	// schema is the Postgres schema holding llmux's tables (default "llmux").
	schema string
	// table is the fully-qualified, sanitized table identifier (schema.keys).
	table string

	mu   sync.RWMutex
	keys map[string]*Key
}

// Limiter enforces a per-minute request limit for a token.
type Limiter interface {
	Allow(token string, rpm int) bool
}

// NewPGStore connects, migrates, seeds keys from config, and returns a store.
// limiter may be nil (defaults to an in-memory token-bucket limiter). schema is
// the Postgres schema to hold llmux's tables; empty defaults to DefaultSchema
// ("llmux") so llmux can share one database with other Vulos products.
func NewPGStore(ctx context.Context, dsn, schema string, cfgs []config.KeyConfig, limiter Limiter) (*PGStore, error) {
	if schema == "" {
		schema = DefaultSchema
	}
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return nil, fmt.Errorf("postgres connect: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("postgres ping: %w", err)
	}
	if limiter == nil {
		limiter = NewMemLimiter()
	}
	// Sanitize the schema/table as SQL identifiers (defends against injection
	// and quotes mixed-case/reserved names) since they are interpolated into DDL
	// and DML strings rather than passed as parameters.
	table := pgx.Identifier{schema, "llmux_keys"}.Sanitize()
	s := &PGStore{pool: pool, limiter: limiter, schema: schema, table: table, keys: map[string]*Key{}}
	if err := s.migrate(ctx); err != nil {
		pool.Close()
		return nil, err
	}
	if err := s.seed(ctx, cfgs); err != nil {
		pool.Close()
		return nil, err
	}
	return s, nil
}

// Close releases the pool.
func (s *PGStore) Close() { s.pool.Close() }

func (s *PGStore) migrate(ctx context.Context) error {
	// Create the dedicated schema first so the table can live under it on a
	// database shared with other products. CREATE SCHEMA/TABLE IF NOT EXISTS are
	// idempotent, so this is safe to run on every startup.
	if _, err := s.pool.Exec(ctx, fmt.Sprintf(`CREATE SCHEMA IF NOT EXISTS %s`, pgx.Identifier{s.schema}.Sanitize())); err != nil {
		return fmt.Errorf("create schema %q: %w", s.schema, err)
	}
	_, err := s.pool.Exec(ctx, fmt.Sprintf(`
CREATE TABLE IF NOT EXISTS %s (
  key            TEXT PRIMARY KEY,
  name           TEXT NOT NULL DEFAULT '',
  budget_usd     DOUBLE PRECISION NOT NULL DEFAULT 0,
  rpm            INTEGER NOT NULL DEFAULT 0,
  allowed_models TEXT[] NOT NULL DEFAULT '{}',
  spend_usd      DOUBLE PRECISION NOT NULL DEFAULT 0,
  created_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);`, s.table))
	return err
}

// seed upserts config keys (preserving existing spend) and caches them.
// The Postgres "key" column stores sha256(rawToken) so that a PG dump never
// exposes live bearer credentials. The in-memory map is keyed by the raw
// token for fast O(1) Lookup; DB operations hash on the fly.
func (s *PGStore) seed(ctx context.Context, cfgs []config.KeyConfig) error {
	for _, c := range cfgs {
		models := c.AllowedModels
		if models == nil {
			models = []string{} // NOT NULL array column
		}
		h := HashToken(c.Key)
		_, err := s.pool.Exec(ctx, fmt.Sprintf(`
INSERT INTO %s (key, name, budget_usd, rpm, allowed_models)
VALUES ($1,$2,$3,$4,$5)
ON CONFLICT (key) DO UPDATE SET
  name=EXCLUDED.name, budget_usd=EXCLUDED.budget_usd,
  rpm=EXCLUDED.rpm, allowed_models=EXCLUDED.allowed_models`, s.table),
			h, c.Name, c.BudgetUSD, c.RPM, models)
		if err != nil {
			return fmt.Errorf("seed key: %w", err)
		}
		// Populate the in-memory cache with the raw token as the map key.
		// Key.Key holds the raw token so callers (admin listing, cacheScope, etc.)
		// always deal in raw tokens; only DB/Redis paths hash.
		s.keys[c.Key] = &Key{
			Key: c.Key, Name: c.Name, BudgetUSD: c.BudgetUSD,
			RPM: c.RPM, AllowedModels: models,
		}
	}
	return nil
}

// Lookup implements Store (from the in-memory key cache).
func (s *PGStore) Lookup(token string) (*Key, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	k, ok := s.keys[token]
	return k, ok
}

// Keys implements Store.
func (s *PGStore) Keys() []*Key {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*Key, 0, len(s.keys))
	for _, k := range s.keys {
		out = append(out, k)
	}
	return out
}

// Allow implements Store via the configured limiter.
func (s *PGStore) Allow(token string) bool {
	k, ok := s.Lookup(token)
	if !ok || k.RPM <= 0 {
		return true
	}
	return s.limiter.Allow(token, k.RPM)
}

// AddSpend implements Store (atomic increment in Postgres).
// token is the raw bearer token; it is hashed before the DB UPDATE so the
// plaintext credential is never written to the spend row.
func (s *PGStore) AddSpend(token string, usd float64) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, _ = s.pool.Exec(ctx, fmt.Sprintf(`UPDATE %s SET spend_usd = spend_usd + $2 WHERE key=$1`, s.table), HashToken(token), usd)
}

// Spend implements Store.
// token is the raw bearer token; it is hashed before the DB SELECT.
func (s *PGStore) Spend(token string) float64 {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var v float64
	_ = s.pool.QueryRow(ctx, fmt.Sprintf(`SELECT spend_usd FROM %s WHERE key=$1`, s.table), HashToken(token)).Scan(&v)
	return v
}

// OverBudget implements Store.
func (s *PGStore) OverBudget(token string) bool {
	k, ok := s.Lookup(token)
	if !ok || k.BudgetUSD <= 0 {
		return false
	}
	return s.Spend(token) >= k.BudgetUSD
}
