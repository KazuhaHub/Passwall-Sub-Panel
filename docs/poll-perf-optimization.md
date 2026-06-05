# Traffic Poll 性能优化计划(v3.5.0-beta.9)

> 状态:**已规划,未实现**。compact 后照此文档接续。
> 触发:管理员"Poll Now"在 ~小几十用户 × 平均几 client 的体量下耗时 ~10s。

## 1. 问题

用户实测手动 Poll 一次 ~10 秒。Phase 1(每 panel 并行拉 `ListInbounds`)已经并发(`MaxPanelConcurrency` 默认 8、上限 64),不是网络瓶颈。**真正的慢在 Phase 2 + recordAndEnforceWith 的 per-user / per-client 串行 DB 写**——尤其 SQLite WAL 每次 commit ~5–10ms。

## 2. 当前热路径(已读代码确认)

`internal/service/traffic/traffic.go`,每轮 `PollOnce` 在 N 用户 × M 平均客户端下的标准工作量:

| 调用 | 位置 | 频次 | 每次成本(SQLite) |
|---|---|---|---|
| `ownership.ListByUser` | 第 203 行 | N(每用户 1) | ~1ms SELECT |
| `traffic.LatestForUser` | 第 588 行(`recordAndEnforceWith`) | N | ~1ms SELECT |
| **`ownership.UpdateCounters`** | 第 529 行(`recordClientStats`) | **N × M** | **~5–10ms UPDATE,最大头** |
| **`users.UpdateTrafficState`** | 第 668 行 + 第 704 行(rollover 分支) | **N**(主路径) | **~5–10ms UPDATE** |
| `disabler.SetEnabledAndSync` | 第 721 / 772 行 | 仅状态变化时 | 不在热路径,可忽略 |
| Snapshot writes | 已用 sink 末尾批量 InsertBatch | 三个 batch | 已优化,不动 |

例:60 用户 × 5 client = 60 + 300 = **360 次串行 UPDATE × ~10ms ≈ 3.6 秒**,加 SELECT + 启动开销可解释 10s。

## 3. 设计:三个新批量方法 + sink 末尾 flush

让 `PollOnce` 在 user 循环里**只往 sink 里追加待写对象**,循环结束后用 3 个 BATCH 方法各**一次** DB 调用 flush。

### 新接口(写在 `internal/ports/repos.go`)

```go
// UserRepo 加:
BatchUpdateTrafficState(ctx context.Context, users []*domain.User) error

// OwnershipRepo 加:
BatchUpdateCounters(ctx context.Context, items []*domain.XUIClientEntry) error

// TrafficRepo 加:
LatestForUsers(ctx context.Context, userIDs []int64) (map[int64]*domain.TrafficSnapshot, error)
```

### MySQL 实现策略(三种 DB 都走得通)

**Batch 写**:GORM 事务包裹 N 条 UPDATE。SQLite 下 N 次自动提交合并为 1 次提交——这是核心收益。

```go
func (r *userRepo) BatchUpdateTrafficState(ctx context.Context, users []*domain.User) error {
    if len(users) == 0 { return nil }
    return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
        for _, u := range users {
            err := tx.Model(&userRow{}).Where("id = ?", u.ID).Updates(map[string]any{
                "lifetime_up_bytes":      u.LifetimeUpBytes,
                "lifetime_down_bytes":    u.LifetimeDownBytes,
                "lifetime_total_bytes":   u.LifetimeTotalBytes,
                "period_baseline_bytes":  u.PeriodBaselineBytes,
                "traffic_period_start":   u.TrafficPeriodStart,
                "lifetime_baseline_at":   u.LifetimeBaselineAt,
            }).Error
            if err != nil { return err }
        }
        return nil
    })
}
```

(列名以现有 `UpdateTrafficState` 的真实实现为准,实现时 grep `user_repo.go` 复用列清单,不要漏。)

`BatchUpdateCounters` 同款,列写 `lifetime_*_bytes` + `last_raw_*_bytes`(以现有 `UpdateCounters` 为准)。

**Batch 读(LatestForUsers)**:用 subquery + IN,一次 SQL 拿全部用户的最新 snapshot:

```go
func (r *trafficRepo) LatestForUsers(ctx context.Context, ids []int64) (map[int64]*domain.TrafficSnapshot, error) {
    if len(ids) == 0 { return map[int64]*domain.TrafficSnapshot{}, nil }
    var rows []trafficRow
    err := r.db.WithContext(ctx).Raw(`
        SELECT t.* FROM traffic_snapshots t
        INNER JOIN (
            SELECT user_id, MAX(captured_at) AS mca
            FROM traffic_snapshots
            WHERE user_id IN ?
            GROUP BY user_id
        ) m ON t.user_id = m.user_id AND t.captured_at = m.mca
    `, ids).Scan(&rows).Error
    if err != nil { return nil, err }
    out := make(map[int64]*domain.TrafficSnapshot, len(rows))
    for i := range rows {
        d := rows[i].toDomain()
        out[d.UserID] = d
    }
    return out, nil
}
```

