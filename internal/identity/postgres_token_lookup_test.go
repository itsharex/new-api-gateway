package identity

import (
	"strings"
	"testing"
)

func TestNewAPITokenQueryUsesOnlyTokensTable(t *testing.T) {
	query := newAPITokenQuery()
	if !strings.Contains(query, "FROM tokens") {
		t.Fatalf("query does not read tokens table: %s", query)
	}
	if strings.Contains(query, "JOIN users") {
		t.Fatalf("query must not join users table: %s", query)
	}
	if !strings.Contains(query, `"group"`) {
		t.Fatalf("query must quote reserved group column for PostgreSQL: %s", query)
	}
}
