package identity

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

var ErrPostgresCacheDBRequired = errors.New("identity postgres cache db is nil")

type PostgresCacheDB interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

type PostgresCache struct {
	DB  PostgresCacheDB
	TTL time.Duration
	Now func() time.Time
}

func (c PostgresCache) Get(ctx context.Context, fingerprint string) (Snapshot, bool, error) {
	if isNilInterface(c.DB) {
		return Snapshot{}, false, ErrPostgresCacheDBRequired
	}
	now := c.now()
	var snapshot Snapshot
	var expired bool
	err := c.DB.QueryRow(ctx, postgresCacheGetSQL(), fingerprint, now).Scan(
		&snapshot.FingerprintDisplay,
		&snapshot.NewAPITokenID,
		&snapshot.TokenNameRaw,
		&snapshot.EmployeeNo,
		&snapshot.TokenStatus,
		&snapshot.TokenGroup,
		&snapshot.ExpiredTime,
		&snapshot.AccessedTime,
		&snapshot.RemainQuota,
		&snapshot.UsedQuota,
		&snapshot.UnlimitedQuota,
		&snapshot.ModelLimitsEnabled,
		&snapshot.ModelLimits,
		&expired,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return Snapshot{}, false, nil
	}
	if err != nil {
		return Snapshot{}, false, err
	}
	if expired {
		return Snapshot{}, false, nil
	}

	snapshot.TokenFingerprint = fingerprint
	snapshot.ResolutionStatus = ResolutionStatusResolved
	snapshot.IdentityCacheStatus = IdentityCacheStatusHit
	_, _ = c.DB.Exec(ctx, `UPDATE token_identity_cache SET last_seen_at = $2 WHERE token_fingerprint = $1`, fingerprint, now)
	return snapshot, true, nil
}

func (c PostgresCache) Set(ctx context.Context, snapshot Snapshot) error {
	if isNilInterface(c.DB) {
		return ErrPostgresCacheDBRequired
	}
	now := c.now()
	ttl := c.TTL
	if ttl <= 0 {
		ttl = 15 * time.Minute
	}
	_, err := c.DB.Exec(ctx, postgresCacheSetSQL(),
		snapshot.TokenFingerprint,
		snapshot.FingerprintDisplay,
		snapshot.NewAPITokenID,
		snapshot.TokenNameRaw,
		snapshot.EmployeeNo,
		snapshot.TokenStatus,
		snapshot.TokenGroup,
		snapshot.ExpiredTime,
		snapshot.AccessedTime,
		snapshot.RemainQuota,
		snapshot.UsedQuota,
		snapshot.UnlimitedQuota,
		snapshot.ModelLimitsEnabled,
		snapshot.ModelLimits,
		now,
		now.Add(ttl),
	)
	return err
}

func (c PostgresCache) now() time.Time {
	if c.Now != nil {
		return c.Now()
	}
	return time.Now().UTC()
}

func postgresCacheGetSQL() string {
	return `
SELECT
  fingerprint_display,
  new_api_token_id,
  token_name_raw,
  employee_no,
  token_status,
  token_group,
  token_expired_time,
  token_accessed_time,
  remain_quota,
  used_quota,
  unlimited_quota,
  model_limits_enabled,
  model_limits,
  expires_at IS NOT NULL AND expires_at <= $2 AS expired
FROM token_identity_cache
WHERE token_fingerprint = $1
LIMIT 1`
}

func postgresCacheSetSQL() string {
	return `
INSERT INTO token_identity_cache (
  token_fingerprint,
  fingerprint_display,
  new_api_token_id,
  token_name_raw,
  employee_no,
  token_status,
  token_group,
  token_expired_time,
  token_accessed_time,
  remain_quota,
  used_quota,
  unlimited_quota,
  model_limits_enabled,
  model_limits,
  resolved_at,
  refreshed_at,
  expires_at,
  last_seen_at,
  resolution_error
) VALUES (
  $1, $2, $3, $4, $5, $6, $7, $8, $9, $10,
  $11, $12, $13, $14, $15, $15, $16, $15, ''
)
ON CONFLICT (token_fingerprint) DO UPDATE SET
  fingerprint_display = EXCLUDED.fingerprint_display,
  new_api_token_id = EXCLUDED.new_api_token_id,
  token_name_raw = EXCLUDED.token_name_raw,
  employee_no = EXCLUDED.employee_no,
  token_status = EXCLUDED.token_status,
  token_group = EXCLUDED.token_group,
  token_expired_time = EXCLUDED.token_expired_time,
  token_accessed_time = EXCLUDED.token_accessed_time,
  remain_quota = EXCLUDED.remain_quota,
  used_quota = EXCLUDED.used_quota,
  unlimited_quota = EXCLUDED.unlimited_quota,
  model_limits_enabled = EXCLUDED.model_limits_enabled,
  model_limits = EXCLUDED.model_limits,
  refreshed_at = EXCLUDED.refreshed_at,
  expires_at = EXCLUDED.expires_at,
  last_seen_at = EXCLUDED.last_seen_at,
  resolution_error = EXCLUDED.resolution_error`
}
