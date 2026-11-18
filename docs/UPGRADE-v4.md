# 升级到 V4：最终 V3 基线与有界迁移

> **适用版本：从 [v4.0.0-beta.2](https://github.com/KazuhaHub/Passwall-Sub-Panel/releases/tag/v4.0.0-beta.2) 起。** 已发布的 [v4.0.0-beta.1](https://github.com/KazuhaHub/Passwall-Sub-Panel/releases/tag/v4.0.0-beta.1) 不包含本次历史迁移收敛，其标签、镜像和历史发行说明不会被重写。使用确切发行标签；已成功启动并完成数据库转换的 beta1 正常升级，不重新运行迁移 CLI。

## 支持路径

- 最终 V3：先成功启动 `v3.9.2` 或 `v3.9.2-beta.20`，完成该版本自己的数据库转换，然后备份并正常启动 V4。
- 更旧或未完成转换的 V3：先运行最终 V3。V4 不再补跑全部历史小步迁移，不会猜测旧数据原意。
- V2：使用冻结 V3 的历史 `psp migrate` 工具导入新 V3 数据库，再成功启动最终 V3；见 [历史手册](UPGRADE-v3.0.0.md)。不能直接导入 V4。
- 已成功启动并完成数据库转换的 `v4.0.0-beta.1`：正常升级，既有 V4 子步骤标记继续有效；不会重写后续产生的期望/观测状态或已应用凭据。曾启动失败或手工修改 schema 的 V4 库须按失败现场检查，不属于这一正常升级路径。
- 新装：正常初始化，不需要准备 V3 数据库。

**V3→V4 不运行 `psp migrate`。** 当前源码已删除 V2→V3 专用工具；旧命令（包括放在 `--config` 后面）会在配置加载和数据库初始化前报错退出，不会误启动面板。

## 升级前

1. 确认实际运行的版本、数据库种类/位置和所有配置、数据、证书及环境文件挂载。固定目标发行标签，保存旧二进制或镜像 ID；不要仅记住已经可能移动的 `:beta` 标签。
2. 停止所有连接同一 PSP 数据库的 PSP 实例，再取得匹配的完整备份：SQLite 备份完整数据目录和匹配的 WAL/journal；MySQL/PostgreSQL 使用对应数据库的可靠 schema+data 备份。仅备份 Docker 数据卷不能替代外部数据库备份。
3. 一起保留原配置、JWT 密钥、加密密钥和环境覆盖。历史配置没有独立 `encryption_key` 时，JWT 密钥也可能是数据库加密材料；升级时不要旋转密钥或重新生成配置。
4. 确认备份可恢复及磁盘空间足够。MySQL/PostgreSQL 的 PSP 数据库账号需要此次迁移使用的建表、改表和索引权限。
5. 如果自行给 PSP 表添加过索引、约束、触发器或视图，先在备份副本检查它们是否依赖退休列，并试迁移。代码仅显式删除登记的应用列/旧索引，但不能保证所有自定义依赖对象原样保留：MySQL/PostgreSQL 的 `DROP COLUMN` 本身可能自动修改/删除依赖索引或约束，也可能因其他依赖报错；见 [MySQL](https://dev.mysql.com/doc/refman/8.4/en/alter-table.html)、[PostgreSQL](https://www.postgresql.org/docs/current/sql-altertable.html) 官方说明。SQLite 原生删列会拒绝阻断删列的依赖；不要盲删对象让启动通过。

## 执行

仅替换现有 PSP 二进制或 Compose 的 PSP `image:`，保留数据库连接、配置、卷名/挂载、网络、权限及环境变量。本版 Docker 镜像为 `ghcr.io/kazuhahub/passwall-sub-panel:v4.0.0-beta.2`；不要从 `main` 下载新 Compose 覆盖生产文件，也不要删除数据卷。

正常启动 PSP 会执行：只读基线检查 → 当前模型 `AutoMigrate` → 必要的 V3→V4 桥接 → 当前流量计数修复及内置角色种子。无需另行执行迁移 CLI 或逐个安装 V3 小版本。

基线检查验证 8 张 PSP 核心表的必要字段、额度三态 nullable 列及既有转换标记；数据库没有可靠的旧二进制版本字段，因此验证的是**数据语义**而非版本字符串。`limits_tristate_v3.9.3` 是历史内部迁移标记名，不表示必须安装一个名为 `v3.9.3` 的发行版。不能为绕过检查手工补标记或改列。

必要桥接保留客户端/挂载 ID、凭据和流量计数，将旧 endpoint 和 attachment 表示收敛为 V4 状态，删除明确退休的应用列和固定旧索引集合。它复用 beta1 的子步骤标记，全部桥接成功后写入 `v3_to_v4_baseline_v1`，后续启动跳过桥接。新装的 `v4_schema_initializing_v1` 仅用于确认空库后 DDL 失败的安全重试，不是旧库导入开关。

数据库结构完成不代表上游客户端已经实际应用。必要的 Ownership/shared-client 上游收敛仍保留，不因本次历史迁移收敛而提前删除或假定成功。

## 启动后检查

- 核对 `psp version`、启动日志、管理员登录和上游连接，无迁移或解密错误。
- 核对用户、分组、客户端/挂载数量及原 UUID/密码/订阅 token，确认额度 `NULL=继承`、`0=明确无限额`、正数=明确上限仍成立。
- 核对流量累计和周期基线，禁用/到期/超额用户不因升级恢复代理服务。
- 检查订阅输出、监听端口和实际连接，以及未完成的上游同步任务；确认管理员自定义模板/规则/证书保留。
- 再次启动不应重放旧 V3 规则或覆盖升级后的期望/观测状态。

## 失败与回退

遇到 `unsupported PSP database baseline` 时，停止升级；本次基线拒绝发生在 schema 写入之前。仅对于尚未被 V4 修改的旧 V3 库，先成功运行最终 V3。若 beta1 曾部分转换失败或 V4 schema 被手工改过，保存现场并检查缺失字段和已有标记，不能直接在同一库启动 V3；要回 V3 必须恢复升级前匹配的备份。不要手工造标记让旧库看起来已转换。

**迁移失败不等于数据库自动回滚。** MySQL DDL 会隐式提交，整个启动迁移不处于一个覆盖全部 DDL 的事务中。已成功的转换保留独立标记，修复明确的权限或依赖问题后可重试；不要清空迁移记录或无依据反复换版本。

如果需要回到 V3：停止 PSP、保存失败现场，然后恢复升级前匹配的**数据库、配置/环境与原密钥**，再使用旧二进制或旧镜像。不能只换回 V3 镜像而继续使用已被 V4 修改的数据库。不要删除未知操作员索引/触发器来“让迁移通过”，也不要执行 `docker compose down -v`。

## 升级到 4.2.0 线（从 v4.1.0 稳定版，或从 v4.1.1-beta.2）

> 适用：PSP 从 `v4.1.0` 稳定版或 `v4.1.1-beta.2` 升到 4.2.0 线，Passwall Node 从 `4.1.0` 升到 4.2.0 线。两个起点在数据库、设置键、Node 兼容与回退上没有差别，见下面的「从 v4.1.1-beta.2 起步」；下文凡写「v4.1.0」之处，对 `v4.1.1-beta.2` 同样成立，除非该处另行说明。4.2.0 线带来访问控制（目的地策略、命中记录、白名单分组、按需解析、按网站用量）与隐私和服务条款页。下面只写代码里读到的事实；各功能的行为见 [access-control.md](access-control.md) 与 [privacy.md](privacy.md)，通用的备份与停机步骤见本文前面的「升级前」，不在此重复。
> 先在预发或备份副本上演练，再动生产。

### 一句话

**什么都不配置，升级之后访问控制与隐私条款这两块什么都不会变**：节点收到的 config 与升级前逐字节相同，core 不重启；条款页不出现，没有用户被提示同意。新功能由管理员逐项主动打开，但有两处默认值要先知道：命中记录的服务器默认档位是 `hits`，从创建第一条拦截或仅报告策略起就会记录账号、目的地与端口（不含来源 IP）；风控的「访问拦截」信号默认开启（见下表）。v4.1.0 到 4.2.0 线之间还有与访问控制无关的改动（例如 #333 原生节点上报处理、#316 SAML 防护、依赖与 Go 版本更新），它们不在本节范围，见各自的发行说明。

### 从 v4.1.1-beta.2 起步：与 v4.1.0 比较，没有需要多做的事

`v4.1.1-beta.2`（标签提交 2026-11-12）相对 `v4.1.0` 共 15 个提交（`git log --oneline v4.1.0..v4.1.1-beta.2`，另有 `v4.1.1-beta.1` 在二者之间）。逐项核对的结果：

| 维度 | 结果 | 依据 |
|---|---|---|
| 数据库结构 | **没有差别。** 两个标签的表和列相同，下文「[升级时数据库会多什么](#升级时数据库会多什么automigratesqlite--postgresql--mysql-三方言都有测试)」整表对它同样成立，从 beta.2 升级时一个对象都不会少、不会多 | `git diff v4.1.0..v4.1.1-beta.2 --stat -- internal cmd` 里非测试文件只有 `internal/domain/server_migration.go`、`internal/pkg/samlguard/samlguard.go`、`internal/service/auth/saml.go`；`internal/adapters/sqlstore` 只修改了一个已有的测试文件 `server_migration_repo_test.go`，没有模型、迁移或 `schema_baseline.go` 的改动 |
| 设置键 | **没有差别。** 没有新增、改名或删除设置键；`kvSettingsRepo.Load` 未变 | 同上，设置相关的非测试文件无改动 |
| 行为 | 有三处行为变化，**4.2.0 线都原样保留，没有反转**，也没有依赖它们的前置步骤：① 原生迁移预检不再因「受管客户端没有入站挂载」报 `missing_attachment`（#326，`Blockers()` 里该检查已删，主线上 `internal/domain/server_migration.go` 里没有这个码）；② SAML 登录对 crewjam 解析时的 panic 改为拒绝登录（#316，`verifyResponse`，主线仍在 `internal/service/auth/saml.go`）；③ 构建用 Go 工具链 1.27.4（`go.mod` 的 `toolchain`）与依赖更新 | `git diff v4.1.0..v4.1.1-beta.2`；`git merge-base --is-ancestor v4.1.1-beta.2 origin/main` 为真，这些提交都在 4.2.0 线的历史里 |
| Node 兼容 | **清单有两处变化，但运行结果相同。** 变化：`docs/compat/passwall-node-v4.json` 的 `min_supported` 在 `v4.1.0` 是 `4.0.0`，在 beta.2 是 `4.1.0`（#312「核实 Passwall Node v4.1.0 并把兼容下限提到 4.1.0」，CI 不再测 4.0.x），同一个提交还新增了 `4.1.0` 这一行（`v4.1.0` 的 `released_nodes` 止于 `4.0.1.11`，beta.2 止于 `4.1.0`）。相同：升级能力门 `agent_upgrade_v1.required_capabilities` 两个标签一致。这个文件只决定 CI 测试矩阵：非测试的 Go 代码里没有任何读 `min_supported` 的地方（只有 `cmd/compatwatch` 的 CI 监视工具读该文件），所以下限变化不会让任何节点被拒或被放行；本节「顺序：先 PSP，再节点」对两个起点同样成立 | 读两个标签下的该文件；`git grep -n 'min_supported' -- '*.go'`（排除测试）只命中 `cmd/compatwatch/main.go` 的一条注释 |
| 回退到它 | 回退目标可以是 `v4.1.1-beta.2`，也可以是 `v4.1.0`，规则相同：只换二进制或镜像，不需要恢复备份。两个标签的基线检查（`internal/adapters/sqlstore/schema_baseline.go`）与设置加载（`settings_kv_repo.go`）代码没有差别，所以 v4.1.0 的结论在 beta.2 上同样成立：只读它认识的表、列、键，多出来的 `dest_*`、`legal_*` 表、`xui_panels`/`groups_` 新列与设置行被忽略 | 读 `v4.1.1-beta.2` 下上述两个文件，并核对它们在两个标签之间没有 diff |

留意两点，免得把它们当成「差别」：`v4.1.1-beta.2` 不是稳定版（预发布，见 [release-runbook.md](release-runbook.md)）；以上全部来自代码和 git 历史，没有对任何运行中的实例做过检查，所以生产机上实际是哪个标签，以管理员核对镜像或二进制为准。

### 顺序：先 PSP，再节点

PSP 只向**本次上报声明了对应能力**的节点下发策略、分组、按需解析和用量设置（`policy.destination.v1`、`policy.destination.groups.v1`、`policy.destination.resolve.v1`、`audit.hits.v1`、`audit.usage.v1`；见 [psp-node-agent.md](psp-node-agent.md) §11，[access-control.md](access-control.md) §4.2 第 1 步、§4.0、§4.0b）。旧节点没有这些能力，所以新旧混跑是安全的，两种顺序在协议上都成立（[access-control.md](access-control.md) §8 的前两行：新 PSP + 旧节点，旧 PSP + 新节点）。推荐先升 PSP：

1. **先升 PSP**（备份、停实例、替换、启动，见前文）。4.1.0 的节点没有能力，在访问控制的状态里显示 `unsupported_version`，不下发任何策略，config ETag 不变，core 不重启。这时可以先看一遍状态页，确认哪些原生节点需要升级。
2. **再逐台升级节点**（服务器菜单的「升级 Passwall Node」，见 [pn-remote-upgrade.md](pn-remote-upgrade.md)；其前置条件是节点声明了升级能力并通过兼容性检查，Docker 手动配置和旧单容器安装仍须手工升级）。每台节点只因升级自己重启一次。混合机队里，**拦截与放行策略**只对已升级的原生节点生效；**白名单分组不是这样**，见下面的警告。
3. **最后才配置策略。** 先升节点、后写策略，第一次发布时所有有能力的节点在一个去抖窗口加一个同步周期内同时重启一次（[access-control.md](access-control.md) §8）。

反过来先升节点、PSP 仍是 4.1.0 也能工作：旧 PSP 静默忽略节点多上报的 `policy_status` 与 audit，不下发策略。但那样期间看不到新节点的状态，也没法试用新功能，没有好处。

3X-UI 与 S-UI 节点不执行任何拦截、放行或来源策略，这些策略对它们没有影响（但白名单分组例外，见下）。仍在从 3X-UI 迁往 Passwall Node 的服务器，迁移步骤见 [server-migration.md](server-migration.md)；迁移与本次升级互相独立，不要在同一个维护窗口里叠做。

> **警告：白名单分组会把成员从不能执行它的节点上移除。** 把一个分组设为「仅报告」或「开启」后，它的成员在**每一台**不能执行白名单的面板上都会被移除（成员的 client 被删除，不只是订阅里隐藏）：所有 3X-UI、S-UI 面板、尚未升级的原生节点、能力不全、超额、被拒或嗅探不足的节点（`group.Service.PanelEnforceReason` 的 `xui` / `sui` / `capability` / `over_limit` / `rejected` / `sniffing`，见 [access-control.md](access-control.md) §3.3）。生产还在从 3X-UI 迁往 Passwall Node 时，这会让成员断网。**在该分组成员用到的每一台节点都已是能执行它的、已升级的原生节点之前，不要把任何分组切到仅报告或开启。** 切换前先看预览接口 `GET /api/admin/dest/groups/:id/preview` 的 `nodes_lost`（保存后会失去的节点），不为空就不要保存。

### 升级时数据库会多什么（`AutoMigrate`，SQLite / PostgreSQL / MySQL 三方言都有测试）

启动时的 `AutoMigrate` 只新增，不改、不删已有数据：

| 对象 | 内容 |
|---|---|
| 新表，访问控制 | `dest_lists`、`dest_policies`、`dest_exemptions`、`dest_source_rules`、`dest_policy_state`、`dest_policy_snapshots`、`dest_agent_policy` |
| 新表，命中与用量记录 | `dest_hits`、`dest_audit_batches`、`dest_audit_drops`、`dest_usage_hourly` |
| 新表，隐私与条款 | `legal_documents`、`legal_consents` |
| `xui_panels` 新列 | `audit_collect`（`varchar(16) NOT NULL DEFAULT 'hits'`）、`dest_resolve`（`varchar(16) NOT NULL DEFAULT 'as_is'`），已有行读到默认值 |
| `groups_` 新列 | `dest_allowlist_state`、`dest_allowlist_list_ids`、`dest_allowlist_base_list_id`、`dest_allowlist_extra_list_id`、`dest_allowlist_state_changed_at`、`dest_allowlist_updated_at`，都可空，升级后为 NULL（等于白名单关闭） |

字段与索引细节见 [access-control.md](access-control.md) §5 与 [privacy.md](privacy.md) §6。PSP 的数据库账号需要建表与改表权限（同前文「升级前」第 4 条）。8 张核心表的基线检查只看必需字段，额外的新表和新列不影响它。

### 升级后默认是什么（访问控制本身关闭，两处记录与风控默认开启）

| 项 | 默认 | 含义 |
|---|---|---|
| 访问策略 | 没有任何策略、豁免、来源规则、白名单分组 | 节点不收到 `policy` 字段 |
| 风控「访问拦截」（`risk_dest_block`） | 开启，阈值 20 次 / 24 小时 | 没有任何节点在记录命中时，对账号显示「无法判断（没有在记录的节点）」；开始记录后才有结论。只观测，不处置（[connection-limits.md](connection-limits.md) §13.12） |
| 服务器 `audit_collect` | `hits` | 只有存在拦截或仅报告规则时才让节点记录命中；没有这类规则时不写记录级别，摘要不变，core 不重启。**不是**按网站用量；记录的是账号、目的地（域名或 IP）、端口与次数，按小时汇总，不含来源 IP |
| 按网站用量（`hits_and_usage`） | 不开，按服务器自愿开启 | 见下文「CPU 代价」 |
| 服务器 `dest_resolve` | `as_is` | 下发的策略与升级前逐字节相同；只有切到 `on_demand` 才会让那一台节点重启一次 |
| 条款与隐私（`legal_enabled`） | `false` | 公开接口 404，注册与登录不变；`legal_consent_version` 为 0 |
| `legal_disclose_usage` | `false` | 公开清单里不出现按网站用量一项，包括节点数 |
| 目录模板的 `counts_as_risk` | BT 与发信（SMTP）模板为 `true`，其余为 `false` | 只有这两类策略被执行的拦截才计入「访问拦截」信号（[access-control.md](access-control.md) §6）。模板都不会自动创建 |
| 保留期 | 命中 30 天、白名单试运行 7 天、用量 7 天 | 设置项见 [access-control.md](access-control.md) §11 |
| 规则数据更新 `dest_rules_update_mode` | `manual` | 只提示，管理员点更新 |

「命中记录会多一次重启」与风控「访问拦截」信号显示「无法判断」，前者只发生在**升级节点之前就已经存在拦截策略**的机队上（见本文末尾降级警告一节）；v4.1.0 没有访问控制，从它升级时没有任何策略，所以不会遇到。「访问拦截」的「无法判断」则从升级起就存在，直到有节点开始记录。

### 升级后管理员该做什么

1. 按前文「启动后检查」核对用户、订阅、流量与上游连接。
2. 打开管理端「访问控制」页和服务器页，或读 `GET /api/admin/dest/status`：确认每台原生节点的状态。`unsupported_version` 表示节点还没升级；`not_enrolled` 表示原生服务器上还没有节点注册。此时应当没有任何节点显示 `rejected`、`over_limit` 或 `sniffing`。
3. 节点逐台升级后，在服务器的「记录方式」里决定每台节点记录什么：保持默认 `hits`、关闭，或为需要的节点开启按网站用量。
4. **不要在节点还没全部升级、迁移还没完成时开启任何白名单分组**（见上文警告）：切换前先看预览的 `nodes_lost`。
5. 创建第一批策略时，**先设为「仅报告」**，观察命中记录，再改为「开启」。仅报告不改变任何连接的判定（[access-control.md](access-control.md) §3.1）。「没有必须先仅报告的门槛」，这是建议，不是限制。
6. 若要对用户公开记录情况：先在系统设置里发布「隐私政策」与「服务条款」，检查无误后再打开 `legal_enabled`。开启访问规则命中记录之前建议先发布隐私政策（[privacy.md](privacy.md) §7）。是否披露按网站用量由 `legal_disclose_usage` 决定，默认不披露。
7. 在上述节点的嗅探配置里确认域名规则需要的入站：嗅探关闭会由节点自动补上，`routeOnly:true`、`metadataOnly` 等会让域名规则在该入站不生效，状态页会逐入站标出（[access-control.md](access-control.md) §4.2 第 7 条）。

### CPU 代价（按网站用量）

**只有按网站用量（B 档）有显著的 CPU 代价**：最坏情况压测下，**Xray 代理进程**多用 +43% 的 CPU（实测的最坏值，不是典型值；没有给出典型值）；Node 的 agent 接收这些 webhook 还要**另外**消耗自己的 CPU，这部分不含在 +43% 里（[access-control.md](access-control.md) §4.0c、§12）。原因是每条连接多一次 webhook 与一次本地 socket 写入。因此它按节点自愿开启，默认不开；对 sing-box 节点无效（sing-box 只执行、不记录）。开启或关闭会让该节点重启一次代理程序。命中记录（A 档）没有这样的压测数字，本文不给出代价结论；它只给拦截与仅报告规则挂 webhook（[access-control.md](access-control.md) §4.0）。

### 回退与降级

整体回退（PSP 回到 v4.1.0，或回到 v4.1.1-beta.2，规则相同）：**只换二进制或镜像就能启动，不需要为此恢复备份**——4.2.0 线的结构变更全是新增，v4.1.0 的基线检查与设置加载只读它认识的表、列、键（读代码得出，没有做端到端演练）。仍然应保留升级前的备份：只有想把数据库完整还原到升级前的样子时才恢复它，那会丢失升级之后写入的一切。若升级之外还有别的问题，遵循前文「失败与回退」，并叠加下面的访问控制降级警告：

- **新增的表与列原样留在库里，旧版本不读它们，也不需要删。** 旧版本的基线检查只验证它自己认识的核心表和字段，不会因为多出来的表或列拒绝启动；旧版本的设置加载只读它认识的键，新版本写入的 `legal_*`、`dest_*` 设置行被忽略。`audit_collect`、`dest_resolve` 有 `NOT NULL DEFAULT`，旧版本插入服务器行时不写它们，由默认值补上。
- **风险在于行为，不在于数据：** 所有策略、豁免、来源规则**立即失效**（每台原生节点因 config 不带 `policy` 重启一次），被拦截的 BT、发信端口、按国家拒绝的来源放开；白名单分组不再限制任何人，旧版本只按分组标签决定成员落在哪些节点，不再排除不能执行白名单的节点；条款与隐私页消失，注册不再要求同意（已记录的同意与已发布的文档留在库里）；命中与用量记录停止增长，已有记录不删除。
- **再升级回 4.2.0 线时，** 策略、白名单、条款、记录恢复，每台节点再重启一次；`dest_*` 表不要手工删除。**留在 v4.1.0 期间删除的账号或入站，不会清理它们的 `dest_exemptions` 与 `dest_source_rules` 行**（v4.1.0 不认识这两张表），这些行会在重新升级后原样回来：再次升级后先检查豁免与来源规则列表，删掉指向已不存在的账号或入站的行，再发布。
- **回退到更早的 4.2.0 测试版（协议还不是 v0.8.0 的那些）：** 升级到带命中记录出口的版本时，`dest_hits` 会被重建，`egress_panel_id` 进入主键。旧测试版写命中用的 `ON CONFLICT (hour_ms, panel_id, user_id, …)` 与新主键不再匹配，**在 PostgreSQL 和 SQLite 上命中入库会失败**（MySQL 的 `ON DUPLICATE KEY` 不受影响）。要回退请直接回到 v4.1.x（按上文，不需要恢复备份，旧版不读 `dest_*` 表）；要留在 4.2.0 线，就用 4.2.0-beta.12 或更新的版本，这个版本起带这次重建。
- 节点回滚到 4.1.0：能力消失，PSP 不再下发，该节点重启一次并失去策略（自动，无需操作）。
- 想在降级前保留变更说明：在访问控制页确认哪些限制依赖它（尤其是对外暴露入站上的「拒绝来源国家或地区」），并在发布说明里写明。

## 降级警告：访问控制（目的地策略）

> **警告级。** 从包含访问控制的版本降级到不含它的版本时，**所有策略、豁免、来源规则立即失效**。

- 旧 PSP 铸出的节点 config 不带 `policy` 字段，每台原生节点收到后**重启一次 core**，之后不再执行任何目的地限制：被拦截的 BT、发信端口、按国家拒绝的来源都会放开。
- `dest_*` 表保持原样（降级不删表，也不应手工删），再次升级到包含本功能的版本后，策略与规则恢复并重新下发，每台节点再重启一次。
- 旧 PSP 不认识节点上报的 `policy_status`，会静默忽略，不影响同步。
- 反过来，升级到本功能时：节点全是旧版则 config 不变、core 不重启；第一次启用任何策略时，所有有能力的节点在一个去抖窗口加一个同步周期内同时重启一次。
- **命中记录会多一次重启。** 节点升级到带 `audit.hits.v1` 能力的 agent 后，若升级前已有拦截策略（只在运行过更早 4.2.0 测试构建的机队上出现），且服务器的 `audit_collect` 是默认值 `hits`，节点收到的策略里多了记录级别，摘要变了，**core 会再重启一次**。想避免这一次：升级节点之前，先把该服务器的 `audit_collect` 设为 `off`（服务器编辑接口 `PUT /servers/:id`），升级完成后再改回 `hits`（改回时节点仍会因摘要变化重启一次，可选在维护窗口里做）。
- **按需解析（`dest_resolve`）默认关闭，不会让任何节点多重启。** 服务器的 `dest_resolve` 默认 `as_is`，下发的策略与升级前逐字节相同。只有把某台节点切到 `on_demand`（且它有 `policy.destination.resolve.v1` 能力、策略里有 CIDR 或私有地址规则）时，那一台节点的 core 才会重启一次；切回 `as_is` 同样一次。行为差异与代价（Xray 解析失败放行、sing-box 解析失败拒绝；Xray 拨号时再解析一次；`routeOnly:true` 的入站让整台节点保持 `as_is`）见 [access-control.md](access-control.md) §4.0b 与 §10。
- **「访问拦截」风险信号会短暂显示「无法判断」一次。** 升级后，已有节点记录的上一版策略没有「记录级别」，第一次发布新策略后、各节点应用新版本之前，风控中心的「访问拦截」检测器对这些账号显示「无法判断（没有在记录的节点）」。节点应用后自动恢复，不影响执行，也不会触发任何处置。

降级前先在访问控制页确认哪些限制依赖它（尤其是对外暴露的入站上的「拒绝来源国家或地区」），并在发布说明里写明这一点。细节与升级降级矩阵见 [access-control.md](access-control.md) §8。

## 升级到 4.3 线

### VLESS 路由线路已删除，升级时丢弃其存储状态

4.3 整体删除“VLESS 路由线路”（节点编辑里的路由标签页、`/api/admin/nodes/:id/vless-route-*` 接口、用户状态页的路由行、后台只读校验与清理）。所有者从未使用过它，因此**不做数据迁移、不保留兼容层**。详情见 [ADR 0039](adr/0039-vless-route-lines-v2.md)（已取代）。

- **数据库：** 首次启动 4.3 时，`migrateV4_3` 的 `dropVLESSRouteState` 步骤删除 `nodes.route_state`、`nodes.route_revision` 两列和表 `vless_route_leases`（SQLite、MySQL、PostgreSQL 均如此；有则删，没有则什么也不做，可重复执行）。**这些状态不能恢复：**需要保留请在升级前备份数据库。
- **3X-UI 遗留规则：** 升级本身不会连接或修改 3X-UI。曾用过该功能的管理员可在「服务器」中选择对应的 3X-UI 服务器，打开「清理旧 VLESS 路由」。预览只列出带旧 PSP 标记且符合旧版格式的规则。确认才会写入模板，Xray 可能重启。预览后模板有变化时必须重新预览。未带标记或不符合旧版格式的规则需在 3X-UI 中手动核对。
- **订阅与凭据不变：** 路由线路派生的订阅条目不再生成；直连与中转线路的订阅、国旗、分组可见性不受影响。代理组成员里引用了已删除路由线路的 `line` 成员按“已删除引用”处理（告警、渲染时跳过）。
- **回退：** 回退到 4.3 之前的版本会得到没有这两列和这张表的库；旧版本会按自己的模型重新建出空的列/表，但原有路由数据已丢失。

### 每条线路一个客户端（按线路的流量倍率）

4.3 起每条用户可见的线路（节点的直连线路和每条中转线路）有自己的客户端与计数器，流量倍率按线路设置。
设计见 [ADR 0043](adr/0043-per-line-client-identity.md)，计费细节见 [traffic-multiplier.md](traffic-multiplier.md)。
升级自动完成，没有需要操作的界面：

- **数据库：** `migrateV4_3` 的 `backfillRawLifetime` 用现有账本补齐原始与计费账本；`convertClientsToLines`
  把每个旧共享客户端转为其挂载的最小 ID 节点的直连客户端（保留 ID、邮箱、计数），并为每个持有客户端的
  用户排一个 `user_resync` 任务。两步都只执行一次。
- **升级后：** 任务按正常流程为其他节点的直连线路以用户自己的 UUID 建客户端，为中转线路建派生身份的
  客户端。3X-UI / S-UI 上需要的新建、拆分与解除挂载由正常生命周期完成；PN 由下一次名册同步完成。
- **订阅：** 只用直连线路的用户订阅不变；但 Naive 与 WireGuard 的用户名取自客户端邮箱，除最小 ID 节点外
  的直连线路会换成新邮箱。中转线路在其身份生效前暂用直连身份（落地照样接受），生效后换为自己的身份，
  客户端需要更新一次订阅。
- **上限：** 3X-UI 的 IP 数与设备数上限按客户端生效，用户在同一服务器上的线路越多，实际可用上限越大（×N），
  后台在原有判定旁显示倍数。
- **规模：** 每台服务器的客户端数约为“用户数 × 可见线路数”。PN 服务器接近单次同步可承载的数量时
  （约 2 万）服务器列表会给出警告。
- **回退：** 回退到 4.3 之前的版本时，旧版本的规划器按分区重新规划，用户在同一服务器上多出来的客户端行
  会被删除，这些行的按服务器用量随之丢失；用户自己的账本（`lifetime_*`，此时为计费值）不受影响。
