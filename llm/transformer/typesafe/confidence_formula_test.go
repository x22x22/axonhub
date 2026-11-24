package typesafe

import (
	"context"
	"encoding/json"
	"math"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/looplj/axonhub/llm"
	"github.com/looplj/axonhub/llm/httpclient"
)

// 本文件覆盖 TypeSafe 官方文档里两类"看起来能用、实际会静默出错"的知识点：
//
//  1. Confidence 的精确公式（docs/05-confidence.md）。文档给出了 Choice、
//     Score、Noul 三个闭式公式，并强调 confidence 只反映分布集中度、不等于
//     "答对的概率"。AxonHub 原样透传这些数值，所以必须验证上游给的
//     confidence 与按其自身 probabilities 复算的结果一致——否则说明解析过程
//     改动了数值，调用方基于 confidence 的分支就会失真。
//  2. 已知失败模式（docs/12-models.md）。Jev 不会做算术、数数、比较日期、
//     生成文本。这些是"能力边界"，不是 AxonHub 的 bug，但调用方常在这里踩坑。
//     用单测把边界钉住，避免有人把它们当成解析层问题去"修"。

// TestConfidenceChoiceFormulaMatchesProbabilities 验证 Choice 的 confidence
// 公式：confidence = (p_max - 1/n) / (1 - 1/n)。
func TestConfidenceChoiceFormulaMatchesProbabilities(t *testing.T) {
	tr := newTestOutbound(t)

	cases := []struct {
		name string
		body string
		n    int
	}{
		{
			// 官方例子：3 个选项、top=0.67 → confidence 0.5
			name: "三选项top0.67给0.5",
			body: `{"model":"jev-1.14.0","answers":{"q":{"type":"choice","choice":"a","confidence":0.5,
			        "probabilities":{"a":0.67,"b":0.2,"c":0.13}}},"usage":{"input_tokens":1,"output_tokens":1}}`,
			n: 3,
		},
		{
			// 官方例子：2 个选项、top=0.75 → confidence 0.5
			name: "两选项top0.75给0.5",
			body: `{"model":"jev-1.14.0","answers":{"q":{"type":"choice","choice":"a","confidence":0.5,
			        "probabilities":{"a":0.75,"b":0.25}}},"usage":{"input_tokens":1,"output_tokens":1}}`,
			n: 2,
		},
		{
			// 官方例子：10 个选项、top=0.55 → confidence 0.5
			name: "十选项top0.55给0.5",
			body: `{"model":"jev-1.14.0","answers":{"q":{"type":"choice","choice":"a","confidence":0.5,
			        "probabilities":{"a":0.55,"b":0.05,"c":0.05,"d":0.05,"e":0.05,
			                         "f":0.05,"g":0.05,"h":0.05,"i":0.05,"j":0.05}}},
			       "usage":{"input_tokens":1,"output_tokens":1}}`,
			n: 10,
		},
		{
			// 全部概率压在一个选项上 → confidence 1.0
			name: "全押一项给1",
			body: `{"model":"jev-1.14.0","answers":{"q":{"type":"choice","choice":"a","confidence":1.0,
			        "probabilities":{"a":1.0,"b":0.0,"c":0.0}}},"usage":{"input_tokens":1,"output_tokens":1}}`,
			n: 3,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp, err := tr.TransformResponse(context.Background(),
				&httpclient.Response{StatusCode: 200, Body: []byte(tc.body)})
			require.NoError(t, err)

			ans := resp.SystemOne.Answers["q"]
			require.NotNil(t, ans.Confidence)

			sum := 0.0
			maxProb := 0.0
			for key := range ans.Probabilities.(map[string]any) {
				p, ok := ans.Probability(key)
				require.True(t, ok, "缺少键 %s", key)
				sum += p
				if p > maxProb {
					maxProb = p
				}
			}

			// 概率必须归一化，否则公式无意义。
			require.InDelta(t, 1.0, sum, 1e-6, "概率之和应为 1")

			// 官方闭式公式复算。
			n := float64(tc.n)
			want := (maxProb - 1.0/n) / (1.0 - 1.0/n)
			require.InDelta(t, want, *ans.Confidence, 0.02,
				"上游 confidence 应与按 probabilities 复算的公式一致（n=%d, p_max=%v）", tc.n, maxProb)
		})
	}
}

