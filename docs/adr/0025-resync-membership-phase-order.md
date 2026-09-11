# ADR 0025：`ResyncMembership` 的相位顺序是越权防线，不是流程顺序

- **状态**：已接受（追认既有决策；实现自 v3.9.0-beta.4 的 audit #1 修复起）
- **日期**：2026-08-14
- **相关代码**：`internal/service/user/user.go`（`ResyncMembership`、`syncSharedLifecycle`、`deletePrunedSharedClients`）、`internal/service/sharedclient/sharedclient.go`（`ProvisionClient`、`buildSharedClientSpec`、`DeleteLegacyForUser`、`ReconcileOrphans`）

## 背景

`user.ResyncMembership` 是"把一个用户在 3X-UI 上的客户端调成它该有的样子"的唯一入口——分组变更、节点增删、配额重置、启用/禁用、后台自愈扫描，最终都落到它。v3.9.0 之后它同时还是**共享客户端迁移的执行者**：把用户从"每节点一个客户端"迁到"每面板每凭据类一个客户端"，并在迁移完成后删掉旧的每节点客户端。

问题出在 provision 与 lifecycle 是**两个分开的动作**：

- `sharedclient.ProvisionClient` 走的是 `buildSharedClientSpec`，它硬编码 `Enable: true`，并把 expiry 与 quota 都留成 0。也就是说，**刚 provision 出来的共享客户端一律是"启用、不过期、无配额上限"**。
- 用户的**真实**状态（`EffectiveEnabled` / `PushExpireTime` / 流量地板）是随后由 `syncSharedLifecycle` → `SyncUserLifecycle` 推上去的。

在这两步之间，一个已被禁用、已过期、或已超额停服的用户，在 3X-UI 上有一个**完全启用**的共享客户端。这个窗口本身是可接受的——真正致命的是它和"删除旧的每节点客户端"这一步的相对顺序：旧客户端持有的才是**正确的**禁用/过期状态。如果先删旧的、再推 lifecycle，而 lifecycle 推送失败（面板不可达、超时、3X-UI 报错），用户就停在一个"共享客户端全开、per-node 回退已被删掉"的状态——**没有任何一层还在拦他**。

这就是代码里反复引用的 **audit #1 越权旁路**。它不是理论：`ProvisionUser` 与 `SyncUserLifecycle` 都要打 3X-UI，而这个面板的整个同步层就是围绕"3X-UI 随时可能不可达"设计的（失败入 `sync_task` 队列重放）。推送失败是**常态路径**，不是异常路径。

危险在于这段顺序**读起来像流程顺序**：建好 → 配好 → 清理旧的。任何一个觉得"删除逻辑放在一起更整洁"、或者"lifecycle 推送失败反正下轮会重试、不该挡住清理"的重构，都会静默打开这个旁路——而且不会有任何测试变红，因为它只在"lifecycle 推送恰好失败"的那条路径上才有区别。

## 决策

**`ResyncMembership` 的相位顺序是一条授权约束，任何重排都必须按安全变更评审。** 顺序是：

1. **双写** —— `psp.SyncUser`：按期望节点集重建 `psp_clients` 计划（DB 侧），返回被裁剪掉的客户端 email（按面板分组）。
2. **provision** —— `migrator.ProvisionUser`：把共享客户端建到 3X-UI 并挂上入站，逐 (client, node) 读回确认。成功才置 `provisioned = true`。
3. **lifecycle 推送** —— `syncSharedLifecycle`：把 `Enable` / expiry / 流量地板从 provision 默认值纠正成用户的**真实**状态。失败记入 `lifeErr`。
4. **孤儿回收** —— `migrator.ReconcileOrphans`：清掉该用户已经不该存在的共享客户端。**不受第 2、3 步结果的门控**。
5. **删除旧客户端** —— `deletePrunedSharedClients` + `migrator.DeleteLegacyForUser`：**仅当 `provisioned && lifeErr == nil`** 才执行。

关键约束逐条说明：

### 1. 删除必须排在 lifecycle 推送之后，且被它的成功与否门控

这是整条顺序存在的理由。`provisioned && lifeErr == nil` 这个条件不是防御性编程，是**授权检查**：它断言"新的执行点已经带着正确的状态上线了"，只有这个断言成立才允许拆掉旧的执行点。任何一项不成立，旧的每节点客户端**必须原地保留**——它们持有的禁用/过期状态是这一轮里唯一正确的那份。

