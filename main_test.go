package main

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestWatchPollsRetriesAfterFailure(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	ticks := make(chan time.Time, 1)
	attempts := 0
	delivered := false
	err := watchPolls(ctx, ticks, func() error {
		attempts++
		if attempts == 1 {
			// Permit exactly one scheduled retry after the rejected delivery.
			ticks <- time.Time{}
			return errors.New("QQ delivery rejected")
		}
		delivered = true
		cancel()
		return nil
	})
	if err != nil {
		t.Errorf("watchPolls() = %v, want nil", err)
	}
	if attempts != 2 || !delivered {
		t.Errorf("watch recovery: attempts = %d, delivered = %t; want 2, true", attempts, delivered)
	}
}

func TestWatchPollsStopsOnCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	ticks := make(chan time.Time)
	attempts := 0
	err := watchPolls(ctx, ticks, func() error {
		attempts++
		cancel()
		return context.Canceled
	})
	if err != nil {
		t.Errorf("watchPolls() = %v, want nil", err)
	}
	if attempts != 1 {
		t.Errorf("watchPolls() attempts = %d, want 1", attempts)
	}
}
