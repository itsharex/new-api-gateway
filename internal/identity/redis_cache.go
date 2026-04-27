package identity

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/redis/go-redis/v9"
)

type RedisCache struct {
	Client *redis.Client
	TTL    time.Duration
}

func (c RedisCache) Get(ctx context.Context, fingerprint string) (Snapshot, bool, error) {
	data, err := c.Client.Get(ctx, "identity:"+fingerprint).Bytes()
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
	data, err := json.Marshal(snapshot)
	if err != nil {
		return err
	}
	ttl := c.TTL
	if ttl <= 0 {
		ttl = 15 * time.Minute
	}
	return c.Client.Set(ctx, "identity:"+snapshot.TokenFingerprint, data, ttl).Err()
}
