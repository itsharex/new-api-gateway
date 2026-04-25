package identity

import (
	"context"
	"errors"
	"regexp"
	"testing"
)

var employeeNoPattern = regexp.MustCompile(`^[A-Z][0-9]{5}$`)

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
	cache := &fakeCache{ok: true, value: Snapshot{TokenFingerprint: "fp", EmployeeNo: "E12345", ResolutionStatus: "resolved"}}
	lookup := &fakeLookup{}
	resolver := Resolver{Cache: cache, Lookup: lookup, EmployeeNoPattern: employeeNoPattern}

	got, err := resolver.Resolve(context.Background(), "canonical", "fp", "tkfp_abc")
	if err != nil {
		t.Fatalf("Resolve error: %v", err)
	}
	if got.EmployeeNo != "E12345" || got.IdentityCacheStatus != "redis_or_local_hit" {
		t.Fatalf("unexpected snapshot %#v", got)
	}
	if lookup.calls != 0 {
		t.Fatalf("lookup called %d times on cache hit", lookup.calls)
	}
}

func TestResolverReturnsErrorForNilLookup(t *testing.T) {
	resolver := Resolver{EmployeeNoPattern: employeeNoPattern}

	if _, err := resolver.Resolve(context.Background(), "canonical", "fp", "tkfp_abc"); err == nil {
		t.Fatal("expected error for nil lookup")
	}
}

func TestResolverReturnsErrorForTypedNilLookup(t *testing.T) {
	var lookup *fakeLookup
	resolver := Resolver{Lookup: lookup, EmployeeNoPattern: employeeNoPattern}

	if _, err := resolver.Resolve(context.Background(), "canonical", "fp", "tkfp_abc"); err == nil {
		t.Fatal("expected error for typed nil lookup")
	}
}

func TestResolverReturnsErrorForNilEmployeeNoPattern(t *testing.T) {
	resolver := Resolver{Lookup: &fakeLookup{token: NewAPIToken{TokenID: 12, TokenName: "E12345"}}}

	if _, err := resolver.Resolve(context.Background(), "canonical", "fp", "tkfp_abc"); err == nil {
		t.Fatal("expected error for nil employee number pattern")
	}
}

func TestResolverLookupConvertsTokenNameToEmployeeNo(t *testing.T) {
	cache := &fakeCache{}
	resolver := Resolver{
		Cache:             cache,
		Lookup:            &fakeLookup{token: NewAPIToken{TokenID: 12, TokenName: " e12345 ", TokenStatus: 1}},
		EmployeeNoPattern: employeeNoPattern,
	}

	got, err := resolver.Resolve(context.Background(), "canonical", "fp", "tkfp_abc")
	if err != nil {
		t.Fatalf("Resolve error: %v", err)
	}
	if got.EmployeeNo != "E12345" {
		t.Fatalf("EmployeeNo = %q", got.EmployeeNo)
	}
	if got.ResolutionStatus != "resolved" {
		t.Fatalf("ResolutionStatus = %q", got.ResolutionStatus)
	}
	if cache.setCalls != 1 {
		t.Fatalf("cache Set called %d times", cache.setCalls)
	}
}

