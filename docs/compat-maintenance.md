# 兼容体系维护 SOP

版本：1；日期：2026-09-19。配套规范：[compat-policy.md](compat-policy.md)；实施手册：[compat-remediation-plan.md](compat-remediation-plan.md)。

这份文档管**例行维护的步骤**，不管设计取舍——取舍在规范和 ADR 0035 里。每一步都指向强制它的机制；没有机制的那几条会明写"人工"，不假装已经被自动化。

## 1. 发现上游有新版本

`compat-watch.yml` 每周一跑 `cmd/compatwatch`，报两类事情：上游面板超出已测上限（`docs/compat/v4-ranges.json` 的 `max_tested` 侧），以及我们自己发布的 Node release 有没有既没被 review、也没被显式排除的。

**监测不是认证，也不是失败。** 下面四件事必须分开报告，不能合并成"有事发生"：

| 事件 | 含义 | 处理 |
| --- | --- | --- |
| 上游有新版本 | 事实，尚未评估 | 走下面的流程 |
| 监测读不到（网络／认证失败） | 观测失败，等同于"不知道" | 修监测，不要当成"没有新版本" |
| 上游版本超出上限 | 未验证，不等于故障 | 走下面的流程 |
| 已支持组合出现行为不兼容 | 真问题 | 按第 5 节处理 |

流程：

1. 记录身份：上游种类、精确版本、发布物摘要或镜像 digest。
2. 建立 **observation** case，不动 `required` 集合。`docs/compat/verification-v1.json` 的 profile 里 `class` 字段是 `required` 或 `observation`；observation 失败单独上报，不自动变更支持范围。
3. 用[隔离实例](compat-remediation-plan.md)跑适配器与受影响的链路。
4. 修复，或者把失败记进证据。
5. 生成证据：`deploy/compat/check-go-results.mjs` 与 `deploy/compat/check-case-set.mjs` 产出的结构化结果。
6. 提政策 PR 审核。
7. 审核通过后转 `required`，或更新支持范围。

**监测不会自动抬高上限。** `cmd/compatwatch` 只读、从不改文件；抬高上限意味着 `3xui-compat.md` 里那次真机评审。

## 2. 移动 Node 的 floor（`min_supported`）

`docs/compat/node-v4.json` 的 `min_supported` **选择测什么，不决定谁能连**。没有生产代码读它，所以提升它只停止测试下面那些版本，不会断开任何节点。拒绝节点是 wire 世代的事（`wire.min_protocol_version`），需要自己的变更、公告和迁移验收。

移动前必须列出：

- 受影响的版本，以及（能拿到时）各版本的安装数量
- 最后一个仍受支持的 PSP
- 桥接路径，或者明确写"需要停机迁移"
- 是否仍接受旧节点的 wire 上报
- **停止测试**的日期
- **真正移除协议**的日期——这是另一个决定，不是同一个

移动后：`docs/compat/node-v4.json` 的 `min_supported_doc` 与 `docs/compat-policy.md` 第 10 节的沿革都要更新，写明现在什么是未被测试的。

**不缩减既有支持**，除非走第 4 节。**新 floor 不是顺手优化 CI 耗时的借口**——`deploy/compat/plan.mjs` 按位置切片，matrix 的大小是这条政策的直接后果。

## 3. 增加一个 Node release

1. 在 `docs/compat/verification-v1.json` 的 `pinned_sources` 里固定它的**提交**。
2. 如果不是要提供，就写进 `excluded` 并给出理由；**只写版本号不算一个决定**，规划器会拒绝。
3. 跑 `node deploy/compat/plan.mjs` 确认规划通过。

**注意固定的是提交，不是 tag 对象。** `git ls-remote --tags` 同时列 `refs/tags/X`（标注 tag 对象）和 `refs/tags/X^{}`（解引用提交）；固定前者会让每个 leg 都报"tag moved"。两个 workflow 都会拿解决出来的提交和它比对，所以填错会在第一次运行就红，而不是安静地测了另一份源码。

## 4. 支持退出

弃用期限按正式政策执行。**尚未正式确定期限时，在 PR 里明示"待决策"**，不能默认已经获得 90 天后断连的授权。规范建议不短于 90 天且覆盖两个发布周期，但那是建议，采用时要写进日期。

安全紧急例外独立记录原因和补救方案。停止维护不等于立即拒绝连接；移除 wire 支持是另一个决定，需要自己测试。

## 5. 每个发布周期，维护者核对

| 检查 | 强制它的机制 |
| --- | --- |
| required 集合非空 | `deploy/compat/plan.mjs` 拒绝"没有规划出任何 required case" |
| 每个 case 都报了，且报的是通过 | `deploy/compat/check-case-set.mjs`，缺一个 artifact 即未通过 |
| 固定 SHA 仍可获取且未移动 | 两个 workflow 里的 `rev-parse HEAD` 比对 |
| 旧客户端仍能解析清单 | `deploy/compat/plan.test.mjs` 的未知字段用例——用**旧读取方**验证，不以新读取方自己同意自己 |
| 支持范围没有超过证据 | **人工**。R01 定义的六类证据在 `docs/compat-policy.md` §9；没有机制阻止一句话超出它 |
| 过期 observation 有人处理 | 部分：`internal/service/nodecompat` 的 staleness 界会让过期观察不再授权高风险操作，但"有人去处理"是人 |

后两项是**人工**的，写在这里是因为假装它们自动执行比不做更糟。

## 6. 交接包

下一个维护者接手时应当拿到：

- 合并的 PR 清单与两仓最终 SHA
- 机器可读的支持政策（`docs/compat/node-v4.json`、`docs/compat/verification-v1.json`、`deploy/compat/profiles.json`）
- 完整的 required case 清单（`node deploy/compat/plan.mjs --emit cases`）
- 通过与失败注入的证据
- 已知限制
- 运行时界面／API 示例
- 发布 needs 图（`compatibility gate` 是所有发布动作的唯一入口）
- 分支保护设置，其中 `compatibility gate` 必须已被 ruleset 要求

## 7. 还没做完的部分

诚实列出，别让下一任以为已经自动化了：

- **`compatibility gate` 还没进 ruleset。** 不改 GitHub 设置的话，这个门不拦任何东西。原值见整改手册 R03。
- **反方向（候选 Node × 清单内旧 PSP）没有 CI。** R05 需要真实旧 PSP 发布物与候选 PN 作为两个进程跑。
- **历史升级／回退、真实第三方隔离、真实代理链路**分别是 R06／R07／R08，都需要真实环境。
- **第三方范围与上限的机器可读政策**还没有（`docs/compat/v4-ranges.json` 是实测记录，不是政策）。
