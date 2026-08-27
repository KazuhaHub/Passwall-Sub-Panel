# V4：删除遗留 per-node 所有权模型

> 状态：**已完成穷尽映射与对抗性验证；今天只删掉了三处，其余全部阻塞。**
> 关联：[inbound-ownership.md](inbound-ownership.md)、[v3.9.0-client-multi-inbound.md](v3.9.0-client-multi-inbound.md)、[ARCHITECTURE.md](ARCHITECTURE.md)。

## 1. 决定性的结构事实

**迁移状态对「维护者」不可知，但对「每台安装上的二进制」是权威可知的——而从来没有人去检查过。**

`DropIfMigrated`（`ownership_repo.go:51`）只在 `user_xui_clients` **恰好零行**时才 DROP 它（`n > 0` 时返回 `done=false` 继续轮询）。因此**表不存在**就是一个单向、机器可判定的证明：这台安装已经排空。

标记本身已经存在，而且可信。**缺的不是知识，是强制。** `cmd/panel/main.go` 分发完子命令就无条件启动：没有版本检查、没有 schema 探测、没有拒绝启动。**PSP 的二进制从来没有拒绝启动过。**

## 2. 这件事已经发生过一次（实测复现）

v3.9.0 把 `ownershipRow` 移出 `schemaModels` 是一个**完全正当**的改动：运行中的面板不该再长回这张表，因为表的缺席正是迁移完成的证据。

但它**悄悄弄坏了 v2 → v3 离线迁移器**：

| 环节 | 位置 |
|---|---|
| 迁移器对**空白**目标库调 `EnsureSchema` | `runner.go:109` |
| `EnsureSchema` 不再创建 `user_xui_clients` | `schema.go` 的 `schemaModels`（有注释明说故意排除） |
| `copyOwnerships` 随即往该表写 | `migrate.go:418` |

实测：

```
HasTable(user_xui_clients) after EnsureSchema = false
copyOwnerships-style insert err = SQL logic error: no such table: user_xui_clients (1)
```

也就是说：**任何带遗留客户端的 v2 库都导不进来**——而那恰恰是迁移器存在的意义。

仓内还有旁证：`ownership_repo_test.go` 里显式调用 `CreateTable(&ownershipRow{})`，注释写着「v3.9.0 retired user_xui_clients from schemaModels; recreate it to test the repo」。**测试知道，迁移器不知道。**

**这就是 V4 问题的缩影**：一个理由充分的 schema 改动，作为「代码变更」发布，悄无声息地弄坏了一条没有测试覆盖的迁移路径。对整个所有权模型做同样的事，就是在**每一台尚未排空的安装**上重演这次失败。

> **结论：V4 删除是一个发布工程问题，不是代码变更问题。** 删除本身是机械的，编译器会带着你走完。不存在的是「保证某台安装在运行假设它已迁移的代码之前，确实已经迁移完」的机制。

（本次已修复：`sqlstore.EnsureLegacyOwnershipTable` + `copyOwnerships` 在**第一批真有行**时惰性建表——空导入不建表，否则那台安装会永远显得「未迁移」。回归测试见 `internal/migrate/ownership_schema_test.go`。）

## 3. 今天能删的

诚实的答案：**几乎没有。三处，约四行可执行代码加一段注释。**

| 项 | 依据 |
|---|---|
| `AdminServersHandler.ownership` | 只写不读的字段，`grep -c 'h.ownership'` = 0，不在 `serverDTO` 里，不喂任何响应 |
| 路由里那段孤儿注释 | 描述一个从未实现的端点，**而且现在是错的**——它断言「生产中还没有东西读 psp_client」，但 `render.go:339` 就在实时渲染路径上读 |
| （已做）迁移器建表修复 | 见 §2 |

**不要**顺手删 `AdminNodeHandler.ownership`——它是活的（`admin_node.go` 五处）。

### 3.1 评估后决定不删：`recordClientStats` 的 `else` 分支

