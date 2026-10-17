package main

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestFlowBackoffSchedule(t *testing.T) {
	cases := []struct {
		name     string
		min, max time.Duration
		want     []time.Duration
	}{
		{
			name: "defaults double from 30s and cap at 10m",
			want: []time.Duration{
				30 * time.Second, time.Minute, 2 * time.Minute, 4 * time.Minute,
				8 * time.Minute, 10 * time.Minute, 10 * time.Minute, 10 * time.Minute,
			},
		},
		{
			name: "explicit bounds",
			min:  time.Second, max: 5 * time.Second,
			want: []time.Duration{time.Second, 2 * time.Second, 4 * time.Second, 5 * time.Second, 5 * time.Second},
		},
		{
			name: "min above max yields max flat",
			min:  time.Minute, max: time.Second,
			want: []time.Duration{time.Second, time.Second},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			b := flowBackoff{min: tc.min, max: tc.max}
			for i, want := range tc.want {
				if got := b.next(); got != want {
					t.Fatalf("next #%d = %s, want %s", i+1, got, want)
				}
			}
		})
	}
}

func TestSleepCtx(t *testing.T) {
	if err := sleepCtx(context.Background(), time.Millisecond); err != nil {
		t.Fatalf("elapsed: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	start := time.Now()
	err := sleepCtx(ctx, time.Hour)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled: err = %v, want context.Canceled", err)
	}
	if time.Since(start) > time.Second {
		t.Fatalf("cancelled sleep took %s; did not honour ctx", time.Since(start))
	}
}
