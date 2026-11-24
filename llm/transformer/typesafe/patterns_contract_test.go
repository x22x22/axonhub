package typesafe

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/looplj/axonhub/llm"
	"github.com/looplj/axonhub/llm/httpclient"
)

// 本文件覆盖 TypeSafe 官方文档 "Patterns" 一章的五个组合模式
// （https://github.com/typesafe-docs/typesafeai docs/07-patterns.md）。
//
// 这些模式不是新的原语，而是把 Noul/Choice/Score 组合成真实工作流的既有写法。
// 对 AxonHub 来说，它们的价值在于：一次请求里能承载多少个问题、哪些字段必须
// 完整透传、以及下游代码要靠哪些数值做分支。任何一处被截断或类型走样，
// 依赖这些模式的调用方就会静默走错分支——这比报错更难发现。
//
// 响应中的数值取自官方文档公布的 jev-1.14.0 录制结果，因此既验证解析，也
// 顺带守住"这些字段名确实是上游真实使用的字段名"。

// probabilityOf 取出 probabilities 里某个键的概率值。
//
// SystemOneAnswer.UnmarshalJSON 用 UseNumber() 解析，所以嵌套对象里的数值是
// json.Number 而不是 float64——这是刻意的：概率不经过 float64 往返可以避免
// 精度损失。测试里断言数值前必须先按真实类型取。
func probabilityOf(t *testing.T, probabilities any, key string) float64 {
	t.Helper()

	probs, ok := probabilities.(map[string]any)
	require.True(t, ok, "probabilities 必须是对象，实际 %T", probabilities)

	raw, ok := probs[key]
	require.True(t, ok, "%q 必须在 probabilities 里，实际键：%v", key, probs)

	f, err := strconv.ParseFloat(fmt.Sprint(raw), 64)
	require.NoError(t, err, "概率必须是可解析的数值，实际 %T = %v", raw, raw)

	return f
}

// TestPatternSpeculativeFanOut 复刻 Speculative fan-out：
// 一条工单一次性问 5 个问题（其中 3 个只对部分分支有意义），代码再挑用哪个。
func TestPatternSpeculativeFanOut(t *testing.T) {
	tr := newTestOutbound(t)

	// 官方文档记录的 5 题并行响应。
	body := []byte(`{
	  "model": "jev-1.14.0",
	  "answers": {
	    "category": {"type": "choice", "choice": "bug_report", "confidence": 0.81,
	                 "probabilities": {"bug_report": 0.86, "billing": 0.06, "feature_request": 0.05, "account": 0.03}},
	    "bug_severity": {"type": "score", "score": 1.0, "confidence": 1.0,
	                     "legend": {"0": "Cosmetic; no impact to functionality",
	                                "1": "Broken or degraded feature; workaround exists",
	                                "2": "Blocking issue; no workaround exists"},
	                     "probabilities": {"0": 0.0, "1": 1.0, "2": 0.0}},
	    "has_reproducible_steps": {"type": "noul", "noul": 0.88},
	    "refund_requested": {"type": "noul", "noul": 0.02},
	    "frustration": {"type": "score", "score": 1.0, "confidence": 1.0,
	                    "legend": {"0": "Calm, matter-of-fact",
	                               "1": "Frustrated but civil",
	                               "2": "Very angry"},
	                    "probabilities": {"0": 0.0, "1": 1.0, "2": 0.0}}
	  },
	  "usage": {"input_tokens": 420, "output_tokens": 71}
	}`)

	resp, err := tr.TransformResponse(context.Background(), &httpclient.Response{StatusCode: 200, Body: body})
	require.NoError(t, err)

	answers := resp.SystemOne.Answers
	require.Len(t, answers, 5, "五个问题必须全部保留，不能因为分支未用到就丢弃")

	// 分支依据：category
	require.Equal(t, "bug_report", answers["category"].Choice)

	// 代码里的复合条件：severity > 1.5 且 repro > 0.6
	severity := answers["bug_severity"]
	require.NotNil(t, severity.Score)
	require.InDelta(t, 1.0, *severity.Score, 1e-9)
	require.InDelta(t, 0.88, *answers["has_reproducible_steps"].Noul, 1e-9)
	require.InDelta(t, 0.02, *answers["refund_requested"].Noul, 1e-9)

	// legend 的键是字符串序号，下游要按 0..n-1 顺序读，不能依赖 map 顺序。
	legend, ok := severity.Legend.(map[string]any)
	require.True(t, ok, "legend 必须是可读取的对象，实际 %T", severity.Legend)
	require.Len(t, legend, 3)
	require.Equal(t, "Cosmetic; no impact to functionality", legend["0"])
	require.Equal(t, "Blocking issue; no workaround exists", legend["2"])

	// 分支本身：若这条工单被判成 bug_report，frustration 仍然生效。
	require.InDelta(t, 1.0, *answers["frustration"].Score, 1e-9)
}

