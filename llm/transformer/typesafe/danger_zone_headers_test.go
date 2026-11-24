package typesafe

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/looplj/axonhub/llm"
	"github.com/looplj/axonhub/llm/httpclient"
)

// rawHeaders 构造一个 canonical 化的 header 集合，模拟真实入站请求。
func rawHeaders(kv map[string]string) http.Header {
	h := http.Header{}
	for k, v := range kv {
		h.Set(k, v)
	}
	return h
}

func oneQuestionRequest(qtype string, assets map[string]llm.SystemOneAsset) *llm.Request {
	return &llm.Request{
		Model: "jev-latest",
		SystemOne: &llm.SystemOneRequest{
			State:  "state",
			Assets: assets,
			Questions: map[string]llm.SystemOneQuestion{
				"q": {Type: qtype, Instructions: "do it"},
			},
		},
	}
}

// TestImagesHeaderBoxTypeCaseInsensitive 题型大小写不应影响开关判断，
// 否则 "Box" 会被当成未知类型从而漏开 header。
func TestImagesHeaderBoxTypeCaseInsensitive(t *testing.T) {
	tr := newTestOutbound(t)

	for _, qtype := range []string{"box", "Box", "BOX", "  box  "} {
		t.Run(strings.TrimSpace(qtype), func(t *testing.T) {
			out, err := tr.TransformRequest(context.Background(), oneQuestionRequest(qtype, nil))
			require.NoError(t, err)
			require.Equal(t, "true", out.Headers.Get(headerDangerZoneImages),
				"题型 %q 应触发图片开关", qtype)
		})
	}
}

// TestImagesHeaderMixedQuestions 只要有一个 box，整个请求就要开开关。
func TestImagesHeaderMixedQuestions(t *testing.T) {
	tr := newTestOutbound(t)

	req := &llm.Request{
		Model: "jev-latest",
		SystemOne: &llm.SystemOneRequest{
			State: "state",
			Questions: map[string]llm.SystemOneQuestion{
				"a": {Type: "noul", Instructions: "?"},
				"b": {Type: "choice", Instructions: "?"},
				"c": {Type: "box", Instructions: "locate"},
			},
		},
	}
	out, err := tr.TransformRequest(context.Background(), req)
	require.NoError(t, err)
	require.Equal(t, "true", out.Headers.Get(headerDangerZoneImages))
}

// TestImagesHeaderAllTextPrimitivesOmitHeader 纯文本三原语不得开启实验开关，
// 避免把正常请求送进实验路径。
func TestImagesHeaderAllTextPrimitivesOmitHeader(t *testing.T) {
	tr := newTestOutbound(t)

	for _, qtype := range []string{"noul", "choice", "score"} {
		t.Run(qtype, func(t *testing.T) {
			out, err := tr.TransformRequest(context.Background(), oneQuestionRequest(qtype, nil))
			require.NoError(t, err)
			require.Empty(t, out.Headers.Get(headerDangerZoneImages))
		})
	}
}

// TestImagesHeaderEmptyAssetsDoesNotEnable 空 assets 不算使用图片。
func TestImagesHeaderEmptyAssetsDoesNotEnable(t *testing.T) {
	tr := newTestOutbound(t)

	out, err := tr.TransformRequest(context.Background(), oneQuestionRequest("noul", map[string]llm.SystemOneAsset{}))
	require.NoError(t, err)
	require.Empty(t, out.Headers.Get(headerDangerZoneImages),
		"空 assets 不应触发图片开关")
}

// TestCallerExplicitImagesHeaderWins 调用方显式设置优先，
// 让它可以主动关闭或使用非 true 的取值。
func TestCallerExplicitImagesHeaderWins(t *testing.T) {
	tr := newTestOutbound(t)

	req := oneQuestionRequest("noul", map[string]llm.SystemOneAsset{
		"i": {Type: "image", Format: "png", DataBase64: "AA"},
	})
	req.RawRequest = &httpclient.Request{
		Headers: rawHeaders(map[string]string{headerDangerZoneImages: "false"}),
	}

	out, err := tr.TransformRequest(context.Background(), req)
	require.NoError(t, err)
	require.Equal(t, "false", out.Headers.Get(headerDangerZoneImages),
		"调用方显式值应覆盖自动推断")
}

// TestLongContextOnlyForwardedWhenSet 未设置时不得凭空补出 Long-Context，
// 否则所有请求都会走实验性的长上下文路径。
func TestLongContextOnlyForwardedWhenSet(t *testing.T) {
	tr := newTestOutbound(t)

	out, err := tr.TransformRequest(context.Background(), oneQuestionRequest("noul", nil))
	require.NoError(t, err)
	require.Empty(t, out.Headers.Get(headerDangerZoneLongContext))
}

// TestLongContextBlankValueIgnored 空白值视为未设置，避免发出空 header。
func TestLongContextBlankValueIgnored(t *testing.T) {
	tr := newTestOutbound(t)

	req := oneQuestionRequest("noul", nil)
	req.RawRequest = &httpclient.Request{
		Headers: rawHeaders(map[string]string{headerDangerZoneLongContext: "   "}),
	}

	out, err := tr.TransformRequest(context.Background(), req)
	require.NoError(t, err)
	require.Empty(t, out.Headers.Get(headerDangerZoneLongContext))
}

// TestLongContextForwardedVerbatim 值原样透传，网关不改写。
func TestLongContextForwardedVerbatim(t *testing.T) {
	tr := newTestOutbound(t)

	req := oneQuestionRequest("noul", nil)
	req.RawRequest = &httpclient.Request{
		Headers: rawHeaders(map[string]string{headerDangerZoneLongContext: "true"}),
	}

	out, err := tr.TransformRequest(context.Background(), req)
	require.NoError(t, err)
	require.Equal(t, "true", out.Headers.Get(headerDangerZoneLongContext))
}

// TestApplyDangerZoneHeadersNilSafety 空输入不得 panic。
// 这个函数在出站主路径上，防御性很重要。
func TestApplyDangerZoneHeadersNilSafety(t *testing.T) {
	t.Run("nil headers", func(t *testing.T) {
		require.NotPanics(t, func() {
			applyDangerZoneHeaders(nil, oneQuestionRequest("box", nil))
		})
	})
	t.Run("nil request", func(t *testing.T) {
		h := http.Header{}
		require.NotPanics(t, func() { applyDangerZoneHeaders(h, nil) })
		require.Empty(t, h.Get(headerDangerZoneImages))
	})
	t.Run("nil systemone", func(t *testing.T) {
		h := http.Header{}
		require.NotPanics(t, func() {
			applyDangerZoneHeaders(h, &llm.Request{Model: "jev-latest"})
		})
	})
}

// TestOutboundKeepsRequiredBaseHeaders 注入 danger-zone 头时不能覆盖基础头。
func TestOutboundKeepsRequiredBaseHeaders(t *testing.T) {
	tr := newTestOutbound(t)

	out, err := tr.TransformRequest(context.Background(), oneQuestionRequest("box", nil))
	require.NoError(t, err)
	require.Equal(t, "application/json", out.Headers.Get("Content-Type"))
	require.Equal(t, "application/json", out.Headers.Get("Accept"))
	require.Equal(t, "true", out.Headers.Get(headerDangerZoneImages))
}
