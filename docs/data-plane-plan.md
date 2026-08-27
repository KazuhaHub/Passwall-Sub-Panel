# 数据面演进计划：先提效，再决定是否自研后端

> 状态：**草案，待讨论**。本文不承诺任何交付日期，也不预设自研后端一定要做——Phase 2 是一个**决策门**，用 Phase 0 量到的数据决定。
> 关联：[ARCHITECTURE.md](ARCHITECTURE.md)、[inbound-ownership.md](inbound-ownership.md)、[panel-adapters.md](panel-adapters.md)、[3xui-compat.md](3xui-compat.md)。

## 0. 一句话

当前 PSP 的性能瓶颈**不是轮询**，而是**每个活跃用户每周期两次串行 HTTP 往返**，且本该拦住它的 no-op skip 在结构上永远失效。先把这个修掉（不需要任何新后端），拿到真实数据后，再决定要不要自研数据面。

---

## 1. 已确认的事实

以下每条都在代码里核过，不是推测。

### 1.1 读侧已经很便宜

traffic poll 是**每面板一次** `ListInboundsSlim`（`internal/service/traffic/traffic.go`，`for panelID := range panelsToFetch`），不是每节点一次；默认 5 分钟一轮（`cron_traffic_pull_minutes`，`settings_kv_repo.go:507`）。50 个面板也只有 0.17 req/s。

**推论：把轮询换成推送，省下的是这一部分——不是瓶颈。**

### 1.2 no-op skip 对活跃用户永远不成立（根因）

| # | 位置 | 事实 |
|---|---|---|
| 1 | `traffic.go:1343` | 推送触发条件是 `totals.deltaTotal != 0`——用户只要动了流量就推 |
| 2 | `user/user.go:479` | 推的 `TotalGB` = `TrafficFloorBytes(limit, 本期已用)` = `limit - used`，**逐字节精确，无量化** |
| 3 | `sharedclient/sharedclient.go:266` | skip 要求 `cur.TotalGB == spec.TotalGB` |
| 4 | — | 第 2 步保证这个数**每周期都不同** → **skip 结构性失效** |

那个为「避免每周期重复全量替换」而写的 skip，对活跃用户从来没生效过。

### 1.3 每次推送是两次**串行**往返

`SyncUserLifecycle`（`sharedclient.go:301`）是普通 `for` 循环，逐个 `SyncLifecycle`；每个 `SyncLifecycle` = 一次 `GetClient`（供 skip 判断）+ 一次 `UpdateClient`。用户有 P 个 PSPClient（≈ 面板数 × 凭据类）就是 `P × 2` 次串行往返。

### 1.4 并发闸是 8，且跨周期共享

`pushSem`（`traffic.go:160`）容量 = `paneltz.ResolveMaxPanelConcurrency(0)` = **8**。注释明确说它跨周期共享：上周期没排空，下周期排队等它。**这意味着过载不是线性劣化，是雪崩。**

### 1.5 xray reload 有上界（此前被高估）

3X-UI 的 `ApplyPendingRestart()` 是 **30 秒 cron**（上游 `internal/web/web.go:292`），不是每次写一次 reload。CHANGELOG 里「N 次背靠背 reload」说的是突发场景，稳态下每面板最多 30 秒一次。

**推论：成本在往返，不在 reload。** 优化方向应对准往返次数。

### 1.6 成本模型

```
每周期墙钟 ≈ N × P × 2 × RTT / 8
  N = 本周期有流量的用户数
  P = 该用户的 PSPClient 数
```

| N | P | RTT 200ms | 占 300s 周期 |
|---|---|---|---|
| 1000 | 1 | 50s | 17% |
| 1000 | 3 | 150s | 50% |
| 2500 | 3 | 375s | **溢出 → 雪崩** |

---

## 2. 现在还不知道的（Phase 0 必须回答）

- **N**：稳态下每周期真正有流量的用户数
- **P**：用户平均 PSPClient 数
- **RTT**：真实面板的 `GetClient` / `UpdateClient` 往返分布（P50/P95），不是代码注释里那个 `~300ms` 的历史值
- 一轮 `PollOnce` 的耗时构成：读 / 写 / DB 各占多少
- `pushSem` 实际排队深度——有没有已经在跨周期堆积

**没有这些数，「提升了多少」无法验收，Phase 1 的参数也只能拍脑袋。**

---

## Phase 0 — 插桩与测量

**目标**：拿到 §2 的全部数字。
**不改任何行为。**

- 在 `PushClientConfig` / `SyncLifecycle` 上加计数器与耗时直方图：推送次数、skip 命中率、每次往返耗时
- 记录每轮 `PollOnce` 的分段耗时与 `pushSem` 排队深度
- 跑够一个完整计费周期的稳态样本（至少 24h，覆盖高峰）

