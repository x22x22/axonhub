package llm

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

// 这一组测试锁住核心类型层的契约：assets 与 box 是出站/入站两端共用的结构，
// 任何字段语义变化都会在网关两侧同时失效。

// TestSystemOneRequestAssetsRoundTrip assets 的每个字段都要能往返，
// 包括只带 part 的 multipart 形态。
func TestSystemOneRequestAssetsRoundTrip(t *testing.T) {
	original := SystemOneRequest{
		State: map[string]any{"page": "img1"},
		Assets: map[string]SystemOneAsset{
			"img1": {Type: "image", Format: "png", DataBase64: "QUJD"},
			"img2": {Type: "image", Part: "upload-2"},
		},
		Questions: map[string]SystemOneQuestion{
			"loc": {Type: "box", Instructions: "Locate it in img1"},
		},
	}

	raw, err := json.Marshal(original)
	require.NoError(t, err)

	var back SystemOneRequest
	require.NoError(t, json.Unmarshal(raw, &back))

	require.Equal(t, "image", back.Assets["img1"].Type)
	require.Equal(t, "png", back.Assets["img1"].Format)
	require.Equal(t, "QUJD", back.Assets["img1"].DataBase64)
	require.Equal(t, "upload-2", back.Assets["img2"].Part)
	require.Empty(t, back.Assets["img2"].DataBase64)
	require.Equal(t, "box", back.Questions["loc"].Type)
}

// TestSystemOneRequestOmitsEmptyAssets 空 assets 不应序列化出 "assets":{}，
// 否则上游会把空对象当成"声明了资产却未引用"。
func TestSystemOneRequestOmitsEmptyAssets(t *testing.T) {
	for name, assets := range map[string]map[string]SystemOneAsset{
		"nil":   nil,
		"empty": {},
	} {
		t.Run(name, func(t *testing.T) {
			raw, err := json.Marshal(SystemOneRequest{
				State:     "text",
				Assets:    assets,
				Questions: map[string]SystemOneQuestion{"q": {Type: "noul", Instructions: "?"}},
			})
			require.NoError(t, err)
			require.NotContains(t, string(raw), `"assets"`)
		})
	}
}

// TestSystemOneBoxZeroValuesSurvive 0 是合法边界，不能被 omitempty 吞掉。
// Box 用非指针的 float64 且无 omitempty，正是为了保证这一点。
func TestSystemOneBoxZeroValuesSurvive(t *testing.T) {
	box := SystemOneBox{Top: 0, Bottom: 1, Left: 0, Right: 0.5}
	raw, err := json.Marshal(box)
	require.NoError(t, err)
	require.JSONEq(t, `{"top":0,"bottom":1,"left":0,"right":0.5}`, string(raw))

	var back SystemOneBox
	require.NoError(t, json.Unmarshal(raw, &back))
	require.Zero(t, back.Top, "top=0 必须保留")
	require.Zero(t, back.Left, "left=0 必须保留")
	require.Equal(t, 1.0, back.Bottom)
	require.Equal(t, 0.5, back.Right)
}

// TestSystemOneAnswerBoxNotInExtra box 是已知字段，不能同时出现在 Extra 里，
// 否则响应会带两份 box。
func TestSystemOneAnswerBoxNotInExtra(t *testing.T) {
	var answer SystemOneAnswer
	require.NoError(t, json.Unmarshal([]byte(
		`{"type":"box","box":{"top":0.1,"bottom":0.2,"left":0.3,"right":0.4},"confidence":0.9}`,
	), &answer))

	require.NotNil(t, answer.Box)
	require.NotContains(t, answer.Extra, "box")
	require.NotContains(t, answer.Extra, "confidence")
	require.NotContains(t, answer.Extra, "type")
}

// TestSystemOneAnswerWithoutBoxKeepsNil 非 box 答案的 Box 必须是 nil，
// 让调用方能靠 nil 判断"这道题不是定位题"。
func TestSystemOneAnswerWithoutBoxKeepsNil(t *testing.T) {
	for _, body := range []string{
		`{"type":"noul","noul":0.9}`,
		`{"type":"choice","choice":"a","confidence":0.8}`,
		`{"type":"score","score":1.2,"confidence":0.7}`,
	} {
		var answer SystemOneAnswer
		require.NoError(t, json.Unmarshal([]byte(body), &answer))
		require.Nil(t, answer.Box, "body=%s", body)
	}
}

// TestSystemOneAnswerMalformedBox 畸形 box 必须报错而不是静默给零值。
func TestSystemOneAnswerMalformedBox(t *testing.T) {
	cases := map[string]string{
		"box 是字符串":      `{"type":"box","box":"nope"}`,
		"坐标是字符串":        `{"type":"box","box":{"top":"high"}}`,
		"box 是数组":       `{"type":"box","box":[0,1,0,1]}`,
		"confidence 非法": `{"type":"box","box":{"top":0},"confidence":"high"}`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			var answer SystemOneAnswer
			require.Error(t, json.Unmarshal([]byte(body), &answer),
				"畸形响应应显式失败: %s", body)
		})
	}
}

