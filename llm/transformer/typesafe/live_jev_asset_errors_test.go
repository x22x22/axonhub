package typesafe

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/looplj/axonhub/llm"
)

// 这组测试把 TypeSafe 对 assets 的拒绝规则钉在真实上游上。
// 每条都是"反例"：构造一个必然被拒绝的负载，断言状态码与错误语义。
// 这些规则官方有文档但实现细节（错误码、消息、粒度）只能实测得到。

// TestLiveAssetsTypeMustBeImage type 可省略，但给了就必须是 "image"。
func TestLiveAssetsTypeMustBeImage(t *testing.T) {
	tr := liveOutbound(t)
	img := buildSolidPNG(512, [3]byte{255, 255, 255}, false)

	req := &llm.Request{
		Model: "jev-latest",
		SystemOne: &llm.SystemOneRequest{
			State:  "Image {img1}",
			Assets: map[string]llm.SystemOneAsset{"img1": {Type: "video", Format: "png", DataBase64: img}},
			Questions: map[string]llm.SystemOneQuestion{
				"q": {Type: "noul", Instructions: "Is {img1} white?"},
			},
		},
	}

	status, body := sendLive(t, tr, req)
	require.Equal(t, 400, status, "type=video 应被拒绝，实际 %d: %s", status, body)
	require.Contains(t, strings.ToLower(string(body)), "image request")
}

// TestLiveAssetsMissingFormat format 与 data_base64 搭配时必填。
func TestLiveAssetsMissingFormat(t *testing.T) {
	tr := liveOutbound(t)
	img := buildSolidPNG(512, [3]byte{255, 255, 255}, false)

	req := &llm.Request{
		Model: "jev-latest",
		SystemOne: &llm.SystemOneRequest{
			State:  "Image {img1}",
			Assets: map[string]llm.SystemOneAsset{"img1": {Type: "image", DataBase64: img}},
			Questions: map[string]llm.SystemOneQuestion{
				"q": {Type: "noul", Instructions: "Is {img1} white?"},
			},
		},
	}

	status, body := sendLive(t, tr, req)
	require.Equal(t, 400, status, "缺 format 应被拒绝，实际 %d: %s", status, body)
	require.Contains(t, string(body), "format must be png, jpg or jpeg",
		"错误消息应明确指出 format 取值，实际: %s", body)
}

// TestLiveAssetsUnsupportedFormat gif 不在允许集合内。
func TestLiveAssetsUnsupportedFormat(t *testing.T) {
	tr := liveOutbound(t)
	img := buildSolidPNG(512, [3]byte{255, 255, 255}, false)

	for _, format := range []string{"gif", "webp", "bmp", "svg"} {
		t.Run(format, func(t *testing.T) {
			req := &llm.Request{
				Model: "jev-latest",
				SystemOne: &llm.SystemOneRequest{
					State:  "Image {img1}",
					Assets: map[string]llm.SystemOneAsset{"img1": {Type: "image", Format: format, DataBase64: img}},
					Questions: map[string]llm.SystemOneQuestion{
						"q": {Type: "noul", Instructions: "Is {img1} white?"},
					},
				},
			}

			status, body := sendLive(t, tr, req)
			require.Equal(t, 400, status, "format=%s 应被拒绝，实际 %d: %s", format, status, body)
		})
	}
}

// TestLiveAssetsEmptyPlaceholderKey 空占位符无法被引用，必须拒绝。
func TestLiveAssetsEmptyPlaceholderKey(t *testing.T) {
	tr := liveOutbound(t)
	img := buildSolidPNG(512, [3]byte{255, 255, 255}, false)

	req := &llm.Request{
		Model: "jev-latest",
		SystemOne: &llm.SystemOneRequest{
			State:  "look at {}",
			Assets: map[string]llm.SystemOneAsset{"": {Type: "image", Format: "png", DataBase64: img}},
			Questions: map[string]llm.SystemOneQuestion{
				"q": {Type: "noul", Instructions: "Is it white?"},
			},
		},
	}

	status, body := sendLive(t, tr, req)
	require.Equal(t, 400, status, "空占位符应被拒绝，实际 %d: %s", status, body)
}

