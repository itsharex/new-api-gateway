package identity

import (
	"context"
	"regexp"
	"testing"
)

type fakeCache struct {
	value Snapshot
	ok    bool
}

func (f *fakeCache) Get(ctx context.Context, fingerprint string) (Snapshot, bool, error) {
	return f.value, f.ok, nil
}

func (f *fakeCache) Set(ctx context.Context, snapshot Snapshot) error {
	f.value = snapshot
	f.ok = true
	return nil
}

type fakeLookup struct {
	token NewAPIToken
}

func (f fakeLookup) FindByCanonicalKey(ctx context.Context, canonicalKey string) (NewAPIToken, error) {
	return f.token, nil
}

func TestResolverUsesCacheHit(t *testing.T) {
	cache := &fakeCache{ok: true, value: Snapshot{TokenFingerprint: "fp", EmployeeNo: "E12345", ResolutionStatus: "resolved"}}
	resolver := Resolver{Cache: cache, Lookup: fakeLookup{}, EmployeeNoPattern: regexp.MustCompile(`^[A-Z][0-9]{5}$`)}

	got, err := resolver.Resolve(context.Background(), "canonical", "fp", "tkfp_abc")
	if err != nil {
		t.Fatalf("Resolve error: %v", err)
	}
	if got.EmployeeNo != "E12345" || got.IdentityCacheStatus != "redis_or_local_hit" {
		t.Fatalf("unexpected snapshot %#v", got)
	}
}

func TestResolverLookupConvertsTokenNameToEmployeeNo(t *testing.T) {
	cache := &fakeCache{}
	resolver := Resolver{
		Cache:             cache,
		Lookup:            fakeLookup{token: NewAPIToken{TokenID: 12, TokenName: " e12345 ", TokenStatus: 1}},
		EmployeeNoPattern: regexp.MustCompile(`^[A-Z][0-9]{5}$`),
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
}

func TestResolverMarksInvalidEmployeeNo(t *testing.T) {
	cache := &fakeCache{}
	resolver := Resolver{
		Cache:             cache,
		Lookup:            fakeLookup{token: NewAPIToken{TokenID: 12, TokenName: "alice", TokenStatus: 1}},
		EmployeeNoPattern: regexp.MustCompile(`^[A-Z][0-9]{5}$`),
	}

	got, err := resolver.Resolve(context.Background(), "canonical", "fp", "tkfp_abc")
	if err != nil {
		t.Fatalf("Resolve error: %v", err)
	}
	if got.ResolutionStatus != "invalid_employee_no" {
		t.Fatalf("ResolutionStatus = %q", got.ResolutionStatus)
	}
}
