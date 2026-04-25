package identity

import "context"

type Cache interface {
	Get(ctx context.Context, fingerprint string) (Snapshot, bool, error)
	Set(ctx context.Context, snapshot Snapshot) error
}

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
