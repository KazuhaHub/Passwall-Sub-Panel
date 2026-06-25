# v3.9.0 共享 client 迁移 —— V4 清理指南

> 目的：把 v3.8(每节点一个 client / ownership 表)→ v3.9(每面板一个共享 client /
> `psp_client`)的**过渡期代码**集中登记,让将来 V4 移除遗留路径时**一处可查、不易漏**。

## 背景

- **v3.8(遗留)**:`user_xui_clients` 表(GORM `ownershipRow` / `domain.XUIClientEntry`),
  每个 (user, node) 一行,经 `ports.OwnershipRepo` 访问。
- **v3.9(当前)**:`psp_client` + `psp_client_inbound`(`ports.PSPClientRepo`),每个用户每面板
  一个共享 client,跨多入站;渲染从 UUID 推导每节点凭据。
- **运行时**:迁移完成后 `user_xui_clients` 会被 `DropIfMigrated` **物理删除**;此后
  `ownership_repo` 的查询**静默返回空**(不报错)。

## 状态拆分迁移记录

v4 前已经把「账号是否能登录面板」和「代理/订阅服务是否可用」拆成两层:

- **账号状态**:`enabled` + `auto_disabled_reason` + `disable_detail`,只负责面板登录 /
  审批 / 删除 / 邮箱验证等账号级状态。
- **服务状态**:`service_disabled_reason` + `service_disable_detail` +
  `service_disabled_at`,只负责代理访问、订阅输出和 3X-UI client enable。

本次不做历史禁用账号的自动搬迁:

- 旧数据里已经 `enabled=false` 的账号保持原样,不会在启动时自动改成服务暂停。
- `service_disabled_*` 列只承接新逻辑之后产生的到期、流量超限、客户端封禁、手动暂停等服务状态。
- 如需恢复历史账号,由管理员在后台按实际情况手动启用账号或调整服务状态。

后续清理注意:

- 自动封禁客户端只暂停服务,不再禁用账号。
- 客户端封禁恢复时通过 `ResumeServiceAndSync` 自动清空 `block_violation_count`。
- 管理员后台应展示和管理服务状态,不能再把「禁用账号」当作所有停用场景的唯一入口。

## 约定:`MIGRATION(v3→v4)` 标记

每一处**只为过渡期存在、V4 该删/该简化**的代码,都带注释标记:

```go
// MIGRATION(v3→v4): <说明 + 移除动作>
```

V4 清理第一步就是:

```bash
grep -rn "MIGRATION(v3→v4)" internal
```

标记是**唯一事实来源**(不维护行号清单,避免漂移)。下面只列**类别 + 移除配方**。

## V4 移除配方(按类别)

### 1. 遗留 ownership 表 + 仓库(靠编译器兜底)

删除 `ports.OwnershipRepo` 接口 + 适配器 `internal/adapters/sqlstore/ownership_repo.go`
+ `ownershipRow`/`user_xui_clients` schema(`schema.go`)+ `domain.XUIClientEntry`。
**删掉接口后,编译器会逐个报出所有调用点** —— 它们全是遗留每节点逻辑,挨个删即可。
这类**不需要**手动标记(类型系统就是登记表)。

涉及的遗留 sync 原语随之一起删:`DelAllOwnedForUser` / `DelAllOwnedForInbound` /
`ClaimClient` 的 `ownership.Add` / 每节点 `RotateClientUUID` / 每节点 `AddClient`·`UpdateClient`。

### 2. 一次性迁移逻辑(带标记)

- `user.Service`:`EnqueueSharedMigration`、`BackfillPSPClients`、`SharedMigrationComplete`、
  `DeleteLegacyForUser`。
- `domain.SyncTaskUserMigrate` 任务类型 + 其处理分支(`runUserTask` / `ProcessDueTasks`)。
- `app.go`:开机的迁移入队 + 开机 heal;reconcile 循环里的 `migrationComplete` 分支 +
  `shouldRunSharedHeal` 的「migrating → 每 tick」分支(改为永远走 backstop 节奏)+
  `sharedHealBackstopEvery`。
- `ownership_repo.go`:`DropIfMigrated`、`gone` 标记、`isMissingTableErr`。

### 3. 读路径里的「psp_client 否则 ownership」回落分支(⚠️ 最易漏,务必带标记)

这些分支**删掉 ownership 仓库后不会编译报错**(它们还有 psp_client 分支会留下),所以**必须**
靠 `MIGRATION(v3→v4)` 标记找到,把 ownership 回落删掉、只留 psp_client 路径:

- `sync.go` `ensureInboundDeletable` —— ownership.Exists 否则 psp。
- `node.go` `ListClientsOfInbound` —— ownership 否则 psp 解析 owner。
- `admin_node.go` `ClaimClient` —— `preExistingOwned` 同时数 ownership + psp。
- `traffic.go` `PollOnce` 的 `panelsToFetch`(ownership ∪ psp 面板并集);`UserServerUsage`
  的「无 psp_client 时回落到 ownership 聚合」分支。

### 4. 已经清理干净的(无需动作)

`user_me.go` `ServerStatus`(B5 已改为走 group 选择器,不再读 ownership)。
渲染 / 订阅 Userinfo 头 / SSO / 仪表盘 / top-users / rollup —— 本就不依赖 ownership。

## V4 清理 checklist

1. `grep -rn "MIGRATION(v3→v4)" internal` —— 处理每一处(类别 2、3)。
2. 删 `ports.OwnershipRepo` + 适配器 + schema + `XUIClientEntry`,跟着编译错误删干净(类别 1)。
3. 删 `internal/migrate` 里仅服务 v2→v3 的部分(若那时也一并退役)。
4. `go build ./... && go vet ./... && go test ./...` 全绿。
5. 删除本文件。
