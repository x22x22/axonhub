package typesafe

import "github.com/looplj/axonhub/llm/httpclient"

// asResponse 把 (status, body) 还原成 TransformResponse 需要的结构。
func asResponse(status int, body []byte) *httpclient.Response {
	return &httpclient.Response{StatusCode: status, Body: body}
}
