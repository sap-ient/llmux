package keys

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/llmux/llmux/core/config"
	"github.com/redis/go-redis/v9"
)

func testDSN(t *testing.T) string {
	dsn := os.Getenv("LLMUX_TEST_POSTGRES")
	if dsn == "" {
		t.Skip("set LLMUX_TEST_POSTGRES to run Postgres integration tests")
	}
	return dsn
}

// testSchema is the dedicated schema integration tests run against, mirroring
// the production default ("llmux") so the shared-database path is exercised.
const testSchema = "llmux"

// qualifiedTable is the fully-qualified, sanitized table identifier used by the
// store under testSchema.
func qualifiedTable() string {
	return pgx.Identifier{testSchema, "llmux_keys"}.Sanitize()
}

// resetSchema drops the test schema (and its tables) for a clean slate.
func resetSchema(t *testing.T, ctx context.Context, dsn string) {
	t.Helper()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if _, err := pool.Exec(ctx, fmt.Sprintf("DROP SCHEMA IF EXISTS %s CASCADE", pgx.Identifier{testSchema}.Sanitize())); err != nil {
		t.Fatal(err)
	}
}

// TestPGStorePersistsAcrossInstances proves budgets/spend survive a restart and
// are shared by another instance (the multi-replica correctness property).
func TestPGStorePersistsAcrossInstances(t *testing.T) {
	dsn := testDSN(t)
	ctx := context.Background()

	// Clean slate.
	resetSchema(t, ctx, dsn)

	cfgs := []config.KeyConfig{{Key: "sk-pg", Name: "team", BudgetUSD: 1.0, AllowedModels: []string{"gpt-4o"}}}
	s1, err := NewPGStore(ctx, dsn, testSchema, cfgs, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer s1.Close()

	if k, ok := s1.Lookup("sk-pg"); !ok || k.Name != "team" || !k.AllowsModel("gpt-4o") {
		t.Fatalf("lookup/seed failed: %+v", k)
	}
	if s1.OverBudget("sk-pg") {
		t.Fatal("should start under budget")
	}
	s1.AddSpend("sk-pg", 0.6)
	if s1.OverBudget("sk-pg") {
		t.Fatal("0.6 < 1.0 should be under budget")
	}
	s1.AddSpend("sk-pg", 0.6) // total 1.2

	// A second instance (simulating another replica / restart) sees the spend.
	s2, err := NewPGStore(ctx, dsn, testSchema, cfgs, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	if got := s2.Spend("sk-pg"); got < 1.19 || got > 1.21 {
		t.Fatalf("cross-instance spend=%v, want ~1.2", got)
	}
	if !s2.OverBudget("sk-pg") {
		t.Fatal("second instance must see over-budget")
	}
	if len(s2.Keys()) < 1 {
		t.Fatal("Keys() empty")
	}
}

// TestPGStoreKeyHashedAtRest verifies the security property: the raw bearer
// token is NEVER stored as the Postgres "key" column value. The column must
// contain sha256(rawToken) so a PG dump never yields live credentials.
func TestPGStoreKeyHashedAtRest(t *testing.T) {
	dsn := testDSN(t)
	ctx := context.Background()

	resetSchema(t, ctx, dsn)

	const rawToken = "sk-at-rest-secret"
	cfgs := []config.KeyConfig{{Key: rawToken, Name: "atrest", BudgetUSD: 1.0}}
	s, err := NewPGStore(ctx, dsn, testSchema, cfgs, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	// Lookup must still work (validates via hash).
	k, ok := s.Lookup(rawToken)
	if !ok || k.Name != "atrest" {
		t.Fatalf("Lookup after hash-seed failed: ok=%v k=%+v", ok, k)
	}

	// Directly inspect the Postgres row: the "key" column must hold the hash,
	// not the raw token. Read from the schema-qualified table to prove the table
	// really lives under the dedicated schema (shared-database path).
	p2, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer p2.Close()

	rows, err := p2.Query(ctx, fmt.Sprintf("SELECT key FROM %s", qualifiedTable()))
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var stored string
		if err := rows.Scan(&stored); err != nil {
			t.Fatal(err)
		}
		if stored == rawToken {
			t.Fatalf("raw token found in Postgres key column: %q", stored)
		}
		if strings.Contains(stored, rawToken) {
			t.Fatalf("raw token appears inside stored key column value: %q", stored)
		}
		wantHash := HashToken(rawToken)
		if stored != wantHash {
			t.Fatalf("stored key = %q, want hash %q", stored, wantHash)
		}
	}
}

// TestRedisLimiterKeyHashedAtRest verifies that the Redis rate-limit key
// contains the sha256 hash of the token, not the raw token itself.
func TestRedisLimiterKeyHashedAtRest(t *testing.T) {
	rdb := testRedisClient(t)
	defer rdb.FlushDB(context.Background())
	lim := NewRedisLimiter(rdb)
	const rawToken = "sk-redis-secret"

	// Trigger a rate-limit entry.
	lim.Allow(rawToken, 100)

	// SCAN for all keys; none should contain the raw token.
	ctx := context.Background()
	keys, err := rdb.Keys(ctx, "*").Result()
	if err != nil {
		t.Fatal(err)
	}
	for _, k := range keys {
		if strings.Contains(k, rawToken) {
			t.Fatalf("raw token found in Redis key: %q", k)
		}
		// The key must contain the hash instead.
		if !strings.Contains(k, HashToken(rawToken)) {
			t.Fatalf("Redis key does not contain expected hash: %q", k)
		}
	}
}

// TestPGStoreUsesDedicatedSchema proves that table creation lands under the
// dedicated "llmux" schema (the cloud-consolidation property: llmux shares one
// database with other products without colliding in public). It also exercises
// the schema default: passing "" must resolve to DefaultSchema.
func TestPGStoreUsesDedicatedSchema(t *testing.T) {
	dsn := testDSN(t)
	ctx := context.Background()

	resetSchema(t, ctx, dsn)

	cfgs := []config.KeyConfig{{Key: "sk-schema", Name: "schema-test", BudgetUSD: 1.0}}
	// Empty schema must default to DefaultSchema ("llmux").
	s, err := NewPGStore(ctx, dsn, "", cfgs, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if s.schema != DefaultSchema {
		t.Fatalf("schema = %q, want default %q", s.schema, DefaultSchema)
	}

	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	// The table must exist under the "llmux" schema...
	var inLlmux bool
	if err := pool.QueryRow(ctx, `SELECT EXISTS (
		SELECT 1 FROM information_schema.tables
		WHERE table_schema=$1 AND table_name='llmux_keys')`, DefaultSchema).Scan(&inLlmux); err != nil {
		t.Fatal(err)
	}
	if !inLlmux {
		t.Fatalf("table llmux_keys not found under schema %q", DefaultSchema)
	}

	// ...and must NOT have been created in public.
	var inPublic bool
	if err := pool.QueryRow(ctx, `SELECT EXISTS (
		SELECT 1 FROM information_schema.tables
		WHERE table_schema='public' AND table_name='llmux_keys')`).Scan(&inPublic); err != nil {
		t.Fatal(err)
	}
	if inPublic {
		t.Fatal("table llmux_keys leaked into public schema")
	}
}

func testRedisClient(t *testing.T) *redis.Client {
	addr := os.Getenv("LLMUX_TEST_REDIS")
	if addr == "" {
		t.Skip("set LLMUX_TEST_REDIS to run Redis integration tests")
	}
	rdb := redis.NewClient(&redis.Options{Addr: addr, DB: 15})
	if err := rdb.Ping(context.Background()).Err(); err != nil {
		t.Skipf("redis not reachable: %v", err)
	}
	return rdb
}

func TestRedisLimiterFixedWindow(t *testing.T) {
	rdb := testRedisClient(t)
	defer rdb.FlushDB(context.Background())
	lim := NewRedisLimiter(rdb)
	tok := fmt.Sprintf("tok-%d", time.Now().UnixNano())

	if !lim.Allow(tok, 2) || !lim.Allow(tok, 2) {
		t.Fatal("first two requests should pass (rpm=2)")
	}
	if lim.Allow(tok, 2) {
		t.Fatal("third request in window should be denied")
	}
}
