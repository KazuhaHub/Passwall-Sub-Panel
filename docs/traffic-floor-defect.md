# 缺陷：流量下限的量纲不匹配（已修复）

> 状态：**已修复并实机验证。** 影响范围（修复前）：**所有设了流量上限的用户**；未设上限的用户从未受影响。
> 修复：`domain.PanelQuotaCap`，把「本期剩余」重基到面板自己的累计计数器上。回归守卫：`internal/adapters/xui/client_live_floor_matrix_test.go`（env 门控）。
> 关联：[ARCHITECTURE.md](ARCHITECTURE.md)、[3xui-compat.md](3xui-compat.md)、`internal/service/user/traffic_floor.go`。

## 1. 一句话

PSP 推给面板的是「**本期剩余**字节」，面板拿它去比「**终身累计**字节」。两者量纲不同，导致面板在用户远未耗尽配额时就把人停掉。

## 2. 证据链（四环，逐环核过源码）

| # | 事实 | 位置 |
|---|---|---|
| 1 | PSP 推 `total = 配额 − 本期已用` | `internal/service/user/traffic_floor.go`；`syncSharedLifecycle` → `SyncUserLifecycle` → `clientObj` 的 `"totalGB"` |
| 2 | 面板判定 `total > 0 AND up + down >= total` | 3X-UI `internal/web/service/inbound_disable.go:46` |
| 3 | `up/down` 是**累计**，写法 `SET up = up + ?` | 3X-UI `internal/web/service/inbound_traffic.go:171` |
| 4 | PSP **从不重置**面板计数器，且把面板自己的重置周期钉成 `never` / `resetDay = 0` | `panelRenewalOff`（`internal/adapters/xui/client.go`）；CHANGELOG「PSP 用内部 period baseline 计量，从不重置 3X-UI 计数」 |

`client_traffics.email` 带 `gorm:"unique"`，即**全 panel 每个 client 只有一行**——那个累计值是跨全部 inbound 的单一 per-client 聚合。

## 3. 实机复现

真实 3X-UI 3.7.0（源码编译）+ 真实 xray-core 26.7.28（`5ca6f4b`，3.7.0 `go.mod` 锁定的 commit），用 **PSP 自己的适配器**推送、**PSP 自己的 `TrafficFloorBytes`** 计算。测试见 `internal/adapters/xui/client_live_floor_test.go` 与 `client_live_floor_matrix_test.go`（env 门控，CI 默认跳过）。

配额 100 GB：

| 终身累计 | 本期已用 | 占配额 | PSP 推的 totalGB | 面板结果 |
|---|---|---|---|---|
| 45 GB | 45 GB | 45% | 55 GB | 正常 |
| 55 GB | 55 GB | **55%** | 45 GB | **停用** |
| 200 GB | **0 GB** | **0%** | 100 GB | **停用** |
| 70 GB | 70 GB | 70% | 30 GB | **停用** |

### 3.1 触发点正好是配额的一半

首期 `终身累计 == 本期已用`，面板条件 `lifetime >= limit − periodUsed` 坍缩成 `periodUsed >= limit/2`。45% 存活、55% 停用，与预测吻合。

**随客户端变老只会更早**：终身累计不断增长，而本期剩余每期归零重来。

### 3.2 新周期第一天、零使用，也会被停

第三行是最难看的形态：老客户在新计费周期开头一字节没用，PSP 推的是**全额配额**，但终身计数器早已压过它。这个用户在每个周期开头就被掐死。

### 3.3 会翻转，不是「掉一拍」

模拟 PSP 下一轮 `SyncLifecycle` 推 `enable=true` + 刷新的 floor：**面板在一个 sweep（`@every 5s`）内重新停用**。

所以不是「断 5 分钟」，而是**每个 5 分钟轮询周期里只有几秒钟是通的**。

## 4. 为什么没有大规模爆掉

只咬**设了流量上限的用户**。`limit <= 0` → PSP 推 `total = 0` → 面板条件 `total > 0` 不成立 → 永不触发。已实测确认。

## 5. 版本范围

**不是 3.7.0 回归。** ≤3.6.0 的判定多一个 `reset = 0` 前置守卫（`depletedClause`），但 PSP 从不设 `ClientSpec.Reset`（Go 零值 0），守卫对 PSP 客户端恒成立。3.4.2 → 3.7.0 行为一致。

> 这一条是**源码核对，未实机**——要实测得再编一台 3.6.0 面板。

## 6. 修复

`domain.PanelQuotaCap(headroom, panelLifetime)`：

```go
total = LastRawTotalBytes + TrafficFloorBytes(limit, periodUsed)   // headroom > 0
total = 0                                                          // headroom <= 0（无限）
```

`LastRawTotalBytes` 是 PSP 上一轮从面板读回的累计值，已经在 `PSPClient` / `XUIClientEntry` 上。面板于是恰好在「从现在起再烧掉剩余额度」时停用——**正是本来想要的语义**。

