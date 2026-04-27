package identity

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrPostgresTokenLookupPoolRequired = errors.New("identity postgres token lookup pool is nil")

type PostgresTokenLookup struct {
	Pool *pgxpool.Pool
}

func (l PostgresTokenLookup) FindByCanonicalKey(ctx context.Context, canonicalKey string) (NewAPIToken, error) {
	if l.Pool == nil {
		return NewAPIToken{}, ErrPostgresTokenLookupPoolRequired
	}

	var token NewAPIToken
	err := l.Pool.QueryRow(ctx, newAPITokenQuery(), canonicalKey).Scan(
		&token.TokenID,
		&token.TokenName,
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

func newAPITokenQuery() string {
	return `
SELECT
  id AS token_id,
  name AS token_name,
  status AS token_status,
  "group" AS token_group,
  expired_time,
  accessed_time,
  remain_quota,
  used_quota,
  unlimited_quota,
  model_limits_enabled,
  model_limits
FROM tokens
WHERE key = $1
LIMIT 1`
}
