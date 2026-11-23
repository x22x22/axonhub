package typesafe

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/looplj/axonhub/llm"
	"github.com/looplj/axonhub/llm/httpclient"
)

// 以下 golden 数据来自 TypeSafe 官方 cookbook 公布的真实响应（jev-1.14.0）：
//   - reading_scanned_documents（疫苗记录：多 Noul + 多 Box + Choice）
//   - feeder_watch（喂鸟器：Noul + Choice + Box + 2×Choice）
// 用真实响应做回归，比手写简化样本更能发现字段语义被破坏。

// TestGoldenFeederWatchTwoBirds 复刻喂鸟器 cookbook 的双鸟帧。
// 验证点：五题混合、box 只框最显著的那只、species 左右各自独立。
func TestGoldenFeederWatchTwoBirds(t *testing.T) {
	tr := newTestOutbound(t)

	body := []byte(`{
	  "model": "jev-1.14.0",
	  "answers": {
	    "bird_present": {"type": "noul", "noul": 0.98},
	    "how_many": {"type": "choice", "choice": "several", "confidence": 1.0,
	                 "probabilities": {"none": 0.0, "one": 0.0, "several": 1.0}},
	    "bird": {"type": "box", "box": {"top": 0.58, "bottom": 0.90, "left": 0.13, "right": 0.42},
	             "confidence": 0.71},
	    "species_left": {"type": "choice", "choice": "cardinal_male", "confidence": 1.0,
	                     "probabilities": {"cardinal_male": 1.0, "downy_woodpecker": 0.0}},
	    "species_right": {"type": "choice", "choice": "downy_woodpecker", "confidence": 1.0,
	                      "probabilities": {"cardinal_male": 0.0, "downy_woodpecker": 1.0}}
	  },
	  "usage": {"input_tokens": 3120, "output_tokens": 68}
	}`)

	resp, err := tr.TransformResponse(context.Background(), &httpclient.Response{StatusCode: 200, Body: body})
	require.NoError(t, err)

	a := resp.SystemOne.Answers
	require.Len(t, a, 5)

	require.InDelta(t, 0.98, *a["bird_present"].Noul, 1e-9)
	require.Equal(t, "several", a["how_many"].Choice)

	bird := a["bird"].Box
	require.NotNil(t, bird)
	// 双鸟帧里 box 只覆盖最显著的一只（cardinal 在左侧）
	require.InDelta(t, 0.13, bird.Left, 1e-9)
	require.InDelta(t, 0.42, bird.Right, 1e-9)
	require.InDelta(t, 0.71, *a["bird"].Confidence, 1e-9)

	require.Equal(t, "cardinal_male", a["species_left"].Choice)
	require.Equal(t, "downy_woodpecker", a["species_right"].Choice)
	require.InDelta(t, 1.0, *a["species_right"].Confidence, 1e-9)
}

// TestGoldenFeederWatchEmptyFrame 空帧是 box 的关键反例：
// bird_present 很低，但 box 仍然返回四个边——调用方必须靠 noul 判断是否采用。
func TestGoldenFeederWatchEmptyFrame(t *testing.T) {
	tr := newTestOutbound(t)

	body := []byte(`{
	  "model": "jev-1.14.0",
	  "answers": {
	    "bird_present": {"type": "noul", "noul": 0.07},
	    "how_many": {"type": "choice", "choice": "none", "confidence": 0.99,
	                 "probabilities": {"none": 0.99, "one": 0.005, "several": 0.005}},
	    "bird": {"type": "box", "box": {"top": 0.47, "bottom": 0.80, "left": 0.04, "right": 0.13},
	             "confidence": 0.14},
	    "species_left": {"type": "choice", "choice": "other", "confidence": 0.38,
	                     "probabilities": {"other": 0.38}},
	    "species_right": {"type": "choice", "choice": "other", "confidence": 0.61,
	                      "probabilities": {"other": 0.61}}
	  },
	  "usage": {"input_tokens": 3100, "output_tokens": 60}
	}`)

	resp, err := tr.TransformResponse(context.Background(), &httpclient.Response{StatusCode: 200, Body: body})
	require.NoError(t, err)

	a := resp.SystemOne.Answers
	require.InDelta(t, 0.07, *a["bird_present"].Noul, 1e-9)
	require.Equal(t, "none", a["how_many"].Choice)

	// 关键断言：目标不存在也返回 box，且 confidence 明显偏低
	require.NotNil(t, a["bird"].Box, "box 无论如何都会返回，调用方需要靠 noul 判断")
	require.Less(t, *a["bird"].Confidence, 0.3, "空帧的 box 置信度应明显偏低")
}

