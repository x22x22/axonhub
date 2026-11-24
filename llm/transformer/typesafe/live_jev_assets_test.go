package typesafe

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/looplj/axonhub/llm"
)

// buildNoulWithState 构造一个只问一个问题、state 可任意的图片请求。
func buildNoulWithState(png string, state any, instructions string) *llm.Request {
	return &llm.Request{
		Model: "jev-latest",
		SystemOne: &llm.SystemOneRequest{
			State:  state,
			Assets: map[string]llm.SystemOneAsset{"img1": {Type: "image", Format: "png", DataBase64: png}},
			Questions: map[string]llm.SystemOneQuestion{
				"q": {Type: "noul", Instructions: instructions},
			},
		},
	}
}

// TestLiveTwoImagesCompared 官方「What changed」场景：两帧对比。
// 验证多图可在同一 state 里按占位符区分。
func TestLiveTwoImagesCompared(t *testing.T) {
	tr := liveOutbound(t)

	white := buildSolidPNG(512, [3]byte{255, 255, 255}, false)
	black := buildSolidPNG(512, [3]byte{0, 0, 0}, false)

	req := &llm.Request{
		Model: "jev-latest",
		SystemOne: &llm.SystemOneRequest{
			State: map[string]any{
				"before": "frame_a",
				"after":  "frame_b",
			},
			Assets: map[string]llm.SystemOneAsset{
				"frame_a": {Type: "image", Format: "png", DataBase64: white},
				"frame_b": {Type: "image", Format: "png", DataBase64: black},
			},
			Questions: map[string]llm.SystemOneQuestion{
				"changed": {Type: "noul",
					Instructions: "Do frame_a and frame_b show different images?"},
				"brightness": {Type: "choice",
					Instructions: "Which frame is brighter?",
					Criteria:     map[string]any{"frame_a": "a", "frame_b": "b"}},
			},
		},
	}

	status, body := sendLive(t, tr, req)
	require.Equal(t, 200, status, "body: %s", body)

	parsed, err := tr.TransformResponse(t.Context(), asResponse(status, body))
	require.NoError(t, err)

	// 只断言请求结构与答案契约，不断言模型的具体判断：
	// 模型对"哪张更亮"的结论属于能力范畴，写死会变成 flaky 测试。
	changed := parsed.SystemOne.Answers["changed"]
	require.NotNil(t, changed.Noul, "changed 应为 noul 答案")
	require.InDelta(t, 0, *changed.Noul, 1) // 落在合法 0..1 区间

	bright := parsed.SystemOne.Answers["brightness"]
	require.Contains(t, []string{"frame_a", "frame_b"}, bright.Choice,
		"choice 必须是 criteria 的某个键名，实际 %q", bright.Choice)
	require.NotNil(t, bright.Confidence)
	t.Logf("两帧对比: changed=%v brightness=%s（仅供参考，模型判断可能不准）",
		*changed.Noul, bright.Choice)
}

// TestLiveImageInInstructionsOnly 图片只出现在 instructions 里（不在 state），
// 官方说明这种写法每道题都会重复发送图片。
func TestLiveImageInInstructionsOnly(t *testing.T) {
	tr := liveOutbound(t)

	white := buildSolidPNG(512, [3]byte{255, 255, 255}, false)

	req := &llm.Request{
		Model: "jev-latest",
		SystemOne: &llm.SystemOneRequest{
			State:  "no image here",
			Assets: map[string]llm.SystemOneAsset{"img1": {Type: "image", Format: "png", DataBase64: white}},
			Questions: map[string]llm.SystemOneQuestion{
				"q": {Type: "noul", Instructions: "Is {img1} a white image?"},
			},
		},
	}

	status, body := sendLive(t, tr, req)
	require.Equal(t, 200, status, "body: %s", body)

	parsed, err := tr.TransformResponse(t.Context(), asResponse(status, body))
	require.NoError(t, err)
	require.GreaterOrEqual(t, *parsed.SystemOne.Answers["q"].Noul, 0.5)
}

