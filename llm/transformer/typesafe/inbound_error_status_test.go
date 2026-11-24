package typesafe

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/looplj/axonhub/llm"
	"github.com/looplj/axonhub/llm/httpclient"
	"github.com/looplj/axonhub/llm/transformer"
)

// TestSystemOneTransformErrorClassifiesClientErrors 钉住状态码分类：
// TransformRequest 因请求本身不合法而失败时，响应必须是 400，
// 不能让客户端误以为是服务端故障而重试。
func TestSystemOneTransformErrorClassifiesClientErrors(t *testing.T) {
	inbound := NewSystemOneInboundTransformer()

	cases := []struct {
		name string
		body string
	}{
		{name: "空 body", body: ""},
		{name: "缺 model", body: `{"state":"x","questions":{"q":{"type":"noul","instructions":"?"}}}`},
		{name: "缺 state", body: `{"model":"jev-latest","questions":{"q":{"type":"noul","instructions":"?"}}}`},
		{name: "缺 questions", body: `{"model":"jev-latest","state":"x","questions":{}}`},
		{name: "不支持流式", body: `{"model":"jev-latest","state":"x","questions":{"q":{"type":"noul","instructions":"?"}},"stream":true}`},
		{name: "非法 JSON", body: `{"model":`},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := inbound.TransformRequest(context.Background(), &httpclient.Request{
				Body: []byte(tc.body),
			})
			require.Error(t, err, "输入应当被拒绝")

			httpErr := inbound.TransformError(context.Background(), err)
			require.Equal(t, http.StatusBadRequest, httpErr.StatusCode,
				"客户端参数错误应为 400，实际 %d: %s", httpErr.StatusCode, httpErr.Body)
			require.Contains(t, string(httpErr.Body), "invalid_request_error")
		})
	}
}

// TestSystemOneTransformErrorKeepsUpstreamStatus 确保上游返回的 ResponseError
// 仍按上游状态码透出，不被 400 分支吞掉。
func TestSystemOneTransformErrorKeepsUpstreamStatus(t *testing.T) {
	inbound := NewSystemOneInboundTransformer()
	upstream := fmt.Errorf("wrapped: %w", &llm.ResponseError{
		StatusCode: http.StatusTooManyRequests,
		Detail:     llm.ErrorDetail{Message: "rate limited", Type: "rate_limit_error"},
	})
	httpErr := inbound.TransformError(context.Background(), upstream)
	require.Equal(t, http.StatusTooManyRequests, httpErr.StatusCode)
}

// TestSystemOneTransformErrorMapsInvalidModelTo422 模型名不存在属于客户端错误，
// 但要与"请求格式错"区分开，沿用 OpenAI/Anthropic inbound 的 422 语义。
func TestSystemOneTransformErrorMapsInvalidModelTo422(t *testing.T) {
	inbound := NewSystemOneInboundTransformer()
	err := fmt.Errorf("%w: no-such-model", transformer.ErrInvalidModel)

	httpErr := inbound.TransformError(context.Background(), err)
	require.Equal(t, http.StatusUnprocessableEntity, httpErr.StatusCode,
		"模型不存在应为 422，实际 %d: %s", httpErr.StatusCode, httpErr.Body)
	require.Contains(t, string(httpErr.Body), "invalid_model_error")
}

// TestSystemOneTransformErrorUnknownStays500 未知错误仍按 500 处理。
func TestSystemOneTransformErrorUnknownStays500(t *testing.T) {
	inbound := NewSystemOneInboundTransformer()
	httpErr := inbound.TransformError(context.Background(), fmt.Errorf("boom"))
	require.Equal(t, http.StatusInternalServerError, httpErr.StatusCode)
}

// TestSystemOneTransformErrorNil 空错误仍按 500 处理。
func TestSystemOneTransformErrorNil(t *testing.T) {
	inbound := NewSystemOneInboundTransformer()
	httpErr := inbound.TransformError(context.Background(), nil)
	require.Equal(t, http.StatusInternalServerError, httpErr.StatusCode)
}

// 保证测试用到的哨兵错误确实来自 transformer 包。
var _ = transformer.ErrInvalidRequest
