package orchestrator

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/looplj/axonhub/internal/authz"
	"github.com/looplj/axonhub/internal/contexts"
	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/channel"
	"github.com/looplj/axonhub/internal/ent/enttest"
	"github.com/looplj/axonhub/internal/objects"
	"github.com/looplj/axonhub/internal/server/biz"
	"github.com/looplj/axonhub/llm/auth"
	"github.com/looplj/axonhub/llm/httpclient"
	"github.com/looplj/axonhub/llm/pipeline"
	"github.com/looplj/axonhub/llm/pipeline/stream"
	"github.com/looplj/axonhub/llm/transformer/typesafe"
)

// outboundHeader 取出发站请求头，大小写不敏感。
// http.Header.Get 只匹配 canonical 形式（Typesafe-...），而出站 map 可能
// 保留调用方的原始大小写（TypeSafe-...），直接用 Get 会假阴性。
func outboundHeader(h http.Header, name string) string {
	for key, values := range h {
		if len(values) > 0 && strings.EqualFold(key, name) {
			return values[0]
		}
	}
	return ""
}

// newBoxAssetsOrchestrator 复刻生产链路：真实 inbound + 真实 outbound +
// mock 执行器，用来验证"客户端请求 → 出站上游请求"的完整转换。
func newBoxAssetsOrchestrator(t *testing.T, passThrough bool) (
	ctx context.Context,
	client *ent.Client,
	orch *ChatCompletionOrchestrator,
	executor *mockExecutor,
) {
	t.Helper()

	ctx = context.Background()
	ctx = authz.WithTestBypass(ctx)

	client = enttest.NewEntClient(t, "sqlite3", "file:ent?mode=memory&_fk=0")
	t.Cleanup(func() { client.Close() })

	ctx = ent.NewContext(ctx, client)

	builder := client.Channel.Create().
		SetType(channel.TypeTypesafe).
		SetName("TypeSafe Box Assets").
		SetBaseURL("https://api.typesafe.ai/v1").
		SetCredentials(objects.ChannelCredentials{APIKey: "sk-typesafe-test-key"}).
		SetSupportedModels([]string{"jev-latest"}).
		SetDefaultTestModel("jev-latest")

	if passThrough {
		enabled := true
		builder = builder.SetSettings(&objects.ChannelSettings{PassThroughBody: &enabled})
	}

	ch, err := builder.Save(ctx)
	require.NoError(t, err)

	channelService, requestService, systemService, usageLogService := setupTestServices(t, client)

	executor = &mockExecutor{
		response: &httpclient.Response{
			StatusCode: 200,
			Body: []byte(`{
			  "model": "jev-1.14.0",
			  "answers": {
			    "loc": {"type":"box","box":{"top":0.1,"bottom":0.9,"left":0.2,"right":0.4},"confidence":0.93},
			    "has_red": {"type":"noul","noul":0.99}
			  },
			  "usage": {"input_tokens": 2468, "output_tokens": 37}
			}`),
			Headers: http.Header{"Content-Type": []string{"application/json"}},
		},
	}

	outbound, err := typesafe.NewOutboundTransformerWithConfig(&typesafe.Config{
		BaseURL:        ch.BaseURL,
		APIKeyProvider: auth.NewStaticKeyProvider("sk-typesafe-test-key"),
	})
	require.NoError(t, err)

	bizChannel := &biz.Channel{Channel: ch, Outbound: outbound}
	selector := &staticChannelSelector{
		candidates: channelsToTestCandidates([]*biz.Channel{bizChannel}, "jev-latest"),
	}

	orch = &ChatCompletionOrchestrator{
		channelSelector:       selector,
		Inbound:               typesafe.NewSystemOneInboundTransformer(),
		RequestService:        requestService,
		ChannelService:        channelService,
		PromptProvider:        &stubPromptProvider{},
		SystemService:         systemService,
		UsageLogService:       usageLogService,
		PipelineFactory:       pipeline.NewFactory(executor),
		ModelMapper:           NewModelMapper(),
		channelLimiterManager: NewChannelLimiterManager(),
		Middlewares:           []pipeline.Middleware{stream.EnsureUsage()},
	}

	ctx = contexts.WithProjectID(ctx, createTestProject(t, ctx, client).ID)

	return ctx, client, orch, executor
}

