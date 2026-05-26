# 评审回复：新版引起的问题说明

以下是给评审员的详细说明（便于直接复制到 PR 评论区）：

---

是的，问题与新版行为变化有关，但不是“模型不再输出 delta”，而是**输出结构的一个细微字段变化**导致下游无法正确接住 delta。

**形象比喻**：
reasoning 流式就像“先建一个空桶，再往里倒水”。
- 旧版本会先发 `output_item.added`，并带上 `summary: []` → 相当于“先放一个空桶”。
- 新版本因为 `omitempty` 把空数组省略了 → 相当于“桶没放出来”。
- 后面虽然持续发 `reasoning_summary_text.delta`（水），但下游找不到桶，就把水丢了。
最终只在结束时看到“整桶水”（一次性全量思考）。

**具体链路**：
1) 网关仍然连续返回 `response.reasoning_summary_text.delta`（我们抓包确认）。
2) 下游在处理 `response.output_item.added` 时，会根据是否存在 `summary` 初始化 reasoning buffer。
3) 新版序列化里 `summary` 是空切片但被 `omitempty` 省略，导致 buffer 未初始化。
4) 后续 delta 来了也无法挂载，表现为“没有流式思考”，只有最终全量思考。

**为什么这次改动能修复**：
- 在 `MarshalJSON` 中对 reasoning item **强制输出 `summary: []`**，确保“桶一定先出现”。
- 非 reasoning 仍旧不输出 summary，避免污染其它 item。
- 已补测试覆盖 nil/空/有内容三种情况。

如果需要，我可以附上具体日志对比截图（`response.output_item.added` 是否含 `summary`）作为补充证据。