跨方言 OK(SQLite / MySQL / Postgres 都支持 `IN ?` 和子查询)。命中现有 `idx_user_time (user_id, captured_at)` 索引。

## 4. `PollOnce` refactor 步骤

在 `internal/service/traffic/traffic.go`:

### 4.1 `pollSink` 扩展

```go
type pollSink struct {
    userSnaps        []*domain.TrafficSnapshot       // 已有
    clientSnaps      []*domain.ClientTrafficSnapshot // 已有
    nodeSnaps        []*domain.NodeTrafficSnapshot   // 已有
    // 新增:
    ownershipUpdates []*domain.XUIClientEntry // 末尾 BatchUpdateCounters
    userUpdates      []*domain.User           // 末尾 BatchUpdateTrafficState
}
```

### 4.2 Pre-fetch latest snapshots(第 198 行附近)

在构建 `byInbound` map 的同时收集 `allUserIDs`,然后:
```go
latestByUser, _ := s.traffic.LatestForUsers(ctx, allUserIDs)
```
传进 `recordAndEnforceWith`(扩参数或挂在 sink 上)。

### 4.3 `recordClientStats`(第 487 行起)

第 529 行:
```go
if err := s.ownership.UpdateCounters(ctx, ownership); err != nil { ... }
```
改成:
```go
sink.ownershipUpdates = append(sink.ownershipUpdates, ownership)
```
(沿用 sink 已有"调用方 nil 检查则 fallback 同步"风格,见 line 545–548 `sink.clientSnaps` 的处理。)

### 4.4 `recordAndEnforceWith`(第 572 行起)

第 588 行 `LatestForUser` → 改为从预取的 `latestByUser[u.ID]` 拿。

第 668 + 704 行两处 `s.users.UpdateTrafficState(ctx, u)` → 改为:
```go
sink.userUpdates = append(sink.userUpdates, u)
```
注意:`u` 是同一指针,两处都追加会重复——可在函数末尾**只追加一次**(用 `userDirty bool` 标记任一修改后)。或在 sink flush 时按 ID 去重(map[int64]*User 收集,后者覆盖前者)——简单起见用 sink 内 map。

### 4.5 末尾 flush(第 391 行附近 sink 当前 flush 块)

紧跟现有 3 个 InsertBatch:
```go
if len(sink.ownershipUpdates) > 0 {
    if err := s.ownership.BatchUpdateCounters(ctx, sink.ownershipUpdates); err != nil {
        log.Warn("traffic poll flush ownership counters", "count", len(sink.ownershipUpdates), "err", err)
    }
}
if len(sink.userUpdates) > 0 {
    if err := s.users.BatchUpdateTrafficState(ctx, sink.userUpdates); err != nil {
        log.Warn("traffic poll flush user traffic state", "count", len(sink.userUpdates), "err", err)
    }
}
```

## 5. 测试

- **`mysql/user_repo_test.go`** / **`ownership_repo_test.go`** / **`traffic_repo_test.go`** 各加一个 batch 方法测试:
  - 用 SQLite 测试 DB(`Open("sqlite", filepath.Join(t.TempDir(), "panel.db"))`,见 `audit_repo_search_test.go:17` 的现成 helper 模式);
  - 插几行,调 batch 方法,assert 全部更新成功 + 没动其他列。
- **`traffic/traffic_test.go`** 已有 PollOnce 测试(grep `func Test.*Poll`)。可加一个 perf 行为测试:N 个用户 × M client,调 `PollOnce`,asserts `fakeRepo` 的 `BatchUpdateCounters` 被调 1 次(而不是 N×M 次)、`BatchUpdateTrafficState` 1 次。

### 现有 fake repo 需要扩

`internal/service/traffic/traffic_test.go` 里的 fake 实现需要补 3 个新方法(返回 nil 或记录调用,看测试需要)。Grep `fakeTrafficRepo` / `fakeOwnership` / `fakeUserRepo`(可能名字略不同)。

## 6. 期望收益

| 体量(N user × M client) | 优化前 | 优化后(SQLite) |
|---|---|---|
| 30 × 4 = 150 ops | ~1.5s | ~50ms |
| 60 × 5 = 360 ops | ~4s | ~100ms |
| 100 × 8 = 900 ops | ~10s | ~200ms |
| 500 × 10 = 5500 ops | ~55s | ~500ms |