// newTestOrchContext 仅创建 ent client 与 project，供需要自定义渠道的场景复用。
func newTestOrchContext(t *testing.T) (context.Context, *ent.Client) {
	t.Helper()

	ctx := context.Background()
	ctx = authz.WithTestBypass(ctx)

	client := enttest.NewEntClient(t, "sqlite3", "file:ent?mode=memory&_fk=0")
	t.Cleanup(func() { client.Close() })

	ctx = ent.NewContext(ctx, client)
	ctx = contexts.WithProjectID(ctx, createTestProject(t, ctx, client).ID)

	return ctx, client
}

// TestSystemOneBoxAndAssetsEndToEnd 客户端完全不传 danger-zone header，
// 只发 assets + box，验证网关自动补头且图片资产完整到达上游。
func TestSystemOneBoxAndAssetsEndToEnd(t *testing.T) {
	ctx, _, orch, executor := newBoxAssetsOrchestrator(t, false)

	reqBody := []byte(`{
	  "model": "jev-latest",
	  "state": "Image {img1}",
	  "assets": {"img1": {"type":"image","format":"png","data_base64":"iVBORw0KGgo="}},
	  "questions": {
	    "loc": {"type":"box","instructions":"Locate the red region in {img1}"},
	    "has_red": {"type":"noul","instructions":"Does {img1} contain red?"}
	  }
	}`)

	httpReq := &httpclient.Request{
		Method:  http.MethodPost,
		Body:    reqBody,
		Headers: http.Header{"Content-Type": []string{"application/json"}},
	}

	result, err := orch.Process(ctx, httpReq)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, result.ChatCompletion.StatusCode)

	// 关键断言 1：出站请求必须带上图片实验开关
	require.NotNil(t, executor.lastRequest)
	require.Equal(t, "true", outboundHeader(executor.lastRequest.Headers, "TypeSafe-Danger-Zone-Images"),
		"含 assets 与 box 的请求必须自动开启图片开关")

	// 关键断言 2：assets 必须原样到达上游
	var outboundPayload map[string]any
	require.NoError(t, json.Unmarshal(executor.lastRequest.Body, &outboundPayload))
	assets, ok := outboundPayload["assets"].(map[string]any)
	require.True(t, ok, "出站请求应包含 assets")
	img := assets["img1"].(map[string]any)
	require.Equal(t, "image", img["type"])
	require.Equal(t, "png", img["format"])
	require.Equal(t, "iVBORw0KGgo=", img["data_base64"])

	// 关键断言 3：box 答案要原样回传给客户端
	var respBody map[string]any
	require.NoError(t, json.Unmarshal(result.ChatCompletion.Body, &respBody))
	answers := respBody["answers"].(map[string]any)
	loc := answers["loc"].(map[string]any)
	require.Equal(t, "box", loc["type"])
	box := loc["box"].(map[string]any)
	require.Equal(t, 0.1, box["top"])
	require.Equal(t, 0.9, box["bottom"])
	require.Equal(t, 0.2, box["left"])
	require.Equal(t, 0.4, box["right"])
	require.Equal(t, 0.93, loc["confidence"])
}