// TestPatternCompanionNoulGuardsChoice 复刻 Companion Noul：
// Choice 永远会挑一个选项（哪怕没一个合适），所以配一个 Noul 问"到底有没有
// 符合的"，代码先看 Noul 再看 Choice。
func TestPatternCompanionNoulGuardsChoice(t *testing.T) {
	tr := newTestOutbound(t)

	cases := []struct {
		name string
		body string
		// wantChoiceOverridden 表示这条响应里 Choice 的答案属于"噪声"：
		// companion Noul 很低，代码必须忽略 Choice。
		wantIgnoreChoice bool
	}{
		{
			// 桌灯坏了，和三款产品（升降桌/椅/显示器支架）都不相关，
			// 但 Choice 仍以 0.96 挑了 desk_pro。Noul 只有 0.02。
			name: "灯不属任何产品_Choice仍是高概率噪声",
			body: `{
			  "model": "jev-1.14.0",
			  "answers": {
			    "product": {"type": "choice", "choice": "desk_pro", "confidence": 0.94,
			                "probabilities": {"desk_pro": 0.96, "chair_lite": 0.03, "monitor_arm": 0.01}},
			    "about_our_product": {"type": "noul", "noul": 0.02}
			  },
			  "usage": {"input_tokens": 300, "output_tokens": 30}
			}`,
			wantIgnoreChoice: true,
		},
		{
			name: "明确属于某产品_Noul与Choice都高",
			body: `{
			  "model": "jev-1.14.0",
			  "answers": {
			    "product": {"type": "choice", "choice": "desk_pro", "confidence": 1.0,
			                "probabilities": {"desk_pro": 1.0, "chair_lite": 0.0, "monitor_arm": 0.0}},
			    "about_our_product": {"type": "noul", "noul": 0.98}
			  },
			  "usage": {"input_tokens": 300, "output_tokens": 30}
			}`,
			wantIgnoreChoice: false,
		},
		{
			// 招聘者来信：Choice 以 0.81 挑 desk_pro，Noul 0.02 —— 依旧是噪声。
			name: "招聘者来信_Choice低概率噪声",
			body: `{
			  "model": "jev-1.14.0",
			  "answers": {
			    "product": {"type": "choice", "choice": "desk_pro", "confidence": 0.72,
			                "probabilities": {"desk_pro": 0.81, "chair_lite": 0.11, "monitor_arm": 0.08}},
			    "about_our_product": {"type": "noul", "noul": 0.02}
			  },
			  "usage": {"input_tokens": 300, "output_tokens": 30}
			}`,
			wantIgnoreChoice: true,
		},
	}

	const yesAbove = 0.7
	const noBelow = 0.3
	const productSure = 0.7

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp, err := tr.TransformResponse(context.Background(),
				&httpclient.Response{StatusCode: 200, Body: []byte(tc.body)})
			require.NoError(t, err)

			a := resp.SystemOne.Answers
			product := a["product"]
			aboutOurs := *a["about_our_product"].Noul

			// 记录下游真正会读的两个量：Noul 值和"被选中项"的概率。
			chosenProb := probabilityOf(t, product.Probabilities, product.Choice)

			ignored := aboutOurs < noBelow
			require.Equal(t, tc.wantIgnoreChoice, ignored,
				"按官方模式，Noul=%v 时应 ignoreChoice=%v", aboutOurs, tc.wantIgnoreChoice)

			if !ignored {
				// 只有 Noul 明确为是、且被选项概率够高时才真的下发。
				require.Greater(t, aboutOurs, yesAbove)
				require.Greater(t, chosenProb, productSure)
			}
		})
	}
}

