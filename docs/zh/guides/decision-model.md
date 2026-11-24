# 决策模型（System One）

决策模型把自然语言和业务状态转成**结构化的类型化判断**，而不是自由文本。你提出问题、给出候选答案，模型返回概率、选项或分值，代码可以直接使用。

AxonHub 通过 `typesafe` 渠道类型接入决策模型，对外暴露统一的 System One 端点。

## 支持的提供商

| 提供商 | 渠道类型 | Base URL | 支持原语 |
|--------|----------|----------|----------|
| 阿里云百炼 | `typesafe` | `https://{WorkspaceId}.cn-beijing.maas.aliyuncs.com/compatible-mode/v1` | `noul`、`choice`、`score` |
| TypeSafe | `typesafe` | `https://api.typesafe.ai/v1` | `noul`、`choice`、`score`、`box` + `assets` |

两家的协议都是 System One 兼容格式，因此共用同一个渠道类型和同一个端点。

### 百炼的额外要求

百炼的决策模型走**业务空间专属域名**，必须把控制台里的 WorkspaceId 拼进域名。缺少这一段的地址会返回 404：

```
错误：https://dashscope.aliyuncs.com/compatible-mode/v1/systemone
正确：https://llm-xxxxxxxxxxxx.cn-beijing.maas.aliyuncs.com/compatible-mode/v1
```

WorkspaceId 形如 `llm-u1n5t35kysm94vz3`，在百炼控制台的业务空间详情页可以查到。

## 三种原语

### noul —— 是 / 否判断

返回 `0` 到 `1` 之间的概率，表示「是」的可能性。

```json
{
  "model": "decision-model-preview",
  "state": "这个产品很好用，我很满意！",
  "questions": {
    "positive": {
      "type": "noul",
      "instructions": "这条评价是正面的吗？"
    }
  }
}
```

响应：

```json
{
  "model": "decision-model-preview",
  "answers": {
    "positive": { "type": "noul", "noul": 0.99 }
  },
  "usage": { "input_tokens": 24, "output_tokens": 0 }
}
```

### choice —— 多选一

给出候选集合，返回选中的键、各选项概率和置信度。

```json
{
  "model": "decision-model-preview",
  "state": "订单支付后超过 24 小时仍未到账，用户无法继续使用核心服务。",
  "questions": {
    "department": {
      "type": "choice",
      "instructions": "应该由哪个团队处理？",
      "criteria": {
        "billing": "支付、退款和账单问题",
        "technical": "产品故障和集成问题"
      }
    }
  }
}
```

响应：

```json
{
  "answers": {
    "department": {
      "type": "choice",
      "choice": "billing",
      "probabilities": { "billing": 0.99, "technical": 0.01 },
      "confidence": 0.98
    }
  }
}
```

### score —— 分级打分

`criteria` 是一个**有序数组**，返回概率加权后的等级索引（可以是小数），并附带完整图例。

```json
{
  "model": "decision-model-preview",
  "state": "页面有个错别字，不影响使用。",
  "questions": {
    "severity": {
      "type": "score",
      "instructions": "问题有多严重？",
      "criteria": [
        "轻微问题，不影响功能",
        "部分功能受影响，但存在替代方案",
        "核心功能不可用，没有替代方案",
        "造成严重业务或安全影响"
      ]
    }
  }
}
```

响应：

```json
{
  "answers": {
    "severity": {
      "type": "score",
      "score": 0.01,
      "legend": {
        "0": "轻微问题，不影响功能",
        "1": "部分功能受影响，但存在替代方案",
        "2": "核心功能不可用，没有替代方案",
        "3": "造成严重业务或安全影响"
      },
      "probabilities": { "0": 0.99, "1": 0.0, "2": 0.0, "3": 0.0 },
      "confidence": 1.0
    }
  }
}
```