func TestResolverCopiesTokenMetadata(t *testing.T) {
	token := NewAPIToken{
		TokenID:            12,
		TokenName:          " e12345 ",
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
	resolver := Resolver{Lookup: &fakeLookup{token: token}, EmployeeNoPattern: employeeNoPattern}

	got, err := resolver.Resolve(context.Background(), "canonical", "fp", "tkfp_abc")
	if err != nil {
		t.Fatalf("Resolve error: %v", err)
	}
	if got.TokenFingerprint != "fp" || got.FingerprintDisplay != "tkfp_abc" {
		t.Fatalf("fingerprint fields not copied: %#v", got)
	}
	if got.NewAPITokenID != token.TokenID || got.TokenNameRaw != token.TokenName || got.TokenStatus != token.TokenStatus || got.TokenGroup != token.TokenGroup {
		t.Fatalf("basic token metadata not copied: %#v", got)
	}
	if got.ExpiredTime != token.ExpiredTime || got.AccessedTime != token.AccessedTime || got.RemainQuota != token.RemainQuota || got.UsedQuota != token.UsedQuota {
		t.Fatalf("quota/time metadata not copied: %#v", got)
	}
	if got.UnlimitedQuota != token.UnlimitedQuota || got.ModelLimitsEnabled != token.ModelLimitsEnabled || got.ModelLimits != token.ModelLimits {
		t.Fatalf("limit metadata not copied: %#v", got)
	}
}

func TestResolverMarksInvalidEmployeeNo(t *testing.T) {
	cache := &fakeCache{}
	resolver := Resolver{
		Cache:             cache,
		Lookup:            &fakeLookup{token: NewAPIToken{TokenID: 12, TokenName: "alice", TokenStatus: 1}},
		EmployeeNoPattern: employeeNoPattern,
	}

	got, err := resolver.Resolve(context.Background(), "canonical", "fp", "tkfp_abc")
	if err != nil {
		t.Fatalf("Resolve error: %v", err)
	}
	if got.ResolutionStatus != "invalid_employee_no" {
		t.Fatalf("ResolutionStatus = %q", got.ResolutionStatus)
	}
	if cache.setCalls != 0 {
		t.Fatalf("cache Set called %d times for invalid employee no", cache.setCalls)
	}
}

func TestResolverMarksMissingEmployeeNo(t *testing.T) {
	cache := &fakeCache{}
	resolver := Resolver{
		Cache:             cache,
		Lookup:            &fakeLookup{token: NewAPIToken{TokenID: 12, TokenName: "   ", TokenStatus: 1}},
		EmployeeNoPattern: employeeNoPattern,
	}

	got, err := resolver.Resolve(context.Background(), "canonical", "fp", "tkfp_abc")
	if err != nil {
		t.Fatalf("Resolve error: %v", err)
	}
	if got.ResolutionStatus != "missing_employee_no" {
		t.Fatalf("ResolutionStatus = %q", got.ResolutionStatus)
	}
	if cache.setCalls != 0 {
		t.Fatalf("cache Set called %d times for missing employee no", cache.setCalls)
	}
}

func TestResolverMarksTokenNotFound(t *testing.T) {
	cache := &fakeCache{}
	resolver := Resolver{
		Cache:             cache,
		Lookup:            &fakeLookup{err: ErrTokenNotFound},
		EmployeeNoPattern: employeeNoPattern,
	}

	got, err := resolver.Resolve(context.Background(), "canonical", "fp", "tkfp_abc")
	if err != nil {
		t.Fatalf("Resolve error: %v", err)
	}
	if got.ResolutionStatus != "not_found" {
		t.Fatalf("ResolutionStatus = %q", got.ResolutionStatus)
	}
	if got.TokenFingerprint != "fp" || got.FingerprintDisplay != "tkfp_abc" {
		t.Fatalf("fingerprint fields not copied: %#v", got)
	}
	if cache.setCalls != 0 {
		t.Fatalf("cache Set called %d times for token not found", cache.setCalls)
	}
}

func TestResolverMarksLookupErrorsAsDBError(t *testing.T) {
	cache := &fakeCache{}
	resolver := Resolver{
		Cache:             cache,
		Lookup:            &fakeLookup{err: errors.New("database unavailable")},
		EmployeeNoPattern: employeeNoPattern,
	}

	got, err := resolver.Resolve(context.Background(), "canonical", "fp", "tkfp_abc")
	if err != nil {
		t.Fatalf("Resolve error: %v", err)
	}
	if got.ResolutionStatus != "db_error" {
		t.Fatalf("ResolutionStatus = %q", got.ResolutionStatus)
	}
	if got.IdentityCacheStatus != "miss_db_lookup" {
		t.Fatalf("IdentityCacheStatus = %q", got.IdentityCacheStatus)
	}
	if cache.setCalls != 0 {
		t.Fatalf("cache Set called %d times for lookup error", cache.setCalls)
	}
}

func TestResolverContinuesToLookupAfterCacheGetError(t *testing.T) {
	cache := &fakeCache{getErr: errors.New("redis unavailable")}
	lookup := &fakeLookup{token: NewAPIToken{TokenID: 12, TokenName: "E12345", TokenStatus: 1}}
	resolver := Resolver{Cache: cache, Lookup: lookup, EmployeeNoPattern: employeeNoPattern}

	got, err := resolver.Resolve(context.Background(), "canonical", "fp", "tkfp_abc")
	if err != nil {
		t.Fatalf("Resolve error: %v", err)
	}
	if lookup.calls != 1 {
		t.Fatalf("lookup called %d times", lookup.calls)
	}
	if got.ResolutionStatus != "resolved" || got.IdentityCacheStatus != "cache_error_db_lookup" {
		t.Fatalf("unexpected snapshot %#v", got)
	}
	if cache.setCalls != 1 {
		t.Fatalf("cache Set called %d times", cache.setCalls)
	}
}

func TestResolverIgnoresCacheSetError(t *testing.T) {
	cache := &fakeCache{setErr: errors.New("redis unavailable")}
	resolver := Resolver{
		Cache:             cache,
		Lookup:            &fakeLookup{token: NewAPIToken{TokenID: 12, TokenName: "E12345", TokenStatus: 1}},
		EmployeeNoPattern: employeeNoPattern,
	}

	got, err := resolver.Resolve(context.Background(), "canonical", "fp", "tkfp_abc")
	if err != nil {
		t.Fatalf("Resolve error: %v", err)
	}
	if got.ResolutionStatus != "resolved" {
		t.Fatalf("ResolutionStatus = %q", got.ResolutionStatus)
	}
	if cache.setCalls != 1 {
		t.Fatalf("cache Set called %d times", cache.setCalls)
	}
}
