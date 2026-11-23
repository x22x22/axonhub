package typesafe

import (
	"bytes"
	"compress/zlib"
	"context"
	"encoding/base64"
	"encoding/binary"
	"hash/crc32"
	"os"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/looplj/axonhub/llm"
	"github.com/looplj/axonhub/llm/auth"
	"github.com/looplj/axonhub/llm/httpclient"
)

// buildSolidPNG 生成 size×size 纯色 PNG。animated 为 true 时插入 acTL 声明动图。
func buildSolidPNG(size int, rgb [3]byte, animated bool) string {
	var raw bytes.Buffer
	row := append([]byte{0}, bytes.Repeat(rgb[:], size)...)
	for range size {
		raw.Write(row)
	}
	var compressed bytes.Buffer
	zw := zlib.NewWriter(&compressed)
	_, _ = zw.Write(raw.Bytes())
	_ = zw.Close()

	var png bytes.Buffer
	png.Write([]byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'})
	writeChunk := func(tag string, data []byte) {
		var length [4]byte
		binary.BigEndian.PutUint32(length[:], uint32(len(data)))
		png.Write(length[:])
		png.WriteString(tag)
		png.Write(data)
		crc := crc32.NewIEEE()
		_, _ = crc.Write([]byte(tag))
		_, _ = crc.Write(data)
		var sum [4]byte
		binary.BigEndian.PutUint32(sum[:], crc.Sum32())
		png.Write(sum[:])
	}
	ihdr := make([]byte, 13)
	binary.BigEndian.PutUint32(ihdr[0:], uint32(size))
	binary.BigEndian.PutUint32(ihdr[4:], uint32(size))
	ihdr[8] = 8
	ihdr[9] = 2
	writeChunk("IHDR", ihdr)
	if animated {
		actl := make([]byte, 8)
		binary.BigEndian.PutUint32(actl[0:], 2) // 2 frames
		writeChunk("acTL", actl)
	}
	writeChunk("IDAT", compressed.Bytes())
	writeChunk("IEND", nil)
	return base64.StdEncoding.EncodeToString(png.Bytes())
}

func liveOutbound(t *testing.T) *OutboundTransformer {
	t.Helper()
	key := os.Getenv("TYPESAFE_API_KEY")
	if key == "" {
		t.Skip("TYPESAFE_API_KEY 未设置，跳过真实调用")
	}
	tr, err := NewOutboundTransformerWithConfig(&Config{
		BaseURL:        "https://api.typesafe.ai/v1",
		APIKeyProvider: auth.NewStaticKeyProvider(key),
	})
	require.NoError(t, err)
	return tr
}

// sendLive 发真实请求并把 4xx 也作为一种可断言的正常结果返回。
// httpclient.Do 对非 2xx 返回 *httpclient.Error，若直接 require.NoError
// 会把"预期被拒绝"的用例误判为测试失败。
func sendLive(t *testing.T, tr *OutboundTransformer, req *llm.Request) (int, []byte) {
	t.Helper()
	out, err := tr.TransformRequest(context.Background(), req)
	require.NoError(t, err)

	resp, err := httpclient.NewHttpClient().Do(context.Background(), out)
	if err == nil {
		return resp.StatusCode, resp.Body
	}

	var httpErr *httpclient.Error
	require.ErrorAs(t, err, &httpErr, "非 2xx 响应应返回 *httpclient.Error")
	return httpErr.StatusCode, httpErr.Body
}

func imageQuestion(png string, qtype string, instructions string) *llm.Request {
	return &llm.Request{
		Model: "jev-latest",
		SystemOne: &llm.SystemOneRequest{
			State:  "Image {img1}",
			Assets: map[string]llm.SystemOneAsset{"img1": {Type: "image", Format: "png", DataBase64: png}},
			Questions: map[string]llm.SystemOneQuestion{
				"q": {Type: qtype, Instructions: instructions},
			},
		},
	}
}

// TestLiveImageDimensionLimits 钉住官方文档声明的 280~1568 像素边界。
// 这条测试的价值在于：边界一旦随版本漂移，会立刻暴露。
func TestLiveImageDimensionLimits(t *testing.T) {
	tr := liveOutbound(t)

	cases := []struct {
		size     int
		accepted bool
	}{
		{279, false},
		{280, true},
		{281, true},
		{1568, true},
		{1569, false},
	}

	for _, tc := range cases {
		t.Run("size", func(t *testing.T) {
			img := buildSolidPNG(tc.size, [3]byte{255, 255, 255}, false)
			status, body := sendLive(t, tr, imageQuestion(img, "noul", "Is it white?"))
			if tc.accepted {
				require.Equal(t, 200, status, "%d 应被接受，实际 %d: %s", tc.size, status, body)
			} else {
				require.Equal(t, 400, status, "%d 应被拒绝，实际 %d: %s", tc.size, status, body)
			}
		})
	}
}

// TestLiveAnimatedPNGRejected 动图 PNG 必须被拒（文档: Animated PNGs are rejected）。
func TestLiveAnimatedPNGRejected(t *testing.T) {
	tr := liveOutbound(t)

	img := buildSolidPNG(512, [3]byte{255, 255, 255}, true)
	status, body := sendLive(t, tr, imageQuestion(img, "noul", "Is it white?"))
	require.Equal(t, 400, status, "动图应被拒绝: %s", body)
	require.Contains(t, string(body), "Invalid image request")
}

// TestLiveBoxAlwaysReturnsBox 官方明确：box 没有"未找到"答案，
// 目标不存在时仍返回四个边，且置信度偏低。
func TestLiveBoxAlwaysReturnsBox(t *testing.T) {
	tr := liveOutbound(t)

	img := buildSolidPNG(512, [3]byte{255, 255, 255}, false)

	req := &llm.Request{
		Model: "jev-latest",
		SystemOne: &llm.SystemOneRequest{
			State:  "Image {img1}",
			Assets: map[string]llm.SystemOneAsset{"img1": {Type: "image", Format: "png", DataBase64: img}},
			Questions: map[string]llm.SystemOneQuestion{
				// 白图里找紫象：必然不存在
				"ghost": {Type: "box", Instructions: "a purple elephant in {img1}"},
				"check": {Type: "noul", Instructions: "Is there a purple elephant in {img1}?"},
			},
		},
	}

	status, body := sendLive(t, tr, req)
	require.Equal(t, 200, status, "body: %s", body)

	parsed, err := tr.TransformResponse(context.Background(), &httpclient.Response{
		StatusCode: status,
		Body:       body,
	})
	require.NoError(t, err)

	ghost := parsed.SystemOne.Answers["ghost"]
	require.NotNil(t, ghost.Box, "目标不存在也必须返回 box")

	// 坐标应落在合法范围
	require.GreaterOrEqual(t, ghost.Box.Left, 0.0)
	require.LessOrEqual(t, ghost.Box.Right, 1.0)
	require.GreaterOrEqual(t, ghost.Box.Top, 0.0)
	require.LessOrEqual(t, ghost.Box.Bottom, 1.0)

	// noul 应明确否定，且 box 置信度不应很高
	require.Less(t, *parsed.SystemOne.Answers["check"].Noul, 0.5,
		"白图里不应找到紫象")
	t.Logf("不存在的目标: box=%+v confidence=%v noul=%v",
		*ghost.Box, *ghost.Confidence, *parsed.SystemOne.Answers["check"].Noul)
}

// TestLiveUnreferencedAssetRejected 未被任何引用命中的 asset 会被上游拒绝。
// 注意：Question 的 instructions 也算引用点，构造反例时不能在那里提到占位符，
// 否则请求就变成"已引用"了。
func TestLiveUnreferencedAssetRejected(t *testing.T) {
	tr := liveOutbound(t)

	img := buildSolidPNG(512, [3]byte{255, 255, 255}, false)

	req := &llm.Request{
		Model: "jev-latest",
		SystemOne: &llm.SystemOneRequest{
			State:  "no reference anywhere", // 故意不引用
			Assets: map[string]llm.SystemOneAsset{"unused": {Type: "image", Format: "png", DataBase64: img}},
			Questions: map[string]llm.SystemOneQuestion{
				"q": {Type: "noul", Instructions: "Is this text?"},
			},
		},
	}

	status, body := sendLive(t, tr, req)
	t.Logf("未引用 asset: 状态码 %d，响应 %s", status, string(body))
	require.Equal(t, 400, status,
		"未被引用的 asset 应被拒绝，实际 %d: %s", status, body)
	require.Contains(t, string(body), "Invalid image request")
}

// TestLiveFormatDetectedFromBytes format 标签与实际字节不符时按字节检测，
// jpeg 标签 + PNG 内容仍可用（官方: The actual format is detected from the bytes）。
func TestLiveFormatDetectedFromBytes(t *testing.T) {
	tr := liveOutbound(t)

	img := buildSolidPNG(512, [3]byte{255, 255, 255}, false)

	req := &llm.Request{
		Model: "jev-latest",
		SystemOne: &llm.SystemOneRequest{
			State:  "Image {img1}",
			Assets: map[string]llm.SystemOneAsset{"img1": {Type: "image", Format: "jpeg", DataBase64: img}},
			Questions: map[string]llm.SystemOneQuestion{
				"q": {Type: "noul", Instructions: "Is {img1} white?"},
			},
		},
	}

	status, body := sendLive(t, tr, req)
	require.Equal(t, 200, status,
		"format 标签与内容不符时应按字节检测而通过，实际 %d: %s", status, body)
}

// TestLiveBareScalarStateRejectedUpstream 文档声称 state 只接受
// string / object / array，裸 number / boolean / null 会被 422 拒绝。
// 这条测试把声明钉在真实上游上，避免文档漂移。
func TestLiveBareScalarStateRejectedUpstream(t *testing.T) {
	tr := liveOutbound(t)

	rejected := map[string]any{
		"number":  42,
		"boolean": true,
		"float":   3.14,
	}

	for name, state := range rejected {
		t.Run(name, func(t *testing.T) {
			req := &llm.Request{
				Model: "jev-latest",
				SystemOne: &llm.SystemOneRequest{
					State: state,
					Questions: map[string]llm.SystemOneQuestion{
						"q": {Type: "noul", Instructions: "Is this a number?"},
					},
				},
			}

			status, body := sendLive(t, tr, req)
			require.Equal(t, 422, status,
				"裸 %s 型 state 应被 422 拒绝，实际 %d: %s", name, status, body)
		})
	}

	// 正向对照：包一层对象即可通过
	t.Run("object wrapper accepted", func(t *testing.T) {
		req := &llm.Request{
			Model: "jev-latest",
			SystemOne: &llm.SystemOneRequest{
				State: map[string]any{"amount": 42, "currency": "USD"},
				Questions: map[string]llm.SystemOneQuestion{
					"q": {Type: "noul", Instructions: "Is this about money?"},
				},
			},
		}

		status, body := sendLive(t, tr, req)
		require.Equal(t, 200, status, "对象包装的 state 应被接受，实际 %d: %s", status, body)
	})
}