**退出条件**：能画出「每周期推送次数 vs 活跃用户数」和 RTT 分布；能算出当前 skip 命中率（预期接近 0）。

---

## Phase 1 — 效率修复（不需要新后端）

### 1a. 给推送决策加迟滞带（最高价值，针对 §1.2 根因）

```
band = max(minBytes, remaining × pct)
若 |cur.TotalGB - spec.TotalGB| <= band 且其余字段全同 → skip
```

设计要点（已推敲，勿简化）：

- **不要量化 `TrafficFloorBytes` 的返回值。** 向下取整会让面板提前掐断用户；向上取整会在 PSP 离线时多烧。加在**决策**上：真推时推的仍是精确值，陈旧度被 band 界住。
- **相对带宽自动调节**：离上限远 → band 大 → 极少推；接近上限 → band 收紧 → 精确。
- **尾部必须豁免**：`TrafficFloorBytes` 在 `used >= limit` 时返回 `1`，那是真正的掐断信号，**不能被 band 吞掉**，否则超额用户不会被停。
- **迟滞自然收敛**：skip 时面板留旧值，下周期继续与旧值比，漂移累积超过 band 才推一次——不会棘轮。
- **正确性代价**：PSP 离线窗口内最多多烧一个 band。该下限本就是「PSP 挂了别被无限白嫖」的兜底，替代品是**无限**，所以有界超烧远优于误伤停机。
- `minBytes` / `pct` 的取值**由 Phase 0 的分布决定**，不预设。

### 1b. 并行化 `SyncUserLifecycle` 的串行循环

`sharedclient.go:307` 改用现成的 `paneltz` 上限并发。P > 1 时把单用户延迟从 `P × 2` 个往返压到 `2` 个。

### 1c. 给 `pushSem` 加跨周期守卫

上周期未排空时跳过本周期推送并记一条 warn，把 §1.4 的雪崩降级为「掉一拍」。

**Phase 1 退出条件**：在真实 3.7.0 面板上，构造活跃用户跑多轮，实测推送次数从「每轮 N 次」降到「每轮 ≪ N 次」；skip 命中率显著上升；`go test ./...` 与两台面板的 `TestLive_*` 全绿。

---

## Phase 2 — 决策门：要不要自研数据面

用 Phase 0/1 的数据回答：**修完之后还剩多少痛？**

| 若…… | 则 |
|---|---|
| Phase 1 后余量充足，痛点主要是兼容税 | **不做自研后端**，把力气放到并行轨（compat CI） |
| Phase 1 后仍接近饱和，且已确认瓶颈在协议本身 | 进入 Phase 3 |

**决策时必须同时接受的代价**（不是效率问题，是所有权问题）：

- **换来**：数据模型所有权。v3.9.2-beta.7 修的四个静默清零（`limitHwid` / `resetDay` 组 / inbound 的 `total`、`subSortIndex` / 客户端的 `comment`、`group`）**没有一个是 PSP 写错代码**，全部源于与 3X-UI 整结构 Save 语义的阻抗失配。
- **换走**：xray-core 兼容责任。现在 3X-UI 替 PSP 挡住了 xray-core 的变化（例：26.7.11 把 `minClientVer` 空值默认从「不限」改成 `26.3.27`，直接让 mihomo/Clash Verge 连不上）。xray-core 发版比 3X-UI 频繁。
- **换走**：运维方在 3X-UI 界面上的操作习惯。上面那四个字段之所以会被清，正因为运维方在用那个界面。自研后端等于砍掉这个逃生口——字段要么搬进 PSP，要么消失。**这是产品决策，不是技术决策。**

**已有的有利条件**（若决定做）：

- PSP **已经是 inbound 配置的真相源**（[inbound-ownership.md](inbound-ownership.md)：自有 DB 存完整配置、订阅渲染零回源、reconcile 反向下发）。「生成什么配置」早就 PSP 说了算。
- 适配器 seam 已存在：`ports.PanelClient` 22 个方法 + 6 个可选能力接口，已有 xui / sui 两个实现。第三个实现是设计**本来就预留的**。
- 因此 agent 的职责比「重写 3X-UI」窄得多：**接收 PSP 已拥有的配置 → 生成 xray config → 管进程 → 上报计数器**。UI、用户体系、订阅、Telegram bot 全都不需要。

**先查再定**：3X-UI 3.7.0 自己已有 master/node 推送协议（`/inbounds/pushClientTraffics`、`node-sync` token scope、节点 mTLS、`/nodes/history/*`）。花半天搞清它的成熟度与协议形状——可能可复用，也可能绑死在它自己的 master 上。不查就自己造，风险是重复造一个明年被上游做得更好的东西。

---

## Phase 3 —（条件性）Agent

仅在 Phase 2 判定为「做」时进入。本节是**架构基线**，不是 MVP 清单——协议层的决定改起来最贵，越晚改越贵，所以先定死。

