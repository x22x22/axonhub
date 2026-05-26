# 评审回复：PR#624 与后续 omitempty 回退证据

以下内容可直接粘贴给评审员：

---

## 1) PR #624 的 summary 修改证据
PR #624 的 diff 明确新增了 `MarshalJSON`，试图保证 reasoning item 的 `summary` 不为空：

```diff
// MarshalJSON omits summary for non-reasoning items and forces an empty array for reasoning items.
func (item Item) MarshalJSON() ([]byte, error) {
  type itemAlias Item

  if item.Type != "reasoning" {
    item.Summary = nil
  } else if item.Summary == nil {
    item.Summary = []ReasoningSummary{}
  }

  return json.Marshal(itemAlias(item))
}
```

来源：PR #624
https://github.com/looplj/axonhub/pull/624/changes

---

## 2) “后续改回 omitempty 的点”证据
后续有明确的 commit 把 `summary` 改回 `omitempty`，导致空 slice 被省略：

- **commit:** `a138cf4fa727e885de69481dc8187d98ee06f7b8`
- **message:** `fix: should omit empty reasoning summary array (#622)`
- **diff 关键片段：**

```diff
-Summary []ReasoningSummary `json:"summary"`
+Summary []ReasoningSummary `json:"summary,omitempty"`
```

该改动直接让 **空 summary 不再输出**，从而触发“流式思考不显示”的问题。

---

## 结论（可直接贴）
- PR #624 引入了“保证 summary 输出”的逻辑（但受 `omitempty` 影响仍有缺口）。
- 后续 commit `a138cf4f` 把 `summary` tag 改回 `omitempty`，导致空数组被省略 → 下游无法初始化 reasoning buffer → 流式思考消失。

如果需要，我可以补充“两个版本日志对比片段”作为佐证。
