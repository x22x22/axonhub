package typesafe

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/looplj/axonhub/llm"
	"github.com/looplj/axonhub/llm/httpclient"
)

// decodeOutboundBody 取出出站 JSON 的通用表示，便于断言字段是否真的发出去。
func decodeOutboundBody(t *testing.T, body []byte) map[string]any {
	t.Helper()
	var payload map[string]any
	require.NoError(t, json.Unmarshal(body, &payload))
	return payload
}

func systemOneRequestWith(state any, assets map[string]llm.SystemOneAsset) *llm.Request {
	return &llm.Request{
		Model: "jev-latest",
		SystemOne: &llm.SystemOneRequest{
			State:  state,
			Assets: assets,
			Questions: map[string]llm.SystemOneQuestion{
				"q": {Type: "noul", Instructions: "Is it white?"},
			},
		},
	}
}

// TestAssetsRoundTripAllFields 保证 assets 的每个字段都能原样到达上游。
// TypeSafe 用 format+data_base64 校验图片，字段丢失会被上游直接拒绝。
func TestAssetsRoundTripAllFields(t *testing.T) {
	tr := newTestOutbound(t)

	assets := map[string]llm.SystemOneAsset{
		"img-one": {Type: "image", Format: "png", DataBase64: "AAAA"},
	}
	out, err := tr.TransformRequest(context.Background(),
		systemOneRequestWith(map[string]any{"photo": "img-one"}, assets))
	require.NoError(t, err)

	payload := decodeOutboundBody(t, out.Body)
	rawAssets, ok := payload["assets"].(map[string]any)
	require.True(t, ok, "assets 应作为对象发出")
	one, ok := rawAssets["img-one"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "image", one["type"])
	require.Equal(t, "png", one["format"])
	require.Equal(t, "AAAA", one["data_base64"])
}

// TestAssetsMultipleImages 多图场景：每张图独立 key，互不覆盖。
func TestAssetsMultipleImages(t *testing.T) {
	tr := newTestOutbound(t)

	assets := map[string]llm.SystemOneAsset{
		"a": {Type: "image", Format: "png", DataBase64: "AAA"},
		"b": {Type: "image", Format: "jpg", DataBase64: "BBB"},
		"c": {Type: "image", Format: "jpeg", DataBase64: "CCC"},
	}
	out, err := tr.TransformRequest(context.Background(),
		systemOneRequestWith(map[string]any{"x": "a", "y": "b", "z": "c"}, assets))
	require.NoError(t, err)

	payload := decodeOutboundBody(t, out.Body)
	rawAssets := payload["assets"].(map[string]any)
	require.Len(t, rawAssets, 3)
	require.Equal(t, "png", rawAssets["a"].(map[string]any)["format"])
	require.Equal(t, "jpg", rawAssets["b"].(map[string]any)["format"])
	require.Equal(t, "jpeg", rawAssets["c"].(map[string]any)["format"])
}

// TestAssetsPartModeRejected part 模式要求整个请求体是 multipart，而 AxonHub
// 的入站与出站都是 JSON，上游只要看到 part 字段就拒（即使同时给了
// data_base64）。因此出站必须提前给出可读错误，而不是把请求送上去换回一条
// 不说原因的 "Invalid image request"。
func TestAssetsPartModeRejected(t *testing.T) {
	tr := newTestOutbound(t)

	assets := map[string]llm.SystemOneAsset{
		"img": {Type: "image", Part: "door-photo"},
	}
	_, err := tr.TransformRequest(context.Background(),
		systemOneRequestWith(map[string]any{"photo": "img"}, assets))

	require.Error(t, err, "part 模式应被显式拒绝")
	require.Contains(t, err.Error(), "multipart", "错误信息应说明原因")
	require.Contains(t, err.Error(), "data_base64", "错误信息应给出替代方案")
	require.Contains(t, err.Error(), "door-photo", "错误信息应指出是哪个 part")
}

// TestAssetsPartModeRejectedEvenWithBase64 同时给 part 与 data_base64 也要拒绝：
// 上游见到 part 就按 multipart 解析，base64 不会被采纳。
func TestAssetsPartModeRejectedEvenWithBase64(t *testing.T) {
	tr := newTestOutbound(t)

	assets := map[string]llm.SystemOneAsset{
		"img": {Type: "image", Format: "png", DataBase64: "QUJD", Part: "some-part"},
	}
	_, err := tr.TransformRequest(context.Background(),
		systemOneRequestWith(map[string]any{"photo": "img"}, assets))
	require.Error(t, err, "part 存在即应拒绝，不能因为带了 base64 就放行")
}

// TestAssetsOmittedWhenEmpty assets 为空时不应发出空对象，
// 避免上游把空 assets 当作声明了资产却未引用。
func TestAssetsOmittedWhenEmpty(t *testing.T) {
	tr := newTestOutbound(t)

	for name, assets := range map[string]map[string]llm.SystemOneAsset{
		"nil":   nil,
		"empty": {},
	} {
		t.Run(name, func(t *testing.T) {
			out, err := tr.TransformRequest(context.Background(),
				systemOneRequestWith("plain text", assets))
			require.NoError(t, err)

			payload := decodeOutboundBody(t, out.Body)
			require.NotContains(t, payload, "assets", "空 assets 不应出现在请求体里")
		})
	}
}

