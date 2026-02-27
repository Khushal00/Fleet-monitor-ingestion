package pipeline

import (
	"context"
	"fmt"
	"time"

	"fleet-monitor/ingestion/internal/domain"
	"fleet-monitor/ingestion/internal/metrics"
	"fleet-monitor/ingestion/internal/store"
)

type DBWriter struct {
	ch        <-chan *domain.TelemetryMessage
	db        *store.TimescaleStore
	batchSize int
	flushMS   int
}

func NewDBWriter(
	ch <-chan *domain.TelemetryMessage,
	db *store.TimescaleStore,
	batchSize int,
	flushMS int,
) *DBWriter {
	return &DBWriter{
		ch:        ch,
		db:        db,
		batchSize: batchSize,
		flushMS:   flushMS,
	}
}

func (w *DBWriter) Run(ctx context.Context) {
	batch := make([]*domain.TelemetryMessage, 0, w.batchSize)
	ticker := time.NewTicker(time.Duration(w.flushMS) * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case msg, ok := <-w.ch:
			if !ok {
				if len(batch) > 0 {
					w.flush(ctx, batch)
				}
				return
			}
			batch = append(batch, msg)
			if len(batch) >= w.batchSize {
				w.flush(ctx, batch)
				batch = batch[:0]
			}

		case <-ticker.C:
			if len(batch) > 0 {
				w.flush(ctx, batch)
				batch = batch[:0]
			}

		case <-ctx.Done():
			if len(batch) > 0 {
				w.flush(ctx, batch)
			}
			return
		}
	}
}

func (w *DBWriter) flush(ctx context.Context, batch []*domain.TelemetryMessage) {
	err := w.db.BatchInsert(ctx, batch)
	if err != nil {
		fmt.Printf("DB write failed (batch=%d), retrying: %v\n", len(batch), err)
		time.Sleep(500 * time.Millisecond)
		err = w.db.BatchInsert(ctx, batch)
		if err != nil {
			fmt.Printf("DB write permanently failed (batch=%d): %v\n", len(batch), err)
			metrics.DBWriteFailures.Add(int64(len(batch)))
			return
		}
	}
	metrics.DBWriteSuccess.Add(int64(len(batch)))
}