SQLite 一次 commit 的固定开销远大于 N 条语句本身。MySQL/PG 也有收益(round-trip 减少)但没 SQLite 那么夸张。

## 7. 落地顺序(给下一回合的 me)

1. 加 3 个接口到 `internal/ports/repos.go`(对应 §3)。
2. 在 mysql adapter 加实现 + 测试:
   - `internal/adapters/mysql/user_repo.go` + `user_repo_test.go`
   - `internal/adapters/mysql/ownership_repo.go` + `ownership_repo_test.go`
   - `internal/adapters/mysql/traffic_repo.go` + `traffic_repo_test.go`
3. **不要忘了** `internal/service/traffic/traffic_test.go` 里的 fake repo 实现 3 个新方法(否则 traffic 包测试编译失败)。
4. refactor `PollOnce` per §4。
5. 加 PollOnce perf 行为测试(§5)。
6. `go build / vet / test ./...` 全绿。
7. CHANGELOG `v3.5.0-beta.9` 一段 Changed:traffic poll 末尾批量 flush,SQLite 实测从 ~10s 降到 < 200ms。
8. commit / tag / push 同前 betas 套路。

## 8. 风险/边界

- **事务范围**:每个 batch 方法内一个事务(N 条 UPDATE)。不是整个 PollOnce 一个事务——那样跨 repo,改动太大。每批一个事务也已够把 N 次 commit 降到 1 次。
- **数据一致性**:flush 失败 → 这一轮 ownership 计数 / user lifetime 不更新,下一轮重新累计(`LastRawXxx` 没推进意味着 monotonicDelta 仍能算对增量,只是同一批 delta 会被重算一次。可接受——本来 PollOnce 失败就是这个语义)。
- **sink 内重复 user**:`recordAndEnforceWith` 主路径 + rollover 分支都会改 `u`。用 map dedup 或末尾 only-once 追加。**确保 flush 时一个 user 只 UPDATE 一次**。
- **现有 inline 方法保留**:`UpdateCounters` / `UpdateTrafficState` / `LatestForUser` 不删——其他调用点(如 `userMe.go` chart query)仍用。批量是 traffic poll 专用。

## 8.5. 实现后审计发现(beta.9 落地后补)

### A. **Rollover 路径必须保持 inline-write**(已修)

§4.4 提出"rollover 分支两次 `UpdateTrafficState` 都入 sink、用 map 去重"——实现后审计发现这是回归:`SetEnabledAndSync(true)`(rollover-reenable)随即做 `GetByID + Update + pushClientConfigToAll`,如果 sink 还没 flush,disabler 看到 OLD lifetime/periodStart/baseline,`u.PeriodUsed()` 算成"OLD 周期接近用满",`floor = limit - used ≈ 0` 被推到 3X-UI——用户表面 re-enable,实际仍被阻断,直到下一轮 poll(~5 分钟)。

修复:`persistRollover` 改回 inline 写 + 从 sink 里 delete 这个 user(避免末尾 batch 重复写)。ClearEmergencyAccess 仍 inline,锁语义不变。每轮 poll rollover 通常 0-1 个用户(monthly),性能成本可忽略。

回归测试:`TestPollOnceRolloverWritesSynchronouslyForDisablerReread` 注入一个 `capturingDisabler`,在 `SetEnabledAndSync(true)` 调用时实读 fake repo 拿 `PeriodUsed()`,断言它接近"this-cycle-delta"而不是"limit"。stash 验证过 pre-fix 报 `PeriodUsed = limit`(完美命中 stale-read 症状)、post-fix 通过。

### B. **常规 safety-net floor push 仍滞后一个周期**(接受,文档化)

[traffic.go](../internal/service/traffic/traffic.go) 末尾 `s.configPusher.PushClientConfig(ctx, u.ID)` 也走 `GetByID`,看到的 lifetime 比内存少 `this_cycle_delta`,floor 偏大同等数量。下一轮自纠。

这个 push 的设计语义是"面板长时间掉线时,3X-UI 自己当兜底门"——5 分钟级别的 floor 滞后不影响这个安全属性。改修需要给每个用户多一次 sync 写,得不偿失。**不修。**

## 9. 不在本次范围

- 把 `ListByUser`(line 203 预循环)批量化(N SELECT)。读 SELECT 比写快得多,收益边际,留待将来。
- 把整个 PollOnce 包一个跨 repo 的事务——需要 tx-aware context propagation,改动太大,本批量化已能消除 95% 收益,不必做。
- MySQL/Postgres 专属的 INSERT ON DUPLICATE KEY UPDATE / `UPDATE ... CASE WHEN` 单语句——跨方言难写,事务包裹的 N 条 UPDATE 已足够快。