注意 `score` 是**加权期望值**，不是整数。同一组输入的语义变化会体现为分值移动：把上面的输入换成核心功能不可用，分值会跳到 `2.19`（`probabilities: {2: 0.78, 3: 0.21}`）。

## 一次提问多个问题

`questions` 支持同时包含多个条目，一次请求拿到多个独立判断：

```json
{
  "model": "decision-model-preview",
  "state": "订单支付后超过 24 小时仍未到账，用户无法继续使用核心服务。",
  "questions": {
    "department": {
      "type": "choice",
      "instructions": "应该由哪个团队处理？",
      "criteria": { "billing": "支付退款", "technical": "产品故障" }
    },
    "escalate": {
      "type": "noul",
      "instructions": "是否需要立即通知值班人员？"
    },
    "severity": {
      "type": "score",
      "instructions": "这个问题有多严重？",
      "criteria": ["轻微", "部分受影响", "核心不可用", "严重业务影响"]
    }
  }
}
```

三个答案会一起返回，各自互不干扰。

## box 与 assets 的限制

**`box` 和 `assets` 只有 TypeSafe 支持，百炼不支持。**

百炼对这两个字段返回 400：

```json
{
  "code": "InvalidParameter",
  "message": "invalid SystemOne request: Input tag 'box' found using 'type'
              does not match any of the expected tags: 'noul', 'choice', 'score'"
}
```

如果需要在图片上定位目标（报边界框），必须用 TypeSafe 渠道。TypeSafe 的 `box` 需要：

1. `assets` 里带图片（PNG/JPEG，base64）
2. 请求头带 `TypeSafe-Danger-Zone-Images: true`
3. 图片尺寸在 **280–1568 像素**之间，越界返回 `Invalid image dimensions`

```json
{
  "model": "jev-latest",
  "state": "分析这张图片",
  "assets": {
    "img1": { "type": "image", "format": "png", "data_base64": "<base64>" }
  },
  "questions": {
    "object": { "type": "box", "instructions": "主物体在 {img1} 的什么位置？" }
  }
}
```

## 配置渠道

### 百炼决策模型

| 字段 | 值 |
|------|-----|
| 名称 | 百炼决策模型 |
| 类型 | `typesafe` |
| Base URL | `https://{WorkspaceId}.cn-beijing.maas.aliyuncs.com/compatible-mode/v1` |
| API Key | 百炼 API Key |
| 支持模型 | `decision-model-preview` |

### TypeSafe

| 字段 | 值 |
|------|-----|
| 名称 | TypeSafe System One |
| 类型 | `typesafe` |
| Base URL | `https://api.typesafe.ai/v1` |
| API Key | TypeSafe API Key |
| 支持模型 | `jev-latest`、`jev-preview`、`jev-1.13.0` |

## 调用端点

配置好渠道和模型后，通过 AxonHub 统一端点调用：

```bash
POST http://<axonhub-host>/typesafe/v1/systemone
Authorization: Bearer <axonhub-api-key>
Content-Type: application/json

{
  "model": "decision-model-preview",
  "state": "你的业务状态",
  "questions": { ... }
}
```

## 地域限制

TypeSafe 对**中国大陆 IP 返回 451**：

```json
{ "title": "Typesafe is not available in your region.", "status": 451 }
```

因此在国内部署的 AxonHub 上，TypeSafe 渠道需要额外的境外出口。**百炼没有这个限制**，国内可直连，延迟约 30–40ms。

## 错误处理

| 状态码 | 场景 | 说明 |
|--------|------|------|
| 400 | 原语类型不支持 | 如百炼收到 `box` |
| 400 | 图片尺寸越界 | TypeSafe 要求 280–1568px |
| 422 | 模型未注册 | `invalid_model_error`，检查模型是否已添加到模型列表 |
| 451 | 地域限制 | TypeSafe 拒绝中国大陆 IP |

## 相关文档

- [渠道配置指南](./channel-management.md)
- [模型管理](./model-management.md)
