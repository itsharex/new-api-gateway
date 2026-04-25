package identity

import (
	"context"
	"errors"
	"reflect"
	"regexp"

	"github.com/your-company/new-api-gateway/internal/employee"
)

const (
	ResolutionStatusResolved          = "resolved"
	ResolutionStatusMissingEmployeeNo = "missing_employee_no"
	ResolutionStatusInvalidEmployeeNo = "invalid_employee_no"
	ResolutionStatusDBError           = "db_error"
	ResolutionStatusNotFound          = "not_found"

	IdentityCacheStatusHit                = "redis_or_local_hit"
	IdentityCacheStatusMissDBLookup       = "miss_db_lookup"
	IdentityCacheStatusCacheErrorDBLookup = "cache_error_db_lookup"
)

var (
	errResolverLookupRequired            = errors.New("identity resolver lookup is nil")
	errResolverEmployeeNoPatternRequired = errors.New("identity resolver employee number pattern is nil")
)

type Resolver struct {
	Cache             Cache
	Lookup            TokenLookup
	EmployeeNoPattern *regexp.Regexp
}

func (r Resolver) Resolve(ctx context.Context, canonicalKey, fingerprintValue, fingerprintDisplay string) (Snapshot, error) {
	if isNilTokenLookup(r.Lookup) {
		return Snapshot{}, errResolverLookupRequired
	}
	if r.EmployeeNoPattern == nil {
		return Snapshot{}, errResolverEmployeeNoPatternRequired
	}

	cacheStatus := IdentityCacheStatusMissDBLookup
	if r.Cache != nil {
		cached, ok, err := r.Cache.Get(ctx, fingerprintValue)
		if err != nil {
			cacheStatus = IdentityCacheStatusCacheErrorDBLookup
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

	employeeNo := employee.Normalize(token.TokenName)
	status := ResolutionStatusResolved
	if employeeNo == "" {
		status = ResolutionStatusMissingEmployeeNo
	} else if err := employee.Validate(employeeNo, r.EmployeeNoPattern); err != nil {
		status = ResolutionStatusInvalidEmployeeNo
	}

	snapshot := Snapshot{
		TokenFingerprint:    fingerprintValue,
		FingerprintDisplay:  fingerprintDisplay,
		NewAPITokenID:       token.TokenID,
		TokenNameRaw:        token.TokenName,
		EmployeeNo:          employeeNo,
		TokenStatus:         token.TokenStatus,
		TokenGroup:          token.TokenGroup,
		ExpiredTime:         token.ExpiredTime,
		AccessedTime:        token.AccessedTime,
		RemainQuota:         token.RemainQuota,
		UsedQuota:           token.UsedQuota,
		UnlimitedQuota:      token.UnlimitedQuota,
		ModelLimitsEnabled:  token.ModelLimitsEnabled,
		ModelLimits:         token.ModelLimits,
		ResolutionStatus:    status,
		IdentityCacheStatus: cacheStatus,
	}
	if r.Cache != nil && snapshot.ResolutionStatus == ResolutionStatusResolved {
		_ = r.Cache.Set(ctx, snapshot)
	}
	return snapshot, nil
}

func isNilTokenLookup(lookup TokenLookup) bool {
	if lookup == nil {
		return true
	}

	value := reflect.ValueOf(lookup)
	switch value.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Ptr, reflect.Slice:
		return value.IsNil()
	default:
		return false
	}
}
