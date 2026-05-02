package identity

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestNewAPILookupQueryJoinTokensAndUsers(t *testing.T) {
	query := normalizeSQL(newAPIUserLookupQuery())
	if !strings.Contains(query, "from tokens t") {
		t.Fatalf("query does not read tokens table: %s", query)
	}
	if !strings.Contains(query, "join users u") {
		t.Fatalf("query must join users table: %s", query)
	}
	if !strings.Contains(query, "u.username") {
		t.Fatalf("query must select u.username: %s", query)
	}
	if !strings.Contains(query, "t.deleted_at is null") {
		t.Fatalf("query must filter soft-deleted tokens: %s", query)
	}
	if !strings.Contains(query, "u.status = 1") {
		t.Fatalf("query must filter enabled users: %s", query)
	}
	if !strings.Contains(query, "where t.key = $1") {
		t.Fatalf("query must filter by key: %s", query)
	}
	if !strings.Contains(query, "limit 1") {
		t.Fatalf("query must have limit 1: %s", query)
	}
}

func TestNewAPILookupQuerySelectsColumnsInOrder(t *testing.T) {
	query := normalizeSQL(newAPIUserLookupQuery())
	assertSQLFragmentsInOrder(t, query,
		"t.id as token_id",
		"t.name as token_name",
		"u.username",
		"t.status as token_status",
		`"group" as token_group`,
		"t.expired_time",
		"t.accessed_time",
		"t.remain_quota",
		"t.used_quota",
		"t.unlimited_quota",
		"t.model_limits_enabled",
		"t.model_limits",
		"from tokens t",
		"join users u on t.user_id = u.id",
		"where t.key = $1",
		"and t.deleted_at is null",
		"and u.status = 1",
		"limit 1",
	)
}

func TestNewAPILookupRequiresPool(t *testing.T) {
	_, err := NewAPILookup{}.FindByCanonicalKey(context.Background(), "key")
	if !errors.Is(err, ErrNewAPILookupPoolRequired) {
		t.Fatalf("FindByCanonicalKey error = %v, want %v", err, ErrNewAPILookupPoolRequired)
	}
}

func normalizeSQL(s string) string {
	return strings.Join(strings.Fields(strings.ToLower(s)), " ")
}

func assertSQLFragmentsInOrder(t *testing.T, sql string, fragments ...string) {
	t.Helper()
	idx := 0
	for _, frag := range fragments {
		pos := strings.Index(sql[idx:], frag)
		if pos < 0 {
			t.Fatalf("fragment %q not found after position %d in SQL: %s", frag, idx, sql)
		}
		idx += pos + len(frag)
	}
}
