package pipeline

import (
	"context"

	"fleet-monitor/ingestion/internal/domain"
	"fleet-monitor/ingestion/internal/metrics"
)

type Dispatcher struct {
	// ingress is the sole admission boundary. A request is accepted only after
	// it enters this bounded queue; Run fans it out without dropping it.
	ingress   chan *domain.TelemetryMessage
	DBChan    chan *domain.TelemetryMessage
	StateChan chan *domain.TelemetryMessage
	AlertChan chan *domain.TelemetryMessage
}

func NewDispatcher(dbSize, stateSize, alertSize int) *Dispatcher {
	return &Dispatcher{
		ingress:   make(chan *domain.TelemetryMessage, stateSize),
		DBChan:    make(chan *domain.TelemetryMessage, dbSize),
		StateChan: make(chan *domain.TelemetryMessage, stateSize),
		AlertChan: make(chan *domain.TelemetryMessage, alertSize),
	}
}

// Dispatch admits a message without waiting for a downstream worker. When
// ingress is full it rejects the request, allowing the HTTP layer to return
// a retryable 503 instead of acknowledging a message that would be dropped.
func (d *Dispatcher) Dispatch(msg *domain.TelemetryMessage) bool {
	select {
	case d.ingress <- msg:
		return true
	default:
		metrics.DBDispatchRejected.Add(1)
		return false
	}
}

// Run is the only fan-out path. It blocks on worker queues rather than
// dropping messages, which propagates saturation to the ingress boundary.
func (d *Dispatcher) Run(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case msg := <-d.ingress:
			if !send(ctx, d.DBChan, msg) ||
				!send(ctx, d.StateChan, msg) ||
				!send(ctx, d.AlertChan, msg) {
				return
			}
		}
	}
}

func send(ctx context.Context, ch chan<- *domain.TelemetryMessage, msg *domain.TelemetryMessage) bool {
	select {
	case ch <- msg:
		return true
	case <-ctx.Done():
		return false
	}
}
