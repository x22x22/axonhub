package typesafe

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/looplj/axonhub/llm/httpclient"
)

// state 的类型边界是一条容易踩的限制：System One 只接受 string / object /
// array。裸 number / boolean / null 会被上游以 422 拒绝，正确写法是包一层
// 对象（{"amount": 42}）。这组测试把该边界钉在入站侧与真实上游两侧。

// TestInboundAcceptsStateString 字符串 state 是最常见形态。
func TestInboundAcceptsStateString(t *testing.T) {
	inbound := NewSystemOneInboundTransformer()

	req, err := inbound.TransformRequest(context.Background(), &httpclient.Request{
		Body: []byte(`{"model":"jev-latest","state":"plain sentence","questions":{"q":{"type":"noul","instructions":"?"}}}`),
	})
	require.NoError(t, err)
	require.Equal(t, "plain sentence", req.SystemOne.State)
}

// TestInboundAcceptsStateObjectAndArray 对象与数组都要原样保留结构。
func TestInboundAcceptsStateObjectAndArray(t *testing.T) {
	inbound := NewSystemOneInboundTransformer()

	cases := map[string]string{
		"object": `{"model":"jev-latest","state":{"amount":42,"currency":"USD"},"questions":{"q":{"type":"noul","instructions":"?"}}}`,
		"array":  `{"model":"jev-latest","state":["a","b",{"c":1}],"questions":{"q":{"type":"noul","instructions":"?"}}}`,
	}

	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			req, err := inbound.TransformRequest(context.Background(), &httpclient.Request{Body: []byte(body)})
			require.NoError(t, err)
			require.NotNil(t, req.SystemOne.State)
		})
	}
}

// TestInboundBareScalarStatePassesThrough 入站是透传层，不在这里做类型裁剪：
// 裸 number/boolean 会被放行，由上游给出 422。AxonHub 若在入站静默改写
// state，会掩盖调用方的契约错误。
func TestInboundBareScalarStatePassesThrough(t *testing.T) {
	inbound := NewSystemOneInboundTransformer()

	for name, state := range map[string]string{
		"number":  `42`,
		"boolean": `true`,
		"float":   `3.14`,
	} {
		t.Run(name, func(t *testing.T) {
			body := `{"model":"jev-latest","state":` + state +
				`,"questions":{"q":{"type":"noul","instructions":"?"}}}`
			req, err := inbound.TransformRequest(context.Background(), &httpclient.Request{Body: []byte(body)})
			require.NoError(t, err, "入站不应拒绝标量 state")
			require.NotNil(t, req.SystemOne.State, "标量 state 应原样保留")
		})
	}
}

// TestInboundNullStateRejected state 为 null 等同于未提供，必须在入站就拒绝，
// 否则会把一个必然失败的请求送到上游。
func TestInboundNullStateRejected(t *testing.T) {
	inbound := NewSystemOneInboundTransformer()

	_, err := inbound.TransformRequest(context.Background(), &httpclient.Request{
		Body: []byte(`{"model":"jev-latest","state":null,"questions":{"q":{"type":"noul","instructions":"?"}}}`),
	})
	require.Error(t, err, "state 为 null 应被拒绝")

	clientErr := inbound.TransformError(context.Background(), err)
	require.Equal(t, 400, clientErr.StatusCode, "应映射为 400 而不是 500")
}

// TestOutboundBareScalarStateForwardedUnchanged 出站不改写标量 state，
// 保证"网关不改语义"这条边界。
func TestOutboundBareScalarStateForwardedUnchanged(t *testing.T) {
	tr := newTestOutbound(t)

	// 断言出站 JSON 文本而不是 Go 值：JSON 往返下 int 会变 float64，
	// 用值比较会得到与"是否原样发出"无关的假失败。
	cases := map[string]struct {
		state any
		want  string
	}{
		"number":  {42, `42`},
		"boolean": {true, `true`},
		"float":   {3.14, `3.14`},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			out, err := tr.TransformRequest(context.Background(), systemOneRequestWith(tc.state, nil))
			require.NoError(t, err)

			payload := decodeOutboundBody(t, out.Body)
			raw, err := json.Marshal(payload["state"])
			require.NoError(t, err)
			require.JSONEq(t, tc.want, string(raw), "标量 state 应原样发出")
		})
	}
}

// TestStateDeepNestingPreserved 深层嵌套（帧历史、分页文档）不能被打平。
func TestStateDeepNestingPreserved(t *testing.T) {
	tr := newTestOutbound(t)

	state := map[string]any{
		"document": map[string]any{
			"sections": []any{
				map[string]any{"title": "Intro", "paragraphs": []any{"p1", "p2"}},
				map[string]any{"title": "Terms", "paragraphs": []any{"p3"}},
			},
		},
	}

	out, err := tr.TransformRequest(context.Background(), systemOneRequestWith(state, nil))
	require.NoError(t, err)

	payload := decodeOutboundBody(t, out.Body)
	sections := payload["state"].(map[string]any)["document"].(map[string]any)["sections"].([]any)
	require.Len(t, sections, 2)

	paras := sections[0].(map[string]any)["paragraphs"].([]any)
	require.Len(t, paras, 2, "嵌套数组不应被压平")
	require.Equal(t, "p1", paras[0])
}
