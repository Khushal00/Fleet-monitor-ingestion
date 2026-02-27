package pipeline

import (
	"context"
	"fmt"
	"time"

	"fleet-monitor/ingestion/internal/domain"
	"fleet-monitor/ingestion/internal/store"
)

type StateWriter struct {
	ch    <-chan *domain.TelemetryMessage
	redis *store.RedisStore
}

func NewStateWriter(
	ch <-chan *domain.TelemetryMessage,
	redis *store.RedisStore,
) *StateWriter {
	return &StateWriter{ch: ch, redis: redis}
}

func (w *StateWriter) Run(ctx context.Context) {
	batch := make([]*domain.TelemetryMessage, 0, 100) // Redis is fast, fixed batch fine
	ticker := time.NewTicker(50 * time.Millisecond)   // 50ms gives real-time feel on dashboard
	defer ticker.Stop()

	for {
		select {
		case msg, ok := <-w.ch:
			if !ok {
				w.flushBatch(ctx, batch)
				return
			}
			batch = append(batch, msg)
			if len(batch) >= 100 {
				w.flushBatch(ctx, batch)
				batch = batch[:0]
			}

		case <-ticker.C:
			if len(batch) > 0 {
				w.flushBatch(ctx, batch)
				batch = batch[:0]
			}

		case <-ctx.Done():
			w.flushBatch(ctx, batch)
			return
		}
	}
}

func (w *StateWriter) flushBatch(ctx context.Context, batch []*domain.TelemetryMessage) {
	for _, msg := range batch {
		if err := w.redis.PipelineStateUpdate(ctx, msg); err != nil {
			fmt.Printf("Redis state update failed for %s: %v\n", msg.VehicleID, err)
		}
	}
}
