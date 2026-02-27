package metrics

import (
	"fmt"
	"net/http"
	"sync/atomic"
)

var (
	MessagesReceived  atomic.Int64
	DBWriteSuccess    atomic.Int64
	DBWriteFailures   atomic.Int64
	DBChannelDrops    atomic.Int64
	StateChannelDrops atomic.Int64
	AlertChannelDrops atomic.Int64
)

func HandleMetrics(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; version=0.0.4")
	fmt.Fprintf(w, "ingestion_messages_received_total %d\n", MessagesReceived.Load())
	fmt.Fprintf(w, "ingestion_db_write_success_total %d\n", DBWriteSuccess.Load())
	fmt.Fprintf(w, "ingestion_db_write_failures_total %d\n", DBWriteFailures.Load())
	fmt.Fprintf(w, "ingestion_db_channel_drops_total %d\n", DBChannelDrops.Load())
	fmt.Fprintf(w, "ingestion_state_channel_drops_total %d\n", StateChannelDrops.Load())
	fmt.Fprintf(w, "ingestion_alert_channel_drops_total %d\n", AlertChannelDrops.Load())
}
