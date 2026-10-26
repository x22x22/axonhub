package orchestrator

import (
	"context"
	"net/http"
	"strings"

	"github.com/looplj/axonhub/internal/log"
	"github.com/looplj/axonhub/llm/httpclient"
	"github.com/looplj/axonhub/llm/pipeline"
	"github.com/looplj/axonhub/llm/transformer/opencode"
	"github.com/looplj/axonhub/llm/transformer/shared"
)

// applySessionHeader forwards the conversation session id to the upstream provider as
// X-Opencode-Session. Providers that key their prompt cache or rate budget by session can
// then treat consecutive turns of one conversation as one stream instead of independent
// requests.
//
// It never overwrites a header the client already sent, and it does nothing when the
// request has no session id (non conversation clients).
func applySessionHeader() pipeline.Middleware {
	return pipeline.OnRawRequest("session-header-pass-through", func(ctx context.Context, request *httpclient.Request) (*httpclient.Request, error) {
		setSessionHeader(ctx, request)

		return request, nil
	})
}

// setSessionHeader is the testable core of applySessionHeader.
func setSessionHeader(ctx context.Context, request *httpclient.Request) {
	if request == nil {
		return
	}

	if request.Headers == nil {
		request.Headers = make(http.Header)
	}

	if request.Headers.Get(opencode.SessionHeader) != "" {
		return
	}

	sessionID, ok := shared.GetSessionID(ctx)
	if !ok || strings.TrimSpace(sessionID) == "" {
		return
	}

	request.Headers.Set(opencode.SessionHeader, strings.TrimSpace(sessionID))

	if log.DebugEnabled(ctx) {
		log.Debug(ctx, "forwarded opencode session header",
			log.String("session_id", sessionID),
			log.String("header", opencode.SessionHeader))
	}
}
