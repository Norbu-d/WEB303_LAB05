package main

import (
	"context"
	"testing"
)

func TestHandler(t *testing.T) {
	cases := []struct {
		name     string
		req      Request
		wantStat string
		wantCode string
		wantTot  float64
	}{
		{"gold + bulk", Request{CustomerTier: "gold", Items: []Item{{"p1", 1200, 6}, {"p2", 350.5, 4}}}, "ok", "", 7311.7},
		{"standard no discount", Request{CustomerTier: "standard", Items: []Item{{"p1", 100, 2}}}, "ok", "", 200},
		{"silver", Request{CustomerTier: "silver", Items: []Item{{"p1", 100, 1}}}, "ok", "", 95},
		{"unknown tier", Request{CustomerTier: "platinum", Items: []Item{{"p1", 100, 1}}}, "error", "INVALID_INPUT", 0},
		{"empty items", Request{CustomerTier: "gold"}, "error", "INVALID_INPUT", 0},
		{"zero quantity", Request{CustomerTier: "gold", Items: []Item{{"p1", 100, 0}}}, "error", "INVALID_INPUT", 0},
		{"negative price", Request{CustomerTier: "gold", Items: []Item{{"p1", -1, 1}}}, "error", "INVALID_INPUT", 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := handler(context.Background(), c.req)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got.Status != c.wantStat || got.Code != c.wantCode || got.Total != c.wantTot {
				t.Fatalf("got %+v", got)
			}
		})
	}
}

func TestCrashHook(t *testing.T) {
	if _, err := handler(context.Background(), Request{Simulate: "crash"}); err == nil {
		t.Fatal("expected error")
	}
}
