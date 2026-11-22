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

	"github.com/looplj/axonhub/llm"
	"github.com/looplj/axonhub/llm/auth"
	"github.com/looplj/axonhub/llm/httpclient"
)

// buildStripePNG 生成 size×size 的图片：左侧 stripeWidth 列红色，其余白色。
// 手工拼 PNG 是为了不让测试依赖图像库。
func buildStripePNG(size, stripeWidth int) string {
	var raw bytes.Buffer
	for range size {
		raw.WriteByte(0) // filter type
		for x := range size {
			if x < stripeWidth {
				raw.Write([]byte{220, 30, 30})
			} else {
				raw.Write([]byte{255, 255, 255})
			}
		}
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
	ihdr[8] = 8 // bit depth
	ihdr[9] = 2 // color type: truecolour
	writeChunk("IHDR", ihdr)
	writeChunk("IDAT", compressed.Bytes())
	writeChunk("IEND", nil)

	return base64.StdEncoding.EncodeToString(png.Bytes())
}

// TestLiveJevBoxAndAssets 用真实 TypeSafe API 验证 box 题型与 assets 资产。
// 这是本地单测证明不了的部分：上游是否真的接受该负载、box 是否真的定位准确。
//
//	TYPESAFE_API_KEY=<key> go test ./transformer/typesafe/ -run TestLiveJevBoxAndAssets -v
func TestLiveJevBoxAndAssets(t *testing.T) {
	key := os.Getenv("TYPESAFE_API_KEY")
	if key == "" {
		t.Skip("TYPESAFE_API_KEY 未设置，跳过真实调用")
	}

	tr, err := NewOutboundTransformerWithConfig(&Config{
		BaseURL:        "https://api.typesafe.ai/v1",
		APIKeyProvider: auth.NewStaticKeyProvider(key),
	})
	if err != nil {
		t.Fatalf("transformer: %v", err)
	}

	const (
		size        = 512
		stripeWidth = 170 // 期望 box.right ≈ 170/512 ≈ 0.332
	)
	stripe := buildStripePNG(size, stripeWidth)

	llmReq := &llm.Request{
		Model: "jev-latest",
		SystemOne: &llm.SystemOneRequest{
			State: "Image {img1}",
			Assets: map[string]llm.SystemOneAsset{
				"img1": {Type: "image", Format: "png", DataBase64: stripe},
			},
			Questions: map[string]llm.SystemOneQuestion{
				"loc":     {Type: "box", Instructions: "Locate the red region in {img1}"},
				"has_red": {Type: "noul", Instructions: "Does {img1} contain red?"},
			},
		},
	}

	out, err := tr.TransformRequest(context.Background(), llmReq)
	if err != nil {
		t.Fatalf("TransformRequest: %v", err)
	}
	if got := out.Headers.Get(headerDangerZoneImages); got != "true" {
		t.Fatalf("图片开关应自动开启，实际 %q", got)
	}

	resp, err := httpclient.NewHttpClient().Do(context.Background(), out)
	if err != nil {
		t.Fatalf("HTTP: %v", err)
	}
	t.Logf("← 状态码 %d", resp.StatusCode)
	t.Logf("← 原始响应 %s", string(resp.Body))
	if resp.StatusCode != 200 {
		t.Fatalf("期望 200，实际 %d: %s", resp.StatusCode, string(resp.Body))
	}

	parsed, err := tr.TransformResponse(context.Background(), resp)
	if err != nil {
		t.Fatalf("TransformResponse: %v", err)
	}

	loc := parsed.SystemOne.Answers["loc"]
	if loc.Box == nil {
		t.Fatalf("box 答案为 nil: %+v", loc)
	}
	t.Logf("box: left=%.3f right=%.3f top=%.3f bottom=%.3f confidence=%.3f",
		loc.Box.Left, loc.Box.Right, loc.Box.Top, loc.Box.Bottom, *loc.Confidence)

	if loc.Box.Left > 0.15 {
		t.Errorf("左边界偏差过大: %.3f，期望接近 0", loc.Box.Left)
	}
	expectedRight := float64(stripeWidth) / float64(size)
	if loc.Box.Right < expectedRight-0.15 || loc.Box.Right > expectedRight+0.15 {
		t.Errorf("右边界偏差过大: %.3f，期望接近 %.3f", loc.Box.Right, expectedRight)
	}

	if noul := parsed.SystemOne.Answers["has_red"]; noul.Noul == nil || *noul.Noul < 0.5 {
		t.Errorf("应判定图片含红色，实际 %+v", noul)
	}
}
