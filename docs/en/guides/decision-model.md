# Decision Models (System One)

Decision models turn natural language and business state into **structured, typed judgments** instead of free-form text. You supply the state and the questions, and the model returns probabilities, choices, or scores that code can use directly.

AxonHub exposes decision models through the `typesafe` channel type and a unified System One endpoint.

## Supported providers

| Provider | Channel type | Base URL | Supported primitives |
|----------|--------------|----------|----------------------|
| Alibaba Cloud Bailian | `typesafe` | `https://{WorkspaceId}.cn-beijing.maas.aliyuncs.com/compatible-mode/v1` | `noul`, `choice`, `score` |
| TypeSafe | `typesafe` | `https://api.typesafe.ai/v1` | `noul`, `choice`, `score`, `box` + `assets` |

Both providers speak the System One-compatible protocol, so they share the same channel type and the same endpoint.

### Bailian-specific requirement

Bailian's decision model is served from a **workspace-scoped domain**. You must build the WorkspaceId into the hostname. An address without it returns 404:

```
Wrong: https://dashscope.aliyuncs.com/compatible-mode/v1/systemone
Right: https://llm-xxxxxxxxxxxx.cn-beijing.maas.aliyuncs.com/compatible-mode/v1
```

A WorkspaceId looks like `llm-u1n5t35kysm94vz3` and is shown on the workspace details page in the Bailian console.

## The three primitives

### noul — yes/no judgment

Returns a probability between `0` and `1` that the answer is yes.

```json
{
  "model": "decision-model-preview",
  "state": "This product works great and I'm very happy with it!",
  "questions": {
    "positive": {
      "type": "noul",
      "instructions": "Is this review positive?"
    }
  }
}
```

Response:

```json
{
  "model": "decision-model-preview",
  "answers": {
    "positive": { "type": "noul", "noul": 0.99 }
  },
  "usage": { "input_tokens": 24, "output_tokens": 0 }
}
```

### choice — pick one

Give a candidate set, and get back the selected key plus per-option probabilities and confidence.

```json
{
  "model": "decision-model-preview",
  "state": "The order was paid over 24 hours ago but has not arrived, and the user cannot use the core service.",
  "questions": {
    "department": {
      "type": "choice",
      "instructions": "Which team should handle this?",
      "criteria": {
        "billing": "Payments, refunds, and invoicing",
        "technical": "Product failures and integration issues"
      }
    }
  }
}
```

Response:

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

### score — graded rating

`criteria` is an **ordered array**. The response carries a probability-weighted level index (which may be fractional) plus the full legend.

```json
{
  "model": "decision-model-preview",
  "state": "There is a typo on the page; it does not affect usage.",
  "questions": {
    "severity": {
      "type": "score",
      "instructions": "How severe is this issue?",
      "criteria": [
        "Cosmetic, no functional impact",
        "Partial impact, workaround exists",
        "Core function unavailable, no workaround",
        "Severe business or security impact"
      ]
    }
  }
}
```

Response:

```json
{
  "answers": {
    "severity": {
      "type": "score",
      "score": 0.01,
      "legend": {
        "0": "Cosmetic, no functional impact",
        "1": "Partial impact, workaround exists",
        "2": "Core function unavailable, no workaround",
        "3": "Severe business or security impact"
      },
      "probabilities": { "0": 0.99, "1": 0.0, "2": 0.0, "3": 0.0 },
      "confidence": 1.0
    }
  }
}
```

Note that `score` is a **weighted expectation**, not an integer. Semantically different input moves the value: changing the state above to a core-function outage pushes the score to `2.19` (`probabilities: {2: 0.78, 3: 0.21}`).

## Asking several questions at once

`questions` accepts multiple entries, so a single request returns several independent judgments:

```json
{
  "model": "decision-model-preview",
  "state": "The order was paid over 24 hours ago but has not arrived, and the user cannot use the core service.",
  "questions": {
    "department": {
      "type": "choice",
      "instructions": "Which team should handle this?",
      "criteria": { "billing": "Payments and refunds", "technical": "Product failures" }
    },
    "escalate": {
      "type": "noul",
      "instructions": "Should the on-call engineer be paged immediately?"
    },
    "severity": {
      "type": "score",
      "instructions": "How severe is this issue?",
      "criteria": ["Cosmetic", "Partial impact", "Core unavailable", "Severe business impact"]
    }
  }
}
```

All answers come back together and do not interfere with each other.

## box and assets limitations

**`box` and `assets` are available on TypeSafe only. Bailian does not support them.**

Bailian returns 400 for these fields:

```json
{
  "code": "InvalidParameter",
  "message": "invalid SystemOne request: Input tag 'box' found using 'type'
              does not match any of the expected tags: 'noul', 'choice', 'score'"
}
```

If you need to localize a target in an image (return a bounding box), you must use a TypeSafe channel. TypeSafe's `box` requires:

1. An image in `assets` (PNG/JPEG, base64)
2. The `TypeSafe-Danger-Zone-Images: true` request header
3. Image dimensions between **280 and 1568 pixels**; anything outside that range returns `Invalid image dimensions`

```json
{
  "model": "jev-latest",
  "state": "Analyze this image",
  "assets": {
    "img1": { "type": "image", "format": "png", "data_base64": "<base64>" }
  },
  "questions": {
    "object": { "type": "box", "instructions": "Where is the main object in {img1}?" }
  }
}
```

## Configuring a channel

### Bailian decision model

| Field | Value |
|-------|-------|
| Name | Bailian Decision Model |
| Type | `typesafe` |
| Base URL | `https://{WorkspaceId}.cn-beijing.maas.aliyuncs.com/compatible-mode/v1` |
| API Key | Your Bailian API key |
| Supported models | `decision-model-preview` |

### TypeSafe

| Field | Value |
|-------|-------|
| Name | TypeSafe System One |
| Type | `typesafe` |
| Base URL | `https://api.typesafe.ai/v1` |
| API Key | Your TypeSafe API key |
| Supported models | `jev-latest`, `jev-preview`, `jev-1.13.0` |

## Calling the endpoint

Once the channel and models are configured, call the unified AxonHub endpoint:

```bash
POST http://<axonhub-host>/typesafe/v1/systemone
Authorization: Bearer <axonhub-api-key>
Content-Type: application/json

{
  "model": "decision-model-preview",
  "state": "your business state",
  "questions": { ... }
}
```

## Regional restrictions

TypeSafe returns **451 for mainland China IP addresses**:

```json
{ "title": "Typesafe is not available in your region.", "status": 451 }
```

So a TypeSafe channel on an AxonHub instance deployed in mainland China needs an additional overseas egress. **Bailian has no such restriction** — it is reachable directly from mainland China with roughly 30–40ms latency.

## Error handling

| Status | Scenario | Explanation |
|--------|----------|-------------|
| 400 | Unsupported primitive | e.g. Bailian receiving `box` |
| 400 | Invalid image dimensions | TypeSafe requires 280–1568px |
| 422 | Model not registered | `invalid_model_error`; check that the model is added to the model list |
| 451 | Regional restriction | TypeSafe rejects mainland China IPs |

## Related documentation

- [Channel management](./channel-management.md)
- [Model management](./model-management.md)
