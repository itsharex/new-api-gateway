package admin

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

type RuntimeProvider interface {
	Snapshot(ctx context.Context, stage string) (AnalysisRuntimeSnapshot, error)
	History(ctx context.Context, stage string, rangeName string) ([]AnalysisRuntimeHistoryPoint, error)
	Consumers(ctx context.Context, stage string) ([]AnalysisRuntimeConsumer, error)
}

type noopRuntimeProvider struct{}

func (noopRuntimeProvider) Snapshot(_ context.Context, stage string) (AnalysisRuntimeSnapshot, error) {
	return AnalysisRuntimeSnapshot{Stage: stage, Available: true}, nil
}

func (noopRuntimeProvider) History(_ context.Context, stage string, _ string) ([]AnalysisRuntimeHistoryPoint, error) {
	return []AnalysisRuntimeHistoryPoint{}, nil
}

func (noopRuntimeProvider) Consumers(_ context.Context, _ string) ([]AnalysisRuntimeConsumer, error) {
	return []AnalysisRuntimeConsumer{}, nil
}

type RedisRuntimeProvider struct {
	repo  Repository
	redis *redis.Client
	now   func() time.Time
}

func NewRedisRuntimeProvider(repo Repository, redisClient *redis.Client) RedisRuntimeProvider {
	return RedisRuntimeProvider{
		repo:  repo,
		redis: redisClient,
		now:   func() time.Time { return time.Now().UTC() },
	}
}