// TestPatternRoutingByConfidence 复刻 Routing by confidence：
// Score 的 score 说明"多严重"，confidence 说明"这个判断有多确定"，
// 低 confidence 必须走人工，而不是照 score 直接升级。
func TestPatternRoutingByConfidence(t *testing.T) {
	tr := newTestOutbound(t)

	cases := []struct {
		name       string
		body       string
		wantTriage bool // confidence < 0.5 → 人工
		wantRoute  string
	}{
		{
			// 官方录制：笔记丢失，可能是自己删的 → score 3.19 但是 confidence 0.34。
			// 若只看 score 会去叫 on-call，正确做法是转人工。
			name: "笔记丢失_高分低confidence需人工",
			body: `{
			  "model": "jev-1.14.0",
			  "answers": {
			    "severity": {"type": "score", "score": 3.19, "confidence": 0.34,
			                 "legend": {"0": "Cosmetic", "1": "Minor", "2": "Major with a workaround",
			                            "3": "Major with no workaround", "4": "Critical"},
			                 "probabilities": {"0": 0.01, "1": 0.14, "2": 0.07, "3": 0.19, "4": 0.59}}
			  },
			  "usage": {"input_tokens": 210, "output_tokens": 40}
			}`,
			wantTriage: true,
		},
		{
			name: "错别字_确定在最低级",
			body: `{
			  "model": "jev-1.14.0",
			  "answers": {
			    "severity": {"type": "score", "score": 0.0, "confidence": 1.0,
			                 "legend": {"0": "Cosmetic", "1": "Minor", "2": "Major with a workaround",
			                            "3": "Major with no workaround", "4": "Critical"},
			                 "probabilities": {"0": 1.0, "1": 0.0, "2": 0.0, "3": 0.0, "4": 0.0}}
			  },
			  "usage": {"input_tokens": 180, "output_tokens": 40}
			}`,
			wantRoute: "backlog",
		},
		{
			name: "PDF导出失败但有替代路径_进本迭代",
			body: `{
			  "model": "jev-1.14.0",
			  "answers": {
			    "severity": {"type": "score", "score": 2.0, "confidence": 1.0,
			                 "legend": {"0": "Cosmetic", "1": "Minor", "2": "Major with a workaround",
			                            "3": "Major with no workaround", "4": "Critical"},
			                 "probabilities": {"0": 0.0, "1": 0.0, "2": 1.0, "3": 0.0, "4": 0.0}}
			  },
			  "usage": {"input_tokens": 200, "output_tokens": 40}
			}`,
			wantRoute: "sprint",
		},
		{
			name: "结账全线500_高confidence直接叫oncall",
			body: `{
			  "model": "jev-1.14.0",
			  "answers": {
			    "severity": {"type": "score", "score": 3.36, "confidence": 0.7,
			                 "legend": {"0": "Cosmetic", "1": "Minor", "2": "Major with a workaround",
			                            "3": "Major with no workaround", "4": "Critical"},
			                 "probabilities": {"0": 0.0, "1": 0.0, "2": 0.0, "3": 0.64, "4": 0.36}}
			  },
			  "usage": {"input_tokens": 205, "output_tokens": 40}
			}`,
			wantRoute: "oncall",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp, err := tr.TransformResponse(context.Background(),
				&httpclient.Response{StatusCode: 200, Body: []byte(tc.body)})
			require.NoError(t, err)

			sev := resp.SystemOne.Answers["severity"]
			require.NotNil(t, sev.Score)
			require.NotNil(t, sev.Confidence, "Score 必须带 confidence，缺了就无法判断是否该动作")

			got := "triage"
			if *sev.Confidence >= 0.5 {
				switch s := *sev.Score; {
				case s < 1.5:
					got = "backlog"
				case s < 2.5:
					got = "sprint"
				default:
					got = "oncall"
				}
			}

			if tc.wantTriage {
				require.Equal(t, "triage", got)
				// 关键断言：若忽略 confidence，仅按 score 3.19 会误判为 oncall。
				require.GreaterOrEqual(t, *sev.Score, 2.5,
					"该样本的 score 落在会触发 on-call 的区间，正是要靠 confidence 拦下")
			} else {
				require.Equal(t, tc.wantRoute, got)
			}
		})
	}
}

