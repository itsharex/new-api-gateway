package identity

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrNewAPILookupPoolRequired = errors.New("identity new-api lookup pool is nil")

// NewAPILookup resolves API keys to tokens + usernames by querying
// the new-api PostgreSQL database (tokens JOIN users).
type NewAPILookup struct {
	Pool *pgxpool.Pool
}

func (l NewAPILookup) FindByCanonicalKey(ctx context.Context, canonicalKey string) (NewAPIToken, error) {
	if l.Pool == nil {
		return NewAPIToken{}, ErrNewAPILookupPoolRequired
	}

	var token NewAPIToken
	err := l.Pool.QueryRow(ctx, newAPIUserLookupQuery(), canonicalKey).Scan(
		&token.TokenID,
		&token.TokenName,
		&token.Username,
		&token.TokenStatus,
		&token.TokenGroup,
		&token.ExpiredTime,
		&token.AccessedTime,
		&token.RemainQuota,
		&token.UsedQuota,
		&token.UnlimitedQuota,
		&token.ModelLimitsEnabled,
		&token.ModelLimits,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return NewAPIToken{}, ErrTokenNotFound
	}
	return token, err
}

func newAPIUserLookupQuery() string {
	return `
SELECT
  t.id AS token_id,
  t.name AS token_name,
  u.username,
  t.status AS token_status,
  t."group" AS token_group,
  t.expired_time,
  t.accessed_time,
  t.remain_quota,
  t.used_quota,
  t.unlimited_quota,
  t.model_limits_enabled,
  t.model_limits
FROM tokens t
JOIN users u ON t.user_id = u.id
WHERE t.key = $1
  AND t.deleted_at IS NULL
  AND u.status = 1
LIMIT 1`
}
