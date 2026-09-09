# `Passwall-Node` 协议类型（草稿，尚未搬家）

这些文件是 [`psp-node-agent.md`](../psp-node-agent.md) §8 的**可编译形式**。

## 它们最终不住在这里

按 §0.5 的约束 1，**协议类型定义住在 `Passwall-Node` 仓库，PSP 用 Go module 引它**——
一份真相源，两侧都被编译器检查。放在 PSP 里等于复制粘贴，正是那一条禁止的事。

放在这里只是因为 `Passwall-Node` 仓库还没建。**搬家清单**：

1. 建 `github.com/KazuhaHub/Passwall-Node`,module path `github.com/KazuhaHub/passwall-node`
2. 这个目录整体搬成该仓库的 `protocol/` 包
3. PSP 的 `go.mod` 加依赖，`internal/adapters/pspnode` 引它
4. **删掉这个目录**——留着就会有第二份真相源，那是这次自研要摆脱的那个问题在自己内部的复刻

在第 4 步完成之前，这里的 `package protocol` **不被 PSP 的任何代码 import**（`go build ./...`
会编译它，但没有生产调用点）。这是刻意的：它现在是一份规格的可编译副本，不是一个依赖。

## 为什么先写类型再写实现

§8 的每一条决定都有一个可以被代码违反的形式。写成类型之后，一部分决定由**编译器**看着：

| §8 的决定 | 类型怎么守住它 |
|---|---|
| `headroom_bytes` 三态（null / 0 / N） | `*int64` + **不带 `omitempty`**——零值和缺席在线上必须可分辨 |
| 时间戳不进 ETag、不触发重铸 | 新鲜度字段在 `Envelope`,不在任何 `Segment` |
| 版本是 `(epoch, version)` 字典序 | `Version` 是结构体，带 `Newer`,没有裸 int64 可比 |
| 「我应用了版本 N」不是「这是我的配置」 | `NodeReport` 里没有任何期望态字段——**看类型就能验证** |
| 逐 client 编址，不提升到 subject 级 | `QuotaEntry.ClientKey` 是 `ClientKey`,`IPShadowEntry.Subject` 是 `SubjectKey`,两个类型不能互换 |

剩下的（引用完整性、闸态迁移、残差算术）要靠测试，见搬家后的 `protocol/conformance`。