// TestPatternRoutingByProbability 复刻 Routing by probability：
// 同样一个 Choice，风险不同的动作要求不同的概率阈值。
func TestPatternRoutingByProbability(t *testing.T) {
	tr := newTestOutbound(t)

	body := []byte(`{
	  "model": "jev-1.14.0",
	  "answers": {
	    "intent": {"type": "choice", "choice": "approve_transfer", "confidence": 0.92,
	               "probabilities": {"check_balance": 0.03, "approve_transfer": 0.95, "other": 0.02}}
	  },
	  "usage": {"input_tokens": 120, "output_tokens": 18}
	}`)

	resp, err := tr.TransformResponse(context.Background(), &httpclient.Response{StatusCode: 200, Body: body})
	require.NoError(t, err)

	intent := resp.SystemOne.Answers["intent"]
	require.Equal(t, "approve_transfer", intent.Choice)

	likelihood := probabilityOf(t, intent.Probabilities, "approve_transfer")
	require.InDelta(t, 0.95, likelihood, 1e-9)

	// 低风险动作 0.7 即可；高风险动作要 0.9 以上才进入确认流程。
	const lowStakes = 0.7
	const highStakes = 0.9
	require.Greater(t, likelihood, lowStakes)
	require.Greater(t, likelihood, highStakes,
		"approve_transfer 属于高风险动作，必须越过更高的阈值")

	// 关键：confidence 对 Choice 只是 top probability 的变换，阈值应放在概率上。
	// 0.03/0.02 这种"其他选项也很低"的形状不能替代对目标选项概率的检查。
	require.Less(t, probabilityOf(t, intent.Probabilities, "check_balance"), 0.5)
}

