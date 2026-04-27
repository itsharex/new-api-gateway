package identity

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

var (
	ErrRedisCacheClientRequired      = errors.New("identity redis cache client is nil")
	ErrRedisCacheFingerprintRequired = errors.New("identity redis cache fingerprint is empty")
)

type RedisCache struct {
	Client *redis.Client
	TTL    time.Duration
}

func (c RedisCache) Get(ctx context.Context, fingerprint string) (Snapshot, bool, error) {
	key, err := redisIdentityKey(fingerprint)
	if err != nil {
		return Snapshot{}, false, err
	}
	if c.Client == nil {
		return Snapshot{}, false, ErrRedisCacheClientRequired
	}

	data, err := c.Client.Get(ctx, key).Bytes()
	if errors.Is(err, redis.Nil) {
		return Snapshot{}, false, nil
	}
	if err != nil {
		return Snapshot{}, false, err
	}
	var snapshot Snapshot
	if err := json.Unmarshal(data, &snapshot); err != nil {
		return Snapshot{}, false, err
	}
	return snapshot, true, nil
}

func (c RedisCache) Set(ctx context.Context, snapshot Snapshot) error {
	key, err := redisIdentityKey(snapshot.TokenFingerprint)
	if err != nil {
		return err
	}
	if c.Client == nil {
		return ErrRedisCacheClientRequired
	}

	data, err := json.Marshal(snapshot)
	if err != nil {
		return err
	}
	return c.Client.Set(ctx, key, data, redisCacheTTL(c.TTL)).Err()
}

func redisIdentityKey(fingerprint string) (string, error) {
	if strings.TrimSpace(fingerprint) == "" {
		return "", ErrRedisCacheFingerprintRequired
	}
	return "identity:" + fingerprint, nil
}

func redisCacheTTL(ttl time.Duration) time.Duration {
	if ttl <= 0 {
		return 15 * time.Minute
	}
	return ttl
}
