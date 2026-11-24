package typesafe

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/looplj/axonhub/llm"
	"github.com/looplj/axonhub/llm/auth"
	"github.com/looplj/axonhub/llm/httpclient"
)

func newTestOutbound(t *testing.T) *OutboundTransformer {
	t.Helper()
	tr, err := NewOutboundTransformerWithConfig(&Config{
		BaseURL:        "https://api.typesafe.ai/v1",
		APIKeyProvider: auth.NewStaticKeyProvider("test-key"),
	})
	require.NoError(t, err)
	return tr
}

func boxSystemOneRequest(assets map[string]llm.SystemOneAsset, qtype string) *llm.Request {
	return &llm.Request{
		Model: "jev-latest",
		SystemOne: &llm.SystemOneRequest{
			State:  "Look at {img1}",
			Assets: assets,
			Questions: map[string]llm.SystemOneQuestion{
				"q": {Type: qtype, Instructions: "Locate the red region"},
			},
		},
	}
}

// box 题型和 assets 都必须触发 TypeSafe 的图片实验开关，否则上游直接拒绝。
func TestTransformRequestEnablesImagesHeaderForAssets(t *testing.T) {
	tr := newTestOutbound(t)

	req := boxSystemOneRequest(map[string]llm.SystemOneAsset{
		"img1": {Type: "image", Format: "png", DataBase64: "AAAA"},
	}, "noul")

	out, err := tr.TransformRequest(context.Background(), req)
	require.NoError(t, err)
	require.Equal(t, "true", out.Headers.Get(headerDangerZoneImages))
}

func TestTransformRequestEnablesImagesHeaderForBox(t *testing.T) {
	tr := newTestOutbound(t)

	// 没有 assets，仅凭 box 题型也要开启
	req := boxSystemOneRequest(nil, "box")

	out, err := tr.TransformRequest(context.Background(), req)
	require.NoError(t, err)
	require.Equal(t, "true", out.Headers.Get(headerDangerZoneImages))
}

func TestTransformRequestOmitsImagesHeaderWithoutImageFeatures(t *testing.T) {
	tr := newTestOutbound(t)

	req := boxSystemOneRequest(nil, "noul")

	out, err := tr.TransformRequest(context.Background(), req)
	require.NoError(t, err)
	require.Empty(t, out.Headers.Get(headerDangerZoneImages),
		"纯文本请求不应开启图片实验开关")
}

func TestTransformRequestBodyCarriesAssets(t *testing.T) {
	tr := newTestOutbound(t)

	req := boxSystemOneRequest(map[string]llm.SystemOneAsset{
		"img1": {Type: "image", Format: "png", DataBase64: "QUJD"},
	}, "box")

	out, err := tr.TransformRequest(context.Background(), req)
	require.NoError(t, err)

	var payload struct {
		Assets map[string]struct {
			Type       string `json:"type"`
			Format     string `json:"format"`
			DataBase64 string `json:"data_base64"`
		} `json:"assets"`
		Questions map[string]struct {
			Type string `json:"type"`
		} `json:"questions"`
	}
	require.NoError(t, json.Unmarshal(out.Body, &payload))
	require.Equal(t, "image", payload.Assets["img1"].Type)
	require.Equal(t, "png", payload.Assets["img1"].Format)
	require.Equal(t, "QUJD", payload.Assets["img1"].DataBase64)
	require.Equal(t, "box", payload.Questions["q"].Type)
}

// 调用方显式设置的 Long-Context 必须原样转发，不能被静默丢弃。
func TestTransformRequestForwardsLongContextHeader(t *testing.T) {
	tr := newTestOutbound(t)

	req := boxSystemOneRequest(nil, "noul")
	// 必须用 Header.Set，它会把键名规范化为 canonical 形式；
	// 直接写字面量 map 会让 Header.Get 找不到。
	rawHeaders := http.Header{}
	rawHeaders.Set(headerDangerZoneLongContext, "true")
	req.RawRequest = &httpclient.Request{Headers: rawHeaders}

	out, err := tr.TransformRequest(context.Background(), req)
	require.NoError(t, err)
	require.Equal(t, "true", out.Headers.Get(headerDangerZoneLongContext))
}

