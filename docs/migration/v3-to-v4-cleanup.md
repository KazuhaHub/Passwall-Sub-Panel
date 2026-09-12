# v3.9.0 共享 client 迁移 —— V4 清理指南

> 目的：把 v3.8(每节点一个 client / ownership 表)→ v3.9(每面板一个共享 client /
> `psp_client`)的**过渡期代码**集中登记,让将来 V4 移除遗留路径时**一处可查、不易漏**。
> 本文也顺手登记其它明确标注为「next major / v4.0.0 删除」的兼容代码。

## 本次落实范围（v4.0.0-beta.2）

当前源码已落实**数据库历史迁移收敛与 CLI 退休**，不是把本文所有历史候选项一次性删除：

- `EnsureSchema` 在 `AutoMigrate` 前只读验证最终 V3 语义基线；更旧或未完成转换的库先成功启动 `v3.9.2` / `v3.9.2-beta.20`。
- 删除 V3 separator 旧数据重解释和额度 `0→NULL` 转换；必要 V3→V4 状态/凭据/旧索引桥接集中执行，保留 beta1 子步骤标记，以 `v3_to_v4_baseline_v1` 记录统一完成，后续不重复桥接。
- 删除 V2→V3 专用 `internal/migrate`；旧 `psp migrate` 和未消费 positional 参数在配置/数据库初始化前明确拒绝。V3→V4 正常启动自动迁移。
- 新装初始化以 `v4_schema_initializing_v1` 支持空库 DDL 失败重试；流量计数 NULL 修复作为当前数据修复继续保留。

**Ownership 及 shared-client 上游收敛仍是必要路径，本次未删除类别 1–3。** 结构基线不能证明离线或未完成迁移的上游客户端已经应用；不能仅为了清理代码提前删除旧客户端、跳过生命周期同步或伪造完成确认。类别 5 的旧订阅客户端设置兼容也不在本次清理范围内。

已发布的 `v4.0.0-beta.1` 标签/镜像不会被重写，本节清理从 `v4.0.0-beta.2` 起生效。历史代码保留在冻结 V3 和 Git 中；部署及回退见 [V4 升级指南](../UPGRADE-v4.md)。本清理登记不涵盖 UI 调整，同版安装向导另见[发行说明](../releases/v4.0.0-beta.2.md)。

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
- `service_disabled_*` 是新状态模型的长期字段,不是 V4 临时迁移字段,不要清理。

## AutoMigrate 边界

`AutoMigrate(schemaModels...)` 只负责创建/补齐当前模型需要的表和列,**不会自动删除**旧表、
旧列、旧索引或旧数据。V4 清理必须显式执行:

- `DropTable` / `DropColumn` / `DropIndex`,或者
- 通过正常启动中的有界 V3→V4 桥接处理支持基线仍必要的变化，不另设 V4 `psp migrate`。

因此,删除 `ownershipRow` 或从 `schemaModels` 移除某个模型,只代表新装不再创建它;升级库里已经存在的
表/列仍要靠明确的迁移或清理代码处理。

## 约定:`MIGRATION(v3→v4)` 标记

每一处**只为过渡期存在、V4 该删/该简化**的代码,都带注释标记:

```go
// MIGRATION(v3→v4): <说明 + 移除动作>
```

V4 清理第一步就是:

```bash
rg -n 'MIGRATION\(v3→v4\)' internal
```

标记辅助检索(不维护行号清单,避免漂移)；是否能够删除仍以当前代码、安全不变量和测试为准。以下类别 1–3 是**待评估配方，不是本次已完成项**。

## V4 移除配方(按类别)

### 1. 遗留 ownership 表 + 仓库（暂保留）

本次保留。只有具备覆盖未完成上游迁移的替代收敛路径和确认依据后，才可以执行以下历史配方；编译通过不能证明上游客户端已安全清理。

删除 `ports.OwnershipRepo` 接口 + 适配器 `internal/adapters/sqlstore/ownership_repo.go`
+ `ownershipRow`/`user_xui_clients` schema(`schema.go`)+ `domain.XUIClientEntry`。
**删掉接口后,编译器会逐个报出所有调用点** —— 它们全是遗留每节点逻辑,挨个删即可。
这类**不需要**手动标记(类型系统就是登记表)。

涉及的遗留 sync 原语随之一起删:`DelAllOwnedForUser` / `DelAllOwnedForInbound` /
`ClaimClient` 的 `ownership.Add` / 每节点 `RotateClientUUID` / 每节点 `AddClient`·`UpdateClient`。

