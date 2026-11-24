package typesafe

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/looplj/axonhub/llm"
	"github.com/looplj/axonhub/llm/httpclient"
)

func boxResponse(t *testing.T, body string) *llm.Response {
	t.Helper()
	tr := newTestOutbound(t)
	resp, err := tr.TransformResponse(context.Background(), &httpclient.Response{
		StatusCode: 200,
		Body:       []byte(body),
	})
	require.NoError(t, err)
	return resp
}

// TestBoxAnswerFullEdges 四个边界值都要精确保留，坐标错位会让裁剪切到错误区域。
func TestBoxAnswerFullEdges(t *testing.T) {
	resp := boxResponse(t, `{
	  "model":"jev-1.14.0",
	  "answers":{"mug":{"type":"box","box":{"top":0.25,"bottom":0.82,"left":0.44,"right":0.84},"confidence":0.92}},
	  "usage":{"input_tokens":290,"output_tokens":21}
	}`)

	mug := resp.SystemOne.Answers["mug"]
	require.Equal(t, "box", mug.Type)
	require.NotNil(t, mug.Box)
	require.InDelta(t, 0.25, mug.Box.Top, 1e-9)
	require.InDelta(t, 0.82, mug.Box.Bottom, 1e-9)
	require.InDelta(t, 0.44, mug.Box.Left, 1e-9)
	require.InDelta(t, 0.84, mug.Box.Right, 1e-9)
	require.InDelta(t, 0.92, *mug.Confidence, 1e-9)
}

// TestBoxAnswerBoundaryValues 0 和 1 是合法边界，不能被 omitempty 吞掉。
// 若用 float64 + omitempty，0.0 会消失，裁剪时左边界就丢了。
func TestBoxAnswerBoundaryValues(t *testing.T) {
	resp := boxResponse(t, `{
	  "model":"jev-1.14.0",
	  "answers":{"all":{"type":"box","box":{"top":0,"bottom":1,"left":0,"right":1},"confidence":1}},
	  "usage":{"input_tokens":1,"output_tokens":1}
	}`)

	box := resp.SystemOne.Answers["all"].Box
	require.NotNil(t, box)
	require.Zero(t, box.Top)
	require.Equal(t, 1.0, box.Bottom)
	require.Zero(t, box.Left)
	require.Equal(t, 1.0, box.Right)

	// 序列化后四个边界必须都还在
	raw, err := json.Marshal(box)
	require.NoError(t, err)
	require.JSONEq(t, `{"top":0,"bottom":1,"left":0,"right":1}`, string(raw))
}

// TestBoxAnswerWithoutConfidence confidence 是可选的边界情况。
func TestBoxAnswerWithoutConfidence(t *testing.T) {
	resp := boxResponse(t, `{
	  "model":"jev-1.14.0",
	  "answers":{"b":{"type":"box","box":{"top":0.1,"bottom":0.2,"left":0.3,"right":0.4}}},
	  "usage":{"input_tokens":1,"output_tokens":1}
	}`)

	b := resp.SystemOne.Answers["b"]
	require.NotNil(t, b.Box)
	require.Nil(t, b.Confidence, "没有 confidence 时不应编造一个")
}

// TestBoxMixedWithOtherPrimitives 一次请求可混四种原语，各自答案类型互不干扰。
func TestBoxMixedWithOtherPrimitives(t *testing.T) {
	resp := boxResponse(t, `{
	  "model":"jev-1.14.0",
	  "answers":{
	    "bx":{"type":"box","box":{"top":0.03,"bottom":0.98,"left":0.02,"right":0.99},"confidence":0.11},
	    "nl":{"type":"noul","noul":0.66},
	    "ch":{"type":"choice","choice":"w","confidence":0.98,"probabilities":{"w":0.99,"b":0.01}},
	    "sc":{"type":"score","score":1.4,"confidence":0.7,"legend":{"0":"dark","1":"mid","2":"light"},"probabilities":{"0":0.0,"1":0.6,"2":0.4}}
	  },
	  "usage":{"input_tokens":100,"output_tokens":20}
	}`)

	a := resp.SystemOne.Answers
	require.NotNil(t, a["bx"].Box)
	require.InDelta(t, 0.66, *a["nl"].Noul, 1e-9)
	require.Equal(t, "w", a["ch"].Choice)
	require.InDelta(t, 1.4, *a["sc"].Score, 1e-9)
	require.Nil(t, a["nl"].Box, "noul 答案不应带 box")
	require.Nil(t, a["bx"].Noul, "box 答案不应带 noul")
}