func (p RedisRuntimeProvider) Snapshot(ctx context.Context, stage string) (AnalysisRuntimeSnapshot, error) {
	stage, streamName, groupName, err := normalizeRuntimeStage(stage)
	if err != nil {
		return AnalysisRuntimeSnapshot{}, err
	}
	snapshot := AnalysisRuntimeSnapshot{Stage: stage, Available: true}
	var streamLag int64
	var streamPending int64
	if p.redis != nil {
		if groups, groupsErr := p.redis.XInfoGroups(ctx, streamName).Result(); groupsErr == nil {
			for _, group := range groups {
				if group.Name != groupName {
					continue
				}
				streamPending = maxInt64(streamPending, group.Pending)
				streamLag = maxInt64(streamLag, group.Lag)
				if snapshot.ActiveConsumers == 0 {
					snapshot.ActiveConsumers = group.Consumers
				}
				break
			}
		}
		if oldestPendingAgeSeconds, pendingErr := maxPendingIdleSeconds(func(start string, count int64) ([]redis.XPendingExt, error) {
			return p.redis.XPendingExt(ctx, &redis.XPendingExtArgs{
				Stream: streamName,
				Group:  groupName,
				Start:  start,
				End:    "+",
				Count:  count,
			}).Result()
		}, 100); pendingErr == nil {
			snapshot.OldestPendingAgeSeconds = oldestPendingAgeSeconds
		}
	}
	if p.repo.db == nil {
		return snapshot, nil
	}
	var queuedCount, leasedCount, retryableFailCount int64
	var latestThroughput, latestQueueP50, latestQueueP95, latestProcessingP50, latestProcessingP95 int64
	var latestActiveConsumers, latestOldestPendingAge int64
	var successRate, retryableFailRate, terminalFailRate, llmJudgeTimeoutRate float64
	if err := p.repo.db.QueryRow(ctx, `
WITH latest_sample AS (
  SELECT
    throughput_per_minute,
    queue_wait_p50_ms,
    queue_wait_p95_ms,
    processing_p50_ms,
    processing_p95_ms,
    active_consumers,
    oldest_pending_age_seconds,
    success_rate,
    retryable_fail_rate,
    terminal_fail_rate,
    llm_judge_timeout_rate
  FROM analysis_runtime_samples
  WHERE stage = $1
  ORDER BY sampled_at DESC
  LIMIT 1
)
SELECT
  COALESCE(COUNT(*) FILTER (WHERE status = 'queued'), 0),
  COALESCE(COUNT(*) FILTER (WHERE status = 'leased'), 0),
  COALESCE(COUNT(*) FILTER (WHERE status = 'failed_retryable'), 0),
  COALESCE((SELECT throughput_per_minute FROM latest_sample), 0),
  COALESCE((SELECT queue_wait_p50_ms FROM latest_sample), 0),
  COALESCE((SELECT queue_wait_p95_ms FROM latest_sample), 0),
  COALESCE((SELECT processing_p50_ms FROM latest_sample), 0),
  COALESCE((SELECT processing_p95_ms FROM latest_sample), 0),
  COALESCE((SELECT active_consumers FROM latest_sample), 0),
  COALESCE((SELECT oldest_pending_age_seconds FROM latest_sample), 0),
  COALESCE((SELECT success_rate FROM latest_sample), 0),
  COALESCE((SELECT retryable_fail_rate FROM latest_sample), 0),
  COALESCE((SELECT terminal_fail_rate FROM latest_sample), 0),
  COALESCE((SELECT llm_judge_timeout_rate FROM latest_sample), 0)
FROM analysis_tasks
WHERE stage = $1
`, stage).Scan(
		&queuedCount,
		&leasedCount,
		&retryableFailCount,
		&latestThroughput,
		&latestQueueP50,
		&latestQueueP95,
		&latestProcessingP50,
		&latestProcessingP95,
		&latestActiveConsumers,
		&latestOldestPendingAge,
		&successRate,
		&retryableFailRate,
		&terminalFailRate,
		&llmJudgeTimeoutRate,
	); err != nil {
		return AnalysisRuntimeSnapshot{}, err
	}
	snapshot.PendingCount = maxInt64(queuedCount+retryableFailCount, streamLag)
	snapshot.LeasedCount = maxInt64(leasedCount, streamPending)
	snapshot.QueueDepth = snapshot.PendingCount + snapshot.LeasedCount
	snapshot.ThroughputPerMinute = latestThroughput
	snapshot.SuccessRate = successRate
	snapshot.RetryableFailRate = retryableFailRate
	snapshot.TerminalFailRate = terminalFailRate
	snapshot.LLMJudgeTimeoutRate = llmJudgeTimeoutRate
	snapshot.QueueWaitP50MS = latestQueueP50
	snapshot.QueueWaitP95MS = latestQueueP95
	snapshot.ProcessingP50MS = latestProcessingP50
	snapshot.ProcessingP95MS = latestProcessingP95
	snapshot.ActiveConsumers = maxInt64(snapshot.ActiveConsumers, latestActiveConsumers)
	snapshot.OldestPendingAgeSeconds = maxInt64(snapshot.OldestPendingAgeSeconds, latestOldestPendingAge)
	return snapshot, nil
}

func (p RedisRuntimeProvider) History(ctx context.Context, stage string, rangeName string) ([]AnalysisRuntimeHistoryPoint, error) {
	stage, _, _, err := normalizeRuntimeStage(stage)
	if err != nil {
		return nil, err
	}
	historyRange, err := normalizeRuntimeHistoryRange(rangeName)
	if err != nil {
		return nil, err
	}
	items, err := p.repo.ListAnalysisRuntimeHistory(ctx, stage, p.now().Add(-historyRange.window))
	if err != nil {
		return nil, err
	}
	return items, nil
}