// TestPatternCompositeScoring 复刻 Composite scoring：
// 四个维度各自 Score，代码按不同岗位权重加权，模型不参与算术。
func TestPatternCompositeScoring(t *testing.T) {
	tr := newTestOutbound(t)

	body := []byte(`{
	  "model": "jev-1.14.0",
	  "answers": {
	    "python_depth": {"type": "score", "score": 4.0, "confidence": 1.0,
	      "legend": {"0": "No Python experience mentioned", "1": "Mentioned but no detail",
	                 "2": "Used in projects, some specifics", "3": "Primary language, multiple projects",
	                 "4": "Deep expertise: architecture, performance, libraries"},
	      "probabilities": {"0": 0.0, "1": 0.0, "2": 0.0, "3": 0.0, "4": 1.0}},
	    "team_leadership": {"type": "score", "score": 2.0, "confidence": 1.0,
	      "legend": {"0": "No management experience mentioned", "1": "Informal mentorship or tech lead role",
	                 "2": "Led a small team or project", "3": "Managed a team with direct reports",
	                 "4": "Managed multiple teams or an engineering org"},
	      "probabilities": {"0": 0.0, "1": 0.0, "2": 1.0, "3": 0.0, "4": 0.0}},
	    "system_design": {"type": "score", "score": 3.0, "confidence": 1.0,
	      "legend": {"0": "No architecture work mentioned", "1": "Contributed to design discussions",
	                 "2": "Designed components of a larger system", "3": "Owned architecture of a significant system",
	                 "4": "Designed systems at scale across multiple domains"},
	      "probabilities": {"0": 0.0, "1": 0.0, "2": 0.0, "3": 1.0, "4": 0.0}},
	    "generalist": {"type": "score", "score": 1.0, "confidence": 1.0,
	      "legend": {"0": "Only one domain or role mentioned", "1": "Some variety but within a narrow field",
	                 "2": "Worked across a few different areas or tech stacks",
	                 "3": "Regularly moved between domains, wore many hats",
	                 "4": "Track record of ramping up in unfamiliar areas and delivering"},
	      "probabilities": {"0": 0.0, "1": 1.0, "2": 0.0, "3": 0.0, "4": 0.0}}
	  },
	  "usage": {"input_tokens": 1500, "output_tokens": 60}
	}`)

	resp, err := tr.TransformResponse(context.Background(), &httpclient.Response{StatusCode: 200, Body: body})
	require.NoError(t, err)

	a := resp.SystemOne.Answers
	dims := map[string]float64{}
	for _, name := range []string{"python_depth", "team_leadership", "system_design", "generalist"} {
		require.NotNil(t, a[name].Score, "%s 缺少 score", name)
		dims[name] = *a[name].Score / 4.0 // 官方做法：先归一化到 0..1
	}

	require.InDelta(t, 1.0, dims["python_depth"], 1e-9)
	require.InDelta(t, 0.5, dims["team_leadership"], 1e-9)
	require.InDelta(t, 0.75, dims["system_design"], 1e-9)
	require.InDelta(t, 0.25, dims["generalist"], 1e-9)

	// 官方文档给出的两组权重。
	icScore := 0.40*dims["python_depth"] + 0.10*dims["team_leadership"] +
		0.40*dims["system_design"] + 0.10*dims["generalist"]
	emScore := 0.15*dims["python_depth"] + 0.40*dims["team_leadership"] +
		0.20*dims["system_design"] + 0.25*dims["generalist"]

	require.InDelta(t, 0.40*1.0+0.10*0.5+0.40*0.75+0.10*0.25, icScore, 1e-9)
	require.InDelta(t, 0.15*1.0+0.40*0.5+0.20*0.75+0.25*0.25, emScore, 1e-9)

	// 这份简历是强 IC、弱管理：IC 综合分必须高于 EM 综合分。
	require.Greater(t, icScore, emScore,
		"同一组维度在不同权重下应得出不同结论，这正是 composite scoring 的意义")
}

// TestPatternMixedPrimitivesSingleRequest 覆盖最容易出错的一点：
// 四种原语混在同一个请求里时，解析结果不能互相污染。
func TestPatternMixedPrimitivesSingleRequest(t *testing.T) {
	tr := newTestOutbound(t)

	body := []byte(`{
	  "model": "jev-1.14.0",
	  "answers": {
	    "mug_visible": {"type": "noul", "noul": 0.93},
	    "mug": {"type": "box", "box": {"top": 0.25, "bottom": 0.82, "left": 0.44, "right": 0.84}, "confidence": 0.92},
	    "carrying": {"type": "choice", "choice": "bag", "confidence": 0.88,
	                 "probabilities": {"package": 0.10, "bag": 0.88, "nothing": 0.02}},
	    "how_damaged": {"type": "score", "score": 2.0, "confidence": 0.9,
	                    "legend": {"0": "none", "1": "minor", "2": "severe", "3": "total"},
	                    "probabilities": {"0": 0.05, "1": 0.05, "2": 0.9, "3": 0.0}}
	  },
	  "usage": {"input_tokens": 290, "output_tokens": 21}
	}`)

	resp, err := tr.TransformResponse(context.Background(), &httpclient.Response{StatusCode: 200, Body: body})
	require.NoError(t, err)

	a := resp.SystemOne.Answers
	require.Len(t, a, 4)

	// 每种原语只应填自己那个字段，其余为 nil，否则下游的 *float64 判断会出错。
	require.InDelta(t, 0.93, *a["mug_visible"].Noul, 1e-9)
	require.Nil(t, a["mug_visible"].Score)
	require.Nil(t, a["mug_visible"].Box)

	require.NotNil(t, a["mug"].Box)
	require.Nil(t, a["mug"].Noul)
	require.Nil(t, a["mug"].Score)

	require.Equal(t, "bag", a["carrying"].Choice)
	require.Nil(t, a["carrying"].Box)

	require.InDelta(t, 2.0, *a["how_damaged"].Score, 1e-9)
	require.Nil(t, a["how_damaged"].Box)

	// type 字段回显的是原语名，不是问题名——下游 switch 依赖这个值。
	wantTypes := map[string]string{
		"mug_visible": "noul",
		"mug":         "box",
		"carrying":    "choice",
		"how_damaged": "score",
	}
	for name, wantType := range wantTypes {
		require.Equal(t, wantType, a[name].Type, "%s 的 type 应为 %q", name, wantType)
	}
}

