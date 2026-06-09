package identity

import (
	"context"
	"errors"
)

// ErrTokenNotFound is returned by TokenLookup when no token exists for a key.
var ErrTokenNotFound = errors.New("token not found")

type Cache interface {
	Get(ctx context.Context, fingerprint string) (Snapshot, bool, error)
	Set(ctx context.Context, snapshot Snapshot) error
}

type ChainCache struct {
	Caches []Cache
}

func (c ChainCache) Get(ctx context.Context, fingerprint string) (Snapshot, bool, error) {
	var firstErr error
	for index, cache := range c.Caches {
		if isNilInterface(cache) {
			continue
		}
		snapshot, ok, err := cache.Get(ctx, fingerprint)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		if !ok {
			continue
		}
		for backfillIndex, backfillCache := range c.Caches {
			if backfillIndex == index || isNilInterface(backfillCache) {
				continue
			}
			_ = backfillCache.Set(ctx, snapshot)
		}
		return snapshot, true, nil
	}
	return Snapshot{}, false, firstErr
}

func (c ChainCache) Set(ctx context.Context, snapshot Snapshot) error {
	var firstErr error
	for _, cache := range c.Caches {
		if isNilInterface(cache) {
			continue
		}
		if err := cache.Set(ctx, snapshot); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// TokenLookup finds New API tokens by their canonical key.
// FindByCanonicalKey returns ErrTokenNotFound when no token exists for the key.
type TokenLookup interface {
	FindByCanonicalKey(ctx context.Context, canonicalKey string) (NewAPIToken, error)
}

type NewAPIToken struct {
	TokenID            int
	TokenName          string
	Username           string
	TokenStatus        int
	TokenGroup         string
	ExpiredTime        int64
	AccessedTime       int64
	RemainQuota        int
	UsedQuota          int
	UnlimitedQuota     bool
	ModelLimitsEnabled bool
	ModelLimits        string
}

type Snapshot struct {
	TokenFingerprint    string
	FingerprintDisplay  string
	NewAPITokenID       int
	TokenNameRaw        string
	Username            string
	TokenStatus         int
	TokenGroup          string
	ExpiredTime         int64
	AccessedTime        int64
	RemainQuota         int
	UsedQuota           int
	UnlimitedQuota      bool
	ModelLimitsEnabled  bool
	ModelLimits         string
	ResolutionStatus    string
	IdentityCacheStatus string
}