// TestConfidenceScoreFormulaMatchesProbabilities 验证 Score 的 confidence
// 公式，它惩罚"远离最可能等级的"概率质量。
func TestConfidenceScoreFormulaMatchesProbabilities(t *testing.T) {
	tr := newTestOutbound(t)

	cases := []struct {
		name string
		body string
	}{
		{
			// 官方例子：3 级 (0, 0.5, 0.5) → confidence 0.25（两个邻居拉扯）
			name: "三邻居分摊给0.25",
			body: `{"model":"jev-1.14.0","answers":{"q":{"type":"score","score":1.5,"confidence":0.25,
			        "legend":{"0":"a","1":"b","2":"c"},"probabilities":{"0":0.0,"1":0.5,"2":0.5}}},
			       "usage":{"input_tokens":1,"output_tokens":1}}`,
		},
		{
			// 官方例子：3 级 (0.5, 0, 0.5) → confidence 0（两端撕扯，最不确定）
			name: "两端撕扯给0",
			body: `{"model":"jev-1.14.0","answers":{"q":{"type":"score","score":1.0,"confidence":0.0,
			        "legend":{"0":"a","1":"b","2":"c"},"probabilities":{"0":0.5,"1":0.0,"2":0.5}}},
			       "usage":{"input_tokens":1,"output_tokens":1}}`,
		},
		{
			// 官方 worked example：(0, 0.57, 0.43) → m=1, spread=0.43, MAD=2/3 → ≈0.35
			name: "worked_example约0.35",
			body: `{"model":"jev-1.14.0","answers":{"q":{"type":"score","score":1.43,"confidence":0.35,
			        "legend":{"0":"a","1":"b","2":"c"},"probabilities":{"0":0.0,"1":0.57,"2":0.43}}},
			       "usage":{"input_tokens":1,"output_tokens":1}}`,
		},
		{
			// 完全集中 → confidence 1.0
			name: "完全集中给1",
			body: `{"model":"jev-1.14.0","answers":{"q":{"type":"score","score":2.0,"confidence":1.0,
			        "legend":{"0":"a","1":"b","2":"c"},"probabilities":{"0":0.0,"1":0.0,"2":1.0}}},
			       "usage":{"input_tokens":1,"output_tokens":1}}`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp, err := tr.TransformResponse(context.Background(),
				&httpclient.Response{StatusCode: 200, Body: []byte(tc.body)})
			require.NoError(t, err)

			ans := resp.SystemOne.Answers["q"]
			require.NotNil(t, ans.Confidence)

			// 按 legend 的键数确定等级数，再按序号取概率。
			legend := ans.Legend.(map[string]any)
			n := len(legend)

			probs := make([]float64, n)
			sum := 0.0
			for i := 0; i < n; i++ {
				p, ok := ans.Probability(itoa(i))
				require.True(t, ok, "缺少等级 %d", i)
				probs[i] = p
				sum += probs[i]
			}
			require.InDelta(t, 1.0, sum, 1e-6, "概率之和应为 1")

			// m = argmax；用严格大于保证与实现一样取首个最大值。
			m := 0
			for i := 1; i < n; i++ {
				if probs[i] > probs[m] {
					m = i
				}
			}

			spread := 0.0
			for i, p := range probs {
				spread += p * math.Abs(float64(i-m))
			}

			madUnif := 0.0
			for i := 0; i < n; i++ {
				madUnif += math.Abs(float64(i) - float64(n-1)/2.0)
			}
			madUnif /= float64(n)

			want := math.Max(0, 1-spread/madUnif)
			require.InDelta(t, want, *ans.Confidence, 0.02,
				"上游 confidence 应与 Score 闭式公式一致（n=%d, m=%d, spread=%.4f）", n, m, spread)
		})
	}
}

