package ws

import (
	"encoding/json"
	"testing"
	"time"
)

func TestEventEnvelopesExposeStableTypesAndPayloads(t *testing.T) {
	events := []envelope{newPositionEvent(VehiclePositionPayload{VehicleID: "v"}), newAlertEvent(VehicleAlertPayload{AlertID: 1}), newOfflineEvent(VehicleOfflinePayload{VehicleID: "v", LastSeenAt: time.Unix(0, 0)}), newOnlineEvent(VehicleOnlinePayload{VehicleID: "v"}), newDeviationEvent(VehicleDeviationPayload{VehicleID: "v"}), newStopArrivedEvent(StopArrivedPayload{VehicleID: "v"}), newAlertResolvedEvent(AlertResolvedPayload{AlertID: 1}), newPingEvent()}
	want := []EventType{EventVehiclePosition, EventVehicleAlert, EventVehicleOffline, EventVehicleOnline, EventVehicleDeviation, EventStopArrived, EventAlertResolved, EventPing}
	for i, event := range events {
		if event.Type != want[i] {
			t.Fatalf("event %d type = %q", i, event.Type)
		}
		if _, err := json.Marshal(event); err != nil {
			t.Fatalf("marshal event %d: %v", i, err)
		}
	}
}
