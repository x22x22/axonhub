package biz

import (
	"context"
	"testing"
	"time"

	"github.com/looplj/axonhub/internal/pkg/xcache"
)

// newPreviousChannelTestService builds a RequestService that only carries the
// previous-channel cache, so the cache behaviour can be tested without a database.
func newPreviousChannelTestService(t *testing.T, ttl time.Duration) *RequestService {
	t.Helper()

	return &RequestService{
		channelCache: xcache.NewFromConfig[int](xcache.Config{
			Mode:   xcache.ModeMemory,
			Memory: xcache.MemoryConfig{Expiration: time.Minute},
		}),
		previousChannelTTL: ttl,
	}
}

func TestPreviousChannelCacheDefaultTTL(t *testing.T) {
	// A conversation keeps its upstream prompt cache for hours, not minutes: on prod2
	// (2026-10-26) 148 channel switches per day came from the 30 minute entry expiring
	// mid-conversation.
	if defaultPreviousChannelTTL != 2*time.Hour {
		t.Fatalf("default previous channel TTL = %v, want 2h", defaultPreviousChannelTTL)
	}
}

func TestPreviousChannelCacheRoundTrip(t *testing.T) {
	svc := newPreviousChannelTestService(t, defaultPreviousChannelTTL)
	ctx := context.Background()

	svc.setPreviousChannelCache(ctx, buildPreviousTraceChannelCacheKey(1), 42)

	got, err := svc.GetPreviousChannelID(ctx, 1)
	if err != nil {
		t.Fatalf("GetPreviousChannelID: %v", err)
	}

	if got != 42 {
		t.Fatalf("GetPreviousChannelID() = %d, want 42", got)
	}

	// An unknown trace must return 0 instead of an error.
	if got, err := svc.GetPreviousChannelID(ctx, 999); err != nil || got != 0 {
		t.Fatalf("GetPreviousChannelID(unknown) = (%d, %v), want (0, nil)", got, err)
	}
}

func TestPreviousChannelCacheSlidesOnHit(t *testing.T) {
	ttl := 250 * time.Millisecond
	svc := newPreviousChannelTestService(t, ttl)
	ctx := context.Background()

	svc.setPreviousChannelCache(ctx, buildPreviousTraceChannelCacheKey(2), 7)

	// Read once at t=120ms: this must renew the entry.
	time.Sleep(120 * time.Millisecond)

	if got, _ := svc.GetPreviousChannelID(ctx, 2); got != 7 {
		t.Fatalf("first read = %d, want 7", got)
	}

	// Read again at t=240ms. The original entry would have expired at 250ms only if the
	// first read did NOT renew it; with sliding expiration it is still alive.
	time.Sleep(120 * time.Millisecond)

	if got, _ := svc.GetPreviousChannelID(ctx, 2); got != 7 {
		t.Fatal("sliding expiration did not extend the previous channel entry")
	}
}

// Reproduces prod2 trace 230349 (session ses_e89294064ffeoPFteUwIoM7mMu on 2026-10-26):
// the first attempt hit channel 138, which then returned 429 three times, so the request
// failed over to channel 134 and completed there. The affinity target must stay on 138:
// 138 held the warm prompt cache and only a retry of the current request moved it away.
func TestRememberSelectedChannelIgnoresRetryChannels(t *testing.T) {
	svc := newPreviousChannelTestService(t, defaultPreviousChannelTTL)
	ctx := context.Background()

	traceID := 230349

	svc.RememberSelectedChannel(ctx, traceID, 138, true)  // attempt 0: first channel
	svc.RememberSelectedChannel(ctx, traceID, 138, false) // same-channel retry
	svc.RememberSelectedChannel(ctx, traceID, 134, false) // channel switch to fallback

	got, err := svc.GetPreviousChannelID(ctx, traceID)
	if err != nil {
		t.Fatalf("GetPreviousChannelID: %v", err)
	}

	if got != 138 {
		t.Fatalf("previous channel after retries = %d, want the first attempt channel 138", got)
	}
}

func TestRememberSelectedChannelStoresFirstAttempt(t *testing.T) {
	svc := newPreviousChannelTestService(t, defaultPreviousChannelTTL)
	ctx := context.Background()

	svc.RememberSelectedChannel(ctx, 7, 1007, true)

	got, err := svc.GetPreviousChannelID(ctx, 7)
	if err != nil {
		t.Fatalf("GetPreviousChannelID: %v", err)
	}

	if got != 1007 {
		t.Fatalf("previous channel = %d, want 1007", got)
	}
}

func TestRememberSelectedChannelIgnoresZeroValues(t *testing.T) {
	svc := newPreviousChannelTestService(t, defaultPreviousChannelTTL)
	ctx := context.Background()

	svc.RememberSelectedChannel(ctx, 0, 1007, true)
	svc.RememberSelectedChannel(ctx, 7, 0, true)

	if got, _ := svc.GetPreviousChannelID(ctx, 7); got != 0 {
		t.Fatalf("previous channel = %d, want 0 for a zero channel id", got)
	}
}