func (p RedisRuntimeProvider) Consumers(ctx context.Context, stage string) ([]AnalysisRuntimeConsumer, error) {
	stage, streamName, groupName, err := normalizeRuntimeStage(stage)
	if err != nil {
		return nil, err
	}
	if p.redis == nil {
		return []AnalysisRuntimeConsumer{}, nil
	}
	consumers, err := p.redis.XInfoConsumers(ctx, streamName, groupName).Result()
	if err != nil {
		return nil, err
	}
	taskState := map[string]AnalysisRuntimeConsumer{}
	if p.repo.db != nil {
		rows, queryErr := p.repo.db.Query(ctx, `
SELECT
  lease_owner,
  COALESCE(COUNT(*) FILTER (WHERE status = 'leased'), 0),
  COALESCE(MAX(last_error_code) FILTER (WHERE last_error_code <> ''), '')
FROM analysis_tasks
WHERE stage = $1 AND lease_owner <> ''
GROUP BY lease_owner
`, stage)
		if queryErr == nil {
			defer rows.Close()
			for rows.Next() {
				var workerID, lastError string
				var leasedCount int64
				if scanErr := rows.Scan(&workerID, &leasedCount, &lastError); scanErr == nil {
					taskState[workerID] = AnalysisRuntimeConsumer{
						WorkerID:      workerID,
						Stage:         stage,
						LeasedCount:   leasedCount,
						LastErrorCode: lastError,
					}
				}
			}
		}
	}
	items := make([]AnalysisRuntimeConsumer, 0, len(consumers))
	for _, consumer := range consumers {
		item := taskState[consumer.Name]
		item.WorkerID = consumer.Name
		item.Stage = stage
		if item.LeasedCount == 0 {
			item.LeasedCount = consumer.Pending
		}
		idleSeconds := int64(consumer.Idle / time.Second)
		item.IdleSeconds = idleSeconds
		item.LastSeenAt = p.now().Add(-consumer.Idle).Format(time.RFC3339)
		items = append(items, item)
	}
	return items, nil
}

func normalizeRuntimeStage(stage string) (normalized string, streamName string, groupName string, err error) {
	switch strings.ToLower(strings.TrimSpace(stage)) {
	case "", "core":
		return "core", "analysis.core", "analysis-core-workers", nil
	case "enrichment":
		return "enrichment", "analysis.enrichment", "analysis-enrichment-workers", nil
	default:
		return "", "", "", fmt.Errorf("invalid analysis stage %q", stage)
	}
}

type runtimeHistoryRange struct {
	name   string
	window time.Duration
}

func normalizeRuntimeHistoryRange(rangeName string) (runtimeHistoryRange, error) {
	switch strings.TrimSpace(rangeName) {
	case "", "1h":
		return runtimeHistoryRange{name: "1h", window: time.Hour}, nil
	case "15m":
		return runtimeHistoryRange{name: "15m", window: 15 * time.Minute}, nil
	case "24h":
		return runtimeHistoryRange{name: "24h", window: 24 * time.Hour}, nil
	default:
		return runtimeHistoryRange{}, fmt.Errorf("invalid analysis runtime range %q", rangeName)
	}
}

func markRuntimeSnapshotAvailable(snapshot AnalysisRuntimeSnapshot) AnalysisRuntimeSnapshot {
	snapshot.Available = true
	snapshot.Error = ""
	return snapshot
}

func unavailableRuntimeSnapshot(stage string, err error) AnalysisRuntimeSnapshot {
	snapshot := AnalysisRuntimeSnapshot{
		Stage:     stage,
		Available: false,
	}
	if err != nil {
		snapshot.Error = err.Error()
	}
	return snapshot
}

func maxPendingIdleSeconds(fetchPage func(start string, count int64) ([]redis.XPendingExt, error), pageSize int64) (int64, error) {
	if pageSize <= 0 {
		pageSize = 100
	}
	start := "-"
	var oldest int64
	for {
		items, err := fetchPage(start, pageSize)
		if err != nil {
			return 0, err
		}
		if len(items) == 0 {
			return oldest, nil
		}
		for _, item := range items {
			idleSeconds := int64(item.Idle / time.Second)
			if idleSeconds > oldest {
				oldest = idleSeconds
			}
		}
		if int64(len(items)) < pageSize {
			return oldest, nil
		}
		lastID := items[len(items)-1].ID
		if strings.TrimSpace(lastID) == "" {
			return oldest, nil
		}
		start = "(" + lastID
	}
}

func maxInt64(left int64, right int64) int64 {
	if right > left {
		return right
	}
	return left
}
