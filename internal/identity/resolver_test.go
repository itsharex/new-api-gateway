package identity

import (
	"context"
	"errors"
	"fmt"
	"testing"
)

type fakeCache struct {
	value        Snapshot
	ok           bool
	getErr       error
	setErr       error
	getCalls     int
	setCalls     int
	setSnapshots []Snapshot
}

func (f *fakeCache) Get(ctx context.Context, fingerprint string) (Snapshot, bool, error) {
	f.getCalls++
	return f.value, f.ok, f.getErr
}

func (f *fakeCache) Set(ctx context.Context, snapshot Snapshot) error {
	f.setCalls++
	f.setSnapshots = append(f.setSnapshots, snapshot)
	if f.setErr != nil {
		return f.setErr
	}
	f.value = snapshot
	f.ok = true
	return nil
}

type fakeLookup struct {
	token        NewAPIToken
	err          error
	calls        int
	canonicalKey string
}

func (f *fakeLookup) FindByCanonicalKey(ctx context.Context, canonicalKey string) (NewAPIToken, error) {
	f.calls++
	f.canonicalKey = canonicalKey
	return f.token, f.err
}

func TestResolverUsesCacheHit(t *testing.T) {
	cache := &fakeCache{ok: true, value: Snapshot{TokenFingerprint: "fp", Username: "alice", ResolutionStatus: ResolutionStatusResolved}}
	lookup := &fakeLookup{}
	resolver := Resolver{Cache: cache, Lookup: lookup}

	got, err := resolver.Resolve(context.Background(), "canonical", "fp", "tkfp_abc")
	if err != nil {
		t.Fatalf("Resolve error: %v", err)
	}
	if got.Username != "alice" || got.IdentityCacheStatus != IdentityCacheStatusHit {
		t.Fatalf("unexpected snapshot %#v", got)
	}
	if lookup.calls != 0 {
		t.Fatalf("lookup called %d times on cache hit", lookup.calls)
	}
}

func TestResolverReturnsErrorForNilLookup(t *testing.T) {
	resolver := Resolver{}

	if _, err := resolver.Resolve(context.Background(), "canonical", "fp", "tkfp_abc"); err == nil {
		t.Fatal("expected error for nil lookup")
	}
}

func TestResolverReturnsErrorForTypedNilLookup(t *testing.T) {
	var lookup *fakeLookup
	resolver := Resolver{Lookup: lookup}

	if _, err := resolver.Resolve(context.Background(), "canonical", "fp", "tkfp_abc"); err == nil {
		t.Fatal("expected error for typed nil lookup")
	}
}

func TestResolverTreatsTypedNilCacheAsNoCache(t *testing.T) {
	var cache *fakeCache
	lookup := &fakeLookup{token: NewAPIToken{TokenID: 12, Username: "bob", TokenStatus: 1}}
	resolver := Resolver{Cache: cache, Lookup: lookup}

	got, err := resolver.Resolve(context.Background(), "canonical", "fp", "tkfp_abc")
	if err != nil {
		t.Fatalf("Resolve error: %v", err)
	}
	if lookup.calls != 1 {
		t.Fatalf("lookup called %d times", lookup.calls)
	}
	if got.ResolutionStatus != ResolutionStatusResolved || got.IdentityCacheStatus != IdentityCacheStatusMissDBLookup {
		t.Fatalf("unexpected snapshot %#v", got)
	}
}

func TestResolverUsesUsernameFromLookup(t *testing.T) {
	cache := &fakeCache{}
	resolver := Resolver{
		Cache:  cache,
		Lookup: &fakeLookup{token: NewAPIToken{TokenID: 12, Username: "charlie", TokenStatus: 1}},
	}

	got, err := resolver.Resolve(context.Background(), "canonical", "fp", "tkfp_abc")
	if err != nil {
		t.Fatalf("Resolve error: %v", err)
	}
	if got.Username != "charlie" {
		t.Fatalf("Username = %q", got.Username)
	}
	if got.ResolutionStatus != ResolutionStatusResolved {
		t.Fatalf("ResolutionStatus = %q", got.ResolutionStatus)
	}
	if cache.setCalls != 1 {
		t.Fatalf("cache Set called %d times", cache.setCalls)
	}
}

func TestResolverCopiesTokenMetadata(t *testing.T) {
	token := NewAPIToken{
		TokenID:            12,
		TokenName:          "my-token",
		Username:           "dave",
		TokenStatus:        1,
		TokenGroup:         "staff",
		ExpiredTime:        1711111111,
		AccessedTime:       1712222222,
		RemainQuota:        300,
		UsedQuota:          25,
		UnlimitedQuota:     true,
		ModelLimitsEnabled: true,
		ModelLimits:        `{"gpt-4":10}`,
	}
	resolver := Resolver{Lookup: &fakeLookup{token: token}}

	got, err := resolver.Resolve(context.Background(), "canonical", "fp", "tkfp_abc")
	if err != nil {
		t.Fatalf("Resolve error: %v", err)
	}
	if got.TokenFingerprint != "fp" || got.FingerprintDisplay != "tkfp_abc" {
		t.Fatalf("fingerprint fields not copied: %#v", got)
	}
	if got.NewAPITokenID != token.TokenID || got.TokenNameRaw != token.TokenName || got.Username != token.Username {
		t.Fatalf("identity metadata not copied: %#v", got)
	}
	if got.TokenStatus != token.TokenStatus || got.TokenGroup != token.TokenGroup {
		t.Fatalf("basic token metadata not copied: %#v", got)
	}
	if got.ExpiredTime != token.ExpiredTime || got.AccessedTime != token.AccessedTime || got.RemainQuota != token.RemainQuota || got.UsedQuota != token.UsedQuota {
		t.Fatalf("quota/time metadata not copied: %#v", got)
	}
	if got.UnlimitedQuota != token.UnlimitedQuota || got.ModelLimitsEnabled != token.ModelLimitsEnabled || got.ModelLimits != token.ModelLimits {
		t.Fatalf("limit metadata not copied: %#v", got)
	}
}