// TestLiveAssetsWhitespaceOnlyKey 仅由空白组成的占位符同样不可用。
func TestLiveAssetsWhitespaceOnlyKey(t *testing.T) {
	tr := liveOutbound(t)
	img := buildSolidPNG(512, [3]byte{255, 255, 255}, false)

	req := &llm.Request{
		Model: "jev-latest",
		SystemOne: &llm.SystemOneRequest{
			State:  "look at {   }",
			Assets: map[string]llm.SystemOneAsset{"   ": {Type: "image", Format: "png", DataBase64: img}},
			Questions: map[string]llm.SystemOneQuestion{
				"q": {Type: "noul", Instructions: "Is it white?"},
			},
		},
	}

	status, body := sendLive(t, tr, req)
	require.Equal(t, 400, status, "纯空白占位符应被拒绝，实际 %d: %s", status, body)
}

// TestLiveAssetsPartialReference 多个 asset 中只引用一部分，未引用的那个会导致整请求被拒。
func TestLiveAssetsPartialReference(t *testing.T) {
	tr := liveOutbound(t)
	img := buildSolidPNG(512, [3]byte{255, 255, 255}, false)

	req := &llm.Request{
		Model: "jev-latest",
		SystemOne: &llm.SystemOneRequest{
			State: "only {used}",
			Assets: map[string]llm.SystemOneAsset{
				"used":   {Type: "image", Format: "png", DataBase64: img},
				"orphan": {Type: "image", Format: "png", DataBase64: img},
			},
			Questions: map[string]llm.SystemOneQuestion{
				"q": {Type: "noul", Instructions: "Is {used} white?"},
			},
		},
	}

	status, body := sendLive(t, tr, req)
	require.Equal(t, 400, status,
		"存在未被引用的 asset 时整请求应被拒，实际 %d: %s", status, body)
}

// TestLiveAssetsAllReferencedPasses 正向对照：每个 asset 都被引用则通过。
// 与上一个测试成对，证明拒绝原因确实是"未引用"而不是别的问题。
func TestLiveAssetsAllReferencedPasses(t *testing.T) {
	tr := liveOutbound(t)
	img := buildSolidPNG(512, [3]byte{255, 255, 255}, false)

	req := &llm.Request{
		Model: "jev-latest",
		SystemOne: &llm.SystemOneRequest{
			State: map[string]any{"first": "a1", "second": "a2"},
			Assets: map[string]llm.SystemOneAsset{
				"a1": {Type: "image", Format: "png", DataBase64: img},
				"a2": {Type: "image", Format: "png", DataBase64: img},
			},
			Questions: map[string]llm.SystemOneQuestion{
				"q": {Type: "noul", Instructions: "Are a1 and a2 both white?"},
			},
		},
	}

	status, body := sendLive(t, tr, req)
	require.Equal(t, 200, status, "全部被引用时应通过，实际 %d: %s", status, body)
}

// TestLiveAssetsCorruptBase64 非法 base64 必须被拒绝，不能当成空图。
func TestLiveAssetsCorruptBase64(t *testing.T) {
	tr := liveOutbound(t)

	req := &llm.Request{
		Model: "jev-latest",
		SystemOne: &llm.SystemOneRequest{
			State:  "Image {img1}",
			Assets: map[string]llm.SystemOneAsset{"img1": {Type: "image", Format: "png", DataBase64: "not-valid-base64!!!"}},
			Questions: map[string]llm.SystemOneQuestion{
				"q": {Type: "noul", Instructions: "Is {img1} white?"},
			},
		},
	}

	status, body := sendLive(t, tr, req)
	require.GreaterOrEqual(t, status, 400,
		"非法 base64 应被拒绝，实际 %d: %s", status, body)
}
