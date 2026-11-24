package llm

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestSystemOneAnswerProbability 覆盖便利方法本身的行为：
// 上游真正下发的 json.Number 路径、Go 内构造的 float64 路径、以及各种取不到
// 值的输入都必须返回 ok=false 而不是 0 或 panic。
func TestSystemOneAnswerProbability(t *testing.T) {
	t.Run("json_Number_保留精度", func(t *testing.T) {
		raw := `{"answers":{"q":{"type":"choice","choice":"a",
		        "probabilities":{"a":0.33333333333333337,"b":0.66666666666666663}}}}`

		var resp SystemOneResponse
		require.NoError(t, json.Unmarshal([]byte(raw), &resp))

		ans := resp.Answers["q"]

		a, ok := ans.Probability("a")
		require.True(t, ok)
		// float64 最近的表示与 17 位输入一致；关键是能取到而不是拿指针。
		require.InDelta(t, 0.33333333333333337, a, 1e-17)

		b, ok := ans.Probability("b")
		require.True(t, ok)
		require.InDelta(t, 0.66666666666666663, b, 1e-17)

		// 两个概率应当仍然构成一个分布。
		require.InDelta(t, 1.0, a+b, 1e-15)
	})

	t.Run("Go_构造的_float64", func(t *testing.T) {
		ans := SystemOneAnswer{
			Type:          "choice",
			Choice:        "billing",
			Probabilities: map[string]any{"billing": 0.9, "tech": 0.1},
		}

		v, ok := ans.Probability("billing")
		require.True(t, ok)
		require.InDelta(t, 0.9, v, 1e-12)
	})

	t.Run("缺失键", func(t *testing.T) {
		ans := SystemOneAnswer{Probabilities: map[string]any{"a": json.Number("0.5")}}

		_, ok := ans.Probability("nope")
		require.False(t, ok)
	})

	t.Run("没有_probabilities_的_noul", func(t *testing.T) {
		noul := 0.8
		ans := SystemOneAnswer{Type: "noul", Noul: &noul}

		_, ok := ans.Probability("anything")
		require.False(t, ok)
	})

	t.Run("box_没有_probabilities", func(t *testing.T) {
		conf := 0.92
		ans := SystemOneAnswer{
			Type:       "box",
			Box:        &SystemOneBox{Top: 0, Bottom: 1, Left: 0, Right: 0.34},
			Confidence: &conf,
		}

		_, ok := ans.Probability("mug")
		require.False(t, ok)
	})

	t.Run("非数值概率不会崩", func(t *testing.T) {
		ans := SystemOneAnswer{Probabilities: map[string]any{"a": "not-a-number"}}

		_, ok := ans.Probability("a")
		require.False(t, ok)
	})

	t.Run("非法_json_Number", func(t *testing.T) {
		ans := SystemOneAnswer{Probabilities: map[string]any{"a": json.Number("oops")}}

		_, ok := ans.Probability("a")
		require.False(t, ok)
	})
}

// TestSystemOneAnswerLegendLevels 覆盖 Score legend 的读取：
// 上游下发的是字符串键的对象，方法要按原样给出可排序的等级表。
func TestSystemOneAnswerLegendLevels(t *testing.T) {
	t.Run("正常_legend", func(t *testing.T) {
		raw := `{"answers":{"sev":{"type":"score","score":1.43,"confidence":0.35,
		        "legend":{"0":"low","1":"medium","2":"high"},
		        "probabilities":{"0":0.0,"1":0.57,"2":0.43}}}}`

		var resp SystemOneResponse
		require.NoError(t, json.Unmarshal([]byte(raw), &resp))

		levels, ok := resp.Answers["sev"].LegendLevels()
		require.True(t, ok)
		require.Len(t, levels, 3)
		require.Equal(t, "low", levels["0"])
		require.Equal(t, "medium", levels["1"])
		require.Equal(t, "high", levels["2"])
	})

	t.Run("没有_legend", func(t *testing.T) {
		_, ok := SystemOneAnswer{Type: "noul"}.LegendLevels()
		require.False(t, ok)
	})

	t.Run("legend_含非字符串值", func(t *testing.T) {
		ans := SystemOneAnswer{Legend: map[string]any{"0": 1}}
		_, ok := ans.LegendLevels()
		require.False(t, ok)
	})
}

// TestProbabilityDistributionSumsToOne 用便利方法复算分布，确认按方法的取值
// 路径能把一组概率还原成和为 1 的分布——这是补偿性检查：若某处把 json.Number
// 换成 float64 丢位，这里会先失败。
func TestProbabilityDistributionSumsToOne(t *testing.T) {
	raw := `{"answers":{"q":{"type":"score","score":1.0,"confidence":0.5,
	        "legend":{"0":"a","1":"b","2":"c"},
	        "probabilities":{"0":0.14285714285714285,"1":0.71428571428571430,"2":0.14285714285714285}}}}`

	var resp SystemOneResponse
	require.NoError(t, json.Unmarshal([]byte(raw), &resp))

	ans := resp.Answers["q"]
	sum := 0.0
	for _, key := range []string{"0", "1", "2"} {
		p, ok := ans.Probability(key)
		require.True(t, ok, "缺少等级 %s", key)
		sum += p
	}

	require.InDelta(t, 1.0, sum, 1e-15, "三个 1/7 级概率之和应为 1")
}