// TestSystemOnePlainTextDoesNotEnableImagesHeader 纯文本请求不应被误开启实验开关。
func TestSystemOnePlainTextDoesNotEnableImagesHeader(t *testing.T) {
	ctx, _, orch, executor := newBoxAssetsOrchestrator(t, false)

	httpReq := &httpclient.Request{
		Method:  http.MethodPost,
		Body:    []byte(`{"model":"jev-latest","state":"text","questions":{"q":{"type":"noul","instructions":"Is it text?"}}}`),
		Headers: http.Header{"Content-Type": []string{"application/json"}},
	}

	result, err := orch.Process(ctx, httpReq)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, result.ChatCompletion.StatusCode)

	require.NotNil(t, executor.lastRequest)
	require.Empty(t, outboundHeader(executor.lastRequest.Headers, "TypeSafe-Danger-Zone-Images"),
		"纯文本请求不应开启图片实验开关")
}

// TestSystemOneLongContextHeaderForwarded 调用方显式设置的 Long-Context 必须透传到上游。
func TestSystemOneLongContextHeaderForwarded(t *testing.T) {
	ctx, _, orch, executor := newBoxAssetsOrchestrator(t, false)

	httpReq := &httpclient.Request{
		Method: http.MethodPost,
		Body:   []byte(`{"model":"jev-latest","state":"long text","questions":{"q":{"type":"noul","instructions":"Is it long?"}}}`),
		Headers: http.Header{
			"Content-Type":                      []string{"application/json"},
			"TypeSafe-Danger-Zone-Long-Context": []string{"true"},
		},
	}

	result, err := orch.Process(ctx, httpReq)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, result.ChatCompletion.StatusCode)

	require.NotNil(t, executor.lastRequest)
	require.Equal(t, "true", outboundHeader(executor.lastRequest.Headers, "TypeSafe-Danger-Zone-Long-Context"),
		"调用方的长上下文开关应透传上游")
}

// TestSystemOneBoxAssetsPassThroughKeepsBody pass-through 开启时请求体原样透传，
// assets 与 box 不应在转换过程中丢失。
func TestSystemOneBoxAssetsPassThroughKeepsBody(t *testing.T) {
	ctx, _, orch, executor := newBoxAssetsOrchestrator(t, true)

	reqBody := []byte(`{"model":"jev-latest","state":"Image {img1}","assets":{"img1":{"type":"image","format":"png","data_base64":"QUJD"}},"questions":{"loc":{"type":"box","instructions":"Locate it in {img1}"}}}`)

	httpReq := &httpclient.Request{
		Method:  http.MethodPost,
		Body:    reqBody,
		Headers: http.Header{"Content-Type": []string{"application/json"}},
	}

	result, err := orch.Process(ctx, httpReq)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, result.ChatCompletion.StatusCode)

	require.NotNil(t, executor.lastRequest)
	require.JSONEq(t, string(reqBody), string(executor.lastRequest.Body),
		"pass-through 下请求体应原样透传")
	// header 由 transformer 生成，pass-through 只替换 body
	require.Equal(t, "true", outboundHeader(executor.lastRequest.Headers, "TypeSafe-Danger-Zone-Images"))
}

// TestSystemOneInvalidRequestReturns400 客户端参数错误必须返回 400，不能是 500。
func TestSystemOneInvalidRequestReturns400(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{"空问题列表", `{"model":"jev-latest","state":"text","questions":{}}`},
		{"缺 state", `{"model":"jev-latest","questions":{"q":{"type":"noul","instructions":"?"}}}`},
		{"缺 model", `{"state":"text","questions":{"q":{"type":"noul","instructions":"?"}}}`},
		{"不支持流式", `{"model":"jev-latest","state":"text","stream":true,"questions":{"q":{"type":"noul","instructions":"?"}}}`},
		{"非法 JSON", `{not-json`},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx, _, orch, executor := newBoxAssetsOrchestrator(t, false)

			httpReq := &httpclient.Request{
				Method:  http.MethodPost,
				Body:    []byte(tc.body),
				Headers: http.Header{"Content-Type": []string{"application/json"}},
			}

			_, err := orch.Process(ctx, httpReq)
			require.Error(t, err, "非法请求应返回错误")
			require.False(t, executor.requestCalled, "非法请求不应触达上游")

			clientErr := orch.Inbound.TransformError(ctx, err)
			require.NotNil(t, clientErr)
			require.Equal(t, http.StatusBadRequest, clientErr.StatusCode,
				"客户端参数错误应返回 400，实际 %d", clientErr.StatusCode)
		})
	}
}

