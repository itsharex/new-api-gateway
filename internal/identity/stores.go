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

// TokenLookup finds New API tokens by their canonical key.
// FindByCanonicalKey returns ErrTokenNotFound when no token exists for the key.
type TokenLookup interface {
	FindByCanonicalKey(ctx context.Context, canonicalKey string) (NewAPIToken, error)
}

type NewAPIToken struct {
	TokenID            int
	TokenName          string
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
	EmployeeNo          string
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