func TestResolverMarksTokenNotFound(t *testing.T) {
	cache := &fakeCache{}
	resolver := Resolver{
		Cache:  cache,
		Lookup: &fakeLookup{err: ErrTokenNotFound},
	}

	got, err := resolver.Resolve(context.Background(), "canonical", "fp", "tkfp_abc")
	if err != nil {
		t.Fatalf("Resolve error: %v", err)
	}
	if got.ResolutionStatus != ResolutionStatusNotFound {
		t.Fatalf("ResolutionStatus = %q", got.ResolutionStatus)
	}
	if got.TokenFingerprint != "fp" || got.FingerprintDisplay != "tkfp_abc" {
		t.Fatalf("fingerprint fields not copied: %#v", got)
	}
	if cache.setCalls != 0 {
		t.Fatalf("cache Set called %d times for token not found", cache.setCalls)
	}
}

func TestResolverMarksWrappedTokenNotFound(t *testing.T) {
	cache := &fakeCache{}
	resolver := Resolver{
		Cache:  cache,
		Lookup: &fakeLookup{err: fmt.Errorf("lookup failed: %w", ErrTokenNotFound)},
	}

	got, err := resolver.Resolve(context.Background(), "canonical", "fp", "tkfp_abc")
	if err != nil {
		t.Fatalf("Resolve error: %v", err)
	}
	if got.ResolutionStatus != ResolutionStatusNotFound {
		t.Fatalf("ResolutionStatus = %q", got.ResolutionStatus)
	}
	if cache.setCalls != 0 {
		t.Fatalf("cache Set called %d times for wrapped token not found", cache.setCalls)
	}
}

func TestResolverMarksLookupErrorsAsDBError(t *testing.T) {
	cache := &fakeCache{}
	resolver := Resolver{
		Cache:  cache,
		Lookup: &fakeLookup{err: errors.New("database unavailable")},
	}

	got, err := resolver.Resolve(context.Background(), "canonical", "fp", "tkfp_abc")
	if err != nil {
		t.Fatalf("Resolve error: %v", err)
	}
	if got.ResolutionStatus != ResolutionStatusDBError {
		t.Fatalf("ResolutionStatus = %q", got.ResolutionStatus)
	}
	if got.IdentityCacheStatus != IdentityCacheStatusMissDBLookup {
		t.Fatalf("IdentityCacheStatus = %q", got.IdentityCacheStatus)
	}
	if cache.setCalls != 0 {
		t.Fatalf("cache Set called %d times for lookup error", cache.setCalls)
	}
}

func TestResolverContinuesToLookupAfterCacheGetError(t *testing.T) {
	cache := &fakeCache{getErr: errors.New("redis unavailable")}
	lookup := &fakeLookup{token: NewAPIToken{TokenID: 12, Username: "eve", TokenStatus: 1}}
	resolver := Resolver{Cache: cache, Lookup: lookup}

	got, err := resolver.Resolve(context.Background(), "canonical", "fp", "tkfp_abc")
	if err != nil {
		t.Fatalf("Resolve error: %v", err)
	}
	if lookup.calls != 1 {
		t.Fatalf("lookup called %d times", lookup.calls)
	}
	if got.ResolutionStatus != ResolutionStatusResolved || got.IdentityCacheStatus != IdentityCacheStatusCacheError {
		t.Fatalf("unexpected snapshot %#v", got)
	}
	if cache.setCalls != 1 {
		t.Fatalf("cache Set called %d times", cache.setCalls)
	}
}

func TestResolverIgnoresCacheSetError(t *testing.T) {
	cache := &fakeCache{setErr: errors.New("redis unavailable")}
	resolver := Resolver{
		Cache:  cache,
		Lookup: &fakeLookup{token: NewAPIToken{TokenID: 12, Username: "frank", TokenStatus: 1}},
	}

	got, err := resolver.Resolve(context.Background(), "canonical", "fp", "tkfp_abc")
	if err != nil {
		t.Fatalf("Resolve error: %v", err)
	}
	if got.ResolutionStatus != ResolutionStatusResolved {
		t.Fatalf("ResolutionStatus = %q", got.ResolutionStatus)
	}
	if cache.setCalls != 1 {
		t.Fatalf("cache Set called %d times", cache.setCalls)
	}
}
