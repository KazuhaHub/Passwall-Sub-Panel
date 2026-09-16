# 审查清单：Passwall Node V4 beta（2026-09-16）

> **给接手的人：先读「怎么用这份清单」。** 这里面只有 **5 条是逐行核实过的**，
> 其余是**线索，不是结论**。把线索当结论处理会浪费你的时间，而且会侵蚀这份清单的可信度——
> 下面就有一条标着 HIGH、推理完整、**结论是错的**。

审查范围：`ce95fc2..886965b`（PSP，+27,403 行 / 228 文件）与 `074e88a..4b40af2`（Node，+8,432 行 / 70 文件）。
方法：6 个维度独立扫描 → 去重后 **41 条**（原始 44 条含 3 对重复）。

## 怎么用这份清单

| 标记 | 含义 | 你该做什么 |
|---|---|---|
| ✅ **已核实** | 逐行读过代码，机制与后果都确认 | 可以直接排期修 |
| ❌ **已推翻** | 核实后发现结论不成立，保留是为了不被重新提出 | **不要修**，读一下为什么 |
| ⬜ **未核实** | 有 file:line 和推理，但没人打开过那段代码复核 | **先复核再动手** |

**两边的测试都是绿的，构建也都通过。** 下面每一条都是测试覆盖不到的地方——
这本身是信息：绿色不等于没问题，只等于没有测到。

**关于未核实条目的可信度校准（抽查得到，不是猜测）**：我随机复核了两条未核实条目的引用——
`health.go:234`（没有探测目标 → 判定为确定不可达）与 `traffic.go:1120`（`hadPrev` 恒为真），
**行号与代码事实都准确**。

所以分界线不在「引用可不可信」,而在**结论**:被推翻的那一条，代码事实同样准确，
错的是它推出的后果。**复核时要验的是「这个失败场景真的会发生吗」,不是「这行代码在不在」。**

顺带，`health.go:234` 那条复核后还更清楚了：作者是**刻意**选了 unreachable,
注释写着「宁可显示无信号，也不要一个陈旧的绿点」——方向是对的，只是
`domain.NodeHealthInconclusive` 这个第三态已经存在，**该用它而不是 unreachable**。
这类「作者已经想过、只是选错了格子」的条目，修起来比看上去便宜。

---

## 一、已核实 —— 可以直接排期

### 🔴 H-1 应急放行在原生节点上完全无效 ✅
`internal/service/nodesync/nodesync.go:776-790`

配额额度算的是 `saturatingSub(TrafficLimitBytes, PeriodUsed + pending)`，
**整个 `nodesync.go` 里 "Emergency" 出现 0 次**。`types.go:200-206` 那套「从应急基线重新计量」没有接进来。

**后果**：用户跑满配额被停 → 管理员给应急放行 → 名册 `Enabled=true`、订阅正常、**界面显示已恢复** →
但额度是 0，闸门关死，**一个字节也过不去**。管理员和用户同时被界面骗。

旧的 3X-UI 路径有专门分支处理这件事，原生路径漏了。

### 🔴 H-2 一次性入册令牌被写进访问日志 ✅
`internal/transport/http/router.go:161`（对照 `:709-710`）

日志中间件的 Skip 谓词**只排除 `/node-bootstrap/`**。而入册路由是
`GET /enroll/:token` 与 `POST /api/enroll/:token`——**令牌在 URL 路径里，原样写进
stdout / journal / 日志聚合器**。同一处的代码注释自己写着「这个一次性令牌就是凭据」。

**后果**：任何能读日志的人（运维、日志平台、被导出的排障包）在有效期内可直接重放。

### 🔴 H-3 交集为空就删客户端，累计计数从 0 重来 ✅
`internal/service/clientprov/clientprov.go:96-105`

`clientplan.Build` 对空节点集返回 `nil` → `keep` 为空 → **该面板下该用户所有 `psp_clients`
行被 `DeleteByID` 删除**，连同 `LifetimeUpBytes` / `LastRaw*` / `LastCounterEpoch`。
**没有空集保护。**

把用户换到一个在该面板上不选任何节点的组，就会触发。**再加回来时计数从 0 开始。**

> 这是 `HANDOFF.md` 十条不要里明写的那一条：**「交集为空仍物化 client，绝不删除」**。

### 🔴 H-4 pspnode 把「节点离线」翻译成「空快照，成功」 ✅
`internal/adapters/pspnode/client.go:106-117`

`domain.ErrNotFound`（它同时表示 agent 离线、没有缓存的全量报告、全量报告陈旧）
被替换成一个**空的成功快照**，`err = nil`。上游读到的是「这个面板确实什么都没有」,
而不是「我不知道」。