// TestAssetsReferenceInNestedState 引用可出现在任意嵌套层级，
// 序列化不应把外层结构压平。
func TestAssetsReferenceInNestedState(t *testing.T) {
	tr := newTestOutbound(t)

	state := map[string]any{
		"frames": []any{
			map[string]any{"seconds_ago": 2, "frame": "f1"},
			map[string]any{"seconds_ago": 0, "frame": "f2"},
		},
		"note": "compare f1 and f2",
	}
	assets := map[string]llm.SystemOneAsset{
		"f1": {Type: "image", Format: "png", DataBase64: "A"},
		"f2": {Type: "image", Format: "png", DataBase64: "B"},
	}
	out, err := tr.TransformRequest(context.Background(), systemOneRequestWith(state, assets))
	require.NoError(t, err)

	payload := decodeOutboundBody(t, out.Body)

	// 嵌套结构必须保留，不能字符串化
	frames, ok := payload["state"].(map[string]any)["frames"].([]any)
	require.True(t, ok, "state.frames 应保持数组结构")
	require.Len(t, frames, 2)
	require.Equal(t, "f1", frames[0].(map[string]any)["frame"])
	require.Equal(t, float64(2), frames[0].(map[string]any)["seconds_ago"])
}

// TestAssetsReferenceInChoiceCriteria 引用放在 criteria 里也要原样发出。
// 官方明确对象 key 不被替换，所以只断言 value 侧的引用保留。
func TestAssetsReferenceInChoiceCriteria(t *testing.T) {
	tr := newTestOutbound(t)

	req := &llm.Request{
		Model: "jev-latest",
		SystemOne: &llm.SystemOneRequest{
			State: "no reference in state",
			Assets: map[string]llm.SystemOneAsset{
				"thumb": {Type: "image", Format: "png", DataBase64: "QQ=="},
			},
			Questions: map[string]llm.SystemOneQuestion{
				"gesture": {
					Type:         "choice",
					Instructions: "Which gesture?",
					Criteria: map[string]any{
						"thumbs_up": "thumb",
						"fist":      "No hand matching the examples.",
					},
				},
			},
		},
	}
	out, err := tr.TransformRequest(context.Background(), req)
	require.NoError(t, err)

	payload := decodeOutboundBody(t, out.Body)
	q := payload["questions"].(map[string]any)["gesture"].(map[string]any)
	criteria := q["criteria"].(map[string]any)
	require.Equal(t, "thumb", criteria["thumbs_up"], "引用应原样出现在 criteria 的 value 里")
	require.Equal(t, "fist", func() string {
		for k := range criteria {
			if k == "fist" {
				return "fist"
			}
		}
		return ""
	}())
}

// TestAssetsStructuredInstructions instructions 允许对象结构，
// 官方示例把问题与候选列表一起放进对象里。
func TestAssetsStructuredInstructions(t *testing.T) {
	tr := newTestOutbound(t)

	req := &llm.Request{
		Model: "jev-latest",
		SystemOne: &llm.SystemOneRequest{
			State: "page content",
			Questions: map[string]llm.SystemOneQuestion{
				"unlisted": {
					Type: "noul",
					Instructions: map[string]any{
						"question": "Does the table record any vaccine not in this list?",
						"list":     []any{"Hepatitis A", "Typhoid"},
					},
				},
			},
		},
	}
	out, err := tr.TransformRequest(context.Background(), req)
	require.NoError(t, err)

	payload := decodeOutboundBody(t, out.Body)
	ins := payload["questions"].(map[string]any)["unlisted"].(map[string]any)["instructions"].(map[string]any)
	require.Contains(t, ins, "question")
	require.Contains(t, ins, "list")
	require.Len(t, ins["list"].([]any), 2)
}

// TestInboundPreservesAssets 入站解析不能丢 assets，否则 pass-through 关闭时会失效。
func TestInboundPreservesAssets(t *testing.T) {
	inbound := NewSystemOneInboundTransformer()

	body := []byte(`{
	  "model": "jev-latest",
	  "state": {"photo": "abc"},
	  "assets": {
	    "abc": {"type": "image", "format": "png", "data_base64": "QUJD"}
	  },
	  "questions": {"q": {"type": "noul", "instructions": "Is it white?"}}
	}`)

	llmReq, err := inbound.TransformRequest(context.Background(), &httpclient.Request{Body: body})
	require.NoError(t, err)
	require.NotNil(t, llmReq.SystemOne)
	require.Contains(t, llmReq.SystemOne.Assets, "abc")
	require.Equal(t, "png", llmReq.SystemOne.Assets["abc"].Format)
	require.Equal(t, "QUJD", llmReq.SystemOne.Assets["abc"].DataBase64)
}

// TestInboundAssetsOptional 不带 assets 的纯文本请求必须照常工作。
func TestInboundAssetsOptional(t *testing.T) {
	inbound := NewSystemOneInboundTransformer()

	body := []byte(`{"model":"jev-latest","state":"text","questions":{"q":{"type":"noul","instructions":"?"}}}`)
	llmReq, err := inbound.TransformRequest(context.Background(), &httpclient.Request{Body: body})
	require.NoError(t, err)
	require.Empty(t, llmReq.SystemOne.Assets)
}
