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

## Phase 3 —（条件性）Agent MVP

仅在 Phase 2 判定为「做」时进入。最小范围：

- client CRUD + 计数器**推送**上报 + 健康上报
- 配置生成继续复用 PSP 现有的 `inboundcfg`
- 单节点试点，与 3X-UI **长期共存**（适配器架构本就支持：存量节点留 3X-UI，新节点上 agent）
- 安全面：mTLS + 重放保护（PSP 已有 SAML 重放保护的先例可参考）

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
