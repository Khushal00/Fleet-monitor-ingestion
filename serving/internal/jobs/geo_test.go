package jobs

import (
	"math"
	"testing"
	"time"
)

func TestGeoDistanceFunctions(t *testing.T) {
	if haversineKm(12, 77, 12, 77) != 0 {
		t.Fatal("identical points have non-zero distance")
	}
	if d := haversineKm(0, 0, 0, 1); math.Abs(d-111.195) > .5 {
		t.Fatalf("one degree distance = %f", d)
	}
	if d := pointToSegmentDistanceKm(0, 1, 0, 0, 0, 2); d > .01 {
		t.Fatalf("point on segment distance = %f", d)
	}
	if d := pointToSegmentDistanceKm(1, 3, 0, 0, 0, 2); d < 100 {
		t.Fatalf("endpoint-clamped distance = %f", d)
	}
}

func TestJobIntervalsNeverCreateInvalidTickerDurations(t *testing.T) {
	if got := intervalFromSeconds(0); got != time.Second {
		t.Fatalf("zero interval = %s, want 1s", got)
	}
	if got := intervalFromSeconds(-5); got != time.Second {
		t.Fatalf("negative interval = %s, want 1s", got)
	}
	if got := intervalFromSeconds(7); got != 7*time.Second {
		t.Fatalf("positive interval = %s, want 7s", got)
	}
}
