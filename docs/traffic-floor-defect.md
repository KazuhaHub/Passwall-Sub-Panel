# 缺陷：流量下限的量纲不匹配（实机确认，未修复）

> 状态：**已实机确认，尚未修复。** 影响范围：**所有设了流量上限的用户**；未设上限的用户完全不受影响。
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

## 6. 修复方向

### 推荐：推「面板终身计数 + 本期剩余」

```go
total = LastRawTotalBytes + TrafficFloorBytes(limit, periodUsed)
```

`LastRawTotalBytes` 就是 PSP 上一轮从面板读回的累计值，已经在 `PSPClient` 上。这样面板恰好在「从现在起再烧掉剩余额度」时停用——**正是本来想要的语义**。

三个支持点：

- 每个 client 在面板上只有一行计数（§2），正好对应 PSP 的 per-client 聚合值，不用按 inbound 拆
- 不需要任何面板侧破坏性操作
- 不增加往返，数据 PSP 手上就有

**已知代价**：面板计数器若被重置（xray 重启、管理员手动重置），下限会偏松，直到下一轮推送自动修正。**有界且自愈**，方向也是安全的那一侧（宁可少停，不可误停）。

### 备选：每期重置面板计数器

PSP 在周期滚动时调面板的重置接口，然后推 `total = 配额`。使面板计数器与本期用量对齐。

**不推荐**：多一次往返，且重置是破坏性操作——若 PSP 自己的计量落后于那次重置，这一期的用量会被吞掉。PSP 的 `monotonicDelta` 能扛住计数器倒退，但没有理由主动制造这种局面。

## 7. 对数据面演进计划的影响

[data-plane-plan.md](data-plane-plan.md) 的 **Phase 1a（推送迟滞带）在此修复前不应开工**——band 是在给这个下限值加容差，而这个值当前的量纲本身就是错的。Phase 1b / 1c 与本缺陷无关，已实现。