// TestConfidenceNoulDerivedScale 验证文档给 Noul 的换算公式：
// 若要用与 Choice 同尺度的数值，confidence = |2p - 1|。
// Noul 本身不返回 confidence，这个公式只用于理解与自建指标。
func TestConfidenceNoulDerivedScale(t *testing.T) {
	tr := newTestOutbound(t)

	cases := []struct {
		noul        string
		wantDerived float64
	}{
		{"0.5", 0.0},   // 完全不确定
		{"0.0", 1.0},   // 完全确定是"否"
		{"1.0", 1.0},   // 完全确定是"是"
		{"0.98", 0.96}, // 官方喂鸟器样本
		{"0.02", 0.96},
	}

	for _, tc := range cases {
		t.Run("noul_"+tc.noul, func(t *testing.T) {
			body := []byte(`{"model":"jev-1.14.0","answers":{"q":{"type":"noul","noul":` +
				tc.noul + `}},"usage":{"input_tokens":1,"output_tokens":1}}`)

			resp, err := tr.TransformResponse(context.Background(),
				&httpclient.Response{StatusCode: 200, Body: body})
			require.NoError(t, err)

			ans := resp.SystemOne.Answers["q"]
			require.NotNil(t, ans.Noul)
			require.Nil(t, ans.Confidence, "Noul 不返回 confidence")

			derived := math.Abs(2**ans.Noul - 1)
			require.InDelta(t, tc.wantDerived, derived, 1e-9)
		})
	}
}

// TestKnownFailureModeMarksJaggedness 用单测钉住文档列出的能力边界：
// Jev 不做算术、不数数、不生成文本。AxonHub 只负责传输，这些请求会拿到
// 看起来正常但语义不可靠的答案——调用方必须自己在代码里算。
func TestKnownFailureModeMarksJaggedness(t *testing.T) {
	tr := newTestOutbound(t)

	// 构造一个"让模型做算术"的请求，验证 AxonHub 不会自作主张拒绝它：
	// 这类请求语法完全合法，被拒绝反而是 bug。
	req := &llm.Request{
		Model: "jev-latest",
		SystemOne: &llm.SystemOneRequest{
			State: "Cart: 3 apples at $0.50 each, 2 pears at $0.75 each.",
			Questions: map[string]llm.SystemOneQuestion{
				// 文档明确说不要这样问，但协议允许。
				"total_is_under_five": {
					Type:         "noul",
					Instructions: "Is the cart total under $5.00?",
				},
				"item_count": {
					Type:         "choice",
					Instructions: "How many items are in the cart?",
					Criteria:     map[string]any{"one": "1", "two": "2", "three": "3", "more": "4+"},
				},
			},
		},
	}

	out, err := tr.TransformRequest(context.Background(), req)
	require.NoError(t, err, "算术类请求语法合法，AxonHub 不应拦截")

	var sent struct {
		Questions map[string]json.RawMessage `json:"questions"`
	}
	require.NoError(t, json.Unmarshal(out.Body, &sent))
	require.Len(t, sent.Questions, 2)

	// 上游仍会给出答案（可能是错的）。验证解析不崩、且数值落在合法域内。
	body := []byte(`{
	  "model": "jev-1.14.0",
	  "answers": {
	    "total_is_under_five": {"type": "noul", "noul": 0.61},
	    "item_count": {"type": "choice", "choice": "more", "confidence": 0.72,
	                   "probabilities": {"one": 0.01, "two": 0.05, "three": 0.22, "more": 0.72}}
	  },
	  "usage": {"input_tokens": 60, "output_tokens": 12}
	}`)

	resp, err := tr.TransformResponse(context.Background(), &httpclient.Response{StatusCode: 200, Body: body})
	require.NoError(t, err)

	a := resp.SystemOne.Answers
	// 真实答案是 5 件、$3.00，模型给了 "more" —— 正是文档警告的不可靠。
	// 单测记录这个事实：解析层不修正，调用方必须自己算。
	require.Equal(t, "more", a["item_count"].Choice)
	require.InDelta(t, 0.61, *a["total_is_under_five"].Noul, 1e-9)

	// 断言解析出的值仍在合法域内（这是 AxonHub 的职责边界）。
	require.GreaterOrEqual(t, *a["total_is_under_five"].Noul, 0.0)
	require.LessOrEqual(t, *a["total_is_under_five"].Noul, 1.0)
	sum := 0.0
	for _, level := range []string{"one", "two", "three", "more"} {
		p, ok := a["item_count"].Probability(level)
		require.True(t, ok, "缺少键 %s", level)
		sum += p
	}
	require.InDelta(t, 1.0, sum, 1e-6)
}