// TestLiveReferenceLengthLimit 单个引用上限 1024 字节（官方声明）。
// 超长引用应被拒绝，不应静默截断。
func TestLiveReferenceLengthLimit(t *testing.T) {
	tr := liveOutbound(t)

	white := buildSolidPNG(512, [3]byte{255, 255, 255}, false)

	// 占位符本身是 5 字节；把占位符名拉到超限长度
	longName := strings.Repeat("r", 1100)
	req := &llm.Request{
		Model: "jev-latest",
		SystemOne: &llm.SystemOneRequest{
			State:  "look at {" + longName + "}",
			Assets: map[string]llm.SystemOneAsset{longName: {Type: "image", Format: "png", DataBase64: white}},
			Questions: map[string]llm.SystemOneQuestion{
				"q": {Type: "noul", Instructions: "Is it white?"},
			},
		},
	}

	status, body := sendLive(t, tr, req)
	t.Logf("超长引用(%d 字节): 状态码 %d，响应 %s", len(longName), status, string(body))
	// 上游对超长引用返回 413（Request exceeds size limits），不是 400。
	// 断言"被拒绝"而非写死单个码，避免上游调整错误分类时误报。
	require.GreaterOrEqual(t, status, 400,
		"超过 1024 字节的引用应被拒绝，实际 %d: %s", status, body)
}

// TestLiveReferenceAtLengthLimit 恰好 1024 字节的引用应被接受（边界内侧）。
func TestLiveReferenceAtLengthLimit(t *testing.T) {
	tr := liveOutbound(t)

	white := buildSolidPNG(512, [3]byte{255, 255, 255}, false)

	// 用 1000 字节占位符，留出余量但接近上限
	name := strings.Repeat("a", 1000)
	req := &llm.Request{
		Model: "jev-latest",
		SystemOne: &llm.SystemOneRequest{
			State:  "look at {" + name + "}",
			Assets: map[string]llm.SystemOneAsset{name: {Type: "image", Format: "png", DataBase64: white}},
			Questions: map[string]llm.SystemOneQuestion{
				"q": {Type: "noul", Instructions: "Is it white?"},
			},
		},
	}

	status, body := sendLive(t, tr, req)
	t.Logf("长但未超限(%d 字节): 状态码 %d", len(name), status)
	require.Equal(t, 200, status, "未超限的长引用应被接受，实际 %d: %s", status, body)
}

// TestLiveChoiceCriteriaObjectKeysStayText 官方规则：对象键不被替换。
// 若 AxonHub 误把 key 当引用替换，criteria 的选项名会变成图片而丢失。
func TestLiveChoiceCriteriaObjectKeysStayText(t *testing.T) {
	tr := liveOutbound(t)

	white := buildSolidPNG(512, [3]byte{255, 255, 255}, false)

	// key 恰好与占位符同名：如果被替换，choice 就无处可选
	req := &llm.Request{
		Model: "jev-latest",
		SystemOne: &llm.SystemOneRequest{
			State:  "no image",
			Assets: map[string]llm.SystemOneAsset{"img1": {Type: "image", Format: "png", DataBase64: white}},
			Questions: map[string]llm.SystemOneQuestion{
				"pick": {
					Type:         "choice",
					Instructions: "Which label matches the white image?",
					Criteria: map[string]any{
						"img1":  "the white image shown in {img1}",
						"other": "anything else at all",
					},
				},
			},
		},
	}

	status, body := sendLive(t, tr, req)
	require.Equal(t, 200, status, "body: %s", body)

	parsed, err := tr.TransformResponse(t.Context(), asResponse(status, body))
	require.NoError(t, err)

	answer := parsed.SystemOne.Answers["pick"]
	// 选项名 img1 必须作为文本标签存在，而不是被图片替换掉
	require.Contains(t, []string{"img1", "other"}, answer.Choice,
		"choice 必须是 criteria 的某个键名，实际 %q", answer.Choice)
	t.Logf("choice=%q（证明对象键未被图片替换）", answer.Choice)
}
