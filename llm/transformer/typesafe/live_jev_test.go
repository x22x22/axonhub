package typesafe

import (
	"context"
	"encoding/json"
	"os"
	"testing"

	"github.com/looplj/axonhub/llm"

	"github.com/looplj/axonhub/llm/auth"
)

// TestLiveJevEndToEnd 是一条可选的联网回归测试：用真实 TypeSafe Jev API 验证
// 出站请求构造与响应解析的完整链路。
//
// 默认 skip，不参与常规 CI。需要时显式开启：
//
//	TYPESAFE_API_KEY=<key> go test ./transformer/typesafe/ -run TestLiveJevEndToEnd -v
//
// 覆盖点：三种决策原语（noul / choice / score）、Auth 由 httpclient 注入、
// 响应 envelope 探测不误判（TypeSafe 原生响应没有 success 字段）。
func TestLiveJevEndToEnd(t *testing.T) {
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

	llmReq := &llm.Request{
		Model: "jev-latest",
		SystemOne: &llm.SystemOneRequest{
			State: map[string]any{
				"ticket": "Charged twice for one order, account locked, need reply today.",
			},
			Questions: map[string]llm.SystemOneQuestion{
				"urgent": {Type: "noul", Instructions: "Need a reply within the hour?"},
				"team": {
					Type:         "choice",
					Instructions: "Which team handles this?",
					Criteria:     map[string]any{"billing": "payments", "shipping": "deliveries", "security": "accounts"},
				},
				"tone": {
					Type:         "score",
					Instructions: "How upset?",
					Criteria:     []any{"calm", "annoyed", "furious"},
				},
			},
		},
	}

	hreq, err := tr.TransformRequest(context.Background(), llmReq)
	if err != nil {
		t.Fatalf("TransformRequest: %v", err)
	}
	t.Logf("→ 出站 URL: %s %s", hreq.Method, hreq.URL)
	t.Logf("→ 出站 Body: %s", string(hreq.Body))

	// 用项目自己的 httpclient 发送（它会注入 Auth 头）
	resp, err := liveClient().Do(context.Background(), hreq)
	if err != nil {
		t.Fatalf("HTTP 调用失败: %v", err)
	}
	t.Logf("← 状态码: %d", resp.StatusCode)
	t.Logf("← 原始响应: %s", string(resp.Body))

	out, err := tr.TransformResponse(context.Background(), resp)
	if err != nil {
		t.Fatalf("TransformResponse: %v", err)
	}

	b, _ := json.MarshalIndent(out.SystemOne, "", "  ")
	t.Logf("← 转换后答案:\n%s", string(b))

	if out.SystemOne == nil || len(out.SystemOne.Answers) != 3 {
		t.Fatalf("答案数量异常: %+v", out.SystemOne)
	}
	t.Logf("✅ 真实端到端通过，命中模型: %s", out.Model)
}
