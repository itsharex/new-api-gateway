package identity

import (
	"context"
	"errors"
	"reflect"
)

const (
	ResolutionStatusResolved         = "resolved"
	ResolutionStatusDBError          = "db_error"
	ResolutionStatusNotFound         = "not_found"
	ResolutionStatusExtractFailed    = "extract_failed"
	ResolutionStatusResolveFailed    = "resolve_failed"

	IdentityCacheStatusHit          = "cache_hit"
	IdentityCacheStatusMissDBLookup = "miss_db_lookup"
	IdentityCacheStatusCacheError   = "cache_error"
)

var errResolverLookupRequired = errors.New("identity resolver lookup is nil")

// Resolver resolves API key fingerprints to user identities via a
// TokenLookup (typically NewAPILookup) with optional caching.
type Resolver struct {
	Cache  Cache
	Lookup TokenLookup
}

func (r Resolver) Resolve(ctx context.Context, canonicalKey, fingerprintValue, fingerprintDisplay string) (Snapshot, error) {
	if isNilInterface(r.Lookup) {
		return Snapshot{}, errResolverLookupRequired
	}

	cacheStatus := IdentityCacheStatusMissDBLookup
	cache := r.Cache
	if !isNilInterface(cache) {
		cached, ok, err := cache.Get(ctx, fingerprintValue)
		if err != nil {
			cacheStatus = IdentityCacheStatusCacheError
		} else if ok {
			cached.IdentityCacheStatus = IdentityCacheStatusHit
			return cached, nil
		}
	}

	token, err := r.Lookup.FindByCanonicalKey(ctx, canonicalKey)
	if err != nil {
		status := ResolutionStatusDBError
		if errors.Is(err, ErrTokenNotFound) {
			status = ResolutionStatusNotFound
		}
		return Snapshot{
			TokenFingerprint:    fingerprintValue,
			FingerprintDisplay:  fingerprintDisplay,
			ResolutionStatus:    status,
			IdentityCacheStatus: cacheStatus,
		}, nil
	}

	snapshot := Snapshot{
		TokenFingerprint:    fingerprintValue,
		FingerprintDisplay:  fingerprintDisplay,
		NewAPITokenID:       token.TokenID,
		TokenNameRaw:        token.TokenName,
		Username:            token.Username,
		TokenStatus:         token.TokenStatus,
		TokenGroup:          token.TokenGroup,
		ExpiredTime:         token.ExpiredTime,
		AccessedTime:        token.AccessedTime,
		RemainQuota:         token.RemainQuota,
		UsedQuota:           token.UsedQuota,
		UnlimitedQuota:      token.UnlimitedQuota,
		ModelLimitsEnabled:  token.ModelLimitsEnabled,
		ModelLimits:         token.ModelLimits,
		ResolutionStatus:    ResolutionStatusResolved,
		IdentityCacheStatus: cacheStatus,
	}
	if !isNilInterface(cache) && snapshot.ResolutionStatus == ResolutionStatusResolved {
		_ = cache.Set(ctx, snapshot)
	}
	return snapshot, nil
}

func isNilInterface[T any](v T) bool {
	if any(v) == nil {
		return true
	}

	value := reflect.ValueOf(v)
	switch value.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Ptr, reflect.Slice:
		return value.IsNil()
	default:
		return false
	}
}
