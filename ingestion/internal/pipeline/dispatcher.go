package pipeline

import (
	"fleet-monitor/ingestion/internal/domain"
	"fleet-monitor/ingestion/internal/metrics"
)

type Dispatcher struct {
	DBChan    chan *domain.TelemetryMessage
	StateChan chan *domain.TelemetryMessage
	AlertChan chan *domain.TelemetryMessage
}

func NewDispatcher(dbSize, stateSize, alertSize int) *Dispatcher {
	return &Dispatcher{
		DBChan:    make(chan *domain.TelemetryMessage, dbSize),
		StateChan: make(chan *domain.TelemetryMessage, stateSize),
		AlertChan: make(chan *domain.TelemetryMessage, alertSize),
	}
}

func (d *Dispatcher) Dispatch(msg *domain.TelemetryMessage) {
	select {
	case d.DBChan <- msg:
	default:
		metrics.DBChannelDrops.Add(1)
	}

	select {
	case d.StateChan <- msg:
	default:
		metrics.StateChannelDrops.Add(1)
	}

	select {
	case d.AlertChan <- msg:
	default:
		metrics.AlertChannelDrops.Add(1)
	}
}