`trafficFloor` 的契约随之明确为**返回本期剩余（headroom），不是给面板的数**；三个推送点各自用自己那个 client 的计数器重基：共享模型走 `SyncLifecycle`，遗留 per-node 走 `pushClientConfigToAll` 与两处 UUID 轮换。reconcile 的四处调用传的是 `0`（无限），不受影响。

### 6.1 一个必须守住的边界

`headroom <= 0` 必须原样透传 0。面板把 `total == 0` 读作「无上限」；在这里叠加偏移会**凭空给没有配额的用户造出一个配额**——正好是反向的同一个缺陷。已有单测钉住。

### 6.2 耗尽哨兵的语义变化

`TrafficFloorBytes` 把「已达/超过上限」编码为 headroom = 1，重基后是 `lifetime + 1`。于是面板对已耗尽的客户端是**一旦再有任何流量就切**，而不是在它静止于上限时就切。

这对一个「PSP 离线期间限制滥用」的兜底来说是**正确的形状**：静止的超额用户不消耗任何东西，而它一旦消耗，就在一个 traffic tick（`@every 5s`）内被切掉。为了多切一个静止用户而去特判另一个包的哨兵编码，不值得。

### 6.3 顺带解决了推送效率问题

重基后，**推给面板的值对活跃用户是恒定的**：

```
cap = lastRaw + (limit − periodUsed)
```

面板计数器涨多少，本期已用就涨多少，剩余就减多少——**两项相消，和不变**。实机验证：连续四轮把终身流量从 10 GB 推到 31 GB，面板持有的 `totalGB` 始终是 100 GB。

这直接影响 [data-plane-plan.md](data-plane-plan.md)：§1.2 断言「不断收缩的下限每周期都挫败 no-op skip」——**那个收缩正是缺陷本身**。修好之后 skip 对单 client 用户应当自然生效，**Phase 1a 的迟滞带可能根本不需要**。

**但 `P > 1` 时完全不成立——这一条我先前推断错了，是实测推翻的。**

`internal/service/sharedclient/skip_rate_test.go` 跑 50 个周期实测：

| P | skip 率 | 修复前 |
|---|---|---|
| 1 | **98%**（50 周期 1 次写） | 0% |
| 2 | **0%** | 0% |
| 3 | **0%** | 0% |

我原本写的是「偏移量远小于修复前的整个 delta，skip 命中率仍会大幅上升」。**算一下就知道不对**：

```
cap_i = lastRaw_i + (配额 − Σ所有 client 的已用)

每周期：lastRaw_i 涨 δ_i，而 headroom 缩 Σδ
        ⇒ cap_i 变化 = δ_i − Σδ = −Σ_{j≠i} δ_j
```

**只要有任何一个别的 client 动了流量，cap_i 就变。** 所有 client 都活跃时，每个 client 的 cap 每周期都变——skip 一次都不命中，和修复前一样糟。

### 6.3.1 为什么不能靠改公式解决

看起来把 headroom 也改成 per-client（`配额 − 本 client 已用`）就恒定了。**但那会削弱兜底本身**：

| 方案 | 单 client 可再烧 | P 个 client 同时烧（PSP 离线）|
|---|---|---|
| 现行 | `配额 − Σ已用` | `P × (配额 − Σ已用)` |
| per-client headroom | `配额 − 本client已用` | `P × 配额 − Σ已用` ← **更松** |

现行方案更紧。所以**这个漂移是安全语义的必然代价，不是实现缺陷**。

### 6.3.2 结论：Phase 1a 对 `P > 1` 仍然必要

- **P = 1**：skip 已自然生效（98%），迟滞带**不需要**
- **P > 1**：skip 结构性失效，**迟滞带是正确的工具**——和 [data-plane-plan.md](data-plane-plan.md) 原本的判断一致

而且 band 该设多大现在更清楚了：client i 每周期的漂移就是**其他 client 那一周期的流量总和**。band 只要盖过典型的单周期流量，skip 就能恢复。真实分布仍由 Phase 0 的 `psp_lifecycle_quota_delta_bytes` 给出——现在知道该盯着 P>1 的用户看。

### 6.4 已知代价

面板计数器若被重置（xray 重启、管理员手动重置），下限会偏松，直到下一轮推送自动修正。**有界且自愈**，且方向是安全的那一侧（宁可少停，不可误停）。

### 6.5 未采纳：每期重置面板计数器

PSP 在周期滚动时调面板的重置接口，然后推 `total = 配额`，使面板计数器与本期用量对齐。

**不采纳**：多一次往返，且重置是破坏性操作——若 PSP 自己的计量落后于那次重置，这一期的用量会被吞掉。PSP 的 `monotonicDelta` 能扛住计数器倒退，但没有理由主动制造这种局面。

## 7. 对数据面演进计划的影响

见 §6.3：修复很可能**顺带解决了 Phase 1a 想解决的问题**。重基后推送值对活跃用户恒定，no-op skip 应当自然生效——迟滞带在拿到 Phase 0 数据前不应开工，且届时可能已无必要。Phase 1b / 1c 与本缺陷无关，已实现。