// TestGoldenVaccinationRecord 复刻疫苗记录 cookbook 的 16 个疫苗同名校验。
// 关键点：相似名称（Hepatitis A / Hepatitis A and B combined）必须被区分。
func TestGoldenVaccinationRecord(t *testing.T) {
	tr := newTestOutbound(t)

	// 官方公布的真实数值（截取 6 个有代表性的）
	body := []byte(`{
	  "model": "jev-1.14.0",
	  "answers": {
	    "vac_00": {"type":"noul","noul":0.98},
	    "vac_00|box": {"type":"box","box":{"top":0.33,"bottom":0.39,"left":0.1,"right":0.9},"confidence":0.9},
	    "vac_02": {"type":"noul","noul":0.02},
	    "vac_02|box": {"type":"box","box":{"top":0.36,"bottom":0.42,"left":0.1,"right":0.9},"confidence":0.3},
	    "vac_03": {"type":"noul","noul":0.98},
	    "vac_03|box": {"type":"box","box":{"top":0.48,"bottom":0.54,"left":0.1,"right":0.9},"confidence":0.88},
	    "unlisted": {"type":"noul","noul":0.02}
	  },
	  "usage": {"input_tokens": 4200, "output_tokens": 120}
	}`)

	resp, err := tr.TransformResponse(context.Background(), &httpclient.Response{StatusCode: 200, Body: body})
	require.NoError(t, err)

	a := resp.SystemOne.Answers

	// Hepatitis A 存在
	require.GreaterOrEqual(t, *a["vac_00"].Noul, 0.5)
	// "Hepatitis A and B combined" 不存在——相似名不应误判
	require.Less(t, *a["vac_02"].Noul, 0.5)
	// Typhoid 存在
	require.GreaterOrEqual(t, *a["vac_03"].Noul, 0.5)
	// 没有未列出的疫苗
	require.Less(t, *a["unlisted"].Noul, 0.5)

	// 按 box.top 排序即为页面顺序
	rows := []struct {
		id  string
		top float64
	}{
		{"vac_00", a["vac_00|box"].Box.Top},
		{"vac_02", a["vac_02|box"].Box.Top},
		{"vac_03", a["vac_03|box"].Box.Top},
	}
	require.Less(t, rows[0].top, rows[1].top)
	require.Less(t, rows[1].top, rows[2].top)
}

// TestGoldenVaccinationBuildRequest 复刻"16 个疫苗 = 33 个问题"的请求构造，
// 验证多 question + 多 box 的一体化请求能正确序列化。
func TestGoldenVaccinationBuildRequest(t *testing.T) {
	tr := newTestOutbound(t)

	vaccines := []string{
		"Hepatitis A", "Hepatitis B", "Hepatitis A and B combined", "Typhoid",
	}
	questions := map[string]llm.SystemOneQuestion{
		"unlisted": {Type: "noul", Instructions: map[string]any{
			"question": "Does the table record any vaccine not in this list?",
			"list":     vaccines,
		}},
	}
	for i, v := range vaccines {
		key := formatIndex("vac_", i)
		questions[key] = llm.SystemOneQuestion{
			Type:         "noul",
			Instructions: "Is " + v + " recorded in the table, with exactly this name?",
		}
		questions[key+"|box"] = llm.SystemOneQuestion{
			Type:         "box",
			Instructions: "the whole " + v + " row of the table",
		}
	}

	req := &llm.Request{
		Model: "jev-latest",
		SystemOne: &llm.SystemOneRequest{
			State:     map[string]any{"page": "img1"},
			Assets:    map[string]llm.SystemOneAsset{"img1": {Type: "image", Format: "png", DataBase64: "AA"}},
			Questions: questions,
		},
	}

	out, err := tr.TransformRequest(context.Background(), req)
	require.NoError(t, err)
	require.Equal(t, "true", out.Headers.Get(headerDangerZoneImages))

	payload := decodeOutboundBody(t, out.Body)
	qs := payload["questions"].(map[string]any)
	require.Len(t, qs, 9, "4 个疫苗各 2 题 + 1 个 unlisted = 9 题")

	// 对象形式的 instructions 要保持结构
	unlisted := qs["unlisted"].(map[string]any)["instructions"].(map[string]any)
	require.Len(t, unlisted["list"].([]any), 4)
}

func formatIndex(prefix string, i int) string {
	if i < 10 {
		return prefix + "0" + string(rune('0'+i))
	}
	return prefix + string(rune('0'+i/10)) + string(rune('0'+i%10))
}

// TestSystemOneSerializationAssetsAndBox 覆盖 llm 包层面的类型编解码，
// 保证 assets 与 box 在 llm.Request / llm.Response 上都能安全往返。
func TestSystemOneSerializationAssetsAndBox(t *testing.T) {
	req := llm.SystemOneRequest{
		State: map[string]any{"photo": "p1"},
		Assets: map[string]llm.SystemOneAsset{
			"p1": {Type: "image", Format: "jpg", DataBase64: "QQ=="},
		},
		Questions: map[string]llm.SystemOneQuestion{
			"b": {Type: "box", Instructions: "the mug"},
		},
	}
	raw, err := json.Marshal(req)
	require.NoError(t, err)

	var back llm.SystemOneRequest
	require.NoError(t, json.Unmarshal(raw, &back))
	require.Equal(t, "jpg", back.Assets["p1"].Format)
	require.Equal(t, "box", back.Questions["b"].Type)

	answer := llm.SystemOneAnswer{
		Type: "box",
		Box:  &llm.SystemOneBox{Top: 0, Bottom: 1, Left: 0, Right: 1},
	}
	conf := 0.5
	answer.Confidence = &conf
	rawAns, err := json.Marshal(answer)
	require.NoError(t, err)
	require.JSONEq(t, `{"type":"box","box":{"top":0,"bottom":1,"left":0,"right":1},"confidence":0.5}`, string(rawAns))
}
