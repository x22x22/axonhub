package orchestrator

import (
	"context"
	"time"

	"github.com/looplj/axonhub/internal/log"
	"github.com/looplj/axonhub/llm"
	"github.com/looplj/axonhub/llm/httpclient"
	"github.com/looplj/axonhub/llm/pipeline"
	"github.com/looplj/axonhub/llm/streams"
)

// withRateLimitTracking creates a middleware that tracks request counts per channel for rate limiting.
func withRateLimitTracking(outbound *PersistentOutboundTransformer, tracker *ChannelRequestTracker) pipeline.Middleware {
	if tracker == nil {
		return &noopRateLimitTracking{}
	}

	return &rateLimitTracking{
		outbound: outbound,
		tracker:  tracker,
	}
}

// rateLimitTracking is a middleware that increments request count for rate limiting.
type rateLimitTracking struct {
	pipeline.DummyMiddleware

	outbound *PersistentOutboundTransformer
	tracker  *ChannelRequestTracker
}

func (m *rateLimitTracking) Name() string {
	return "track-rate-limit"
}

func (m *rateLimitTracking) OnOutboundRawRequest(ctx context.Context, request *httpclient.Request) (*httpclient.Request, error) {
	channel := m.outbound.GetCurrentChannel()
	if channel == nil {
		return request, nil
	}

	m.tracker.IncrementRequest(channel.ID)

	if log.DebugEnabled(ctx) {
		log.Debug(ctx, "Incremented rate limit request count",
			log.Int("channel_id", channel.ID),
			log.String("channel_name", channel.Name),
			log.Int64("current_rpm", m.tracker.GetRequestCount(channel.ID)),
		)
	}

	return request, nil
}

func (m *rateLimitTracking) OnOutboundLlmResponse(ctx context.Context, response *llm.Response) (*llm.Response, error) {
	channel := m.outbound.GetCurrentChannel()
	if channel == nil || response == nil || response.Usage == nil {
		return response, nil
	}

	totalTokens := response.Usage.TotalTokens
	if totalTokens > 0 {
		m.tracker.AddTokens(channel.ID, totalTokens)

		if log.DebugEnabled(ctx) {
			log.Debug(ctx, "Incremented rate limit token count",
				log.Int("channel_id", channel.ID),
				log.String("channel_name", channel.Name),
				log.Int64("tokens", totalTokens),
				log.Int64("current_tpm", m.tracker.GetTokenCount(channel.ID)),
			)
		}
	}

	return response, nil
}

func (m *rateLimitTracking) OnOutboundLlmStream(ctx context.Context, stream streams.Stream[*llm.Response]) (streams.Stream[*llm.Response], error) {
	return &rateLimitTrackingStream{
		ctx:      ctx,
		stream:   stream,
		tracker:  m.tracker,
		outbound: m.outbound,
	}, nil
}

// implicitRateLimitCooldown caps the cooldown when the upstream did not send Retry-After.
// A 429 is often per-request rather than channel-wide: on prod2 channel 138 answered
// requests successfully at 06:12:31, 06:12:34 and 06:12:35 while answering others with
// 429 between 06:12:32 and 06:12:38. Penalising such a channel for the generic 60 second
// default removes a mostly healthy channel from the pool.
const implicitRateLimitCooldown = 15 * time.Second

// OnOutboundRawError handles raw HTTP errors, specifically capturing 429 Too Many Requests.
// Every 429 cools the channel down: when the upstream sends Retry-After we honour it,
// otherwise a short implicit cooldown is used.
//
// Cooling only when Retry-After was present left most throttled channels selectable. On
// prod2 (2026-10-26) channel 138 recorded 1239 failed 429 executions in one day while the
// cooldown log had 2 entries, so the balancer kept selecting it, retrying it and moving the
// conversation onto a channel without a warm prompt cache.
func (m *rateLimitTracking) OnOutboundRawError(ctx context.Context, err error) {
	// Safety check: outbound might be nil in edge cases
	if m.outbound == nil {
		return
	}

	channel := m.outbound.GetCurrentChannel()
	if channel == nil {
		return
	}

	if !httpclient.IsRateLimitErr(err) {
		return
	}

	// ParseRetryAfter falls back to httpclient.DefaultRetryAfterSeconds when Retry-After is
	// missing; cap that implicit value so a single throttled request does not evict a
	// channel that is still serving other requests.
	cooldown, ok := httpclient.ParseRetryAfter(err)
	if !ok {
		return
	}

	if !httpclient.HasRetryAfterHeader(err) {
		cooldown = min(cooldown, implicitRateLimitCooldown)
	}

	// Set cooldown for this channel
	m.tracker.SetCooldown(channel.ID, time.Now().Add(cooldown))

	log.Warn(ctx, "channel cooling down due to 429",
		log.Int("channel_id", channel.ID),
		log.String("channel_name", channel.Name),
		log.Duration("cooldown", cooldown),
	)
}

// rateLimitTrackingStream wraps a stream to track token usage for rate limiting.
//
//nolint:containedctx // ctx is used for logging.
type rateLimitTrackingStream struct {
	ctx      context.Context
	stream   streams.Stream[*llm.Response]
	tracker  *ChannelRequestTracker
	outbound *PersistentOutboundTransformer
}

func (s *rateLimitTrackingStream) Current() *llm.Response {
	event := s.stream.Current()
	if event == nil {
		return event
	}

	// Track tokens if usage information is present (typically in the last chunk)
	if event.Usage != nil && event.Usage.TotalTokens > 0 {
		channel := s.outbound.GetCurrentChannel()
		if channel != nil {
			s.tracker.AddTokens(channel.ID, event.Usage.TotalTokens)

			if log.DebugEnabled(s.ctx) {
				log.Debug(s.ctx, "Incremented rate limit token count from stream",
					log.Int("channel_id", channel.ID),
					log.String("channel_name", channel.Name),
					log.Int64("tokens", event.Usage.TotalTokens),
					log.Int64("current_tpm", s.tracker.GetTokenCount(channel.ID)),
				)
			}
		}
	}

	return event
}

func (s *rateLimitTrackingStream) Next() bool {
	return s.stream.Next()
}

func (s *rateLimitTrackingStream) Close() error {
	return s.stream.Close()
}

func (s *rateLimitTrackingStream) Err() error {
	return s.stream.Err()
}

// noopRateLimitTracking is a no-op middleware when rate limit tracking is disabled.
type noopRateLimitTracking struct {
	pipeline.DummyMiddleware
}

func (m *noopRateLimitTracking) Name() string {
	return "track-rate-limit-noop"
}

func (m *noopRateLimitTracking) OnOutboundLlmStream(ctx context.Context, stream streams.Stream[*llm.Response]) (streams.Stream[*llm.Response], error) {
	return stream, nil
}