**后果**：在线 IP 覆盖度、inbound 列表、客户端列表全部变成确定的零。
异地并发检测拿到的是「这个人一个地方都没连」而非「这一侧没数据」。

### 🟠 H-5 远程升级：摘要与二进制来自同一个可变来源 ✅（工作流 3 票中 2 票确认）
`NODE: internal/upgrade/release.go:157,169`

`SHA256SUMS.txt` 和 tarball 都从同一个 GitHub release 目录取，**没有签名，PSP 也不下发预期摘要**。
两者一致就接受。

**后果**：谁拿到 release 发布权（泄露的 PAT、被盗的维护者账号、被改的 CI），就能同时替换二者，
**下一个点「升级」的节点以 root 执行它**。这是整个系统爆炸半径最大的一条路径。

---

## 二、已推翻 —— 不要修

### ❌ 组限额缓存 fail-open「会清空节点的配额授权」
`internal/adapters/sqlstore/group_limits_cache.go:76` · 原标 HIGH

**机制真，后果假。** 冷缓存 + DB 读错确实返回零值 `GroupLimits{}`
（代码注释自己承认 "only a cold cache fails open"）。

但在原生路径上 `nodesync.go:776` 是 `if user.TrafficLimitBytes > 0`——
零限额意味着**根本不发配额条目**，而协议规定**条目缺席 = 保持上次已知，永不解释成无限额**。
**节点的闸门纹丝不动。**

真正的暴露面在那些把 0 读成「无限」的旧路径，**不在原生节点**。
降为中危，并且描述要重写。

> 留着这条，是因为它推理完整、标着 HIGH、读起来完全可信——而它是错的。
> **这就是上面那张表存在的理由。**

---

## 三、一个模式：单一真相源正在侵蚀 ⬜

不是单点缺陷，是一类。**这些拷贝今天都等价，所以测试全绿、CI 全过。**

| 共享包里的东西 | 现状 |
|---|---|
| `protocol.QuotaEntry.RefreshDue` | **无生产调用者**；规则在 `NODE: internal/state/sqlite/clients.go:318,364` 手写了两遍 |
| `protocol.ShouldSendFull` | PSP 侧从不调用（`nodesync.go:932` 自己重新推导一遍） |
| `protocol.Converged` | **两个仓库都没人用**；ETag 判定散成三份不一致的拷贝（`internal/domain/nodeagent.go:116`） |
| `agent.upgrade.v1` 线上类型 | 两边各定义一份（`internal/service/nodeagentupgrade/upgrade.go:29`） |
| `deployment.ValidReleaseVersion` | PSP 已经 import 了它，却又抄了一份私有副本（`upgrade.go:112`） |
| 30s / 60s 默认值 | Go 里三处，**协议包里没有** |

**危险不在今天，在哪天有人去改共享包里的规则**——那里有契约和测试。
改完什么都不会重新编译，两边继续跑旧语义，而 `protocol_test.go` 照样绿。

> `RefreshDue` 在 **2026-09-12 的审查里就报过**，四天后不但没修，又多了两个。
> 这正是当初把协议拆成独立仓库要防的那件事，现在在自己内部复刻了。

---

## 四、未核实线索（按主题，⬜ 全部需要先复核）

### 配额与计量

| 严重度 | 问题 | 位置 |
|---|---|---|
| M | `PeriodEndsAtMS` 用 UTC 算，而 PSP 按宿主本地时区滚周期——跨时区会错开数小时 | `nodesync.go:710` |
| M | 兜住时钟偏移泄漏的那个 Issue **根本不存在**（§8.4 承诺过要报偏移） | `nodesync.go:59` |
| M | `applyScheduledQuotaTx` 不更新 `quota_fingerprint`，PSP 的重发被丢弃 | `NODE: internal/state/sqlite/clients.go:376` |
| M | `OverburnHeadroomBytes` 实际是 Σheadroom,**不是**文档里那个跨节点残差公式；`conformance.AssessFleetQuota` 从没被调用 | `nodesync.go:911` |
| M | `ValidateEnvelope` 拒绝负残差，而一致性契约要求的正是这个负值——按文档实现会让全车队 500 | `NODE: protocol/envelope.go:104` |
| M | `s.grants` 从不清理，退役节点永久抬高车队超用数字 | `nodesync.go:907` |
| L | `aggregateCoverage` 仍把 `Present` 当「计数器已上报」,期末关闸会让 40 个客户端全被记成陈旧 | `nodesync.go:873` |
| L | `hadPrev` 在赋值之后才读 `LastCounterEpoch`,条件恒为真 | `internal/service/traffic/traffic.go:1120` |

### 「不知道」被渲染成确定结论

