package identity

import (
	"context"
	"regexp"

	"github.com/your-company/new-api-gateway/internal/employee"
)

type Resolver struct {
	Cache             Cache
	Lookup            TokenLookup
	EmployeeNoPattern *regexp.Regexp
}

func (r Resolver) Resolve(ctx context.Context, canonicalKey, fingerprintValue, fingerprintDisplay string) (Snapshot, error) {
	if r.Cache != nil {
		cached, ok, err := r.Cache.Get(ctx, fingerprintValue)
		if err != nil {
			return Snapshot{}, err
		}
		if ok {
			cached.IdentityCacheStatus = "redis_or_local_hit"
			return cached, nil
		}
	}

	token, err := r.Lookup.FindByCanonicalKey(ctx, canonicalKey)
	if err != nil {
		return Snapshot{
			TokenFingerprint:    fingerprintValue,
			FingerprintDisplay:  fingerprintDisplay,
			ResolutionStatus:    "db_error",
			IdentityCacheStatus: "miss",
		}, nil
	}

	employeeNo := employee.Normalize(token.TokenName)
	status := "resolved"
	if employeeNo == "" {
		status = "missing_employee_no"
	} else if err := employee.Validate(employeeNo, r.EmployeeNoPattern); err != nil {
		status = "invalid_employee_no"
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
		IdentityCacheStatus: "miss_db_lookup",
	}
	if r.Cache != nil {
		_ = r.Cache.Set(ctx, snapshot)
	}
	return snapshot, nil
}