// TestDistributionShapeDiagnostics 覆盖文档 "Diagnosing low confidence" 那张表：
// 不同的概率形状对应不同的处置方式，AxonHub 必须完整保留分布让调用方能判断。
func TestDistributionShapeDiagnostics(t *testing.T) {
	tr := newTestOutbound(t)

	cases := []struct {
		name string
		body string
		// wantDiagnosis 是按其概率形状应得的诊断结论。
		wantDiagnosis string
	}{
		{
			// 两个选项分掉大部分质量 → 选项边界不清
			name: "两项分摊_边界不清",
			body: `{"model":"jev-1.14.0","answers":{"q":{"type":"choice","choice":"billing","confidence":0.4,
			        "probabilities":{"billing":0.55,"technical":0.45}}},"usage":{"input_tokens":1,"output_tokens":1}}`,
			wantDiagnosis: "overlapping_options",
		},
		{
			// 质量摊到所有选项 → 都不合适或缺证据
			name: "全面摊开_缺证据",
			body: `{"model":"jev-1.14.0","answers":{"q":{"type":"choice","choice":"billing","confidence":0.0,
			        "probabilities":{"billing":0.25,"technical":0.25,"sales":0.25,"account":0.25}}},
			       "usage":{"input_tokens":1,"output_tokens":1}}`,
			wantDiagnosis: "spread_out",
		},
		{
			// 明确命中 → 直接采用
			name: "明确命中",
			body: `{"model":"jev-1.14.0","answers":{"q":{"type":"choice","choice":"billing","confidence":0.93,
			        "probabilities":{"billing":0.95,"technical":0.04,"sales":0.01,"account":0.0}}},
			       "usage":{"input_tokens":1,"output_tokens":1}}`,
			wantDiagnosis: "clear",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp, err := tr.TransformResponse(context.Background(),
				&httpclient.Response{StatusCode: 200, Body: []byte(tc.body)})
			require.NoError(t, err)

			ans := resp.SystemOne.Answers["q"]
			probs := ans.Probabilities.(map[string]any)

			// 取前两名，按官方诊断表的判据。
			var p1, p2 float64
			for key := range probs {
				p, ok := ans.Probability(key)
				require.True(t, ok, "缺少键 %s", key)
				switch {
				case p > p1:
					p2 = p1
					p1 = p
				case p > p2:
					p2 = p
				}
			}

			got := "clear"
			switch {
			case p1+p2 > 0.9 && p1 < 0.7:
				got = "overlapping_options"
			case p1 < 0.4:
				got = "spread_out"
			}

			require.Equal(t, tc.wantDiagnosis, got,
				"p1=%.3f p2=%.3f 应诊断为 %s", p1, p2, tc.wantDiagnosis)
		})
	}
}

// itoa 是 strconv.Itoa 的小包装，避免为本文件再引入一个 import 组。
func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var buf [20]byte
	pos := len(buf)
	for i > 0 {
		pos--
		buf[pos] = byte('0' + i%10)
		i /= 10
	}
	return string(buf[pos:])
}