// TestPatternChoiceConfidenceOnlyTopProbability 守住官方明确指出的一个易错点：
// Choice 的 confidence 只看最大概率，不区分第二、第三名如何分布。
func TestPatternChoiceConfidenceOnlyTopProbability(t *testing.T) {
	tr := newTestOutbound(t)

	// 两组概率 top 相同(0.6)，其余分布不同，但官方记录的 confidence 都是 0.4。
	shapes := []struct {
		name string
		body string
	}{
		{
			name: "次名与三名不同",
			body: `{"model":"jev-1.14.0","answers":{"q":{"type":"choice","choice":"a","confidence":0.4,
			        "probabilities":{"a":0.6,"b":0.3,"c":0.1}}},"usage":{"input_tokens":10,"output_tokens":1}}`,
		},
		{
			name: "次名与三名相同",
			body: `{"model":"jev-1.14.0","answers":{"q":{"type":"choice","choice":"a","confidence":0.4,
			        "probabilities":{"a":0.6,"b":0.2,"c":0.2}}},"usage":{"input_tokens":10,"output_tokens":1}}`,
		},
	}

	var confidences []float64
	for _, s := range shapes {
		t.Run(s.name, func(t *testing.T) {
			resp, err := tr.TransformResponse(context.Background(),
				&httpclient.Response{StatusCode: 200, Body: []byte(s.body)})
			require.NoError(t, err)

			ans := resp.SystemOne.Answers["q"]
			require.NotNil(t, ans.Confidence)
			confidences = append(confidences, *ans.Confidence)

			require.InDelta(t, 0.6, probabilityOf(t, ans.Probabilities, "a"), 1e-9)
		})
	}

	require.Len(t, confidences, 2)
	require.InDelta(t, confidences[0], confidences[1], 1e-9,
		"confidence 只由最大概率决定，两种分布形状应得出同一个值")
}

// TestPatternNoulHasNoConfidence 守住 Noul 与其他原语的结构差异：
// Noul 不返回 confidence（不确定性已由 noul 值本身承载）。
func TestPatternNoulHasNoConfidence(t *testing.T) {
	tr := newTestOutbound(t)

	body := []byte(`{
	  "model": "jev-1.14.0",
	  "answers": {"expects_reply": {"type": "noul", "noul": 0.44}},
	  "usage": {"input_tokens": 88, "output_tokens": 6}
	}`)

	resp, err := tr.TransformResponse(context.Background(), &httpclient.Response{StatusCode: 200, Body: body})
	require.NoError(t, err)

	ans := resp.SystemOne.Answers["expects_reply"]
	require.InDelta(t, 0.44, *ans.Noul, 1e-9)
	require.Nil(t, ans.Confidence, "Noul 不应携带 confidence")
	require.Nil(t, ans.Probabilities, "Noul 不应携带 probabilities")

	// 官方的三档处置：<0.3 归档，>0.7 处理，中间带单独走一路。
	const yesAbove, noBelow = 0.7, 0.3
	require.True(t, *ans.Noul > noBelow && *ans.Noul < yesAbove,
		"0.44 落在中间带，必须有自己的分支而不是四舍五入")
}