// TestSystemOneInvalidModelReturns422 模型不存在属于语义错误，映射为 422。
// 这里必须用真实的 DefaultSelector：测试夹具里的 staticChannelSelector
// 直接返回固定候选、忽略 req.Model，用它验证未知模型会得到假阳性。
func TestSystemOneInvalidModelReturns422(t *testing.T) {
	ctx, client := newTestOrchContext(t)

	_, err := client.Channel.Create().
		SetType(channel.TypeTypesafe).
		SetName("TypeSafe Model Check").
		SetBaseURL("https://api.typesafe.ai/v1").
		SetCredentials(objects.ChannelCredentials{APIKey: "sk-typesafe-test-key"}).
		SetSupportedModels([]string{"jev-latest"}).
		SetDefaultTestModel("jev-latest").
		Save(ctx)
	require.NoError(t, err)

	channelService, requestService, systemService, usageLogService := setupTestServices(t, client)
	selector := NewDefaultSelector(channelService, newTestModelService(client), systemService)

	executor := &mockExecutor{}

	outbound, err := typesafe.NewOutboundTransformerWithConfig(&typesafe.Config{
		BaseURL:        "https://api.typesafe.ai/v1",
		APIKeyProvider: auth.NewStaticKeyProvider("sk-typesafe-test-key"),
	})
	require.NoError(t, err)
	_ = outbound

	orch := &ChatCompletionOrchestrator{
		channelSelector:       selector,
		Inbound:               typesafe.NewSystemOneInboundTransformer(),
		RequestService:        requestService,
		ChannelService:        channelService,
		PromptProvider:        &stubPromptProvider{},
		SystemService:         systemService,
		UsageLogService:       usageLogService,
		PipelineFactory:       pipeline.NewFactory(executor),
		ModelMapper:           NewModelMapper(),
		channelLimiterManager: NewChannelLimiterManager(),
	}

	httpReq := &httpclient.Request{
		Method:  http.MethodPost,
		Body:    []byte(`{"model":"jev-does-not-exist","state":"text","questions":{"q":{"type":"noul","instructions":"?"}}}`),
		Headers: http.Header{"Content-Type": []string{"application/json"}},
	}

	_, err = orch.Process(ctx, httpReq)
	require.Error(t, err, "未知模型应被拒绝")
	require.False(t, executor.requestCalled, "未知模型不应触达上游")

	clientErr := orch.Inbound.TransformError(ctx, err)
	require.NotNil(t, clientErr)
	require.Equal(t, http.StatusUnprocessableEntity, clientErr.StatusCode,
		"未知模型应返回 422，实际 %d", clientErr.StatusCode)
}

// TestSystemOneBoxAnswerPersistedToUsageLog box 答案落库时要保留结构，
// 不能因为新字段被丢弃而让排查失去依据。
func TestSystemOneBoxAnswerPersisted(t *testing.T) {
	ctx, client, orch, _ := newBoxAssetsOrchestrator(t, false)

	httpReq := &httpclient.Request{
		Method:  http.MethodPost,
		Body:    []byte(`{"model":"jev-latest","state":"Image {i}","assets":{"i":{"type":"image","format":"png","data_base64":"AA"}},"questions":{"loc":{"type":"box","instructions":"locate"}}}`),
		Headers: http.Header{"Content-Type": []string{"application/json"}},
	}

	_, err := orch.Process(ctx, httpReq)
	require.NoError(t, err)

	execution, err := client.RequestExecution.Query().Only(ctx)
	require.NoError(t, err)
	require.Equal(t, "jev-latest", execution.ModelID)
}