| 严重度 | 问题 | 位置 |
|---|---|---|
| **H** | reconcile **永久停用**一个只是「尚未 Applied」的节点——快照省略它，适配器把省略当不存在 | `internal/service/reconcile/reconcile.go:884` |
| M | 仪表盘丢弃 `node_inconclusive`,探测看不见的车队显示「全部健康」 | `web-react/src/api/dashboard.ts:27` |
| M | 没有探测目标的节点被判定为**确定不可达**,而非无法判定 | `internal/service/health/health.go:234` |
| L | `NativeAgentStatus` 的 TS 联合类型漏了后端会返回的 `awaiting_checkin` | `web-react/src/api/servers.ts:111` |
| L | 用户端「取不到节点状态」和「你没有节点」渲染成同一句话 | `web-react/src/views/user/MeView.tsx:131` |

### 入册与凭据

| 严重度 | 问题 | 位置 |
|---|---|---|
| M | 未认证的 bootstrap 凭据下发，审计记在 actor `admin` 名下 | `internal/transport/http/handler/admin_servers.go:1329` |
| M | 3X-UI 入册回调创建管理员作用域凭据的服务器，**不写任何审计** | `internal/transport/http/handler/node_enroll.go:203` |
| M | 入册回调串行探测调用方提供的**无上限**地址列表，没有总体截止时间 | `internal/transport/http/handler/node_enroll.go:253` |
| L | `node_auth.go` 声称 bearer 永不入库——凭据恢复功能让这句话变成假的 | `internal/transport/http/handler/node_auth.go:18` |
| L | 铸令牌拒绝明文 HTTP，渲染脚本却不拒绝 | `internal/transport/http/handler/enroll_script.go:28` |

### 远程升级

| 严重度 | 问题 | 位置 |
|---|---|---|
| M | 刚下载、**尚未安装**的二进制以 uid 0 执行来做验证 | `NODE: internal/upgrade/release.go:360` |
| M | PSP 接受任何合法版本串；已审release 白名单**只在 React 里拦** | `internal/service/nodeagentupgrade/upgrade.go:94` |
| M | 同一个白名单被安装脚本、安装文件两条兄弟路由绕过 | `internal/transport/http/handler/node_installation.go:141` |
| M | `TimeoutStartSec=9min` 短于 helper 自己最坏预算——systemd 可能在激活后 SIGKILL,**把一次成功的升级回滚掉** | `NODE: deployment/passwall-node-upgrade.service:9` |
| L | 被中断的升级会永久遗留备份目录与暂存件，无人回收 | `NODE: internal/upgrade/helper_linux.go:606` |

### 迁移

| 严重度 | 问题 | 位置 |
|---|---|---|
| M | PSP 从没探到过核心版本时，迁移预检**静默替换**成推荐的 Xray 版本，不给警告 | `internal/service/servermigration/servermigration.go:63` |
| L | 转换失败会把仍是 3X-UI 的面板从适配器池里注销，且不会自愈 | `internal/transport/http/handler/node_bootstrap.go:376` |
| L | Apply 把挂载置为 `pending` 的同时清掉 `first_failed_at`——全代码库唯一一个没有失败时钟的 pending | `internal/adapters/sqlstore/server_migration_repo.go:144` |

### 节奏与协议

| 严重度 | 问题 | 位置 |
|---|---|---|
| M | agent 在「立即上报」时**整个跳过** `ShouldSendFull`,连 `want_full_report` 一起忽略 | `NODE: internal/agent/run.go:82` |

### 文档已失真

| 严重度 | 问题 | 位置 |
|---|---|---|
| L | Node 的 HANDOFF 说 PSP 引用 beta2；`go.mod` 实际是 beta4 | `NODE: HANDOFF.md:241` |
| L | PSP 的 HANDOFF 说 applied epoch/version/ETag 组合会被校验为「确由本面板铸造」——实际没有 | `HANDOFF.md:227` |

---

## 五、建议顺序

1. **H-1 应急放行** —— 用户已付费、管理员已操作、界面已确认，服务仍然不通。唯一一条正在直接伤害真实用户的。
2. **H-2 令牌进日志** —— 修起来最小（Skip 谓词多加一个前缀），暴露面最直接。
3. **H-3 删客户端** —— 加空集保护。触发条件（换组）是日常操作。
4. **H-5 升级签名** —— 爆炸半径最大，但需要设计（签名 or PSP 下发预期摘要），不是一行。
5. **H-4 / reconcile 那条** —— 两条都是「不知道被当成确定答案」,建议一起改，因为它们共用同一个 `ErrNotFound` 语义。
6. **单一真相源侵蚀** —— 不紧急但会持续变差；每多一份拷贝，将来改规则时漏改的概率就翻一倍。

**未核实的 30 条：先复核再动手。** 已推翻的那一条就是从这批里来的。