### 2. shared-client 上游迁移逻辑（暂保留，带标记）

- `user.Service`:`EnqueueSharedMigration`、`BackfillPSPClients`、`SharedMigrationComplete`。
- `domain.SyncTaskUserMigrate` 任务类型 + 其处理分支(`runUserTask` / `ProcessDueTasks`)。
- `sharedclient.Service`:`DeleteLegacyForUser`、`SetOwnershipRepo` 及其仅为删 legacy per-node
  client 服务的 ownership 依赖。(原 `MigrateUser` 一次性 helper 已在 v3.9.0 删除——迁移由
  `user.ResyncMembership` 的 provision→lifecycle→删 legacy 安全顺序驱动,不再有独立 helper。)
- `app.go`:`sharedMigratorAdapter`、`SetOwnershipRepo`、`SetSharedMigrator`、开机迁移入队、
  `DropIfMigrated` 轮询、开机 heal;reconcile 循环里的 `migrationComplete` 分支 +
  `shouldRunSharedHeal` 的「migrating → 每 tick」分支(改为永远走 backstop 节奏)+
  `sharedHealBackstopEvery`。
- `ownership_repo.go`:`DropIfMigrated`、`gone` 标记、`isMissingTableErr`。

### 3. 读路径里的「psp_client 否则 ownership」回落分支（暂保留）

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

### 5. 旧订阅客户端设置兼容(非 shared-client,但也是 V4 清理)

v3.3.0 把旧的 `sub_client_rules` + `sub_import_clients` 合并为 `sub_clients`。为了升级兼容,
当前仍保留一次性折叠逻辑:

- `internal/adapters/sqlstore/sub_clients_legacy.go` 整个文件。
- `ports.UISettings.SubClientRules` / `ports.UISettings.SubImportClients` 两个 deprecated 字段。
- `settings_kv_repo.go` 中 `sub_client_rules` / `sub_import_clients` 的 legacy KV descriptor。
- 对应测试:`internal/adapters/sqlstore/sub_clients_legacy_test.go` 以及 settings 默认值测试中对
  deprecated 字段的断言。
- 文档引用:`docs/ARCHITECTURE.md` 中 sub settings 的 v3.3.0 兼容说明。

本次未改动这些兼容路径。后续若清理，需明确旧 KV 数据的承接规则及测试，不假定每个部署都曾运行过某个小版本；V4 不提供新的 `psp migrate` 子命令。

### 6. 数据库历史迁移收敛（本次已落实）

已删除当前源码中的 V3 `cleanupLegacyState` 及额度三态 `0→NULL` 转换，不将全部历史规则搬进新 CLI：

- `schema_baseline.go` 只读检查最终 V3 必要表/列、额度三态及转换标记，拒绝未完成的 V3 separator 形态。
- `schema_upgrade_v4.go` 集中必要的 client 身份索引、attachment 应用状态/凭据快照、endpoint 期望/观测拆分和退休列删除；保留旧子步骤标记，完成后写 `v3_to_v4_baseline_v1`。
- 最终 V3 对 `idx_sub_logs_user_id`、`idx_sub_logs_accessed_at`、`idx_users_email` 的删除是 best-effort，因此 V4 在桥接中一次性规范化固定残留集合。
- `repairTrafficCounterNulls` 是持续数据修复，角色种子是当前不变量维护，均不作为历史迁移删掉。
- DDL/转换有明确重试边界，不承诺全库自动回滚；只显式删除登记的应用列/索引，但数据库删列本身可能影响自定义依赖对象，须先检查并试迁移，见[升级指南](../UPGRADE-v4.md)。

## 当前状态与后续 checklist

- [x] 数据库历史小步迁移收敛、最终 V3 只读基线与必要 V3→V4 桥接。
- [x] 删除 V2→V3 专用包，旧 CLI fail-closed 保护，并同步当前升级指引。
- [ ] 类别 1–3：在具备替代上游收敛与迁移确认测试后再评估删除；本次保持必要路径。
- [ ] 类别 5：旧订阅客户端设置兼容另行评估，未冒称已清理。
- 本次变更纳入 `v4.0.0-beta.2`；发布前必须通过跨数据库 CI，已发布 beta1 不修改。

本文继续保留为边界与待办登记，不因局部清理完成而删除整张清单。