// TestSystemOneAnswerNumericPrecision 浮点精度不能因 UseNumber 或中间转换而漂移，
// 否则阈值判断（如 confidence > 0.95）会在边界上抖动。
func TestSystemOneAnswerNumericPrecision(t *testing.T) {
	var answer SystemOneAnswer
	require.NoError(t, json.Unmarshal([]byte(
		`{"type":"noul","noul":0.123456789012345,"confidence":0.9999999}`,
	), &answer))

	require.InDelta(t, 0.123456789012345, *answer.Noul, 1e-15)
	require.InDelta(t, 0.9999999, *answer.Confidence, 1e-15)

	raw, err := json.Marshal(answer)
	require.NoError(t, err)

	var back SystemOneAnswer
	require.NoError(t, json.Unmarshal(raw, &back))
	require.InDelta(t, 0.123456789012345, *back.Noul, 1e-15)
}

// TestSystemOneResponseAllFourPrimitives 四种原语共存时各自字段互不污染。
func TestSystemOneResponseAllFourPrimitives(t *testing.T) {
	raw := `{
	  "answers": {
	    "b": {"type":"box","box":{"top":0,"bottom":1,"left":0,"right":0.5},"confidence":0.9},
	    "n": {"type":"noul","noul":0.8},
	    "c": {"type":"choice","choice":"x","confidence":0.7,"probabilities":{"x":0.7,"y":0.3}},
	    "s": {"type":"score","score":1.5,"confidence":0.6,"legend":{"0":"low","1":"high"}}
	  }
	}`

	var resp SystemOneResponse
	require.NoError(t, json.Unmarshal([]byte(raw), &resp))
	require.Len(t, resp.Answers, 4)

	require.NotNil(t, resp.Answers["b"].Box)
	require.Nil(t, resp.Answers["b"].Noul)

	require.NotNil(t, resp.Answers["n"].Noul)
	require.Nil(t, resp.Answers["n"].Box)

	require.Equal(t, "x", resp.Answers["c"].Choice)
	require.NotNil(t, resp.Answers["s"].Score)
}

// TestSystemOneQuestionTypeValues 题型是自由字符串，新题型（含未来新增）
// 不能被硬编码拦截，否则加 box 就要改多处校验。
func TestSystemOneQuestionTypeValues(t *testing.T) {
	for _, qtype := range []string{"noul", "choice", "score", "box", "future_primitive"} {
		req := SystemOneQuestion{Type: qtype, Instructions: "?"}
		raw, err := json.Marshal(req)
		require.NoError(t, err)

		var back SystemOneQuestion
		require.NoError(t, json.Unmarshal(raw, &back))
		require.Equal(t, qtype, back.Type)
	}
}

// TestSystemOneQuestionStructuredInstructions instructions/criteria 允许任意结构，
// 官方的对象型 instructions（问题 + 候选列表）必须原样保留。
func TestSystemOneQuestionStructuredInstructions(t *testing.T) {
	original := `{"type":"noul","instructions":{"question":"Any unlisted vaccine?","list":["A","B"]},"criteria":{"true":"yes","false":"no"}}`

	var q SystemOneQuestion
	require.NoError(t, json.Unmarshal([]byte(original), &q))

	ins, ok := q.Instructions.(map[string]any)
	require.True(t, ok, "对象型 instructions 应保留为 map")
	require.Contains(t, ins, "question")
	require.Len(t, ins["list"].([]any), 2)

	crit, ok := q.Criteria.(map[string]any)
	require.True(t, ok, "对象型 criteria 应保留为 map")
	require.Equal(t, "yes", crit["true"])

	raw, err := json.Marshal(q)
	require.NoError(t, err)
	require.JSONEq(t, original, string(raw))
}

// TestSystemOneQuestionExtraRoundTrip 未知题型字段必须能入站保留、出站还原，
// 否则上游给原语加参数时网关会静默丢弃。
func TestSystemOneQuestionExtraRoundTrip(t *testing.T) {
	original := `{"type":"box","instructions":"locate","future_param":{"mode":"strict"},"flag":true}`

	var q SystemOneQuestion
	require.NoError(t, json.Unmarshal([]byte(original), &q))
	require.Equal(t, "box", q.Type)
	require.Contains(t, q.Extra, "future_param")
	require.Contains(t, q.Extra, "flag")

	raw, err := json.Marshal(q)
	require.NoError(t, err)
	require.JSONEq(t, original, string(raw), "未知字段必须原样往返")
}

// TestSystemOneQuestionExtraNotDuplicated 已知字段不得同时进入 Extra，
// 否则序列化会出现重复键。
func TestSystemOneQuestionExtraNotDuplicated(t *testing.T) {
	var q SystemOneQuestion
	require.NoError(t, json.Unmarshal([]byte(
		`{"type":"choice","instructions":"pick","criteria":{"a":"x"}}`,
	), &q))

	require.Empty(t, q.Extra, "只有未知字段才进 Extra")
}

// TestSystemOneQuestionExtraEmptyStaysNil 没有未知字段时 Extra 应保持 nil，
// 避免序列化路径多做一次 map 合并。
func TestSystemOneQuestionExtraEmptyStaysNil(t *testing.T) {
	var q SystemOneQuestion
	require.NoError(t, json.Unmarshal([]byte(`{"type":"noul","instructions":"?"}`), &q))
	require.Nil(t, q.Extra)
}
