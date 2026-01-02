# 使用 /v1/responses 接口调用 /v1/chat/completions 服务提供者

本文档说明如何使用 AxonHub 通过 OpenAI Responses API (`/v1/responses`) 调用只支持 Chat Completions API (`/v1/chat/completions`) 的服务提供者。

## 概述

AxonHub 的转换器架构提供了不同 LLM API 之间的无缝格式转换。您可以使用 `/v1/responses` 端点调用任何 OpenAI 兼容的提供者，即使他们原生不支持 Responses API 格式。

## 工作原理

请求流程如下：

1. **客户端** → 发送 Responses API 格式的请求到 `/v1/responses`
2. **入站转换器** → 将 Responses 格式转换为统一的内部格式
3. **协调器** → 根据模型选择合适的通道
4. **出站转换器** → 将统一格式转换为提供者的原生格式（例如 Chat Completions）
5. **提供者** → 以原生格式处理请求
6. **响应** → 通过转换器流回客户端，返回 Responses API 格式

## 配置

使用此功能非常简单：

1. 配置一个支持 Chat Completions 的模型通道（例如 OpenAI、DeepSeek、Moonshot 等）
2. 使用该模型调用 `/v1/responses` 端点

无需特殊配置！转换器架构会自动处理格式转换。

### 支持的通道类型示例

这些通道类型支持 Chat Completions，可与 `/v1/responses` 配合使用：

- `openai` - 官方 OpenAI API
- `deepseek` - DeepSeek API
- `moonshot` - Moonshot AI
- `zhipu` - ChatGLM / 智谱 AI
- `doubao` - 字节跳动豆包
- `siliconflow` - 硅基流动
- `ppio` - PPio
- `vercel` - Vercel AI
- 以及更多...

## 使用示例

### 使用 OpenAI SDK

```python
from openai import OpenAI

client = OpenAI(
    api_key="your-axonhub-api-key",
    base_url="http://localhost:8090/v1"
)

# 使用 responses API 调用 chat completions 提供者（例如 deepseek-chat）
response = client.responses.create(
    model="deepseek-chat",
    input="法国的首都是哪里？"
)

print(response.output_text())
```

### 使用 curl

```bash
curl -X POST http://localhost:8090/v1/responses \
  -H "Authorization: Bearer your-axonhub-api-key" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "deepseek-chat",
    "input": "法国的首都是哪里？"
  }'
```

## 支持的功能

使用 `/v1/responses` 调用 Chat Completions 提供者时，支持以下功能：

- ✅ 基本文本生成
- ✅ 流式响应
- ✅ Temperature 和其他采样参数
- ✅ 工具/函数调用（如果提供者支持）
- ✅ 通过 `previous_response_id` 进行多轮对话
- ✅ 自定义元数据
- ✅ Token 使用量跟踪

## 限制

某些 Responses API 功能可能受底层提供者支持的限制：

- 图像生成工具（需要提供者支持）
- 原生推理模型功能（需要提供者支持）
- 提示缓存（需要提供者支持）

## 优势

使用 `/v1/responses` 调用 Chat Completions 提供者具有以下优势：

1. **统一 API**：在不同提供者之间使用相同的客户端代码
2. **简化的上下文管理**：使用 `previous_response_id` 而不是管理消息数组
3. **现代 API 设计**：Responses API 是 OpenAI 更新、更流畅的接口
4. **提供者灵活性**：无需更改客户端代码即可切换提供者

## 测试

集成测试位于 `integration_test/openai/responses/chat_completions_backend/`，演示了此功能。

## 另请参阅

- [OpenAI Responses API 文档](https://platform.openai.com/docs/api-reference/responses)
- [AxonHub 架构概述](../README.md)
- 通道配置（参见主 README.md）