// box 答案要能解析成结构化字段，而不是只落进 Extra。
func TestTransformResponseParsesBoxAnswer(t *testing.T) {
	tr := newTestOutbound(t)

	body := []byte(`{
	  "model": "jev-1.14.0",
	  "answers": {
	    "loc": {"type": "box", "box": {"top": 0.01, "bottom": 1.0, "left": 0.0, "right": 0.34}, "confidence": 0.87},
	    "has_red": {"type": "noul", "noul": 0.99}
	  },
	  "usage": {"input_tokens": 1729, "output_tokens": 20}
	}`)

	resp, err := tr.TransformResponse(context.Background(), &httpclient.Response{
		StatusCode: 200,
		Body:       body,
	})
	require.NoError(t, err)
	require.NotNil(t, resp.SystemOne)

	loc := resp.SystemOne.Answers["loc"]
	require.Equal(t, "box", loc.Type)
	require.NotNil(t, loc.Box, "box 应解析为结构化字段")
	require.InDelta(t, 0.01, loc.Box.Top, 1e-9)
	require.InDelta(t, 1.0, loc.Box.Bottom, 1e-9)
	require.InDelta(t, 0.0, loc.Box.Left, 1e-9)
	require.InDelta(t, 0.34, loc.Box.Right, 1e-9)
	require.NotNil(t, loc.Confidence)
	require.InDelta(t, 0.87, *loc.Confidence, 1e-9)

	require.InDelta(t, 0.99, *resp.SystemOne.Answers["has_red"].Noul, 1e-9)
}

// box 字段不能被重复塞进 Extra，否则响应里会出现两份。
func TestBoxAnswerIsNotDuplicatedInExtra(t *testing.T) {
	var answer llm.SystemOneAnswer
	require.NoError(t, json.Unmarshal(
		[]byte(`{"type":"box","box":{"top":0,"bottom":1,"left":0,"right":0.5},"confidence":0.9}`),
		&answer,
	))
	require.NotNil(t, answer.Box)
	require.NotContains(t, answer.Extra, "box")
}

// 往返序列化要保持 box 原样。
func TestBoxAnswerRoundTrip(t *testing.T) {
	original := `{"type":"box","box":{"top":0,"bottom":1,"left":0,"right":0.5},"confidence":0.9}`

	var answer llm.SystemOneAnswer
	require.NoError(t, json.Unmarshal([]byte(original), &answer))

	out, err := json.Marshal(answer)
	require.NoError(t, err)
	require.JSONEq(t, original, string(out))
}

// pass-through 开启时请求体由 orchestrator 原样透传，assets 自然保留；
// 但 pass-through 可被渠道关闭，此时必须由 transformer 自己序列化 assets。
// 本测试锁定后一条路径，避免 assets 在关闭 pass-through 时静默丢失。
func TestTransformRequestBodyCarriesAssetsWithoutPassThrough(t *testing.T) {
	tr := newTestOutbound(t)

	llmReq := &llm.Request{
		Model: "jev-latest",
		SystemOne: &llm.SystemOneRequest{
			State: "Image {img1}",
			Assets: map[string]llm.SystemOneAsset{
				"img1": {Type: "image", Format: "jpeg", DataBase64: "Wg=="},
			},
			Questions: map[string]llm.SystemOneQuestion{
				"loc": {Type: "box", Instructions: "Locate it"},
			},
		},
	}

	out, err := tr.TransformRequest(context.Background(), llmReq)
	require.NoError(t, err)

	var payload struct {
		Assets map[string]struct {
			Format string `json:"format"`
		} `json:"assets"`
	}
	require.NoError(t, json.Unmarshal(out.Body, &payload))
	require.Contains(t, payload.Assets, "img1", "assets 必须出现在出站请求体里")
	require.Equal(t, "jpeg", payload.Assets["img1"].Format)
}