// TestBoxAnswerMissingBoxField 上游只给 type 不给 box 时，
// 解析不能 panic，且要把 Box 保持为 nil 让调用方判断。
func TestBoxAnswerMissingBoxField(t *testing.T) {
	resp := boxResponse(t, `{
	  "model":"jev-1.14.0",
	  "answers":{"b":{"type":"box"}},
	  "usage":{"input_tokens":1,"output_tokens":1}
	}`)

	b := resp.SystemOne.Answers["b"]
	require.Equal(t, "box", b.Type)
	require.Nil(t, b.Box)
}

// TestBoxAnswerWrongCoordinateType 坐标传成字符串属于畸形响应，
// 应报错而不是静默得到 0。
func TestBoxAnswerWrongCoordinateType(t *testing.T) {
	tr := newTestOutbound(t)
	_, err := tr.TransformResponse(context.Background(), &httpclient.Response{
		StatusCode: 200,
		Body:       []byte(`{"model":"jev-1.14.0","answers":{"b":{"type":"box","box":{"top":"high"}}},"usage":{}}`),
	})
	require.Error(t, err, "坐标类型错误应显式失败，不能静默变成 0")
}

// TestBoxAnswerOutOfRangeKept 越界坐标原样保留：AxonHub 是网关，
// 不做几何修正，否则会掩盖上游问题。
func TestBoxAnswerOutOfRangeKept(t *testing.T) {
	resp := boxResponse(t, `{
	  "model":"jev-1.14.0",
	  "answers":{"b":{"type":"box","box":{"top":-0.2,"bottom":1.3,"left":-0.1,"right":1.5},"confidence":0.5}},
	  "usage":{"input_tokens":1,"output_tokens":1}
	}`)

	box := resp.SystemOne.Answers["b"].Box
	require.NotNil(t, box)
	require.InDelta(t, -0.2, box.Top, 1e-9)
	require.InDelta(t, 1.3, box.Bottom, 1e-9)
	require.InDelta(t, -0.1, box.Left, 1e-9)
	require.InDelta(t, 1.5, box.Right, 1e-9)
}

// TestBoxQuestionAcceptsNoCriteria box 不定义 criteria；带了也不该被网关拦下，
// 上游会忽略它。这里锁定"网关不做额外校验"的立场。
func TestBoxQuestionAcceptsNoCriteria(t *testing.T) {
	tr := newTestOutbound(t)

	req := &llm.Request{
		Model: "jev-latest",
		SystemOne: &llm.SystemOneRequest{
			State: "image",
			Questions: map[string]llm.SystemOneQuestion{
				"q": {Type: "box", Instructions: "the mug"},
			},
		},
	}
	out, err := tr.TransformRequest(context.Background(), req)
	require.NoError(t, err)

	payload := decodeOutboundBody(t, out.Body)
	q := payload["questions"].(map[string]any)["q"].(map[string]any)
	require.Equal(t, "box", q["type"])
	require.NotContains(t, q, "criteria", "未设置 criteria 时不应补出该字段")
}

// TestBoxAnswerInExtraIsNotLost 上游若额外返回未知字段，仍要保留在 Extra 里，
// 避免严格结构吞掉未来新增的答案字段。
func TestBoxAnswerInExtraIsNotLost(t *testing.T) {
	resp := boxResponse(t, `{
	  "model":"jev-1.14.0",
	  "answers":{"b":{"type":"box","box":{"top":0,"bottom":1,"left":0,"right":1},"confidence":0.5,"future_field":{"k":1}}},
	  "usage":{"input_tokens":1,"output_tokens":1}
	}`)

	b := resp.SystemOne.Answers["b"]
	require.Contains(t, b.Extra, "future_field", "未知字段应保留在 Extra")
}