### 3.0 指导原则

工业界（Envoy xDS、kubelet ↔ apiserver）在这个问题上收敛出的答案**不是「推送替代轮询」**，而是：

> **声明式 + 水平触发 + 版本化快照。推送只是延迟优化，正确性靠周期性全量对账兜底。**

水平触发有个关键附带好处：**丢消息不致命**。xDS 和 kubelet 都不保证每条消息送达，靠「下一次对账会修好」。这免掉了「推送必须可靠投递」这个最难的工程问题——不需要消息队列，不需要 exactly-once。

### 3.1 必须在第一天定死的（协议层）

| # | 决定 | 理由 |
|---|---|---|
| 1 | **声明式，下行只有期望状态，没有命令** | 命令式 RPC 必须携带全量字段，而携带全量就必须知道所有字段——这正是 v3.9.2-beta.7 四个静默清零的结构性根源 |
| 2 | **版本化快照 + ACK/NACK** | 每份配置带单调递增 `generation`；agent 回报 `applied_generation`。控制面才能回答「哪台卡在哪个版本」 |
| 3 | **spec / status 分离 + 字段所有权** | 见 §3.2，这是本计划最重要的一条设计约束 |
| 4 | **Fail-static** | 控制面不可达时 agent 保持最后一份已知良好配置**继续服务**，绝不 fail closed |
| 5 | **反向连接**：agent 主动连控制面（gRPC 双向流） | 穿 NAT；且**节点不再需要暴露任何管理端口**——现在每台 3X-UI 都得把面板 HTTP 端口暴露给 PSP，那是实打实的攻击面 |
| 6 | **版本偏斜策略 + 能力协商** | 明文规定 agent 可落后控制面多少，写进文档。已有 `ports.CapabilityProvider` 先例可扩 |
| 7 | **短期凭据 + 轮换**，bootstrap 走 token → CSR → 签发 | 不要长期静态 token（就是现在 3X-UI API token 的形态） |

### 3.2 设计约束：字段所有权必须是**机制**，不是逐字段判断

v3.9.2-beta.7 修的四组静默清零（`limitHwid` / `resetDay` 组 / inbound 的 `total`、`subSortIndex` / 客户端的 `comment`、`group`）**没有一个是 PSP 写错代码**，全部源于与 3X-UI 整结构 Save 语义的阻抗失配。当时的修法是逐字段人工判断「pin 还是 preserve」，并写长注释解释理由——那是**在没有所有权模型时的手工替代品**。

K8s 的工业解法是 server-side apply 的 field manager：每个字段记录是谁写的，冲突显式暴露而不是静默覆盖。

**约束**：自研协议**不得**把逐字段人工判断搬过来。资源要显式划分所有权域：

- `psp-owned`：配额、到期、启用状态、凭据 —— 控制面写，agent 不碰
- `agent-owned`（status）：流量计数、健康、已应用版本 —— agent 写，控制面只读
- `operator-owned`：运维在本地设置的东西 —— **双方都只读**，apply 根本不触碰

判据：**一个新字段加进来时，不需要任何人做「pin 还是 preserve」的判断**——它属于哪个域决定了一切。做不到这点，说明所有权模型没设计对。

### 3.3 协议形状（草案）

```
agent ──(mTLS, gRPC 双向流, agent 发起)──> PSP

  上行  Register(node_identity, agent_version, capabilities)
  下行  ConfigSnapshot{generation, resources[], checksum}
  上行  Ack{generation} / Nack{generation, reason}
  上行  Status{applied_generation, xray_state, health}   ← 周期心跳，兼做 lease
  上行  TrafficReport{counters[]}                        ← 周期推送
```

- 心跳超时 → 控制面标记节点 unreachable，但 **agent 侧继续按旧配置服务**（§3.1-4）
- 周期性全量 resync（如 5 分钟）作为水平触发兜底——**这就是原来那个轮询，但它降级为安全网，不再是主路径**

### 3.4 留钩子、暂不实现

- **渐进式发布**：先做到「能指定单节点推送」即可，ring/canary/自动回滚以后再说
- **增量（delta）传输**：先全量快照，PSP 的配置体量小，不值得

### 3.5 明确不照搬

完整 xDS 三层（LDS/RDS/CDS/EDS）、多租户控制面分片、自研服务发现、agent 自动升级。

这些解决的是万节点 / 多租户 / 强监管的问题——**大厂那套的复杂度大部分是为规模和组织付的税，不是为正确性**。正确性来自 §3.1 那七条，它们在 10 个节点和 10000 个节点上同样必要，而且都不贵。

### 3.6 仓库与产物布局：**单仓**

判据（不是偏好，是可判定的规则）：

