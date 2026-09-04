package handler

import "testing"

func TestPaginationAndRedisParsers(t *testing.T) {
	for _, tt := range []struct {
		page, limit         string
		wantPage, wantLimit int
	}{{"", "", 1, 50}, {"2", "25", 2, 25}, {"-1", "101", 1, 50}, {"bad", "0", 1, 50}} {
		p, l := parsePagination(tt.page, tt.limit)
		if p != tt.wantPage || l != tt.wantLimit {
			t.Fatalf("parsePagination(%q,%q) = %d,%d", tt.page, tt.limit, p, l)
		}
	}
	if !parseBool("true") || !parseBool(1) || parseBool("false") {
		t.Fatal("parseBool did not handle Redis values")
	}
	if parseFloat("12.5") != 12.5 || parseFloat(nil) != 0 {
		t.Fatal("parseFloat did not handle values")
	}
	if parseMapFloat(map[string]string{"v": "4.5"}, "v") != 4.5 || !parseMapBool(map[string]string{"v": "1"}, "v") {
		t.Fatal("map parsers failed")
	}
}