`OwnershipRepo.UpdateCounters` 生产上确实不可达（唯一调用点是 `if sink != nil` 的 else 臂，而唯一的生产调用方总是传非 nil sink）。

**但它是有文档的刻意保留**：注释写明「keeps the inline single-row write for non-poll callers (tests, ad-hoc) so this helper stays usable outside the poll loop」。删掉换来 4 行，代价是把一个有明确契约的辅助函数变成对 nil-sink 调用方**静默不落盘**。不划算。

### 3.2 评估后决定不删：`GetByMatch`

无生产调用方，但它是 `TestOwnershipBatchUpdateCounters` 的**读回工具**，而它守的东西是真的：`BatchUpdateCounters` 不能改写身份列、零 ID 行必须整批中止。**`BatchUpdateCounters` 在每一台未迁移安装的流量轮询上都是活代码。** 它还是这个 repo 唯一的按身份读取（`Exists` 只返回 bool，其余都返回集合）。

删端口声明是免费的但等于没删；删适配器方法要付出一个真实测试或一个替代读法。**不是今天的事。**

## 4. 阻塞项与解锁条件

两个前置条件门控其余一切。**任何一个都不是靠「再审一遍代码」能解锁的**，它们都是关于「部署现状」的事实。

- **P1（每台安装）**：`user_xui_clients` 零行**且已被物理 DROP**，于是 `ownershipRepo.gone` latch 住。
- **P2（全机队）**：**v4 二进制不能在 P1 为假的地方运行。** 这个机制不存在，从来没有过。

| 阻塞项 | 前置条件 |
|---|---|
| `ports.OwnershipRepo`（13 个方法）、`ownershipRepo`、`ownershipRow`、`domain.XUIClientEntry`、行↔域映射 | P1 ∧ P2 |
| 读路径：`ListByUser` / `ListByUsers` / `ListByInbound` / `Exists`、轮询预取与回退、reconcile 两处、`UserNodeUsage`、`UserServerUsage` 回退 | P1 ∧ P2 |
| 写路径：`Add` / `Remove` / `RemoveByMatch` / `BatchUpdateCounters` / `UpdateUUID` / `DelAllOwnedForUser` / `DelAllOwnedForInbound` / `UnclaimAllForInbound` | P1 ∧ P2 |
| `gone` latch、`confirmGone`、`isMissingTableErr` | 必须**与 repo 同生共死**，绝不能提前——`isMissingTableErr` 在 `xui_panel_repo.go:135` 也是 load-bearing 的，管着已迁移安装上的「删除面板」功能 |
| reconcile 的零条目早退 | **只能与 `AddClientToInbound` 的删除在同一个 commit 里**。先删早退会在下一次 `LevelFull` 上，把遗留客户端**重新长回整个已迁移用户群** |

## 5. 需要的发布工程机制

按本仓处理破坏性迁移的既有做法（版本下限 + 远程下发的 advisory + 升级确认弹窗），最小可行方案是**三段式**：

1. **一个明确的「强制迁移」版本。** 该版本正常运行两套模型，但把「排空进度」提升为一等状态：在管理界面上可见，并通过 advisory 告知运维方必须完成。
2. **v4 二进制启动时拒绝。** 检查 `user_xui_clients` 是否存在且非空；是则**打印明确指引并退出非零**，而不是带着「假设已迁移」的代码跑起来。这正是 §2 那次失败缺的那一环。
3. **只有在 (2) 存在之后**，才执行 §4 的删除，且必须遵守表里的同 commit 约束。

## 6. 什么都不做的风险

两套模型无限期并存，每一次改动都要在两条路径上各做一遍——**本轮的连接限制功能就是活例子**：三个推送点里有两个是遗留路径，光把重基逻辑穿过去就多花了一半功夫，而且代码审查在遗留侧又抓出一个漏掉的比较（`clientUnchanged` 看不到 `LimitIP`）。

**每加一个 PSP 拥有的字段，这笔税就重一分。**