> **agent 是否只被我们自己的控制面驱动？**
> 是 → 单仓。否（要做成通用组件、被多个控制面驱动）→ 分仓。

Kubernetes 就是前者：apiserver 和 kubelet **同仓、同 tag**（`kubernetes/kubernetes`），配一份明文版本偏斜策略。Envoy 是后者，所以它独立于 Istio——因为 Envoy 是通用代理，有许多不同的控制面。

我们的 agent 是为 PSP 定制的，**不打算被第三方控制面驱动** → 单仓。

具体理由：

- **协议契约必须共享**。分仓要么引入第三个 proto 制品，要么 vendoring——两条都会制造新的版本对齐工作，而那正是本项目要消灭的东西
- **一次 CI 能把两侧对着测**。可以在一个 `go test` 里同时拉起控制面和 agent 做集成测试；分仓要跨仓 CI 编排
- **多二进制已是既有模式**：`cmd/` 下已有 `panel` / `dump-user` / `reset-admin-password`
- **发布链路已经合适**：`build` 是 6 平台 matrix，`docker` job **不在容器里编译**、只 COPY 预编译二进制。加一个产物≈ release.yml +10~15 行 + 一个 20 行 Dockerfile

**必须说清楚的一点**：单仓**不消除版本偏斜**。线上仍会出现「PSP v4.1 + 某台节点 agent v4.0」。单仓消除的是**源码偏斜**，并让**偏斜可测**（在一个 CI 里跑 N×M 版本矩阵）。所以 §3.1-6 的版本协商照做不误。

**保留拆分能力**：proto / 协议类型放在**不依赖 `internal/` 的公开包**（如 `api/agent/v1/`）。拆仓的成本取决于接缝干不干净——接缝干净时拆是低风险的，现在拆是给自己上难度。等出现真实理由（第三方要实现 agent、或两侧发布节奏确实分叉）再拆。

产物命名：

| 位置 | 名称 |
|---|---|
| Go 包 | `cmd/agent` |
| 二进制 | `psp-agent`（面板是 `psp`，前缀一致） |
| 镜像 | `ghcr.io/kazuhahub/passwall-agent`（面板是 `passwall-sub-panel`，同族可辨） |
| systemd unit | `passwall-agent.service` |
| 协议包 | `api/agent/v1`（拆分接缝） |

### 3.7 交付与共存

- 单节点试点，与 3X-UI **长期共存**（适配器架构本就支持：存量节点留 3X-UI，新节点上 agent）
- 配置生成复用 PSP 现有的 `inboundcfg`
- 退出条件：试点节点稳定运行一个完整计费周期，收敛状态可观测，fail-static 经过真实断网验证

---

## 并行轨 — 上游兼容 CI

**与 Phase 编号无关，可随时开始；且无论 Phase 2 结论如何都要做**——因为共存意味着 3X-UI 适配器会长期留存，就得长期测。

把 v3.9.2-beta.7 那次人工流程脚本化（每步都已实操验证过两遍）：拉上游 tag → stub `internal/web/dist` 后编译 → 按上游 `go.mod` 取对应 xray-core → `setting -getApiToken` 拿 token → 起面板 → 跑 8 个 `TestLive_*`。

- 触发：定时 + 手动 dispatch（可传上游 tag）
- 绿 → job summary 提示「可抬 `max_tested_xui`」；红 → 开 issue 附失败用例名
- 加一条**升级路径** leg：起旧版本 → 造状态 → 换二进制 → 起新版本 → 断言（这是全新安装测不出来的那类问题，API token scope 迁移就是这么发现的）
- 注：`PSP_LIVE_XUI_NO_GITHUB` 在 Actions 上**不需要**——runner 有外网，`getPanelUpdateInfo` / `getXrayVersion` 能拿到真数据

**收益**：把「每个上游 minor 花大半天」变成看一眼红绿灯，失败用例名直接定位契约变更。

---

## 明确不做的事

- **不为「推送替代轮询」这个理由做自研后端**——§1.1 已证明读侧不是瓶颈。
- **不在 Phase 0 完成前定 band 参数**。
- **不做一刀切迁移**。
- **不在 Phase 1 里顺手扩大范围**（例如 inbound 层的 `specToRaw` 已知问题另开）。

## 风险

| 风险 | 缓解 |
|---|---|
| Phase 0 插桩本身影响性能 | 计数器与直方图用无锁/采样实现，先在单面板验证开销 |
| band 取值过大导致离线超烧 | band 相对化 + 尾部豁免；上限由业务可接受的超烧额度反推 |
| Phase 1b 并行化引入对同一 client 的竞态 | 适配器已有 per-email 写锁（`lockClientEmail`）；并行粒度限制在**不同** client |
| 自研后端半途而废，留下两套半成品 | Phase 2 决策门显式化；Phase 3 以单节点试点为退出条件，不达标就回退 |
