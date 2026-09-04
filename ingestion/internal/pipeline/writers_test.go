package pipeline

import (
	"context"
	"errors"
	"sync"
	"testing"

	"fleet-monitor/ingestion/internal/domain"
)

type recordingTelemetryStore struct {
	mu      sync.Mutex
	batches [][]*domain.TelemetryMessage
}

func (s *recordingTelemetryStore) BatchInsert(_ context.Context, batch []*domain.TelemetryMessage) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.batches = append(s.batches, append([]*domain.TelemetryMessage(nil), batch...))
	return nil
}

type recordingStateStore struct {
	batches [][]*domain.TelemetryMessage
	err     error
}

func (s *recordingStateStore) PipelineStateUpdates(_ context.Context, batch []*domain.TelemetryMessage) error {
	s.batches = append(s.batches, append([]*domain.TelemetryMessage(nil), batch...))
	return s.err
}

func TestDBWriterFlushesFullBatchAndRemainingMessagesOnClose(t *testing.T) {
	ch := make(chan *domain.TelemetryMessage, 3)
	ch <- &domain.TelemetryMessage{VehicleID: "1"}
	ch <- &domain.TelemetryMessage{VehicleID: "2"}
	ch <- &domain.TelemetryMessage{VehicleID: "3"}
	close(ch)
	store := &recordingTelemetryStore{}
	NewDBWriter(ch, store, 2, 10000).Run(context.Background())
	if len(store.batches) != 2 || len(store.batches[0]) != 2 || len(store.batches[1]) != 1 {
		t.Fatalf("batches = %#v", store.batches)
	}
}

func TestDBWriterNormalizesInvalidBatchConfiguration(t *testing.T) {
	w := NewDBWriter(make(chan *domain.TelemetryMessage), &recordingTelemetryStore{}, 0, 0)
	if w.batchSize != 1 || w.flushMS != 1 {
		t.Fatalf("normalized configuration = batch %d, flush %d", w.batchSize, w.flushMS)
	}
}

func TestStateWriterFlushesAndSkipsEmptyBatch(t *testing.T) {
	store := &recordingStateStore{err: errors.New("transient")}
	w := &StateWriter{redis: store}
	w.flushBatch(context.Background(), nil)
	w.flushBatch(context.Background(), []*domain.TelemetryMessage{{VehicleID: "v"}})
	if len(store.batches) != 1 || len(store.batches[0]) != 1 {
		t.Fatalf("batches = %#v", store.batches)
	}
}
