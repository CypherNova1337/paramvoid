package ratelimit

import (
	"context"
	"testing"
	"time"
)

func TestPenalizeHalvesDownToFloor(t *testing.T) {
	l := New(20, 2, true)
	l.Penalize(0)
	if got := l.Rate(); got != 10 {
		t.Fatalf("after one penalize, rate = %v, want 10", got)
	}
	for i := 0; i < 10; i++ {
		l.Penalize(0)
	}
	if got := l.Rate(); got < 2 {
		t.Fatalf("rate dropped below floor: %v", got)
	}
}

func TestNoAdaptKeepsRateFixed(t *testing.T) {
	l := New(15, 1, false)
	l.Penalize(0)
	l.PenalizeNetwork()
	if got := l.Rate(); got != 15 {
		t.Fatalf("non-adaptive rate changed: %v, want 15", got)
	}
}

func TestRewardRampsBackTowardMax(t *testing.T) {
	l := New(20, 1, true)
	l.Penalize(0) // -> 10
	for i := 0; i < 40; i++ {
		l.Reward()
	}
	if got := l.Rate(); got <= 10 || got > 20 {
		t.Fatalf("reward should ramp within (10,20], got %v", got)
	}
}

func TestFloorNeverAboveMax(t *testing.T) {
	l := New(5, 100, true) // min > max
	if l.Rate() != 5 {
		t.Fatalf("rate = %v, want clamped to 5", l.Rate())
	}
}

func TestWaitHonorsContextCancellation(t *testing.T) {
	l := New(1, 1, true)
	// Drain the initial token, then a cancelled context must return promptly.
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_ = l.Wait(context.Background()) // consume one token
	start := time.Now()
	err := l.Wait(ctx)
	if err == nil {
		t.Fatalf("expected context error while rate-limited")
	}
	if time.Since(start) > time.Second {
		t.Fatalf("Wait did not honor context deadline promptly")
	}
}
