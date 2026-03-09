package handler

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
)

// ── JSON response writers ─────────────────────────────────────────────────────

// writeJSON sets Content-Type, writes the HTTP status, and encodes v as JSON.
// Headers must be set before WriteHeader — order is not optional.
func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

// writeError writes a standard {"error": "..."} JSON response.
func writeError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

// ── Pagination ────────────────────────────────────────────────────────────────

// parsePagination reads ?page= and ?limit= from the request query string.
// Defaults: page=1, limit=50. Limit is capped at 100.
func parsePagination(pageStr, limitStr string) (page, limit int) {
	page = 1
	limit = 50
	if n, e := strconv.Atoi(pageStr); e == nil && n > 0 {
		page = n
	}
	if n, e := strconv.Atoi(limitStr); e == nil && n > 0 && n <= 100 {
		limit = n
	}
	return
}

// ── Redis interface{} parsers ─────────────────────────────────────────────────
// Used when reading HMGet results, which return []interface{}.

// parseBool converts a Redis interface{} value to bool.
// Redis stores booleans as "1"/"0" or "true"/"false".
func parseBool(v interface{}) bool {
	if v == nil {
		return false
	}
	s := fmt.Sprintf("%v", v)
	return s == "1" || s == "true"
}

// parseFloat converts a Redis interface{} value to float64.
func parseFloat(v interface{}) float64 {
	if v == nil {
		return 0
	}
	var f float64
	fmt.Sscanf(fmt.Sprintf("%v", v), "%f", &f)
	return f
}

// ── Redis map[string]string parsers ──────────────────────────────────────────
// Used when reading HGetAll results, which return map[string]string.

// parseMapFloat reads a float64 from a Redis HGetAll result map.
func parseMapFloat(m map[string]string, key string) float64 {
	f, _ := strconv.ParseFloat(m[key], 64)
	return f
}

// parseMapBool reads a bool from a Redis HGetAll result map.
func parseMapBool(m map[string]string, key string) bool {
	v := m[key]
	return v == "1" || v == "true"
}
