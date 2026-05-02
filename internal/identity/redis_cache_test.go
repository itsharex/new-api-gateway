package identity

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"time"
)

func TestSnapshotJSONRoundTrip(t *testing.T) {
	original := Snapshot{
		TokenFingerprint:    "fp",
		FingerprintDisplay:  "tkfp_abc",
		NewAPITokenID:       123,
		TokenNameRaw:        " E12345 ",
		Username:            "E12345",
		TokenStatus:         1,
		TokenGroup:          "staff",
		ExpiredTime:         1711111111,
		AccessedTime:        1712222222,
		RemainQuota:         300,
		UsedQuota:           25,
		UnlimitedQuota:      true,
		ModelLimitsEnabled:  true,
		ModelLimits:         `{"gpt-4":10}`,
		ResolutionStatus:    "resolved",
		IdentityCacheStatus: "miss_db_lookup",
	}
	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("Marshal error: %v", err)
	}
	var decoded Snapshot
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Unmarshal error: %v", err)
	}
	if !reflect.DeepEqual(decoded, original) {
		t.Fatalf("decoded snapshot = %#v, want %#v", decoded, original)
	}
}

func TestRedisCacheGetRequiresClient(t *testing.T) {
	_, _, err := RedisCache{}.Get(context.Background(), "fp")
	if !errors.Is(err, ErrRedisCacheClientRequired) {
		t.Fatalf("Get error = %v, want %v", err, ErrRedisCacheClientRequired)
	}
}

func TestRedisCacheSetRequiresClient(t *testing.T) {
	err := RedisCache{}.Set(context.Background(), Snapshot{TokenFingerprint: "fp"})
	if !errors.Is(err, ErrRedisCacheClientRequired) {
		t.Fatalf("Set error = %v, want %v", err, ErrRedisCacheClientRequired)
	}
}

func TestRedisCacheGetRejectsBlankFingerprint(t *testing.T) {
	_, _, err := RedisCache{}.Get(context.Background(), " ")
	if !errors.Is(err, ErrRedisCacheFingerprintRequired) {
		t.Fatalf("Get error = %v, want %v", err, ErrRedisCacheFingerprintRequired)
	}
}

func TestRedisCacheSetRejectsBlankFingerprint(t *testing.T) {
	err := RedisCache{}.Set(context.Background(), Snapshot{TokenFingerprint: "\t"})
	if !errors.Is(err, ErrRedisCacheFingerprintRequired) {
		t.Fatalf("Set error = %v, want %v", err, ErrRedisCacheFingerprintRequired)
	}
}

func TestRedisIdentityKeyRejectsBlankFingerprint(t *testing.T) {
	for _, fingerprint := range []string{"", " ", "\t\n"} {
		if _, err := redisIdentityKey(fingerprint); !errors.Is(err, ErrRedisCacheFingerprintRequired) {
			t.Fatalf("redisIdentityKey(%q) error = %v, want %v", fingerprint, err, ErrRedisCacheFingerprintRequired)
		}
	}
}

func TestRedisIdentityKeyPrefixesFingerprint(t *testing.T) {
	key, err := redisIdentityKey("fp")
	if err != nil {
		t.Fatalf("redisIdentityKey error: %v", err)
	}
	if key != "identity:fp" {
		t.Fatalf("key = %q", key)
	}
}

func TestRedisCacheDefaultTTL(t *testing.T) {
	if ttl := redisCacheTTL(0); ttl != 15*time.Minute {
		t.Fatalf("default ttl = %v", ttl)
	}
	if ttl := redisCacheTTL(-time.Second); ttl != 15*time.Minute {
		t.Fatalf("negative ttl = %v", ttl)
	}
	if ttl := redisCacheTTL(time.Hour); ttl != time.Hour {
		t.Fatalf("configured ttl = %v", ttl)
	}
}
