package typesafe

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/looplj/axonhub/llm/httpclient"
)

// 入站拒绝矩阵：每一种畸形请求都要在触达上游前失败，并映射到正确状态码。
// 这些是"反例"测试，与各处 happy path 成对存在。

// TestInboundRejectsMalformedRequests 覆盖结构层面的各种缺失与非法。
func TestInboundRejectsMalformedRequests(t *testing.T) {
	inbound := NewSystemOneInboundTransformer()

	cases := []struct {
		name       string
		body       string
		wantStatus int
	}{
		{"空 body", ``, 400},
		{"只有空白", `   `, 400},
		{"非法 JSON", `{`, 400},
		{"JSON 是数组", `[]`, 400},
		{"JSON 是字符串", `"hello"`, 400},
		{"缺 model", `{"state":"x","questions":{"q":{"type":"noul","instructions":"?"}}}`, 400},
		{"model 为空串", `{"model":"","state":"x","questions":{"q":{"type":"noul","instructions":"?"}}}`, 400},
		{"缺 state", `{"model":"jev-latest","questions":{"q":{"type":"noul","instructions":"?"}}}`, 400},
		{"state 为 null", `{"model":"jev-latest","state":null,"questions":{"q":{"type":"noul","instructions":"?"}}}`, 400},
		{"缺 questions", `{"model":"jev-latest","state":"x"}`, 400},
		{"questions 为空对象", `{"model":"jev-latest","state":"x","questions":{}}`, 400},
		{"questions 为 null", `{"model":"jev-latest","state":"x","questions":null}`, 400},
		{"请求流式", `{"model":"jev-latest","state":"x","stream":true,"questions":{"q":{"type":"noul","instructions":"?"}}}`, 400},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := inbound.TransformRequest(context.Background(), &httpclient.Request{
				Body: []byte(tc.body),
			})
			require.Error(t, err, "应拒绝: %s", tc.name)

			clientErr := inbound.TransformError(context.Background(), err)
			require.NotNil(t, clientErr)
			require.Equal(t, tc.wantStatus, clientErr.StatusCode,
				"应返回 %d，实际 %d（body=%s）", tc.wantStatus, clientErr.StatusCode, tc.body)
		})
	}
}

// TestInboundRejectsNilRequest 请求对象本身为 nil 也要安全拒绝。
func TestInboundRejectsNilRequest(t *testing.T) {
	inbound := NewSystemOneInboundTransformer()

	_, err := inbound.TransformRequest(context.Background(), nil)
	require.Error(t, err)

	clientErr := inbound.TransformError(context.Background(), err)
	require.NotNil(t, clientErr)
	require.Equal(t, 400, clientErr.StatusCode, "nil 请求应 400 而不是 panic 或 500")
}

// TestInboundAcceptsMinimalValidRequest 正向对照：最小可用请求必须通过，
// 证明上面那些拒绝确实源于缺失，而不是校验过严。
func TestInboundAcceptsMinimalValidRequest(t *testing.T) {
	inbound := NewSystemOneInboundTransformer()

	req, err := inbound.TransformRequest(context.Background(), &httpclient.Request{
		Body: []byte(`{"model":"jev-latest","state":"x","questions":{"q":{"type":"noul","instructions":"?"}}}`),
	})
	require.NoError(t, err)
	require.Equal(t, "jev-latest", req.Model)
	require.Len(t, req.SystemOne.Questions, 1)
}

// TestInboundAcceptsStreamFalse 显式 stream:false 与不传等价，都要通过。
func TestInboundAcceptsStreamFalse(t *testing.T) {
	inbound := NewSystemOneInboundTransformer()

	req, err := inbound.TransformRequest(context.Background(), &httpclient.Request{
		Body: []byte(`{"model":"jev-latest","state":"x","stream":false,"questions":{"q":{"type":"noul","instructions":"?"}}}`),
	})
	require.NoError(t, err)
	// Stream 是 *bool：显式 false 与未传都表示非流式
	if req.Stream != nil {
		require.False(t, *req.Stream)
	}
}

// TestInboundKeepsQuestionWithoutInstructions 缺少 instructions 属于上游语义问题，
// 入站是透传层不拦截（由上游给出明确错误），避免网关与上游规则不一致。
func TestInboundKeepsQuestionWithoutInstructions(t *testing.T) {
	inbound := NewSystemOneInboundTransformer()

	req, err := inbound.TransformRequest(context.Background(), &httpclient.Request{
		Body: []byte(`{"model":"jev-latest","state":"x","questions":{"q":{"type":"noul"}}}`),
	})
	require.NoError(t, err, "入站不应校验 instructions 是否存在")
	require.Contains(t, req.SystemOne.Questions, "q")
}

// TestInboundPreservesUnknownFieldsAtQuestionLevel 上游未来新增的题型字段
// 不应在入站被丢弃。当前 SystemOneQuestion 是显式结构，未知字段会被忽略，
// 这里锁定已知行为的边界，避免误以为它是透传容器。
func TestInboundQuestionUnknownFieldsPreserved(t *testing.T) {
	inbound := NewSystemOneInboundTransformer()

	req, err := inbound.TransformRequest(context.Background(), &httpclient.Request{
		Body: []byte(`{"model":"jev-latest","state":"x","questions":{"q":{"type":"noul","instructions":"?","future_hint":"abc","nested":{"k":1}}}}`),
	})
	require.NoError(t, err)

	q := req.SystemOne.Questions["q"]
	require.Equal(t, "noul", q.Type)
	require.Equal(t, `"abc"`, string(q.Extra["future_hint"]),
		"未知题型字段必须保留，不能静默丢弃")
	require.JSONEq(t, `{"k":1}`, string(q.Extra["nested"]))
}
