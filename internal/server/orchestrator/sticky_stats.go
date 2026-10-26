package orchestrator

import (
	"context"
	"sync/atomic"

	"github.com/looplj/axonhub/internal/metrics"
)

// StickyOutcome classifies the conversation-affinity decision of one channel selection.
type StickyOutcome string

const (
	// StickyHit means the previous channel was a candidate and was pinned.
	StickyHit StickyOutcome = "hit"
	// StickyMissNoCache means the conversation had no recorded previous channel.
	StickyMissNoCache StickyOutcome = "miss_no_cache"
	// StickyMissNotCandidate means the recorded channel is no longer a candidate
	// (archived, filtered by a profile, or removed from the model associations).
	StickyMissNotCandidate StickyOutcome = "miss_not_in_candidates"
	// StickyRejectedUnavailable means the recorded channel was a candidate but unusable
	// (for example cooling down after a 429), so normal load balancing took over.
	StickyRejectedUnavailable StickyOutcome = "rejected_unavailable"
)

// stickyStats counts affinity outcomes. The counters are atomic and lock free; each
// outcome is also exported through the OTel counter chat_sticky_outcome_count so the
// affinity can be evaluated without reverse engineering channel switches from the
// gateway database.
type stickyStats struct {
	hit                 atomic.Int64
	missNoCache         atomic.Int64
	missNotCandidate    atomic.Int64
	rejectedUnavailable atomic.Int64
}

func (s *stickyStats) record(ctx context.Context, outcome StickyOutcome) {
	if s == nil {
		return
	}

	switch outcome {
	case StickyHit:
		s.hit.Add(1)
	case StickyMissNoCache:
		s.missNoCache.Add(1)
	case StickyMissNotCandidate:
		s.missNotCandidate.Add(1)
	case StickyRejectedUnavailable:
		s.rejectedUnavailable.Add(1)
	}

	if metrics.Metrics != nil {
		metrics.Metrics.RecordStickyOutcome(ctx, string(outcome))
	}
}

// snapshot returns the counters keyed by outcome.
func (s *stickyStats) snapshot() map[StickyOutcome]int64 {
	if s == nil {
		return nil
	}

	return map[StickyOutcome]int64{
		StickyHit:                 s.hit.Load(),
		StickyMissNoCache:         s.missNoCache.Load(),
		StickyMissNotCandidate:    s.missNotCandidate.Load(),
		StickyRejectedUnavailable: s.rejectedUnavailable.Load(),
	}
}