失败不是终点：错误经 `firstErr` 返回，调用方把它转成一条 `sync_task`，下一轮重跑整条顺序。所以"保留旧客户端"只是推迟清理，代价仅是多留一批冗余客户端；而"提前清理"的代价是一个越权用户在无人拦截的状态下继续用。

### 2. `ReconcileOrphans` 故意**不**受同样的门控

这看起来像遗漏，其实是相反方向的判断。孤儿回收删的是**已经没有 `psp_client` 行在管**的客户端（合并前的旧凭据类客户端，或 DB 行已删但 3X-UI 侧删除失败留下的残留）。删掉一个无人管理的客户端**只会让约束更紧**，不会放松任何东西，所以它不需要"新执行点已就绪"这个前置条件。

反过来，如果给它套上同样的门控，它就会在"另一个面板宕机导致 provision 失败"时**停止清理健康面板上的孤儿**——而跨面板耦合正是这些孤儿的成因。它的安全性由 `ReconcileOrphans` 内部**按面板**的覆盖度检查负责：只删共享方案的 email（永不碰 legacy 每节点回退），且不删除"活着的替代客户端还没覆盖到其入站"的客户端。

### 3. 逐用户串行

整个过程在 `lockUser(userID)` 下串行执行。同一个用户可能被自愈扫描、`sync_task` 排空和请求线程同时 resync；不串行的话，一轮里的重新分配会和另一轮的孤儿回收互相撞车。锁是**按用户**的，不同用户仍然并行。

## 后果

**正面**：任何单点失败都退化成"旧客户端多留一轮"，而不是"越权用户失去全部拦截"。整条顺序是幂等的，重跑即收敛。

**代价**：迁移期内会短暂存在冗余客户端（一个用户在同一节点上同时有共享客户端和 legacy 客户端）。这被接受为迁移成本——渲染对两者产出同一份订阅（见 ADR 0024），用户无感。

**运维**：`sync_task` 队列里堆积的 `user_migrate` 任务说明某个面板一直推不上去，不是数据丢失。

**必须保持的不变量**（未来修改时不能破坏）：

- 删除（`deletePrunedSharedClients` / `DeleteLegacyForUser`）必须排在 `syncSharedLifecycle` **之后**，并且必须同时被 `provisioned` 与 `lifeErr == nil` 门控。
- `syncSharedLifecycle` 的错误必须向上传播（进 `firstErr`），否则 `sync_task` 不会重试，用户会永远停在 provision 默认值上。
- `buildSharedClientSpec` 的 `Enable: true` / 零 expiry / 零 quota 是"待纠正的默认值"，不是"用户的状态"。如果哪天让它直接带上真实状态，本顺序的门控**仍然不能删**——那只是缩小了窗口，没有消除"推送可能失败"这件事。
- `ReconcileOrphans` 不要被"顺手"塞进第 5 步的门控里。

## 考虑过但未采用的方案

- **独立的一次性 `MigrateUser`（provision → 删除 legacy，中间无 lifecycle 推送）**：曾经存在，已被删除。`sharedclient.go` 里保留了一条显式警告——复活它就会重新打开 audit #1 旁路（推送失败窗口内，禁用/过期用户拿着一个全开的共享客户端且没有回退）。这条注释本身就是本 ADR 要固化的东西。
- **先删 legacy 再 provision**（"先清场再重建"）：更符合直觉，但删除与 provision 之间的窗口里用户**一个可用节点都没有**；provision 失败则彻底断服。当前顺序里失败只意味着"多留旧的"，方向相反。
- **把 lifecycle 推送失败当作尽力而为，不挡住清理**：这正是 audit #1 的形态。理由是"下一轮 `sync_task` 会补推"——但补推之前的这段时间里，用户处于完全无拦截状态，而这段时间的长度取决于面板什么时候恢复，不可控。
- **只在迁移完成后做一次全局清理，而不在每次 resync 里删**：会让 `DeleteLegacyForUser` 失去它的按节点粒度——它现在**保留**尚未被共享客户端覆盖的节点，正是这一点让部分迁移永远不会让用户断服。
