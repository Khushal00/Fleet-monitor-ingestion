package pipeline

import (
	"context"
	"testing"
	"time"

	"fleet-monitor/ingestion/internal/domain"
)

func TestDispatcherFansOutAndRejectsWhenIngressSaturated(t *testing.T) {
	d := NewDispatcher(1, 1, 1)
	first, second := &domain.TelemetryMessage{VehicleID: "one"}, &domain.TelemetryMessage{VehicleID: "two"}
	if !d.Dispatch(first) {
		t.Fatal("first message was not admitted")
	}
	if d.Dispatch(second) {
		t.Fatal("saturated ingress admitted a message")
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go d.Run(ctx)
	for name, ch := range map[string]<-chan *domain.TelemetryMessage{"db": d.DBChan, "state": d.StateChan, "alert": d.AlertChan} {
		select {
		case got := <-ch:
			if got != first {
				t.Fatalf("%s got wrong message", name)
			}
		case <-time.After(time.Second):
			t.Fatalf("%s did not receive fan-out", name)
		}
	}
}