// TestPatternScoreBorderSumBeatsConfidence 复刻文档里的"两个相邻等级分摊"陷阱：
// 相邻等级之间的分摊几乎不降低 confidence，决策若卡在边界上必须直接加概率。
func TestPatternScoreBorderSumBeatsConfidence(t *testing.T) {
	tr := newTestOutbound(t)

	// 官方例子：(0, 0.57, 0.43) → m=1, spread=0.43, MAD_unif=2/3, confidence≈0.35
	body := []byte(`{
	  "model": "jev-1.14.0",
	  "answers": {
	    "severity": {"type": "score", "score": 1.43, "confidence": 0.35,
	                 "legend": {"0": "low", "1": "medium", "2": "high"},
	                 "probabilities": {"0": 0.0, "1": 0.57, "2": 0.43}}
	  },
	  "usage": {"input_tokens": 100, "output_tokens": 12}
	}`)

	resp, err := tr.TransformResponse(context.Background(), &httpclient.Response{StatusCode: 200, Body: body})
	require.NoError(t, err)

	sev := resp.SystemOne.Answers["severity"]
	require.InDelta(t, 0.35, *sev.Confidence, 0.01)

	high := probabilityOf(t, sev.Probabilities, "2")

	// 44% 的概率落在最高等级，但 confidence 只有 0.35 —— 正是因为这个分摊
	// 发生在相邻等级。如果决策关心"是否达到 high"，必须直接读 0.43。
	require.Greater(t, high, 0.4)
	require.Less(t, *sev.Confidence, 0.5,
		"confidence 低到会被误判为'不确定'，但边界概率已经明确")

	// 官方给的下界公式可复算：MAD 归一化后的 spread 检查。
	// 这里只验证数值关系自洽：confidence = 1 - spread/MAD_unif
	spread := 0.43 // 只有 |2-1| 这一项非零
	madUnif := 2.0 / 3.0
	require.InDelta(t, 1-spread/madUnif, *sev.Confidence, 0.01)
}

// TestPatternLongContextFanOut 覆盖长上下文场景：
// 一次请求带上较长的 state 与多个问题，验证请求体与响应都不被截断。
func TestPatternLongContextFanOut(t *testing.T) {
	tr := newTestOutbound(t)

	// 构造约 40KB 的长文本（模拟长工单线程），并用官方建议的
	// TypeSafe-Danger-Zone-Long-Context 头显式开启。
	longState := make([]byte, 0, 40*1024)
	for i := 0; i < 400; i++ {
		longState = append(longState, []byte("message segment with support context and customer history. ")...)
	}

	rawReq := &llm.Request{
		Model: "jev-latest",
		RawRequest: &httpclient.Request{
			Headers: map[string][]string{
				// 故意用原始大小写，验证 header 查找不依赖规范化
				"TypeSafe-Danger-Zone-Long-Context": {"true"},
			},
		},
		SystemOne: &llm.SystemOneRequest{
			State: string(longState),
			Questions: map[string]llm.SystemOneQuestion{
				"category": {Type: "choice", Instructions: "What is this thread about?",
					Criteria: map[string]any{"billing": "payments", "technical": "bugs"}},
				"urgent": {Type: "noul", Instructions: "Does the thread convey urgency?"},
				"severity": {Type: "score", Instructions: "How severe?",
					Criteria: []any{"low", "medium", "high"}},
			},
		},
	}

	out, err := tr.TransformRequest(context.Background(), rawReq)
	require.NoError(t, err)

	require.Equal(t, "true", lookupHeaderCanonicalInsensitive(out.Headers, "TypeSafe-Danger-Zone-Long-Context"),
		"长上下文头必须透传给上游")

	// 长 state 必须原样发出，不能被截断。
	var sent struct {
		State     string                     `json:"state"`
		Questions map[string]json.RawMessage `json:"questions"`
	}
	require.NoError(t, json.Unmarshal(out.Body, &sent))
	require.Equal(t, string(longState), sent.State, "长 state 不应被改动")
	require.Len(t, sent.Questions, 3, "长上下文请求里的多个问题必须都保留")
}
