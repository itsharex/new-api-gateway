package identity

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestNewAPITokenQueryUsesOnlyTokensTable(t *testing.T) {
	query := normalizeSQL(newAPITokenQuery())
	if !strings.Contains(query, "from tokens") {
		t.Fatalf("query does not read tokens table: %s", query)
	}
	if strings.Contains(query, "join users") {
		t.Fatalf("query must not join users table: %s", query)
	}
	if strings.Contains(query, "from users") {
		t.Fatalf("query must not read users table: %s", query)
	}
	if !strings.Contains(query, `"group"`) {
		t.Fatalf("query must quote reserved group column for PostgreSQL: %s", query)
	}
}

func TestNewAPITokenQuerySelectsTokenColumnsInOrder(t *testing.T) {
	query := normalizeSQL(newAPITokenQuery())
	assertSQLFragmentsInOrder(t, query,
		"id as token_id",
		"name as token_name",
		"status as token_status",
		`"group" as token_group`,
		"expired_time",
		"accessed_time",
		"remain_quota",
		"used_quota",
		"unlimited_quota",
		"model_limits_enabled",
		"model_limits",
		"from tokens",
		"where key = $1",
		"limit 1",
	)
}

func TestPostgresTokenLookupRequiresPool(t *testing.T) {
	_, err := PostgresTokenLookup{}.FindByCanonicalKey(context.Background(), "canonical")
	if !errors.Is(err, ErrPostgresTokenLookupPoolRequired) {
		t.Fatalf("FindByCanonicalKey error = %v, want %v", err, ErrPostgresTokenLookupPoolRequired)
	}
}

func normalizeSQL(query string) string {
	return strings.Join(strings.Fields(strings.ToLower(query)), " ")
}

func assertSQLFragmentsInOrder(t *testing.T, query string, fragments ...string) {
	t.Helper()

	offset := 0
	for _, fragment := range fragments {
		index := strings.Index(query[offset:], fragment)
		if index < 0 {
			t.Fatalf("query missing %q after offset %d: %s", fragment, offset, query)
		}
		offset += index + len(fragment)
	}
}
