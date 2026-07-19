# Passwall-Sub-Panel 架构设计文档

| 字段 | 值 |
|---|---|
| 文档版本 | 3.9.1 |
| 最后更新 | 2026-07-02 |
| 状态 | 活跃维护 |

## v3.0.0 数据库重构

> 升级到 v3.0.0 必读：[docs/UPGRADE-v3.0.0.md](UPGRADE-v3.0.0.md)。重构不可原地升级，需运行一次性迁移程序 `cmd/migrate-db-v2/`。

| 维度 | 重构前 (≤ v2.5.x) | 重构后 (v3.0.0+) |
|---|---|---|
| 配置存储 | 4 张专用表（`ui_settings` 30+ 列宽表 + `mail_settings` + `saml_config` + `oidc_config`） | **统一 KV `settings` 表**（type/name/value/encrypted）合并原 `ui_settings` + 邮件触发阈值；mail/saml/oidc 保留单行专用表为未来多账号/多 IdP 留扩展，并统一改名 `_settings` 后缀 |
| ownership 表 | `xui_clients`（字面误导，实际是 user↔panel client 占有映射） | **`user_xui_clients`**（语义清晰的 join 表命名）；**v3.9.0 起本身已是过渡期遗留表**，见 §5.1 |
| panel_name 冗余 | 三张表（`nodes` / `xui_clients` / `client_traffic_snapshots`）冗余存，admin 改 panel 名后历史快照永远显示旧名 | **彻底删除冗余列**；service 层从 in-memory panel pool 按 `panel_id` 实时查询，admin 改名立即生效 |
| `rule_sets` DB 表 | 存在但实际未被注入（死代码） | **删除**；规则集真实来源是 `config/rulesets/*.yaml` |
| 流量保留策略 | 三张 snapshot 表（`traffic_snapshots` / `client_traffic_snapshots` / `node_traffic_snapshots`）**无 retention 清理**，默认 5 分钟轮询下 `client_traffic_snapshots` 年增长数千万行 | **新增 `traffic_snapshot_retention_days`（默认 180 天）**，cron 自动清理 |
| client lifetime | 仅在 `users` / `nodes` 层维护；per-client 历史用量需扫 snapshot 表 | **`user_xui_clients` 加 `lifetime_*_bytes` + `last_raw_*_bytes`**，对称于 users/nodes；admin 可直接 `ORDER BY lifetime_total_bytes` 看 client 历史 |
| snapshot 语义 | `traffic_snapshots` / `node_traffic_snapshots` 存 lifetime，但 `client_traffic_snapshots` 存 raw counter（语义不一致） | **三表统一存 lifetime**；raw counter 作为 baseline 收纳进 `user_xui_clients.last_raw_*_bytes` |
| period 用量计算 | `traffic.Service.periodUsage` + `mailer.Service.periodUsage` 各自 `LastBefore(period_start)` 随机点查（50 user 即 50 次 query/poll） | **`users.period_baseline_bytes` + `User.PeriodUsed()`**：lifetime - baseline，O(1) 内存计算；mailer 重复实现合并 |
| 空 delta snapshot | 即使 client 没新流量也每轮写入 | **零 delta 跳过**：用户离线时 client snapshot 写入量约降至 1/3 |
| 升级模式 | 原地 ALTER / DROP | **side-by-side 新库**（Cloudreve `drive`/`drive_v2` 模式）；v3.0.0 主程序只识别新 schema，旧库永久 backup；迁移由 `cmd/migrate-db-v2/` 一次性脚本完成（跑完即删） |

> 自 v3.0.0 起版本号回归标准 semver。下面两张表是 v3.0.0 语义化版本化**之前**的内部开发里程碑记录（v6/v7/v8 是历史内部代号，不是 semver），作为项目早期演进的存档保留，不再新增同类条目——v3.0.0 之后的变更一律进 [CHANGELOG.md](../CHANGELOG.md)，重大特性额外在本文档相应章节记录（如 §7 数据流、§9 API）。

## 变更摘要 (v7 → v8)

| 维度 | v7 | v8 |
|---|---|---|
| 认证 | SAML SSO | **SAML + OIDC 双 SSO 支持** |
| 订阅客户端 | 仅 mihomo | **mihomo + sing-box，支持客户端检测与阻止** |
| 存储 | 部分 YAML | **运行时配置迁到 MySQL**；规则集/模板继续 YAML 文件 |
| 邮件通知 | 无 | **完整邮件系统**（到期提醒、停用通知、公告群发） |
| 同步机制 | 简单同步 | **异步任务队列**（sync_tasks 表，支持重试） |
| 日志 | 仅审计 | **审计 + 订阅访问日志** |
| 用户自助 | 基础 | **紧急访问、凭证重置、个人规则** |
| 品牌定制 | 无 | **站点名称、Logo、Icon、页脚文本可自定义** |

## 变更摘要 (v6 → v7)

| 维度 | v6 | v7 |
|---|---|---|
| 认证 | 本地账号 | **SAML SSO (Entra ID) 主 + 本地账号备** |
| 业务数据存储 | YAML 文件 | **MySQL**（用户/分组/归属表/节点元数据/流量） |
| 配置内容存储 | YAML | YAML（规则集/模板保持不变）|
| client.email 约定 | `psp_{username}` | **统一格式 `u{userID}-n{nodeID}@{domain}`**（该格式本身已在 v3.9.0 被共享 client 模型取代，见 §2.1） |
| 节点管理 | 仅 3X-UI 后台 | **面板提供完整 inbound CRUD**（封装 3X-UI API） |
| 多协议凭证 | 各协议独立 | **统一从 UUID 派生**（含 Trojan / SS / SS-2022） |
| 流量限额 | 复用 3X-UI total_gb | **面板主动管理 + 超限自动 disable 全部归属 client** |
| 分组结构 | tag_filter 单一过滤 | **tag_filter + layout（排序权重 + 自定义分隔符占位）** |

---

## 1. 项目背景与目标

### 1.1 现状

维护者使用 [3X-UI](https://github.com/MHSanaei/3x-ui) 自建一组代理节点对外分发，当前痛点：

- Clash 订阅文件手写散落，分流规则改动要逐份同步
- 节点新增需在每份 yaml 各 proxy-group 重复粘贴
- 无到期日/流量管理，凭管理员记忆运营
- 凭证、节点配置散布多份文件，撤销不便
- 朋友无自助查看到期/流量的渠道

### 1.2 设计目标

| 类别 | 目标 |
|---|---|
| **核心** | 所有运维操作走管理后台 UI，零手编配置 |
| **核心** | 订阅 URL 动态渲染，节点/规则/模板改一处全员生效 |
| **核心** | 完整 inbound CRUD 集成（面板封装 3X-UI API） |
| **核心** | SSO 登录（SAML / OIDC），保留本地账号作为 fallback |
| **核心** | 严格管理边界，**绝不**误伤非纳管资源 |
| **核心** | 流量限额由面板主动管理，超限自动暂停代理服务（不锁面板登录，见 §7.9） |
| 次要 | 用户自助页（到期、流量、订阅 URL、改密码、2FA/passkey 自助管理） |
| 次要 | 多客户端格式（mihomo / Sing-box / URI list） |
| 次要 | 流量历史曲线（日/月/永久） |
| 次要 | 审计日志 + 认证事件日志 + 邮件日志 + 证书事件日志 |
| **Future** | Canvas LMS 联动（LTI 1.3 / 外部工具）——见 §19 |

### 1.3 非目标

- 不做企业级机场系统（无支付、分销、邀请、工单）
- 不做高可用（单机部署，朋友圈量级，备份足矣）
- 不做客户端 App

---

## 2. 核心概念

| 概念 | 说明 | 真相源 |
|---|---|---|
| **节点 (Node)** | 一个 3X-UI inbound = 一个节点 | 3X-UI（凭据回显）+ 面板（**自 v3.5 起是连接配置的真相源**，见 §3.2）|
| **客户端 (Client)** | 3X-UI 里的一条 client 记录。**自 v3.9.0 起，一个面板用户在同一 3X-UI 面板上通常只对应一个共享 client**，挂载到该用户所有可访问的 inbound（详见下方"共享 client"） | 3X-UI（凭据回显）+ 面板 `psp_clients`（凭据/流量真相源） |
| **共享 client / `psp_client`** | v3.9.0 模型：一行 = 一个 `(user, panel, credClass)`，存 email/uuid/password + 流量计数，通过 `psp_client_inbounds` 挂载到该用户在该面板上可访问的多个 inbound。取代了 v3.8 及更早"每个 (user, node) 一个独立 client"的模型 | 面板 MySQL `psp_clients` / `psp_client_inbounds` |
| **用户 (User)** | 面板侧逻辑用户，在每个面板上对应一个共享 client | 面板 MySQL |
| **UPN (User Principal Name)** | 所有用户的唯一标识（本地用户和 SSO 用户统一） | 面板 MySQL |
| **分组 (Group)** | 用户分组，含 tag_filter + layout + 可覆盖的 scope 设置（v3.8.0，见 §6.3） | 面板 MySQL |
| **归属表 (Ownership) / `user_xui_clients`** | v3.0–v3.8 模型：每个用户在 3X-UI 里拥有的 client 白名单，一行 = 一个 (user, node)。**v3.9.0 起为遗留表**，新用户/新挂载不再写入，仅供未完成迁移的存量数据与过渡期回退逻辑使用（`MIGRATION(v3→v4)` 标记，计划 v4.0.0 删除，见 [migration/v3-to-v4-cleanup.md](migration/v3-to-v4-cleanup.md)） | 面板 MySQL |
| **规则集 (Rule Set)** | Clash/sing-box rules 分片 + 策略组顺序 | 面板 YAML (`config/rulesets/*.yaml`) |
| **模板 (Template)** | Clash/Sing-box 配置框架 | 面板 YAML (`config/templates/*.yaml`) |
| **layout** | 分组级渲染布局（节点排序 + 分隔符占位） | 面板 MySQL |
| **同步任务 (Sync Task)** | 异步可重试的 3X-UI 操作 | 面板 MySQL |
| **订阅客户端注册表** | UA 检测族 → 渲染格式 → 导入 App 两层注册表 | 面板 MySQL (`UISettings.SubClients`) |
| **作用域设置 (scope settings)** | v3.8.0：全局默认 + 各 Group 覆盖的两级设置模型，见 §6.3 | 面板 MySQL `scope_settings` |
| **账号状态 / 服务状态** | v3.9.0 起两条独立轴：账号状态（`enabled` + `auto_disabled_reason`）只管面板登录；服务状态（`service_disabled_reason`）只管代理/订阅可用性，见 §7.9 | 面板 MySQL `users` |
| **IdP / SP** | 身份提供方 / 服务提供方 (本面板) | - |
| **SAML metadata** | IdP 公布的 XML，定义证书、端点、claim 映射 | IdP |

### 2.1 唯一识别符体系

#### 标识符全景

| 层 | 标识符 | 类型 | 用途 |
|---|---|---|---|
| **面板用户** | `users.id` | INT auto | 面板内部主键 |
| | `users.upn` | VARCHAR(255) UNIQUE | **所有用户的唯一标识**（本地用户和 SSO 用户统一） |
| | `users.email` | VARCHAR(255) | 通知邮箱（SSO 用户来自 Email claim） |
| | `users.display_name` | VARCHAR(128) | 显示名称（SSO 来自 claim，本地由管理员设置） |
| | `users.uuid` | UUID v4 | **协议凭证**（VLESS/VMess 的 uuid；Trojan/SS 派生 password）|
| | `users.sub_token` | 32B base64url | 订阅 URL 凭证（独立于 uuid，可单独重置）|
| **3X-UI inbound** | `inbound.id` | INT | 3X-UI 内部主键 |
| | `(panel_id, inbound_id)` | 元组 | 跨多个 3X-UI 面板的全局引用 |
| **3X-UI client（共享，v3.9.0+）** | `client.uuid` / `password` | 字符串 | 存于面板，作为渲染与下发的单一真相源（不再现算派生），见下 |
| | `client.email` | VARCHAR | **面板内按用户全局唯一的匹配键**（不再区分 inbound） |
| **面板 nodes** | `nodes.id` | INT auto | 内部主键 |
| | `(panel_id, inbound_id)` | 唯一索引 | 与 3X-UI inbound 1:1 映射 |
| **共享 client 表 `psp_clients`** | `(panel_id, email)` | 唯一索引 | 一个面板用户在一个面板上的共享 client；`(user_id, panel_id, cred_class)` 是概念键 |
| **挂载表 `psp_client_inbounds`** | `(client_id, node_id)` | 唯一索引 | 共享 client 挂载到的 PSP 节点集合，`provisioned` 标记 3X-UI 侧已确认 |
| **遗留归属表 `user_xui_clients`**（v3.0–v3.8，过渡期保留） | `(panel_id, inbound_id, client_email)` | 唯一索引 | 面板用户 ←→ 3X-UI client 匹配（v3.0.0 前叫 `xui_clients`）；不再是新增数据的目标 |

#### 关键设计：**email 是主匹配键，凭据全对称存储**

| 跨系统操作 | 匹配键 | 原因 |
|---|---|---|
| 拉某用户在某面板的流量 | **email** | 一个 email 现在对应**一个**共享 client，3X-UI 对同一 email 的每个挂载 inbound 回显同一份聚合流量；面板按 email 读一次即拿到该用户在该面板的总量，**不再按 inbound 求和**（否则会重复计数） |
| 建/改一个用户在某面板的 client | email | `POST /clients/add`（携带 `inboundIds[]` 一次挂多个 inbound）/ `POST /clients/update/{email}`；不再需要先 GET inbound 再改 `settings.clients[]` |
| 调整某用户可访问的 inbound 集合 | email + `inboundIds[]` | `attach` / `detach` / `bulkAttach` / `bulkDetach` 增量挂载/摘除，一次调用覆盖多个 inbound（Xray 只重启一次） |
| 删除某用户在某面板的 client | email | `POST /clients/del/{email}`，一次删光该 email 在所有 inbound 的挂载 |

#### email 命名约定（v3.9.0，共享 client 模型）

所有用户（本地 + SSO）统一格式，由 `domain.PSPClientEmail` 生成：

| 场景 | 格式 | 例 |
|---|---|---|
| 默认（`CredClass=0`，绝大多数用户） | `u{userID}@{domain}` | `u42@psp.local` |
| 同一用户同一面板同时有 SS-2022-128 与 SS-2022-256 inbound（极少见） | `u{userID}-c1@{domain}` | `u42-c1@psp.local` |
| 同一用户同一面板存在不同 flow 的 VLESS inbound 混用（罕见） | `u{userID}-k{8hex}@{domain}` | `u42-k1a2b3c4d@psp.local` |

- `domain` 来自 `UISettings.EmailDomain`（默认 `psp.local`）
- **v3.8 及更早的 `u{userID}-n{nodeID}@{domain}`（按节点区分）已被取代**：一个用户在同一面板现在通常只有一个共享 client，跨所有挂载的 inbound；`CredClass`/flow 后缀只在协议凭据不兼容时才分裂出第二个 client
- 历史导入的 client（老朋友手工建的）**保留原 email** 不强制改名，且不并入共享模型（见 §7.8）

#### 凭据存储（v3.9.0：全对称，不再现算）

`psp_clients` 表直接存 `UUID` 和 `Password`（生成一次、落库、后续 render 与下发都读存值，不再有"两处独立实现同一个派生函数"的 lockstep 风险）：

| 协议 | 字段 | 值 |
|---|---|---|
| VLESS | id | `psp_client.UUID` |
| VMess | id | `psp_client.UUID` |
| Hysteria2 | auth | `psp_client.UUID` |
| Trojan | password | `psp_client.Password` |
| SS legacy | password | `psp_client.Password` |
| SS-2022 | password | `psp_client.Password`（格式：`base64(32 字节)`，同时是合法的 Trojan/SS/SS-2022-256 凭据） |

`Password` 由 `crypto.NewProxyPassword(UUID)` 一次性确定性生成（`= base64(SHA-256(UUID))`，与旧版 SS-2022 派生值相同，故 v3.9.0 迁移对 VLESS/VMess/Hysteria2/SS-2022-256 用户是**逐字节无感**的；Trojan/普通 SS 用户的密码从"直接等于 UUID"改为这个新值，迁移后需要重拉一次订阅）。UUID 重置会重新生成并推送到共享 client，所有协议密码同步刷新。

---

## 3. 整体架构

```
┌──────────────────────────────────────────────────────────────┐
│         React 18 + MUI (Material Design 3) 管理后台 SPA        │
│                                                               │
│  /login          [SSO 登录] 或 [本地账号登录 + 2FA/passkey]     │
│  Dashboard       仪表盘                                       │
│  Directory       用户管理 / 分组管理（含 scope 覆盖 Policies）  │
│  Infrastructure  服务器 / 节点 / 证书管理（ACME/DNS）           │
│  Subscription    规则库 / 配置方案 / 订阅客户端注册表           │
│  Reporting       流量看板 / 日志（审计/订阅/认证/邮件/证书）    │
│                  / 同步任务                                    │
│  Settings        基本设置 / 账户安全（scope 轨）/ 品牌 / 订阅   │
│                  策略（scope 轨）/ 邮件 / SSO / 运行时          │
│  /user/me        用户自助（含 2FA、passkey、恢复码自助管理）    │
└─────────────────────────────┬────────────────────────────────┘
                              │ REST + JWT
                              ▼
┌──────────────────────────────────────────────────────────────┐
│                   Go 后端 (Gin + RBAC)                        │
│                                                               │
│  HTTP 层：                                                     │
│  ├─ /api/auth/*              认证（本地/SAML/OIDC/2FA/passkey/│
│  │                             找回密码/自助注册）              │
│  ├─ /api/admin/*             管理 API (role=admin/operator)   │
│  ├─ /api/user/me/*           用户自助 API                      │
│  ├─ /{sub_path}/:token        订阅渲染（公开，动态路径）         │
│  └─ /health, /api/version     健康检查 / 版本                  │
│                                                               │
│  Service 层（节选，按职责分组）：                                │
│  ├─ 认证/账户安全：AuthSvc（本地/SAML/OIDC）、AuthPolicySvc     │
│  │   （2FA 强制策略）、TOTPSvc、PasskeySvc、LoginGuardSvc       │
│  │   （锁定）、CaptchaSvc、RecoverySvc（找回密码）、             │
│  │   RegistrationSvc（自助注册）                                │
│  ├─ 用户/分组：UserSvc（CRUD、改组联动 sync、账号/服务状态）、   │
│  │   GroupSvc（CRUD + layout + scope 覆盖）、ScopedSettingsSvc  │
│  ├─ 节点/客户端：NodeSvc（inbound CRUD）、InboundCfgSvc         │
│  │   （轴 A 连接配置读写）、SyncSvc（遗留 per-node 写护栏）、    │
│  │   SharedClientSvc（v3.9.0 共享 client 建立/挂载/生命周期）   │
│  ├─ 渲染/流量：RenderSvc（mihomo/sing-box/URI list）、          │
│  │   TrafficSvc（cron 拉取 + 快照 + 超限暂停服务）              │
│  ├─ 运维：ReconcileSvc（周期对账，轴 A + 轴 B）、AuditSvc、      │
│  │   MailerSvc（到期/停用/公告）、CertSvc（ACME 证书自动化）、   │
│  │   GeoIPSvc（IP 归属地库自动更新）                             │
└──────┬─────────────────────────────────────┬────────────────┘
       ▼                                     ▼
┌─────────────────────┐              ┌─────────────────────┐
│    MySQL / SQLite    │              │    3X-UI HTTP API   │
│  业务/运行时配置表    │              │   (N 个面板)         │
└─────────────────────┘              └─────────────────────┘
       ▲
       │
┌──────┴───────────────────────────────────────────────────┐
│      文件系统 (config/)                                    │
│  config.yaml          主配置（最小化：listen/jwt/db）        │
│  rulesets/*.yaml      规则集 + 策略组顺序                    │
│  templates/*.yaml     订阅模板                              │
└──────────────────────────────────────────────────────────┘
```

### 3.1 存储分层

| 数据 | 存储 | 理由 |
|---|---|---|
| 业务数据（users/groups/nodes/psp_clients/traffic/audit 等） | **MySQL / SQLite** | 频繁读写、需事务、需 JOIN |
| 配置数据（settings KV / scope_settings / mail_settings / saml_settings / oidc_settings / xui_panels） | **MySQL / SQLite** | 运行时可编辑，管理员通过 UI 修改 |
| 规则集内容 | **YAML 文件** | 本地配置资产，支持 Monaco 编辑器和版本发布默认值 |
| 订阅模板 | **YAML 文件** | 本地配置资产，支持 Monaco 编辑器和版本发布默认值 |
| 主配置 (config.yaml) | **YAML 文件** | 启动时加载，最小化配置（listen/jwt/db） |

**注意**：3X-UI 面板凭证、SAML/OIDC、邮件、UI 设置、分组覆盖设置等运行时配置存 MySQL/SQLite；规则集和订阅模板仍以 YAML 文件为真相源，不做数据库迁移。

### 3.2 本地优先与远端异步一致性

用户、分组、节点元数据、inbound 连接配置等面板状态以本地数据库为真相源。管理员操作先提交本地状态，再尝试同步 3X-UI；远端失败（面板离线、单节点错误等）不回滚本地操作，而是写入 `sync_tasks` 异步重试。

| 场景 | 本地处理 | 远端处理 |
|---|---|---|
| 修改用户启用、到期、分组 | 先更新 `users` / `groups` | `ResyncMembership` 把变更推到该用户所有共享 client（生命周期 + 挂载集）；失败进入 `user_resync` 任务 |
| 紧急访问、重置订阅/协议凭证 | 先写新凭证或新到期时间 | 推共享 client 失败时进入 `user_resync` |
| 节点元数据启停 | 先更新 `nodes.enabled` 等本地字段 | 推 inbound enable 失败时进入节点同步任务 |
| 新建 inbound | 需要先拿到 3X-UI 返回的 `inbound_id`，随后把整份连接配置写入本地快照 | 远端创建失败时仅保留 `node_create` 任务，不创建无法映射的本地节点 |
| 修改 inbound 连接配置 | **先写本地配置快照（`nodes` 的 inbound 配置列），local-first** | 再推 3X-UI；失败进入 `node_update`。**v3.5 起 PSP 是 inbound 连接配置的真相源**（端口/TLS/Reality/stream 等存本地、render 只读本地、reconcile 反向下发覆盖漂移），详见 [inbound-ownership.md](inbound-ownership.md) |

---

## 4. 管理边界（核心安全约束）

⚠️ **本节决定面板"绝不误伤"用户私人资源的能力，是整个系统最重要的设计。**

### 4.1 现状观察

3X-UI 实际部署中，单个 inbound 内部混杂了维护者私人客户端与朋友客户端。**inbound 级别无法干净划分纳管/非纳管**，必须下沉到 client 级别。

### 4.2 边界规则

| 层 | 是否过滤 | 识别方式 |
|---|---|---|
| inbound（读 / 列表） | 不过滤，全部可见 | - |
| inbound（写：修改） | 允许，但提示"含未纳管 client" | 见 §4.4 |
| inbound（写：删除） | **必须 inbound 内全部 client 都在纳管范围内**（遗留归属表 `user_xui_clients` **或** 共享 client 表 `psp_clients` 命中即算纳管） | 见 §4.4 |
| client（读 / 列表） | 不过滤，UI 区分纳管/未纳管 | - |
| **client（写）** | **必须命中纳管范围**（同上，两张表任一命中即可） | 见 §4.3 |

### 4.3 client 写护栏

```go
// internal/service/sync/sync.go（遗留 per-node 写路径，过渡期仍保留）
func (s *Service) ensureClientOwned(ctx context.Context, panelID int64, inboundID int, email string) error {
    exists, err := s.ownership.Exists(ctx, panelID, inboundID, email)
    if err != nil {
        return err
    }
    if !exists {
        return fmt.Errorf("%w: panel_id=%d inbound=%d email=%s",
            domain.ErrClientNotOwnedByPanel, panelID, inboundID, email)
    }
    return nil
}
```

所有调 3X-UI 写 client API（`AddClient` / `UpdateClient` / `DelClientByEmail`）入口都过此 guard。未命中归属表 → 拒绝执行。v3.9.0 起，新建/新挂载走 `SharedClientSvc.ProvisionClient`（流程见 §7.5.3），其安全边界由 `psp_client_inbounds` 的挂载集 + 建号/改组的业务逻辑本身保证（只会把用户挂到其分组 `tag_filter` 匹配的节点），语义上等价但不复用这段遗留护栏代码。

### 4.4 inbound 写护栏

```go
// internal/service/sync/sync.go
func (s *Service) ensureInboundDeletable(ctx context.Context, panelID int64, inboundID int) error {
    c, err := s.pool.Get(panelID)
    if err != nil {
        return err
    }
    in, err := c.GetInbound(ctx, inboundID)
    if err != nil {
        return err
    }
    for _, cs := range in.ClientStats {
        ok, err := s.ownership.Exists(ctx, panelID, inboundID, cs.Email)
        if err != nil {
            return err
        }
        if ok {
            continue
        }
        // v3.9.0：遗留归属表未命中时，回落检查是否为共享 client 纳管
        if s.pspClients != nil {
            if c2, perr := s.pspClients.GetByEmail(ctx, panelID, cs.Email); perr == nil && c2 != nil {
                continue
            }
        }
        return fmt.Errorf("%w: panel_id=%d inbound=%d unmanaged_client=%s",
            domain.ErrInboundHasUnmanagedClients, panelID, inboundID, cs.Email)
    }
    return nil
}
```

`UpdateInbound` 允许（含未纳管 client 也能改），但 UI 提示"该 inbound 内有 N 个未纳管 client，修改会影响他们"。`DeleteInbound` 必须全部 client 纳管。

> 这类"遗留归属表 否则 回落共享 client"的双检查分支散布在多处（`ensureInboundDeletable`、`node.go ListClientsOfInbound`、`admin_node.go ClaimClient`、`traffic.go PollOnce`/`UserServerUsage`），全部标了 `MIGRATION(v3→v4)` 注释，登记在 [migration/v3-to-v4-cleanup.md](migration/v3-to-v4-cleanup.md)，v4.0.0 删除遗留归属表时需要**手动**清理（编译器不会报错提示，因为共享 client 分支仍然合法存在）。

### 4.5 UI 中的区分

| 场景 | 表现 |
|---|---|
| 节点详情页 client 列表 | 纳管标蓝色 + 可操作；未纳管灰色不可点 |
| 流量看板 | 仅统计纳管 client |
| 对账任务 | 仅扫描纳管范围内的 client，其他完全不感知 |

---

## 5. 数据模型

### 5.1 MySQL / SQLite Schema (v3.9.0+)

全部表通过 GORM AutoMigrate 自动创建。规则集/模板由 YAML 仓储读取，不以数据库表为真相源。

**总览（31 张表，按职责分 7 类，详见 [internal/adapters/sqlstore/schema.go](../internal/adapters/sqlstore/schema.go) 及各表专属 `*_repo.go`）**：

| 分类 | 表 | 说明 |
|---|---|---|
| **配置 (6)** | `settings` | KV 主配置（type/name/value/encrypted/updated_at），类型分组：site / auth / sub / security / runtime / notice / notify / geo / cert |
| | `scope_settings` | v3.8.0：per-Group 设置覆盖（稀疏表，只有显式覆盖的字段才有行），叠加在 `settings` 之上，见 §6.3 |
| | `mail_settings` | SMTP 连接（单行） |
| | `saml_settings` | SAML SSO（单行） |
| | `oidc_settings` | OIDC SSO（单行） |
| | `xui_panels` | 下游 3X-UI 面板凭据 |
| **业务实体 (7)** | `users` | 含 `lifetime_*_bytes` + `period_baseline_bytes` 用于 O(1) 用量计算；v3.7 起含 TOTP/恢复码；v3.9 起账号状态与服务状态拆分为独立字段（见 §2） |
| | `groups_` | 用户分组（`groups` 在某些 MySQL 版本是关键字） |
| | `nodes` | 3X-UI inbound 在面板侧的元数据 + **v3.5 起完整连接配置快照**（端口/TLS/Reality/stream/sniffing/allocate）+ v3.6 证书绑定 + v3.8 中转线路 |
| | `nodes_separator` | 分组渲染布局中的纯展示分隔行（不对应任何 3X-UI inbound） |
| | `psp_clients` | **v3.9.0 一等公民**：一行 = 一个 (user, panel, credClass) 共享 client，存 uuid/password + 流量计数 |
| | `psp_client_inbounds` | 共享 client 的挂载junction，`(client_id, node_id)` 唯一，含 `provisioned`（3X-UI 侧已确认）与 `flow_override` |
| | `user_xui_clients` | **遗留**（v3.0–v3.8 每节点一个 client 的归属表）；v3.9.0 起不再新增写入，过渡期只读兼容，计划 v4.0.0 删除 |
| **认证/账户安全 (3, v3.7.0+)** | `webauthn_credentials` | 用户注册的 passkey/WebAuthn 凭据 |
| | `auth_tokens` | 找回密码 / 邮箱验证 / 自助注册的一次性令牌与验证码 |
| | `auth_events` | 登录/2FA 验证事件审计轨迹 |
| **证书 (4, v3.6.4+)** | `acme_accounts` | ACME CA 账户资料 |
| | `dns_credentials` | ACME DNS-01 挑战用的 DNS 服务商凭据 |
| | `tls_certificates` | PSP 托管的 TLS 证书（状态：pending/valid/failed，自动续期） |
| | `cert_events` | 证书签发/续期事件日志 |
| **时序快照 (6)** | `traffic_snapshots` / `traffic_snapshots_hourly` | user 级流量（lifetime 语义 + 小时聚合，用于看板加速） |
| | `client_traffic_snapshots` / `client_traffic_snapshots_hourly` | per-client 流量历史 + 小时聚合 |
| | `node_traffic_snapshots` / `node_traffic_snapshots_hourly` | node 级流量历史 + 小时聚合 |
| **日志/事件 (4)** | `audit_log` | admin 操作审计 |
| | `sub_logs` | 订阅访问日志 |
| | `mail_sent` | 发件历史 + 幂等键 (unique user_id,kind,window_key) |
| | `sync_tasks` | 同步任务（带状态/重试） |
| | `mail_templates` | 邮件模板（按 kind 主键，多行） |

**v3.0.0 升级迁移**：通过独立的 `cmd/migrate-db-v2/` 一次性程序完成。v3.0.0 主程序**完全不识别旧 schema**；旧库由 admin 手工保留作永久 backup，无原地 ALTER。详见 [docs/UPGRADE-v3.0.0.md](UPGRADE-v3.0.0.md) 与 `cmd/migrate-db-v2/README.md`。v3.0.0 → v3.9.0 之间的表结构演进全部靠 GORM AutoMigrate 增量加列/加表（无破坏性变更、无需迁移工具），详见各版本 [CHANGELOG.md](../CHANGELOG.md)。

**SQL DDL（节选关键字段，完整定义以 [internal/adapters/sqlstore/schema.go](../internal/adapters/sqlstore/schema.go) 为准）**：

```sql
-- 用户：v3.0.0 lifetime/period 基线 + v3.7.0 2FA + v3.9.0 账号/服务状态拆分
CREATE TABLE users (
  id                       BIGINT AUTO_INCREMENT PRIMARY KEY,
  upn                      VARCHAR(255) UNIQUE NOT NULL,
  sso_provider             VARCHAR(64) NOT NULL DEFAULT 'local',
  sso_subject              VARCHAR(255) NOT NULL DEFAULT '',
  email                    VARCHAR(255),
  password_hash            VARCHAR(255),
  role                     VARCHAR(16) NOT NULL DEFAULT 'user',
  sub_token                VARCHAR(64) UNIQUE NOT NULL,
  uuid                     CHAR(36) NOT NULL,
  group_id                 BIGINT NOT NULL INDEX,
  enabled_rule_sets        JSON,
  personal_rules           TEXT,
  expire_at                DATETIME,
  traffic_limit_bytes      BIGINT,
  traffic_reset_period     VARCHAR(16) DEFAULT 'never',
  traffic_period_start     DATETIME,
  lifetime_up_bytes        BIGINT DEFAULT 0,
  lifetime_down_bytes      BIGINT DEFAULT 0,
  lifetime_total_bytes     BIGINT DEFAULT 0,
  period_baseline_bytes    BIGINT DEFAULT 0,
  lifetime_baseline_at     DATETIME,
  display_name             VARCHAR(128),
  remark                   VARCHAR(255),
  -- 账号状态（只管面板登录）
  enabled                  BOOLEAN NOT NULL,
  auto_disabled_reason     VARCHAR(32),
  disable_detail           TEXT,
  -- 服务状态（v3.9.0 新增；只管代理/订阅可用性，与账号登录解耦，见 §7.9）
  service_disabled_reason  VARCHAR(32) NOT NULL DEFAULT '',
  service_disable_detail   TEXT,
  service_disabled_at      DATETIME,
  self_registered          BOOLEAN NOT NULL DEFAULT FALSE,   -- v3.8.0 自助注册标记
  block_violation_count    INT DEFAULT 0,
  last_block_violation_at  DATETIME,
  emergency_used_count     INT,
  emergency_until          DATETIME,
  emergency_baseline_bytes BIGINT DEFAULT 0,
  token_version            INT NOT NULL DEFAULT 0,           -- v3.7.0：JWT 失效版本号
  -- 2FA / TOTP（v3.7.0）
  totp_secret              VARCHAR(255) NOT NULL DEFAULT '', -- AES-GCM 密文
  totp_enabled              BOOLEAN NOT NULL DEFAULT FALSE,
  recovery_codes            JSON,                             -- 备用码 SHA-256 哈希数组
  last_online_at            DATETIME,                         -- 最近一次任意归属 client 活跃时间
  created_at                DATETIME,
  updated_at                DATETIME,
  INDEX idx_user_sso (sso_provider, sso_subject)
);

CREATE TABLE groups_ (
  id BIGINT AUTO_INCREMENT PRIMARY KEY, slug VARCHAR(64) UNIQUE NOT NULL,
  name VARCHAR(128) NOT NULL, tag_filter JSON, layout JSON,
  remark VARCHAR(255), require_2fa BOOLEAN NOT NULL DEFAULT FALSE,
  created_at DATETIME
);

-- nodes：v3.5.0 起新增完整 inbound 连接配置快照（轴 A，见 inbound-ownership.md）
-- + v3.6.4 证书绑定 + v3.8.0 中转线路 + v3.9.0 节点级流量基线
CREATE TABLE nodes (
  id BIGINT AUTO_INCREMENT PRIMARY KEY,
  panel_id BIGINT NOT NULL INDEX, inbound_id INT NOT NULL,
  display_name VARCHAR(255) NOT NULL, server_address VARCHAR(255),
  flow VARCHAR(64), protocol VARCHAR(32) DEFAULT '', port INT DEFAULT 0,
  region VARCHAR(16) NOT NULL, tags JSON,
  sort_order INT DEFAULT 0, enabled BOOLEAN DEFAULT TRUE,
  kind VARCHAR(16) DEFAULT 'real',
  lifetime_up_bytes BIGINT DEFAULT 0, lifetime_down_bytes BIGINT DEFAULT 0,
  lifetime_total_bytes BIGINT DEFAULT 0,
  last_traffic_up_bytes BIGINT DEFAULT 0, last_traffic_down_bytes BIGINT DEFAULT 0,
  last_traffic_total_bytes BIGINT DEFAULT 0,
  -- v3.9.0：节点级流量改读 inbound 自身计数器，需要基线做单调 delta
  last_inbound_up_bytes BIGINT DEFAULT 0, last_inbound_down_bytes BIGINT DEFAULT 0,
  last_inbound_total_bytes BIGINT DEFAULT 0, last_inbound_seeded BOOLEAN DEFAULT FALSE,
  health_state VARCHAR(32) DEFAULT '', health_checked_at DATETIME,
  health_detail VARCHAR(512) DEFAULT '',
  -- v3.5.0 inbound 连接配置快照（轴 A；去 clients[]，下发时 RMW 合并保留 live client）
  inbound_listen VARCHAR(64) DEFAULT '', inbound_remark VARCHAR(255) DEFAULT '',
  inbound_settings TEXT, stream_settings TEXT, sniffing TEXT, allocate TEXT,
  inbound_expiry_time BIGINT DEFAULT 0,
  config_synced_at DATETIME, config_sync_state VARCHAR(32) DEFAULT '', -- synced/drift/pending
  -- v3.6.4 托管证书绑定
  cert_source VARCHAR(16) DEFAULT '', cert_id BIGINT DEFAULT 0 INDEX,
  -- v3.8.0 中转 / 转发线路
  relays JSON, hide_direct BOOLEAN DEFAULT FALSE,
  created_at DATETIME,
  UNIQUE KEY uk_panel_inbound (panel_id, inbound_id)
);

CREATE TABLE nodes_separator (
  id BIGINT AUTO_INCREMENT PRIMARY KEY, name VARCHAR(255) NOT NULL,
  mode VARCHAR(16) NOT NULL DEFAULT 'global', node_ids JSON,
  sort_order INT DEFAULT 0, created_at DATETIME
);

-- v3.9.0 共享 client 模型：一个 (user,panel,credClass) 一个 client，跨多 inbound
CREATE TABLE psp_clients (
  id BIGINT AUTO_INCREMENT PRIMARY KEY,
  user_id BIGINT NOT NULL INDEX, panel_id BIGINT NOT NULL,
  email VARCHAR(255) NOT NULL, cred_class INT NOT NULL DEFAULT 0,
  uuid VARCHAR(36) NOT NULL DEFAULT '', password VARCHAR(128) NOT NULL DEFAULT '',
  lifetime_up_bytes BIGINT DEFAULT 0, lifetime_down_bytes BIGINT DEFAULT 0,
  lifetime_total_bytes BIGINT DEFAULT 0,
  last_raw_up_bytes BIGINT DEFAULT 0, last_raw_down_bytes BIGINT DEFAULT 0,
  last_raw_total_bytes BIGINT DEFAULT 0,
  period_baseline_up_bytes BIGINT DEFAULT 0, period_baseline_down_bytes BIGINT DEFAULT 0,
  period_baseline_total_bytes BIGINT DEFAULT 0,
  created_at DATETIME,
  UNIQUE KEY uk_psp_client (panel_id, email)
);

CREATE TABLE psp_client_inbounds (
  id BIGINT AUTO_INCREMENT PRIMARY KEY,
  client_id BIGINT NOT NULL INDEX, node_id BIGINT NOT NULL,
  flow_override VARCHAR(64) NOT NULL DEFAULT '',
  provisioned BOOLEAN DEFAULT FALSE,
  UNIQUE KEY uk_psp_client_inbound (client_id, node_id)
);

-- 遗留：v3.0–v3.8 每节点一个 client 的归属表；v3.9.0 起不再新增，仅过渡期兼容
CREATE TABLE user_xui_clients (
  id BIGINT AUTO_INCREMENT PRIMARY KEY,
  user_id BIGINT NOT NULL INDEX, panel_id BIGINT NOT NULL INDEX,
  inbound_id INT NOT NULL, client_email VARCHAR(255) NOT NULL,
  client_uuid VARCHAR(36) NOT NULL, created_at DATETIME,
  lifetime_up_bytes BIGINT DEFAULT 0, lifetime_down_bytes BIGINT DEFAULT 0,
  lifetime_total_bytes BIGINT DEFAULT 0,
  last_raw_up_bytes BIGINT DEFAULT 0, last_raw_down_bytes BIGINT DEFAULT 0,
  last_raw_total_bytes BIGINT DEFAULT 0,
  UNIQUE KEY uk_owner_match (panel_id, inbound_id, client_email)
);

-- 流量时序：三类主表 + 三类小时聚合表，语义统一为 lifetime
CREATE TABLE traffic_snapshots (
  id BIGINT AUTO_INCREMENT PRIMARY KEY, user_id BIGINT NOT NULL,
  up_bytes BIGINT, down_bytes BIGINT, total_bytes BIGINT,
  captured_at DATETIME NOT NULL,
  KEY idx_user_time (user_id, captured_at)
);
-- traffic_snapshots_hourly / client_traffic_snapshots_hourly / node_traffic_snapshots_hourly
-- 结构与对应主表相同，captured_at 改为 hour_start，用于看板加速，主表仍是明细真相源

CREATE TABLE client_traffic_snapshots (
  id BIGINT AUTO_INCREMENT PRIMARY KEY,
  user_id BIGINT NOT NULL, panel_id BIGINT NOT NULL,
  inbound_id INT NOT NULL, client_email VARCHAR(255) NOT NULL,
  up_bytes BIGINT, down_bytes BIGINT, total_bytes BIGINT,
  captured_at DATETIME NOT NULL,
  KEY idx_client_time (user_id, panel_id, inbound_id, client_email, captured_at)
);

CREATE TABLE node_traffic_snapshots (
  id BIGINT AUTO_INCREMENT PRIMARY KEY, node_id BIGINT NOT NULL,
  up_bytes BIGINT, down_bytes BIGINT, total_bytes BIGINT,
  captured_at DATETIME NOT NULL,
  KEY idx_node_time (node_id, captured_at)
);

CREATE TABLE xui_panels (
  id BIGINT AUTO_INCREMENT PRIMARY KEY, name VARCHAR(128) UNIQUE NOT NULL,
  url VARCHAR(512) NOT NULL,
  api_token TEXT,    -- AES-GCM 密文
  username VARCHAR(255),
  password TEXT,     -- AES-GCM 密文
  panel_version VARCHAR(32) DEFAULT '', xray_version VARCHAR(32) DEFAULT '', -- v3.6.0 版本感知
  version_checked_at DATETIME,
  remark VARCHAR(255), created_at DATETIME, updated_at DATETIME
);

CREATE TABLE audit_log (
  id BIGINT AUTO_INCREMENT PRIMARY KEY,
  actor VARCHAR(255) NOT NULL, action VARCHAR(64) NOT NULL,
  target VARCHAR(255), before_json JSON, after_json JSON,
  ip VARCHAR(64), at DATETIME INDEX
);

-- v3.7.0：登录 / 2FA 验证事件审计轨迹（供账户锁定与安全排查用）
CREATE TABLE auth_events (
  id BIGINT AUTO_INCREMENT PRIMARY KEY, user_id BIGINT NOT NULL,
  event_type VARCHAR(32) NOT NULL, status VARCHAR(16),
  ip_address VARCHAR(64), user_agent VARCHAR(512), details JSON,
  created_at DATETIME INDEX
);

CREATE TABLE sub_logs (
  id BIGINT AUTO_INCREMENT PRIMARY KEY, user_id BIGINT INDEX,
  ip VARCHAR(64), ua VARCHAR(255), client_type VARCHAR(32),
  accessed_at DATETIME INDEX
);

CREATE TABLE sync_tasks (
  id BIGINT AUTO_INCREMENT PRIMARY KEY,
  type VARCHAR(64) NOT NULL, status VARCHAR(32) NOT NULL,
  target_type VARCHAR(64) NOT NULL, target_id BIGINT NOT NULL,
  summary VARCHAR(255), payload TEXT, last_error TEXT,
  attempts INT, next_run_at DATETIME,
  created_at DATETIME, updated_at DATETIME, finished_at DATETIME,
  INDEX idx_task_due (type, status, next_run_at),
  INDEX idx_task_target (target_type, target_id)
);

-- 配置 KV 主表
CREATE TABLE settings (
  id BIGINT AUTO_INCREMENT PRIMARY KEY,
  type VARCHAR(32) NOT NULL,           -- 分组：site/auth/sub/security/runtime/notice/notify/geo/cert
  name VARCHAR(128) NOT NULL,          -- 字段名（snake_case）
  value TEXT,                          -- string / 数字串 / JSON
  encrypted BOOLEAN NOT NULL DEFAULT FALSE,  -- 透明 AES-GCM enc/dec 标记
  updated_at DATETIME,
  UNIQUE KEY uk_setting_kv (type, name),
  INDEX idx_setting_type (type)
);
-- v3.9.1：Save() 改为按 (type,name) UPDATE-in-place，不再批量 INSERT ... ON DUPLICATE KEY
-- UPDATE（后者在 InnoDB 下每次保存都会预留并丢弃一批自增 id，造成 id 大段空洞）。

-- v3.8.0：per-Group 设置覆盖（稀疏表，全局 settings 不变，叠加解析见 §6.3）
CREATE TABLE scope_settings (
  id BIGINT AUTO_INCREMENT PRIMARY KEY,
  scope_type VARCHAR(16) NOT NULL,     -- 目前恒为 "group"
  scope_id BIGINT NOT NULL,            -- groups_.id
  type VARCHAR(32) NOT NULL, name VARCHAR(128) NOT NULL,
  value TEXT, encrypted BOOLEAN NOT NULL DEFAULT FALSE,
  updated_at DATETIME,
  UNIQUE KEY uk_scope_setting (scope_type, scope_id, type, name)
);

CREATE TABLE mail_settings (
  id BIGINT PRIMARY KEY, enabled BOOLEAN,
  smtp_host VARCHAR(255), smtp_port INT, smtp_username VARCHAR(255),
  smtp_password TEXT,                  -- AES-GCM 密文
  from_email VARCHAR(255), from_name VARCHAR(128), encryption VARCHAR(16),
  updated_at DATETIME
);

CREATE TABLE mail_templates (
  kind VARCHAR(32) PRIMARY KEY, subject VARCHAR(255), body TEXT,
  enabled BOOLEAN, updated_at DATETIME
);

CREATE TABLE mail_sent (
  id BIGINT AUTO_INCREMENT PRIMARY KEY, user_id BIGINT NOT NULL,
  kind VARCHAR(32) NOT NULL, window_key VARCHAR(128) NOT NULL,
  to_email VARCHAR(255) NOT NULL, sent_at DATETIME,
  UNIQUE KEY uk_mail_once (user_id, kind, window_key)
);

CREATE TABLE saml_settings (
  id BIGINT PRIMARY KEY, enabled BOOLEAN, mode VARCHAR(16),
  sp_entity_id VARCHAR(255), sp_acs_url VARCHAR(255),
  sp_cert_pem TEXT, sp_key_pem TEXT,         -- key AES-GCM 密文
  idp_metadata_url VARCHAR(255), idp_metadata_refresh_sec BIGINT,
  attr_upn VARCHAR(255), attr_email VARCHAR(255),
  attr_display_name VARCHAR(255), attr_groups VARCHAR(255),
  role_rules JSON, default_group_slug VARCHAR(64), allow_auto_create BOOLEAN,
  new_user_expire_days INT, new_user_traffic_limit_bytes BIGINT,
  new_user_traffic_reset_period VARCHAR(16), updated_at DATETIME
);

CREATE TABLE oidc_settings (
  id BIGINT PRIMARY KEY, enabled BOOLEAN,
  issuer_url VARCHAR(255), client_id VARCHAR(255),
  client_secret VARCHAR(512),                 -- AES-GCM 密文
  redirect_url VARCHAR(255), scopes JSON,
  attr_username VARCHAR(128), attr_email VARCHAR(128),
  attr_display_name VARCHAR(128), attr_groups VARCHAR(128),
  role_rules JSON, default_group_slug VARCHAR(64), allow_auto_create BOOLEAN,
  new_user_expire_days INT, new_user_traffic_limit_bytes BIGINT,
  new_user_traffic_reset_period VARCHAR(16), updated_at DATETIME
);

-- v3.7.0：passkey / WebAuthn 凭据
CREATE TABLE webauthn_credentials (
  id BIGINT AUTO_INCREMENT PRIMARY KEY, user_id BIGINT NOT NULL INDEX,
  credential_id VARCHAR(512) NOT NULL UNIQUE, credential TEXT NOT NULL,
  sign_count BIGINT NOT NULL DEFAULT 0, name VARCHAR(255) DEFAULT '',
  created_at DATETIME, last_used_at DATETIME
);

-- v3.7.0：找回密码 / 邮箱验证 / 自助注册的一次性令牌
CREATE TABLE auth_tokens (
  id BIGINT AUTO_INCREMENT PRIMARY KEY, user_id BIGINT DEFAULT 0,
  purpose VARCHAR(32) NOT NULL, token_hash VARCHAR(64) DEFAULT '',
  code_hash VARCHAR(64) NOT NULL DEFAULT '', email VARCHAR(255),
  expires_at DATETIME, used_at DATETIME, attempts INT NOT NULL DEFAULT 0,
  created_at DATETIME,
  INDEX idx_authtoken_user_purpose (user_id, purpose),
  INDEX idx_authtoken_tokenhash (token_hash)
);

-- v3.6.4：PSP 托管的 ACME 证书自动化
CREATE TABLE acme_accounts (
  id BIGINT AUTO_INCREMENT PRIMARY KEY, name VARCHAR(255) NOT NULL DEFAULT '',
  email VARCHAR(255) NOT NULL, directory VARCHAR(255) NOT NULL,
  key_type VARCHAR(32) NOT NULL DEFAULT '', account_key TEXT, registration TEXT,
  created_at DATETIME, updated_at DATETIME,
  UNIQUE KEY uk_acme_email_dir (email, directory)
);

CREATE TABLE dns_credentials (
  id BIGINT AUTO_INCREMENT PRIMARY KEY, name VARCHAR(128) NOT NULL UNIQUE,
  provider VARCHAR(64) NOT NULL, credentials TEXT,  -- AES-GCM 密文
  created_at DATETIME, updated_at DATETIME
);

CREATE TABLE tls_certificates (
  id BIGINT AUTO_INCREMENT PRIMARY KEY, name VARCHAR(255) NOT NULL,
  domains JSON, acme_account_id BIGINT INDEX, dns_credential_id BIGINT INDEX,
  cert_pem TEXT, key_pem TEXT,  -- AES-GCM 密文
  status VARCHAR(16) NOT NULL DEFAULT 'pending' INDEX, last_error TEXT,
  not_before DATETIME, not_after DATETIME, auto_renew BOOLEAN DEFAULT TRUE,
  created_at DATETIME, updated_at DATETIME
);

CREATE TABLE cert_events (
  id BIGINT AUTO_INCREMENT PRIMARY KEY, cert_id BIGINT INDEX,
  cert_name VARCHAR(255), kind VARCHAR(16), success BOOLEAN, message TEXT,
  created_at DATETIME INDEX
);
```

**`settings` KV 表的字段分组**（type）：

| type | 用途 | 字段示例 |
|---|---|---|
| `site` | 站点品牌 | site_title, app_title, logo_url, footer_text, theme_color, email_domain, sub_base_url |
| `auth` | JWT + 登录策略 | login_mode, jwt_issuer, jwt_access_ttl_minutes, disallow_user_local_login |
| `sub` | 订阅渲染 | sub_path, sub_clients (JSON，检测族→导入 App 两层注册表), sub_update_interval_hours |
| `security` | 限流 / 留存 / 应急 / 2FA / 验证码 / 账户锁定 / 找回密码 / 自助注册 | sub_per_ip_per_min, audit_retention_days, traffic_snapshot_retention_days, emergency_access_*, totp_enabled, passkey_enabled, captcha_*, lockout_*, password_recovery_*, registration_* |
| `runtime` | Cron / 性能 / 时区 | timezone, cron_traffic_pull_minutes, cron_reconcile_minutes, max_panel_concurrency |
| `notice` | 用户视图 | quick_links (JSON), global_announcement (JSON) |
| `notify` | 邮件触发阈值 | expire_before_days, traffic_remain_percent |
| `geo` | IP 归属地库（v3.6.4） | geo_ip_enabled, geo_ip_auto_update, geo_ip_update_token（加密） |
| `cert` | PSP 证书自动化（v3.6.4） | cert_renew_before_days, cert_renew_check_interval_hours |

**哪些 `settings` 字段可以按 Group 覆盖**：见 §6.3；白名单是 `internal/ports/repos.go` 的 `OverridableScopeKeys`，`security.lockout_*` / `security.captcha_*` / `auth.login_mode` 等"识别身份前"生效的设置**永远全局**（有意为之，见 §6.3 与 §19.3）。

### 5.2 group.layout 字段

按组定义节点排序与分隔符占位。JSON 结构：

```json
{
  "separators": [
    {"position": 0,  "name": "🇹🇼 高级线路 (推荐)"},
    {"position": 4,  "name": "🇺🇸 US 节点"},
    {"position": 6,  "name": "🇨🇳 国内回程"}
  ],
  "sort": [
    {"node_id": 12, "weight": 100},
    {"node_id": 13, "weight": 101},
    {"node_id": 7,  "weight": 200}
  ],
  "default_sort_strategy": "by_region_then_id"
}
```

- `separators[].position` 是渲染后在节点序列里的插入位置（0-indexed，插在该位置之前）
- `sort[]` 显式权重；未列出的节点按 `default_sort_strategy` 排
- 不同 group 完全独立：`light` 组可以用中文友好分隔符，`full` 组用 `----- TW -----`

### 5.3 配置文件（保留 YAML 的部分）

| 文件 | 用途 |
|---|---|
| `config.yaml` | 监听端口、JWT 密钥、数据库 DSN、配置目录 |
| `config/rulesets/*.yaml` | 规则集，包含规则片段、策略组顺序和启用状态 |
| `config/templates/*.yaml` | 订阅模板（mihomo / sing-box） |

**注意**：3X-UI 面板凭证、SAML/OIDC 配置、系统设置等通过管理后台写入数据库。规则集和模板通过管理后台编辑，但最终落盘到 YAML 文件。

`rulesets/*.yaml` 的关键字段：

```yaml
slug: default
name: 默认规则
enabled: true
proxy_group_order:
  - 节点选择
  - 中国大陆
proxy_group_members:
  中国大陆:
    - kind: node
      node_id: 42
    - kind: builtin
      value: DIRECT
    - kind: proxy_group
      value: 节点选择
    - kind: node_set
      value: remaining
proxy_group_options:
  节点选择:
    type: url-test
    url: https://www.gstatic.com/generate_204
    interval: 300
    lazy: true
    timeout: 5000
    tolerance: 50
rules:
  - DOMAIN-SUFFIX,example.com,节点选择
```

---

## 6. 后台页面 × 操作矩阵

### 6.1 管理员后台 (role=admin)

侧栏按分区组织（`AdminLayout.tsx`）：Dashboard / Directory / Infrastructure / Subscription / Reporting / Settings。

| 页面 | 分区 | 列表显示 | 操作 |
|---|---|---|---|
| **总览 (Dashboard)** | Dashboard | 统计概览 + 告警 | - |
| **用户管理** | Directory | UPN / 显示名 / 分组 / 到期 / 配额 / 已用 / 账号状态 / 服务状态 | 新增、编辑、改组、改到期、改配额、重置 UUID、启用/禁用账号(含原因)、暂停/恢复服务(含原因)、重置 sub_token、重置密码、重置 2FA、管理 passkey、解绑 SSO、删除、批量延期、批量改组 |
| **分组** | Directory | 分组名 / slug / 成员数 | 新增、编辑、编辑 layout（Node access）、编辑覆盖设置（Policies，见 §6.3）、删除 |
| **服务器** | Infrastructure | 3X-UI 面板列表 + 版本感知 | 新增、编辑、删除、连接测试、升级 3X-UI / Xray |
| **节点管理** | Infrastructure | 显示名 / 协议 / 端口 / region / tags / 启用 / 健康状态 / 配置同步状态 | 新增 inbound、编辑（连接配置本地优先）、改 region/tag/sort、启用/禁用、删除、导入未纳管 inbound、认领 client、生成 Reality 密钥对、管理分隔符 |
| **证书管理** | Infrastructure | ACME 证书列表 + 状态 | 新增、编辑、续期、下载、删除、管理 DNS 凭据 / ACME 账户、绑定节点证书源 |
| **规则库** | Subscription | 规则集列表 | 新增、编辑、删除、重置为默认、启用/禁用 |
| **配置方案** | Subscription | 模板列表 | 新增、编辑、删除、重置为默认、设为默认 |
| **订阅客户端** | Subscription | 检测族 → 导入 App 注册表 | 新增/编辑/删除检测族与 App、客户端过滤模式 |
| **流量统计** | Reporting | Top-N 排行 / 用户维度 / 节点维度 | 查看详情、手动设置用量、手动触发轮询 |
| **日志管理** | Reporting | 订阅日志 / 审计日志 / 认证事件 / 邮件日志 / 证书事件 | 查看详情、清空、按策略清理 |
| **同步任务** | Reporting | 任务列表 | 重试、取消、清理已完成 |
| **系统设置** | Settings | - | 基本设置、账户安全（scope 轨，见 §6.3）、站点品牌、订阅策略（scope 轨）、邮件提醒、SSO 认证、运行时与数据（cron/留存/GeoIP/JWT） |

### 6.2 用户自助页 (role=user)

| 内容 | 操作 |
|---|---|
| 到期倒计时 / 流量进度条 / 订阅 URL + 二维码 | 改密码、重置 sub_token、紧急访问、查看订阅客户端导入 |
| 账户安全（v3.7.0+） | 启用/禁用 TOTP、注册/管理 passkey、重新生成恢复码、查看最近登录/2FA 事件 |
| 快捷链接 | 管理员配置的外部链接（如客户端下载、教程等） |
| 全局公告 | 管理员发布的置顶公告 |

### 6.3 operator 角色 + 权限扩展点（预留接口，勿绕过）

**operator（日常运营）** 是介于 admin 与 user 之间的受限管理角色：

- **能做**：登录后台；普通用户（role=user）CRUD；流量查看 / 手动设用量 / 紧急访问；节点启用/禁用；同步任务重试/取消；查看分组 / 规则库 / 配置方案 / 日志 / 审计。
- **不能做**：3X-UI 服务器（面板凭据）、系统设置、邮件 SMTP、SSO（SAML/OIDC）、证书管理；节点 / 分组 / 规则库 / 配置方案 / 分隔符 / scope 覆盖设置的增改删；日志清空与保留期；同步任务清理；操作 admin/operator 账户或分配 admin/operator 角色。

**授权实现（四处，必须保持一致）：**

| 层 | 位置 | 机制 |
|---|---|---|
| 后端路由 | `internal/transport/http/router.go` | `staffGroup`（admin+operator）vs `adminGroup`（仅 admin）两组；`middleware.RequireRole(...)` 按角色放行 |
| 后端 handler | `internal/transport/http/handler/admin_user.go` | `ensureOperatorAllowed`（operator 不可动 admin/operator 账户）、`guardOperatorRoleAssignment`（operator 只能分配 role=user） |
| 前端路由 | `web-react/src/router/RequireAuth.tsx` + `router/home.ts` `ADMIN_ONLY_ROUTES` | servers / certs / settings / sub-clients 对非 admin 重定向到 dashboard |
| 前端按钮 | `web-react/src/utils/permissions.ts` | **能力层** `useCan('config.write' \| 'users.elevate' \| ...)`，各 View 按能力隐藏操作按钮 |

**预留扩展点 —— 自定义角色 / 细粒度权限（重点，别忘）：**

- **前端所有操作门控只走能力层** `utils/permissions.ts`：调用点一律 `useCan(capability)`，**绝不内联 `role === 'admin'`**。将来支持自定义角色或后端下发权限集时，**只改 `permissions.ts` 的 `ROLE_CAPS` / `roleCan`**，所有 `useCan(...)` 调用点保持不变。
- **后端**新增角色：在 `domain.Role` 加枚举值，并纳入 `router.go` 的 staffGroup/adminGroup（或将来更细的分组）；`ensureOperatorAllowed` 这类守卫按需推广为基于能力的检查。
- **前后端能力定义必须对齐**：`permissions.ts` 的 `ROLE_CAPS` 与 `router.go` 的路由分组是同一套规则的两端，改一处要同步另一处。

**Group scope 覆盖（v3.8.0）**：设置系统是"全局默认 + 各 Group 覆盖"两级模型（不做 OU 树、不做 per-user 覆盖）。可覆盖字段是显式白名单 `ports.OverridableScopeKeys`（当前含 2FA 方式、通知阈值、紧急访问参数、部分登录/自助策略、订阅渲染与反滥用策略），前端 `web-react/src/components/scope/scopeOverrides.ts` 的 `SCOPE_KEYS` 是其镜像。**在用户身份被识别之前生效的设置**（`lockout_*` / `captcha_*` / `login_mode` / `disallow_user_local_login` 等）**有意排除在外、永远全局**——因为登录守卫要在查到用户所属组之前就决策，若做成 per-group 需要按 UPN 预取所属组，而对不存在/攻击者构造的 UPN 必须 fail-safe 回全局，否则会变成用户名枚举面；这条已在设计评审中定为非目标（不是待办），详见 [v3.8.0-group-scoped-admin.md §10-1](v3.8.0-group-scoped-admin.md)。

---

## 7. 关键数据流（核心场景演练）

### 7.1 SSO 登录（SAML / OIDC）

```
用户访问 /login → 点 "使用 SSO 登录"
  ① 后端检测启用的 SSO 类型（SAML 或 OIDC）
  ② SAML: 生成 AuthnRequest，302 跳转 IdP
     OIDC: 生成 state/nonce，302 跳转授权端点
  ③ 用户在 IdP 完成认证
  ④ SAML: POST SAML Response 到 /api/auth/saml/acs
     OIDC: GET 回调到 /api/auth/oidc/callback
  ⑤ 后端验证响应，解析用户信息（UPN、email、display_name、groups）
  ⑥ 查 users WHERE upn=?：
     - 找到 → 更新信息
     - 未找到 → 自动建用户（role=auto判定, group=默认组）
  ⑦ 对比 admin_group_ids，命中 → role=admin
  ⑧ 签发 JWT (HS256, access/refresh TTL 可配置)，HttpOnly cookie
  ⑨ 302 跳回 /admin 或 /user/me（按 role）
```

### 7.2 本地账号登录（含 2FA / passkey，v3.7.0+）

```
用户访问 /login → 输入 UPN 和密码
  ① 后端查 users WHERE upn=?
  ② bcrypt 验证 password_hash
  ③ 账户锁定检查（LoginGuard，按 ip / ip+upn 计失败次数）+ 触发条件下要求验证码
  ④ 若该用户已启用 TOTP/passkey/邮箱验证码任一 2FA 方式 → 签发临时凭证，前端跳 2FA 挑战
  ⑤ 2FA 验证通过后签发正式 JWT
  ⑥ 跳转

注：本地账号仅供以下场景：
  - 初次启动建第一个 admin
  - SSO 故障 fallback
  - 不能用 SSO 登录的特殊用户（含 v3.8.0 起支持的自助注册用户）
```

### 7.3 新建本地用户（v3.9.0 共享 client 模型）

```
UI: 用户管理 → 新增 → source=local, username, group, expire_at, traffic_limit_gb → 提交

后端:
  1. 校验 username 唯一
  2. 生成 uuid (v4)、sub_token、bcrypt(初始密码)、共享 client 密码
     （password = base64(SHA-256(uuid))，与旧版 SS-2022 派生值同构，见 §2.1）
  3. 先 INSERT users（本地用户立即生效）
  4. 调 user.ResyncMembership(ctx, newUserID)：
       a. 按 group.tag_filter 解析该用户可访问的节点集合
       b. 用 clientplan 按 (凭据兼容性类, flow) 把节点分桶成一个或多个 psp_client（绝大多数用户仅 1 个）
       c. 对每个 psp_client 调 SharedClientSvc.ProvisionClient：
          - 若该 email 在 3X-UI 尚不存在 → AddClientToInbounds 一次性创建并挂载全部目标 inbound
            （email = "u{userID}{suffix}@{domain}"，一次 Xray 重启）
          - GetClient 读回确认 → 仅对确认挂载的 (client, node) 标记 provisioned
       d. 调 SharedClientSvc.SyncLifecycle 推送 enable/expiry/quota-floor 到共享 client
     远端任一步失败不回滚本地用户，写 sync_task（user_resync）后续重试
  5. 写 psp_clients / psp_client_inbounds（新建用户不再写遗留的 user_xui_clients）
  6. 返回 {sub_url, initial_password}
```

### 7.4 重置用户 UUID

```
UI: 用户编辑 → 重置 UUID（带确认对话框，提示"所有协议密码会同步换，朋友现有客户端要重新拉订阅"）

后端:
  1. 生成新 uuid (v4)
  2. 先更新 users.uuid / sub_token（本地订阅立即使用新凭证）
  3. 重新计算共享 client 密码（= base64(SHA-256(新 uuid))）
  4. 对该用户名下每个 psp_client → SharedClientSvc.SyncLifecycle 推新凭据
     远端失败时写 user_resync 任务，不回滚本地凭证
  5. AuditLog（before/after 不存 uuid 明文，存哈希值 + 操作者）
  6. 返回成功
```

### 7.5 节点纳入面板（两种路径）

⚠️ **默认走路径 A（导入现有 inbound），路径 B 仅在确实需要建新 inbound 时使用。维护者已有的 inbound 全部走 A。**

#### 7.5.1 路径 A：导入现有 inbound（推荐 ⭐）

适用：3X-UI 后台已经存在 inbound（含混杂的私人 + 朋友 client），只需在面板登记元数据并接管连接配置。

```
UI: 节点管理 → "未纳管的 inbound" tab → 列出所有 3X-UI inbound 但不在面板 nodes 表内的
    → 选择某条 → "纳入管理" → 填 display_name / region / tags / sort_order → 保存

后端:
  1. 校验：(panel_id, inbound_id) 不在 nodes 表
  2. GET 该 inbound 一次，捕获其完整连接配置（去 clients[]）存入 nodes 快照列（v3.5 接管，§10.4.3）
  3. INSERT nodes 表
  4. AuditLog

inbound 内现有 client（维护者私人 + 朋友）完全不感知，保持原样工作。
此后该 inbound 的连接配置以 PSP 为真相源；client 挂载仍走 §7.5.3 的共享 client 流程，只做追加。
```

#### 7.5.2 路径 B：从面板新建 inbound（可选）

```
UI: 节点管理 → 新增 → 选协议 (VLESS+Reality 等) → 填地址/端口/Reality 参数 → 设 region/tags → 保存

后端:
  1. 校验协议参数合法
  2. 本地先写配置快照，再调 AddInbound 在 3X-UI 创建新 inbound（settings.clients[] 为空）
  3. 拿到 3X-UI 返回的 inbound_id
  4. INSERT nodes 表
  5. AuditLog

远端失败写 node_create 异步任务；新建 inbound 需要远端返回 inbound_id，故此路径不能先创建无映射关系的本地节点。

后续：根据 group.tag_filter 自动补 client（见 §7.5.3）。
```

#### 7.5.3 给用户加权限到节点（v3.9.0 共享 client 模型）

```
触发：用户改组、分组扩容、新建用户、新节点上线。

user.ResyncMembership(ctx, userID)：
  1. 按 group.tag_filter 重新解析该用户可访问的节点集合
  2. clientplan.Partition 按 (凭据兼容性类, flow) 分桶（通常仍是 1 个 psp_client）
  3. 对每个 psp_client 调 SharedClientSvc.ProvisionClient：
       a. 读 psp_client_inbounds 得到期望挂载集，与 3X-UI GetClient().InboundIDs 做差分
       b. 若 email 已存在但挂载集变化：AttachClient 追加新 inbound / DetachClient 摘除已不在组内的
          （批量场景走 BulkAttach/BulkDetach，一次 Xray 重启覆盖多个用户）
       c. GetClient 读回确认，仅对确认的挂载标记 provisioned
  4. SharedClientSvc.SyncLifecycle 同步 enable/expiry/quota-floor
  5. AuditLog

现有其他用户的 client 原封不动；该用户已挂载但仍在组内的 inbound 不受影响。
```

### 7.6 节点编辑

```
UI: 节点详情 → 改 region 或协议参数

仅改 region/tags/sort → 只写 nodes 表，不动 3X-UI
改协议参数 → local-first 写本地配置快照 + 调 UpdateInbound（RMW 保留全部 live client）；远端失败写 node_update 异步任务
```

### 7.7 节点删除

```
UI: 节点详情 → 删除按钮

后端:
  1. 先标记 nodes.enabled=false，避免新订阅继续下发该节点
  2. 写 node_delete 异步任务，由 worker 调 GetInbound → 列所有 client
  3. 校验：所有 client 都在纳管范围内？（遗留归属表 或 共享 client 表任一命中）
     - 是 → 继续
     - 否 → 任务失败并记录未纳管 client，UI 在任务详情提示"请先处理未纳管 client"
  4. 对每个纳管 client 摘除该 inbound 的挂载（共享 client 走 DetachClient；遗留归属表走 DelOwnedClient + 删行）
  5. 调 DelInbound
  6. 删 nodes 表条目
  7. AuditLog
```

### 7.8 导入已有 client

```
UI: 节点详情 → 未纳管 client 列表 → 点击某条 → "认领"对话框
   → 选已有面板用户 OR 创建新用户 → 提交

后端:
  1. 选已有用户:
     - 记录该用户对当前 client 的 ownership（遗留归属表；不并入共享 client 模型）
     - 若 client 有 id/uuid，可记录为辅助键；SS/SS-2022 允许为空
  2. 新建用户:
     - source=local, username 自动建议（清洗 email），group=默认
     - 生成面板用户 uuid；认领动作本身不改 3X-UI client
  3. 写 user_xui_clients 表，匹配键为 (panel, inbound_id, client_email)
  4. 不调 3X-UI 任何写 API（无缝迁移）
  5. AuditLog

认领的 client 保留原 email（不改名为 u{uid}@{domain} 格式），独立于共享 client 模型运作。
```

### 7.9 流量超限 → 暂停服务（不锁账号，v3.9.0）

```
TrafficSvc cron 每 N 分钟（默认 5）:
  1. 按 email 拉每个共享 client 的流量（一次，不按 inbound 求和，避免重复计数）
  2. 折算单调 delta，累加进 psp_client 与 user 的 lifetime 计数
  3. INSERT traffic_snapshots（+ 小时聚合表）
  4. 算当前计费周期已用量:
       周期内已用 = users.lifetime_total_bytes - users.period_baseline_bytes（O(1) 内存计算）
  5. 若 user.traffic_limit_bytes > 0 且 已用 > limit:
       - 调 user.SetServiceSuspendedAndSync(userID, reason=DisabledTrafficExceeded)
       - 内部：写 users.service_disabled_reason（不动 users.enabled，账号仍可登录查看原因）
       - 对该用户所有 psp_client 调 SharedClientSvc.SyncLifecycle(enable=false)
       - 异步发送"代理服务已暂停"邮件
       - AuditLog
  6. 重置周期触发（每月 1 号 / 每季首日）:
       - period_start = now
       - 若 service_disabled_reason=traffic_exceeded → 调 user.ResumeServiceAndSync(userID)
           （清空 service_disabled_reason + SyncLifecycle(enable=true) + 异步"服务已恢复"邮件）
       - AuditLog

账号状态（enabled + auto_disabled_reason）与服务状态（service_disabled_reason）是两条独立轴：
超限只影响服务状态，用户仍可登录面板自助页查看暂停原因、申请紧急访问或联系管理员。
```

### 7.10 订阅请求

```
GET /{sub_path}/abc123 (UA: mihomo)

后端:
  1. 查 users WHERE sub_token=? → user
  2. 检查账号状态（enabled && now < expire_at）与服务状态（service_disabled_reason 为空，
     否则按 §7.9 语义拒绝并说明原因）
  3. 查 user.group_id → group（tag_filter + layout + scope 覆盖设置）
  4. 拉 nodes 表 + 应用 tag_filter → 过滤匹配节点
  5. RenderSvc:
     a. 加载默认模板（按 UA 或 ?client 参数选）
     b. 应用 group.layout:
        - 节点按 layout.sort 排序，未列入的按 default_sort_strategy
        - 在 layout.separators 指定位置插入分隔符
     c. 每个节点的连接配置**直接读本地 nodes 快照**（v3.5 起零回源 3X-UI，见 §10.3），
        注入该用户共享 client 的存储凭据（v3.9.0 起 render 读 psp_client.UUID/Password，
        不再现算派生，见 §2.1）
     d. {{ proxies }} 替换为节点 YAML 列表
     e. proxy-groups 内的 @all / @region:TW / @tag:reality 展开
     f. {{ rules_common }} 拼接模板绑定的 rule_sets；按规则集 proxy_group_order 生成策略组顺序
     g. {{ rules_personal }} 插入 user.personal_rules
  6. 写 sub_logs
  7. 写 Subscription-Userinfo header（流量 + 到期 + 限额）
  8. 返回 yaml
```

---

## 8. API 端点

> 下表按功能分组列出路由前缀与代表性端点；完整逐路由清单以 [internal/transport/http/router.go](../internal/transport/http/router.go) 为准（本文档不追求逐路由重复，避免重蹈过去脱节的覆辙）。

### 8.1 公开端点

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/health` | 健康检查 |
| GET | `/api/version` | 版本号 |
| GET | `/{sub_path}/:token` | 订阅渲染（动态路径，默认 `/sub/`，经 `NoRoute` 处理） |

### 8.2 认证端点 (`/api/auth`)

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/methods` | 可用登录方式 |
| GET | `/captcha` | 图形验证码挑战（v3.7.0） |
| POST | `/local/login` | 本地账号登录 |
| POST | `/2fa/verify` \| `/2fa/email/send` \| `/2fa/passkey/begin` \| `/2fa/passkey/finish` | 登录 2FA 挑战（TOTP / 邮箱验证码 / passkey，v3.7.0） |
| POST | `/passkey/begin` \| `/passkey/finish` | 无用户名 passkey 登录（v3.7.0） |
| POST | `/refresh` | 刷新 access token |
| POST | `/forgot-password` \| `/reset-password` | 自助找回密码（v3.7.0） |
| POST | `/register` \| `/resend-verification` \| `/verify-email` | 自助注册 + 邮箱验证（v3.8.0） |
| GET | `/saml/login` \| POST `/saml/acs` \| GET `/saml/metadata` | SAML SSO |
| GET | `/oidc/login` \| GET `/oidc/callback` | OIDC SSO |
| GET | `/sso-complete` | SSO 登录完成落地页 |

### 8.3 用户自助端点 (`/api/user/me`)

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `` | 个人资料 |
| GET | `/traffic` \| `/traffic/history` | 流量报告 / 历史 |
| GET | `/server-status` | 各面板服务状态 |
| GET/PUT | `/rules` | 个人规则 |
| POST | `/emergency-access` | 紧急访问 |
| POST | `/reset-credentials` \| `/change-password` | 重置凭证 / 修改密码 |
| POST | `/2fa/begin` \| `/2fa/enable` \| `/2fa/disable` | TOTP 自助管理（v3.7.0） |
| POST | `/2fa/recovery/regenerate` | 重新生成恢复码 |
| POST | `/2fa/stepup/passkey/begin` \| `/finish` | 敏感操作前的 passkey 阶梯验证 |
| GET/POST/PATCH/DELETE | `/passkeys[/:id]` | passkey 自助注册/改名/删除 |

### 8.4 管理端点 (`/api/admin`)

**用户管理：**

| 方法 | 路径 | 说明 |
|---|---|---|
| GET/POST | `/users` | 列表 / 创建 |
| GET/PUT/DELETE | `/users/:id` | 详情 / 更新 / 删除 |
| POST | `/users/:id/reset-credentials` \| `/reset-password` \| `/reset-emergency-usage` | 重置类操作 |
| POST | `/users/:id/set-enabled` | 账号状态（登录权限） |
| POST | `/users/:id/set-service-status` | 服务状态（代理/订阅可用性，v3.9.0） |
| POST | `/users/:id/reset-2fa` \| `/2fa/recovery/regenerate` | 管理员重置用户 2FA |
| GET/DELETE | `/users/:id/passkeys[/:pkid]` | 管理员查看/撤销用户 passkey |
| POST | `/users/:id/unlink-sso` | 解绑 SSO |
| GET/PUT | `/users/:id/rules` | 用户个人规则 |

**节点管理：**

| 方法 | 路径 | 说明 |
|---|---|---|
| GET/POST | `/nodes` | 列表 / 新建 inbound |
| GET | `/nodes/:id` \| `/nodes/unmanaged` | 详情 / 未纳管 inbound 列表 |
| PUT | `/nodes/:id/metadata` \| `/nodes/:id/inbound` | 元数据 / 连接配置更新 |
| POST | `/nodes/:id/set-enabled` \| `/detach` \| `/recreate-inbound` \| `/claim` | 启停 / 分离 / 重建 / 认领 client |
| PUT | `/nodes/reorder` \| DELETE `/nodes/:id` | 排序 / 删除 |
| POST | `/nodes/import` | 导入现有 inbound |
| GET/POST/PUT/DELETE | `/nodes/separator[/:id]`（含 `/reorder`） | 分隔符管理 |
| POST | `/nodes/generate-reality-keypair` | 生成 Reality 密钥对 |
| PUT | `/nodes/:id/cert-source` | 绑定节点证书源（v3.6.4） |

**证书管理（v3.6.4）：**

| 方法 | 路径 | 说明 |
|---|---|---|
| GET/POST | `/certs` | 列表 / 创建 |
| GET/PUT/DELETE | `/certs/:id` | 详情 / 更新 / 删除 |
| POST | `/certs/:id/renew` | 续期 |
| GET | `/certs/:id/download` \| `/cert-events` | 下载 / 事件日志 |
| GET/POST/PUT/DELETE | `/dns-credentials[/:id]` | DNS 凭据 CRUD |
| GET | `/dns-providers` \| `/acme-key-types` | 可选提供商 / 密钥类型 |
| GET/POST/PUT/DELETE | `/acme-accounts[/:id]` | ACME 账户 CRUD |

**分组管理：**

| 方法 | 路径 | 说明 |
|---|---|---|
| GET/POST | `/groups` | 列表 / 创建 |
| GET/PUT/DELETE | `/groups/:id` | 详情 / 更新 / 删除 |
| PUT | `/groups/:id/layout` | 更新渲染布局 |
| GET/PUT/DELETE | `/groups/:id/scope-settings[/:type/:name]` | scope 覆盖设置读写（v3.8.0） |

**规则 / 模板 / 服务器：**

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/rules` \| `/templates` | 列表 |
| GET/PUT/DELETE | `/rules/:slug` \| `/templates/:slug` | CRUD |
| POST | `/rules/:slug/reset` \| `/templates/:slug/reset` | 重置为默认 |
| GET/POST/PUT/DELETE | `/servers[/:id]` | 3X-UI 面板 CRUD |
| POST | `/servers/probe` \| `/servers/:id/upgrade-panel` \| `/upgrade-xray` | 连接测试 / 远程升级（v3.6.0） |
| GET | `/servers/:id/xray-versions` \| `/web-cert` | 版本列表 / Web 证书 |

**流量 / 日志 / 运维：**

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/traffic/top` \| `/traffic/nodes/top` \| `/traffic/nodes/history` | 排行 / 节点维度 |
| GET/PUT | `/traffic/user/:id` \| `/traffic/user/:id/history` \| `/nodes` \| `/servers` | 用户维度流量报告/历史/设置 |
| POST | `/traffic/poll` | 手动触发轮询 |
| GET/DELETE | `/audit` \| `/sub-logs` \| `/email-logs` | 审计 / 订阅 / 邮件日志 |
| GET | `/auth-events` | 认证事件日志（v3.7.0） |
| POST | `/sub-logs/purge` \| `/email-logs/purge` | 按策略清理 |
| GET | `/dashboard/summary` \| `/alerts` | 仪表盘摘要 / 告警 |
| GET/POST | `/sync-tasks[/:id/retry\|/cancel]` \| POST `/sync-tasks/purge` | 同步任务管理 |
| POST | `/reconcile/run` | 手动触发对账 |

**系统设置：**

| 方法 | 路径 | 说明 |
|---|---|---|
| GET/PUT | `/settings/ui` | 全局 UI 设置（含 §6.3 的可覆盖字段） |
| GET/PUT | `/settings/mail` \| PUT `/settings/mail/templates/:kind` | 邮件设置 / 模板 |
| POST | `/settings/mail/templates/:kind/preview` \| `/reset` \| `/test` \| `/announcement` | 模板预览/重置、测试邮件、群发公告 |
| GET/PUT | `/settings/saml` \| `/settings/oidc` | SSO 配置 |
| POST | `/settings/saml/fetch` | 拉取 IdP metadata |
| GET/POST | `/settings/geoip/status` \| `/settings/geoip/update` | GeoIP 库状态 / 手动更新（v3.6.4） |

---

## 9. 订阅渲染管线

### 9.1 模板占位符

| 占位符 | 展开内容 |
|---|---|
| `{{ proxies }}` | 用户授权节点的 Clash proxy block 列表 + group.layout 应用 |
| `{{ rules_common }}` | 当前模板 `rule_sets` 绑定的规则集内容拼接 |
| `{{ rules_personal }}` | user.personal_rules 原文 |
| `@all` (在 proxy-groups.proxies) | 该用户所有授权节点名（按 layout 顺序，含分隔符） |
| `@region:TW` | region=TW 的节点名 |
| `@tag:reality` | tags 含 reality 的节点名 |
| `@region:TW+tag:reality` | AND 组合 |

规则集内的 `proxy_group_order` 是 mihomo `proxy-groups` 和 sing-box selector outbounds 的展示顺序。字段留空时使用项目内置默认顺序：`🚀 节点选择` → `🎮 UDP控制` → `🇨🇳 中国大陆` → `💬 Ai平台` → `📹 油管视频` → `🎥 奈飞视频` → `📺 巴哈姆特` → `🌍 国外媒体` → `🎮 游戏平台` → `📲 电报消息` → `Ⓜ️ 微软Bing` → `📢 谷歌FCM` → `🌏 国内媒体` → `📺 哔哩哔哩` → `Ⓜ️ 微软云盘` → `Ⓜ️ 微软服务` → `🍎 苹果服务` → `🎶 网易音乐` → `🎯 全球直连` → `🛑 广告拦截` → `🍃 应用净化` → `🐟 漏网之鱼`；不在默认或自定义列表中的代理组，按规则内容中的首次出现顺序放在列表开头。模板负责声明使用哪些规则集；规则集负责声明自定义策略组顺序和规则内容。

`proxy_group_members` 可选地覆盖单个策略组内部的成员顺序，Mihomo 与 sing-box 共用。支持具体节点（稳定 `node_id`）、内置出口、其他策略组以及 `remaining` / `region:XX` / `tag:name` 动态节点集合；展开后按首次出现去重。字段缺失时继续使用内置的名称匹配默认顺序。后台入口为「规则库 → 编辑规则集 → 策略组成员」。

`proxy_group_options` 可选地覆盖单个策略组的 Mihomo 类型，支持 `select`、`url-test`、`fallback`、`load-balance`。三个自动类型共用 `url`、`interval`、`lazy`、`timeout`；`url-test` 额外支持 `tolerance`，`load-balance` 额外支持 `round-robin` / `consistent-hashing` / `sticky-sessions`。该字段仅影响 Mihomo；sing-box 始终生成 `selector`。模板绑定多个规则集且同名组重复配置时，members 与 options 分别按规则集顺序取第一个配置。

### 9.2 分组级 layout

每个 group 可以独立配置：

- **节点排序**：拖拽 UI 调整权重
- **分隔符插入**：在排序后的节点序列任意位置插一行"占位节点"（127.0.0.1:1 假节点，仅用于 Clash UI 视觉分组）
- **分隔符文字**：完全自定义（"🇹🇼 高级线路" / "----- TW -----" / "TIER 1" 都行）

UI 实现：分组详情 → "Node access" tab → 左侧节点池（可拖入）+ 右侧"渲染结果"（可拖排序、可加分隔符行、可双击改分隔符文字）。

### 9.3 用户级凭证注入（v3.9.0：读存储值，不再现算）

| 协议 | 字段 | 值来源 |
|---|---|---|
| VLESS | id | `psp_client.UUID` |
| VMess | id | `psp_client.UUID` |
| Hysteria2 | auth | `psp_client.UUID` |
| Trojan | password | `psp_client.Password` |
| Shadowsocks (legacy) | password | `psp_client.Password` |
| Shadowsocks 2022 | password | `psp_client.Password`（`<inbound serverPSK>:<Password>`，格式同构历史派生值） |

render 三条输出路径（mihomo/sing-box/URI list）统一从预取的 `psp_client` 记录读取存储凭据；`crypto.DeriveProxyPassword` 现算派生函数已退役（仅保留 `NewProxyPassword` 供建号/迁移一次性生成用），消除了"渲染与下发各自实现同一派生逻辑"的 lockstep 隐患。

---

## 10. 3X-UI 集成

### 10.1 真实 API 清单（v3.9.0：`/panel/api/clients/*` 一等公民端点）

Base path: `/panel/api` | 鉴权：`Authorization: Bearer <api-token>` 优先，session cookie 兜底。**PSP 3.9.1 最低要求 3X-UI 3.4.2**（共享 client 模型依赖 3.3.0，节点侧 REALITY 扫描接口再把 floor 提至 3.4.2），已测上限 3.5.0，见 [3xui-compat.md](3xui-compat.md)。

**Inbound CRUD（`/inbounds/*`）：**

| Method | Path | 用途 |
|---|---|---|
| GET | `/inbounds/list`、`/inbounds/list/slim` | 列出所有 inbound（含 `clientStats` 流量；slim 省去 `settings.clients` 大字段，用于流量轮询） |
| GET | `/inbounds/get/:id` | 单 inbound 详情 |
| POST | `/inbounds/add`、`/inbounds/update/:id`、`/inbounds/del/:id` | 新建 / 更新（body = 完整 Inbound 对象，PSP 侧走 RMW 保留 `clients[]`） / 删除 |
| POST | `/inbounds/setEnable/:id` | 切启用 |

**Client CRUD（`/clients/*`，v3.2.0+ 起为一等公民端点，取代旧版 `/inbounds/{id}/addClient` 等 inbound 作用域端点）：**

| Method | Path | 用途 |
|---|---|---|
| POST | `/clients/add` | 建 client（body `{client, inboundIds[]}`，一次挂多个 inbound） |
| POST | `/clients/update/{email}` | 按 email 更新（merge 语义，省略字段不清空，v3.2.0 起实测确认） |
| POST | `/clients/del/{email}` | 按 email 全局删除 |
| GET | `/clients/get/{email}` | 按 email 查询（回 `{client, inboundIds}`） |
| POST | `/clients/{email}/attach`、`/clients/{email}/detach` | 增量挂载 / 摘除 inbound（v3.9.0 起用于共享 client 挂载差分） |
| POST | `/clients/bulkCreate`、`/clients/bulkAttach`、`/clients/bulkDetach`、`/clients/bulkDel` | 批量操作，N 次 HTTP + N 次 Xray 重启收敛为 1 次 |

**其他：**

| Method | Path | 用途 |
|---|---|---|
| GET | `/server/status`、`/server/getPanelUpdateInfo`、`/server/getXrayVersion` | 版本感知（v3.6.0） |
| POST | `/server/updatePanel`、`/server/installXray` | 远程升级（管理员触发） |
| POST | `/server/scanRealityTargets` | 从所选 3X-UI/Xray 节点扫描 REALITY 目标；PSP 仅代理，不从中央主机探测（v3.9.1） |

> **已废弃/已删除，不要再用**：`/inbounds/{id}/addClient`、`/inbounds/{id}/delClient/:uuid`、`/inbounds/{id}/copyClients`、`/inbounds/getClientTraffics/:email`、`/inbounds/getClientTrafficsById/:id`、`/inbounds/{id}/resetClientTraffic/:email` —— 均已从 3X-UI 3.2.0 移除或从未被 PSP 封装（迁移设计与实测记录见 [3xui-3.2-clients-migration.md](3xui-3.2-clients-migration.md)）。流量轮询改为完全依赖 `ListInboundsSlim` 返回的 `clientStats`，无独立 traffic 端点调用。

### 10.2 Go 客户端封装

`internal/adapters/xui/client.go` 实现 `ports.XUIClient` 接口：

```go
type Client struct {
    baseURL  string
    apiToken string                  // 优先用 Bearer token
    username string                  // fallback cred
    password string
    httpClient *http.Client
    cookie   atomic.Value            // 兜底 session 缓存
}

// inbound
func (c *Client) ListInbounds(ctx) ([]Inbound, error)
func (c *Client) ListInboundsSlim(ctx) ([]InboundSlim, error)  // 流量轮询用，省 settings.clients
func (c *Client) GetInbound(ctx, id) (*Inbound, error)
func (c *Client) AddInbound(ctx, spec InboundSpec) (int, error)
func (c *Client) UpdateInbound(ctx, id, spec InboundSpec) error   // RMW 保留 clients[]
func (c *Client) DelInbound(ctx, id) error
func (c *Client) SetInboundEnable(ctx, id, enable bool) error

// client（一等公民，按 email 寻址，不再需要先 GET inbound）
func (c *Client) AddClient(ctx, inboundID int, spec ClientSpec) error       // →POST /clients/add
func (c *Client) UpdateClient(ctx, inboundID int, oldUUID string, spec ClientSpec) error
func (c *Client) UpdateClientWithInbound(ctx, inb *Inbound, uuid string, spec ClientSpec) error
func (c *Client) DelClientByEmail(ctx, inboundID int, email string) error
func (c *Client) GetClient(ctx, email string) (*ClientDetail, error)        // {client, inboundIds}
func (c *Client) ListClientInbounds(ctx, email string) ([]int, error)

// v3.9.0 多 inbound 挂载面（共享 client 模型）
func (c *Client) AddClientToInbounds(ctx, spec ClientSpec, inboundIDs []int) error
func (c *Client) AttachClient(ctx, email string, inboundIDs []int) error
func (c *Client) DetachClient(ctx, email string, inboundIDs []int) error
func (c *Client) BulkAttach(ctx, emails []string, inboundIDs []int) error
func (c *Client) BulkDetach(ctx, emails []string, inboundIDs []int) error
func (c *Client) BulkCreateClients(ctx, specs []ClientSpec) error
func (c *Client) BulkDelByEmail(ctx, emails []string) error

// server / 版本感知
func (c *Client) GetServerStatus(ctx) (*ServerStatus, error)
func (c *Client) UpgradePanel(ctx) error
func (c *Client) UpgradeXray(ctx, tag string) error
```

**关键实现细节：**

1. **鉴权优先级**：`api_token` 配置存在 → 直接走 `Authorization: Bearer`，**不需要 Login**；否则 fallback 走 cookie session（自动登录 + 过期重登）。
2. **client 操作按 email 为一等公民寻址**：自 3X-UI 3.2.0 起 `/clients/*` 不再要求先 GET inbound 再改 `settings.clients[]`；读-改-写（RMW）模式**只保留在 `UpdateInbound`**（轴 A 配置对账），用于确保下发配置更新时不清空当前挂载的全部 client。
3. **错误码**：3X-UI 未认证返回 404（混淆探测），需要靠 response body 区分。
4. **每个 3X-UI 面板一个 Client 实例**，按 `panel_id` 路由；panel name 由 service 层从 in-memory pool 在渲染时现查，DB 层不存冗余列。
5. **`tgId` 字段必须发 int64**（PSP v3.6.2 曾误发 string 导致 add/update 全部失败，已修复，见 [3xui-3.2-clients-migration.md §13](3xui-3.2-clients-migration.md)）。

### 10.3 节点连接配置存储（v3.5 起 PSP 为真相源）

| 字段 | 存储位置 | 理由 |
|---|---|---|
| 协议、端口、TLS/Reality、stream/sniffing/allocate（inbound 连接配置） | **面板 `nodes` 配置列（v3.5）**；`inbound_settings` / `stream_settings` 落盘 AES-GCM 加密 | PSP 为真相源；render 只读本地（零回源）、reconcile 反向下发，详见 [inbound-ownership.md](inbound-ownership.md) |
| server_address（客户端拨号地址） | 面板 `nodes.server_address` | 公网地址，proxy 块的 `server:` 字段 |
| display_name | 面板 `nodes.display_name` | 与 3X-UI remark 解耦 |
| region | 面板 `nodes.region` | 面板侧分类 |
| tags | 面板 `nodes.tags` | 灵活组合过滤（含 `server:xxx` 表达"同物理机"） |
| sort_order | 面板 `nodes.sort_order` | 全局默认排序，可被 group.layout 覆盖 |

**inbound 连接配置不以 3X-UI 为单源真相**——整份配置（去 `clients[]`）镜像进 `nodes` 表，PSP 成为真相源。clients 仍由 §10.4/§10.5 的对账机制管理、`clients[]` 不入配置快照（下发时 RMW 合并保留所有 live client）。

### 10.4 分层对账（详细）

目标：检测并修复"面板期望状态 vs 3X-UI 实际状态"的漂移；最坏漂移时间控制在分钟级而非小时级。

#### 10.4.0 对账分层

| 层 | 频率（默认） | 检查范围 | 触发 |
|---|---|---|---|
| **L1 写后即时验证** | 实时 | 仅被写的那一个 client / 挂载 | 每次共享 client 建立/挂载操作完成后立即 `GetClient` 校验生效 |
| **L2 流量采集顺便快查** | **5 min** | 存在性 + enable 一致性 | TrafficSvc cron 本来就在拉 `ListInboundsSlim`，顺便对比 |
| **L3 完整周期对账** | **15 min** | 全部检查项（轴 A + 轴 B） | 独立 cron |
| **L4 管理员手动** | 任意 | 同 L3 | dashboard "立即对账" 按钮 |

所有层共用同一套检查项实现，只是触发频率和扫描范围不同。频率全部可在设置里调。

#### 10.4.1 扫描范围

**仅遍历纳管范围**（共享 client 表 `psp_clients` + 过渡期遗留归属表 `user_xui_clients`）。纳管范围外的 client（维护者私人 + 未纳管老朋友）**完全不感知、不读、不写**。

#### 10.4.2 检查项（轴 B：client 层）

| # | 检查项 | 漂移场景 | 修复动作 |
|---|---|---|---|
| 1 | **client 存在性** | 面板有记录，3X-UI 该用户该面板找不到共享 client | `ProvisionClient` 重建并挂载 |
| 2 | **挂载集一致性** | `psp_client_inbounds` 期望挂载集 ≠ 3X-UI `GetClient().InboundIDs` | `AttachClient`/`DetachClient` 差分收敛 |
| 3 | **凭证一致性** | 面板存储的 uuid/password ≠ 3X-UI 当前 client | `SyncLifecycle` 推送面板存储值 |
| 4 | **enable 一致性** | 面板账号/服务状态推导的期望 enable ≠ 3X-UI `client.enable` | `SyncLifecycle` 以面板为准 |
| 5 | **配额/到期字段强制** | `client.totalGB`/`expiryTime` 不等于面板计算的 floor/到期值 | `SyncLifecycle` 重新推送 |

#### 10.4.3 节点元数据对账（nodes 表，轴 A）

| # | 检查项 | 漂移场景 | 修复动作 |
|---|---|---|---|
| 6 | **inbound 存在性** | 面板 `nodes` 表有记录，3X-UI 找不到对应 inbound | 标记 `nodes.enabled=false` + 写告警到 dashboard。**不删 nodes 行** |
| 7 | **inbound 启用状态** | 面板 `nodes.enabled=true` 但 3X-UI `inbound.enable=false` | 不在轴-A 配置对账里强制；inbound 启停由节点 enable 路径单独驱动（§3.2） |
| 8 | **inbound 连接配置漂移（轴 A）** | 已捕获节点的本地快照 ≠ 3X-UI live（端口 / TLS / Reality / stream / sniffing / allocate；语义 JSON 比较，忽略 `clients[]` / 键序 / remark）| `UpdateInbound` 下发 PSP 版本（RMW **保留全部 live client**）、推后 `GetInbound` 回采收敛；从未捕获的节点则改为从 live **回填捕获**（pull，不下发）。详见 [inbound-ownership.md](inbound-ownership.md) |

#### 10.4.4 输出

- 每条修复动作写 `audit_log`，`action='reconcile'`，含 before/after JSON
- 漂移摘要写 dashboard："今日检测到 N 项漂移，自动修复 K 项，M 项需人工介入"

#### 10.4.5 绝对不做的事 🚫

- ❌ **删除任何 3X-UI client**（即使纳管范围外的私人 client 也不动）
- ⚠️ **inbound 连接配置**（port / TLS / Reality / stream / settings）——**v3.5 起改为 PSP 真相源，reconcile 会下发覆盖 3X-UI 上的漂移**（§10.4.3 #8）。但下发走 RMW，**`clients[]` 全程原样保留**——配置层覆盖与 client 层"绝不误伤"是两条独立的轴，互不影响。inbound 的 `remark` 不在强制之列
- ❌ **新建 inbound**（管理员显式动作）
- ❌ **重命名认领而来的 client.email**（§7.8 认领的老朋友 client 保留原 email 是有意为之）

### 10.5 现有 inbound 复用与共存机制 ⭐

面板默认假设 3X-UI 里**已经有现成的 inbound** 在跑（且内部混杂着维护者私人 client 和朋友 client）。"不冲突 + 复用现有 inbound" 靠下面机制：

#### 10.5.1 导入 = 接管配置，但不扰动运行中的 inbound

§7.5.1 导入路径除了写面板侧 `nodes` 展示元数据，**还会把该 inbound 的整份连接配置（去 `clients[]`）捕获进 `nodes` 的配置列**——即"接管"，此后 PSP 是其配置真相源。导入这一步**本身仍不调 3X-UI 任何写 API**：只 `GET` 读取一次。

接管之后，该 inbound 的连接配置由 reconcile 轴 A 维持（§10.4.3 #8）：若被人在 3X-UI 直接改，下一轮会按 PSP 快照下发回去（RMW 保留全部 client）。

#### 10.5.2 client 挂载是"追加/差分"语义，不是"替换"

共享 client 建立/挂载走 `AddClientToInbounds`/`AttachClient`（v3.9.0），本质上仍是"面板只追加自己的 email，不动其他 client"——3X-UI 一等公民 client 模型下，其余 client（维护者私人、老朋友）作为独立记录存在，不受影响。

#### 10.5.3 纳管范围是写护栏的最后防线

面板所有写 client 操作入口都过 §4.3 的护栏：目标 email 必须命中共享 client 表或遗留归属表。即使代码有 bug、即使内部 API 被恶意调用，都**无法**触达纳管范围外的私人/老朋友 client。

#### 10.5.4 老朋友的渐进迁移路径

不必一次性把所有老朋友迁到面板纳管：

1. **第一阶段**：面板上线，**仅**纳管 1-2 个新朋友，老朋友继续靠手工 yaml 服务
2. **第二阶段**：通过 §7.8 "认领"流程，按需把老朋友逐个导入（不动 3X-UI，保留原 email/uuid 无缝迁移）
3. **第三阶段**：所有朋友都进面板后，老 yaml 文件归档

---

## 11. 流量统计与限额

### 11.1 采集

`TrafficSvc` 每 N 分钟（默认 5）：

1. 每 panel 并行调 `ListInboundsSlim`，拿到该面板所有 inbound 的 `clientStats`
2. **按 email 聚合**：3X-UI 对同一 email 的每个挂载 inbound 回显**同一份**聚合流量（一等公民模型），面板侧读一次、直接采用，**不再按 inbound 求和**（否则会重复计数）
3. 折算单调 delta，累加进 `psp_client` 与 `user` 的 lifetime 计数
4. INSERT `traffic_snapshots`（+ 小时聚合表）
5. **节点级流量**改读 3X-UI inbound 自身的 `up`/`down` 计数器（而非"求和该 inbound 上所有纳管 client"），语义变为"该 inbound 总流量"（含该 inbound 上非 PSP 托管的 client）

### 11.2 计费周期

| 配置 | 行为 |
|---|---|
| `traffic_reset_period = never` | 永久累计，从用户创建至今 |
| `traffic_reset_period = monthly` | 每月 1 号 00:00（面板时区）归零 |
| `traffic_reset_period = quarterly` | 每季首日归零 |

实现：`users.traffic_period_start` 记录当前周期起点；`users.period_baseline_bytes` 在 period rollover 时 freeze 为当时的 `lifetime_total_bytes`。**周期内已用 = lifetime_total_bytes - period_baseline_bytes**（O(1) 内存减法，零 DB 查询；通过 `domain.User.PeriodUsed()` 暴露）。

### 11.3 超限 → 暂停服务

| 触发 | 动作 |
|---|---|
| 周期内已用 > traffic_limit_bytes | `SetServiceSuspendedAndSync`：写 `service_disabled_reason='traffic_exceeded'`（**不动** `enabled`）+ 对该用户所有 `psp_client` 推 `enable=false` |
| 到达下一周期起点 | period_start 更新；若 `service_disabled_reason='traffic_exceeded'` → `ResumeServiceAndSync` 自动恢复 |
| 管理员手动改 traffic_limit_bytes 调高 | 立即重检查 → 若已不超限，自动恢复服务 |
| 管理员手动恢复服务 | 清 `service_disabled_reason`，推 `enable=true` |

**永远不删 client**，只是暂停服务；且**从不锁账号登录**——详见 §7.9 的账号状态/服务状态拆分。

### 11.4 看板聚合查询

| 指标 | 计算 |
|---|---|
| 永久用量 | 最新 total |
| 当前周期已用 | `lifetime_total_bytes - period_baseline_bytes`（O(1)） |
| 今日 | 最新 total - 今日 00:00 之前最后一条 |
| 30 天曲线 | 优先读小时聚合表，按日取末次快照，相邻 diff |

---

## 12. SSO（SAML 2.0 & OIDC）详细设计

### 12.1 配置（管理后台 + 数据库）

SAML 配置通过管理后台 `/api/admin/settings/saml` 读写，落到 `saml_settings` 单行表；OIDC 同理落到 `oidc_settings`，同为一等 SSO 方式（自 v3.5.0 起并列可用，非 SAML 的附属特性）。`config.yaml` 只保留启动所需的监听、数据库、JWT 等最小配置。

| 配置 | 存储 | 说明 |
|---|---|---|
| SP entity_id / ACS URL / 证书私钥 | `saml_settings` | 用于生成 SP metadata、签名和验签 |
| IdP metadata URL / 刷新间隔 | `saml_settings` | 自动拉取并缓存 metadata |
| attribute_mapping | `saml_settings` / `oidc_settings` JSON | UPN、email、display_name、groups claim 映射 |
| admin_group_ids / default_group_slug | `saml_settings` / `oidc_settings` JSON / 字段 | 首次登录入组与管理员识别 |
| new_user_defaults | `saml_settings` / `oidc_settings` JSON | 首次 SSO 用户默认流量、到期、重置周期 |
| issuer_url / client_id / client_secret | `oidc_settings` | 标准 OIDC RP 配置，`client_secret` AES-GCM 加密 |

### 12.2 SP / RP 实现

- SAML 使用 [`crewjam/saml`](https://github.com/crewjam/saml)：SP middleware 提供 AuthnRequest 发起、ACS 处理、Logout；metadata URL 自动拉取 + 缓存 + 后台刷新；内置 signature 验证、replay 防护、时间窗校验。
- OIDC 使用 [`coreos/go-oidc`](https://github.com/coreos/go-oidc)：标准 Authorization Code + PKCE 流程，state/nonce 校验。

### 12.3 SP metadata 暴露

面板自身的 SP metadata 通过 `GET /api/auth/saml/metadata` 暴露 XML，方便在 IdP 后台一键导入 SP 配置。

### 12.4 用户首次登录的入库逻辑

```
IdP 响应验签/校验通过 →
  upn = response.attr.upn（或 OIDC claim 映射）
  user = SELECT * FROM users WHERE upn=? AND sso_provider LIKE 'saml:%' OR 'oidc:%'
  if not user:
    user = CreateFromSSO(upn, display_name, groups)
  else:
    # 每次登录重算 role（防止 admin 权限被遗忘清除）
    user.role = admin if intersect(groups, admin_group_ids) else user
    update last_online_at
```

### 12.5 退化路径

| 故障 | 备用 |
|---|---|
| IdP metadata URL 拉不到 | 用上次缓存的 metadata；超过 7 天没刷新 → 报警 + 仍允许登录直到证书过期 |
| IdP 完全不可达 | 本地账号 fallback；UI 上"本地登录"按钮始终可见（除非管理员显式关闭） |
| IdP 配置变更（证书轮换） | metadata 自动刷新；管理员可在 SSO 设置页点"立即刷新" |

---

## 13. 安全设计

| 风险 | 缓解 |
|---|---|
| 误操作私人 client | §4 纳管范围护栏 |
| 订阅 URL 泄漏 | sub_token 独立于密码；可重置 |
| 本地账号密码泄漏 | bcrypt cost=10；登录限流 + 账户锁定（v3.7.0，按 ip 或 ip+upn，锁定期/阈值可配） |
| 弱口令/无二次验证的账户 | TOTP + passkey/WebAuthn + 邮箱验证码三种 2FA 方式（v3.7.0）；`Group.Require2FA` + 全局 `Require2FAForStaff` 可强制启用（作用域锁两级，不做 per-user 强制，见 §6.3） |
| 登录/注册被脚本刷 | CAPTCHA（image / 可扩展第三方，v3.7.0），按触发策略（如失败次数）弹出 |
| SAML Response 伪造 / 重放 | IdP 证书验签 + InResponseTo + NotBefore/NotOnOrAfter 校验 + ID 黑名单 |
| OIDC 授权码劫持 | state/nonce 校验 + PKCE |
| 自助注册账户滥用 | 邮箱验证后才能登录；注册开关/邮箱域名限制/默认配额均为全局策略（决策见 [v3.8.0-group-scoped-admin.md](v3.8.0-group-scoped-admin.md)） |
| 3X-UI 凭证落地 | `xui_panels` 表敏感字段 AES-GCM 加密，key 从 env 读 |
| SAML SP 私钥 / OIDC client secret 泄漏 | AES-GCM 加密；生产建议使用受控备份与最小权限访问 |
| 暴力扫 sub_token | `/{sub_path}/:token` 限流；token 长 32B base64url |
| 内部越权 | RBAC + JWT claim 包含 role + `token_version`（v3.7.0，改密/降权后旧 JWT 立即失效）；admin API 中间件校验 |
| UUID 暴力枚举导致协议密码泄漏 | UUID v4 = 122 bit 随机；共享 client 密码用 SHA-256 派生抹平结构 |
| 操作可追溯 | 所有写操作 AuditLog（含 actor/action/before/after/IP）+ 登录/2FA 事件独立记入 `auth_events`（v3.7.0） |
| 数据库暴露 | 监听 127.0.0.1 + 强密码；面板与 DB 同机部署 |

---

## 14. 技术栈

| 层 | 选型 | 备注 |
|---|---|---|
| 后端 | Go 1.26 + Gin + GORM | - |
| DB | MySQL 8.0 / SQLite（纯 Go 驱动，默认） | utf8mb4 |
| SAML | `github.com/crewjam/saml` | 生产级 SAML 2.0 SP |
| OIDC | `github.com/coreos/go-oidc/v3` | 生产级 OIDC RP |
| WebAuthn | `github.com/go-webauthn/webauthn`（Go）+ `@simplewebauthn/browser`（React） | Passkeys / FIDO2（v3.7.0） |
| YAML | `gopkg.in/yaml.v3` | |
| 密码 | `golang.org/x/crypto/bcrypt` | cost=10 |
| JWT | `github.com/golang-jwt/jwt/v5` | HS256 |
| AES-GCM | `crypto/aes` 标准库 | 凭证加密 |
| 前端 | React 18 + Vite + MUI (Material Design 3) + Zustand | v2.0 起从 Vue 3 + Element Plus 全量重写 |
| 国际化 | i18next + react-i18next | zh-CN / en-US |
| 图表 | ECharts（按需 import）| 流量趋势 |
| 前端打包 | `go:embed` 嵌到二进制 | 单二进制部署 |

---

## 15. 部署方案

### 15.1 依赖

- MySQL 8.0+ 或 SQLite（默认，纯 Go 驱动，适合自用/单机部署）
- 域名 + HTTPS 证书（SSO / passkey 要求）
- Linux 服务器（systemd）或 Docker 环境

### 15.2 二进制 + systemd

```
/opt/passwall-sub-panel/
├── psp                      # 二进制（含前端 dist 嵌入）
├── config/
│   ├── config.yaml
│   ├── rulesets/*.yaml
│   └── templates/*.yaml
├── data/
│   └── panel.db             # SQLite 模式时使用
```

### 15.3 Docker

```yaml
services:
  psp:
    image: ghcr.io/kazuhahub/passwall-sub-panel:latest
    container_name: psp
    network_mode: host          # 默认 host：方便容器内回访宿主机 3X-UI
    restart: unless-stopped
    volumes:
      - ./config:/app/config:ro
      - psp-data:/app/data
    environment:
      PSP_CONFIG: /app/config/config.yaml
      PSP_JWT_SECRET: ${PSP_JWT_SECRET:-}
      PSP_ENCRYPTION_KEY: ${PSP_ENCRYPTION_KEY:-}
volumes:
  psp-data:
```

### 15.4 初次启动

启动后自动创建默认 admin 账号（admin/admin），首次登录后建议修改密码。

---

## 16. 决策记录

| # | 决策点 | 选定 | 状态 |
|---|---|---|---|
| 1 | 数据库 | MySQL 8.0 + SQLite 双驱动 | ✅ 已实现 |
| 2 | 前端框架 | React 18 + MUI + TypeScript（v2.0 起，原 v1 为 Vue 3 + Element Plus） | ✅ 已实现 |
| 3 | SSO 方案 | SAML + OIDC 双支持 | ✅ 已实现 |
| 4 | 同步策略 | 异步任务队列 (sync_tasks) | ✅ 已实现 |
| 5 | 用户标识 | UPN 统一标识（移除 username/source） | ✅ 已实现 |
| 6 | Email 约定 | `u{userID}{suffix}@{domain}` 统一格式（suffix：默认空，SS-2022-128 混用用 `-c1`，flow 分裂用 `-k{8hex}`）。**v3.9.0 前的 `u{userID}-n{nodeID}@{domain}`（按节点区分）已被取代**——一个用户在同一面板现在通常只有一个跨 inbound 的共享 client | ✅ 已实现（v3.9.0 重定） |
| 7 | 订阅格式 | mihomo + sing-box + URI list | ✅ 已实现 |
| 8 | 配置存储 | MySQL/SQLite（KV `settings` 主表 + `scope_settings` 覆盖层 + 单行 `mail_settings`/`saml_settings`/`oidc_settings`）+ YAML（规则集/模板/启动配置） | ✅ 已实现 |
| 9 | 节点粒度 | inbound 为最小粒度，不引入 server 抽象层；同物理机多协议用 `server:xxx` tag 关联 | ✅ 已实现 |
| 10 | 3X-UI 鉴权 | Bearer api_token 优先，username/password + cookie 兜底 | ✅ 已实现 |
| 11 | client 模型 | v3.9.0：一个用户一个面板一个共享 client，跨多 inbound（取代每节点一个 client） | ✅ 已实现，详见 [v3.9.0-client-multi-inbound.md](v3.9.0-client-multi-inbound.md) |
| 12 | inbound 配置真相源 | v3.5.0：PSP 本地存储，render 零回源，reconcile 反向下发 | ✅ 已实现，详见 [inbound-ownership.md](inbound-ownership.md) |
| 13 | 设置作用域模型 | v3.8.0：全局默认 + 各 Group 覆盖两级（不做 OU 树、不做 per-user 层） | ✅ 已实现，详见 [v3.8.0-group-scoped-admin.md](v3.8.0-group-scoped-admin.md) |
| 14 | 账号状态 vs 服务状态 | v3.9.0：拆分为独立轴，超限/违规只暂停代理服务，不锁面板登录 | ✅ 已实现 |
| 15 | 2FA 方式 | TOTP + passkey/WebAuthn + 邮箱验证码，作用域锁两级（staff 全局 ∨ Group 覆盖，废弃 per-user 强制） | ✅ 已实现（v3.7.0 + v3.8.0） |
| 16 | 版本升级政策 | 不支持跨大版本跳级，每个 major 只携带 N-1→N 迁移逻辑 | ✅ 已实现，见 §17 |

---

## 17. 版本升级政策

### 17.1 大版本升级路径

**不支持跨大版本跳级升级**。例如 v3.x → v5.x 必须先升级到 v4.x 再升级到 v5.x，每次只跨一个主版本（major）。

每个 major 版本的二进制只携带 **N-1 → N** 的迁移逻辑。例：

| 当前安装 | 目标 | 升级路径 |
|---|---|---|
| v2.5.x | v3.0.0 | 直接 `psp migrate`（v3.0.0 携带 ≤ v2.5.x → v3 迁移逻辑） |
| v2.5.x | v5.0.0 | v2.5.x → **v3 → v4 → v5**，三步，每步分别跑该版本的 `psp migrate` |
| v3.x | v4.0.0 | 直接 `psp migrate`（v4 携带 v3.x → v4 迁移逻辑） |

minor / patch 内升级**不需要**跑 migrate（按 [[feedback_semver]] 规则 minor 只加功能、patch 只修 bug，DB schema 只做 AutoMigrate 兼容的增量变更）。

### 17.2 设计依据

- **代码体积可控**：迁移代码一直累加会让二进制无限膨胀；只保留 N-1 让每次发版的迁移代码量稳定在数百行级别
- **测试矩阵可控**：迁移路径只有"上一个 major → 当前"一条，CI 验证成本线性 O(1)
- **每次迁移都被充分实战检验**：用户被迫逐步升级 → 每条迁移路径都被大量真实部署跑过
- **同行业惯例**：PostgreSQL（pg_upgrade 只支持 N-1）、MariaDB、MongoDB、Cloudreve 都是这个政策

### 17.3 实现位置

- 迁移逻辑作为主程序的 `migrate` 子命令：`psp migrate --src=<旧库> --dst=<新库>`
- 代码位置：[internal/migrate/](../internal/migrate/)
- 运行时安全：normal panel 启动路径不会调用 migrate 包的任何 runtime 逻辑
- 当下一个 major（vN+1）发版时：删除 vN-1→vN 的迁移、新增 vN→vN+1 的迁移、额外内嵌 vN 全部 `cleanupLegacyState` 段（见 §17.4）

### 17.4 同 major 内部演进：cleanupLegacyState

某些"破坏性"改动**不需要**走完整的 `psp migrate` 流水线——它们在同一个 major 内部发生（beta 期间尤其常见），目标是 admin 直接换二进制重启就完成迁移。

这类一次性清理由 [internal/adapters/sqlstore/schema.go](../internal/adapters/sqlstore/schema.go) 的 **`cleanupLegacyState`** 函数承担，在 `EnsureSchema` 的 AutoMigrate 之后跑一次。

约束（必须严格遵守）：

1. **每段必须幂等**
2. **每段必须挂版本注释**
3. **严格 curated，绝不"自动 DROP 不认识的表"**
4. **打 log**：每次实际命中了清理逻辑就 `log.Warn` 一条

**大版本节点回收规则**：vN+1 发版时，`cleanupLegacyState` 里所有 vN.x 标签的段从 `EnsureSchema` 里**移除**，但**搬进 vN+1 的 `psp migrate` 子命令**，作为 vN→vN+1 迁移流程的必跑前序步骤。这样 admin 可以从 vN 任意 minor 直接升 vN+1，`cleanupLegacyState` 本身也不会无限增长（最多累积一个 major 内的演进）。

**记录每一段**：加新段时同时在 [CHANGELOG.md](../CHANGELOG.md) 对应版本下记一笔"legacy cleanup: ..."。

### 17.4.1 当前 cleanupLegacyState 段位 (registry)

下次 major 发版（v4.0.0）时**整张表清空**——这些段搬进 v4 的 `psp migrate` 子命令。

| 段标签 | 引入版本 | 解决的问题 | 搬迁到 |
|---|---|---|---|
| separator 表迁移 | v3.0.0-beta.7 | `nodes` 表里 `kind='separator'` 的行迁到独立 `nodes_separator` 表 | v4.0.0 migrate |
| separator visibility 模型重塑 | v3.0.0-rc.4 | 把 `show_in_all_groups` (bool) + `group_ids` 替换成 `mode` (string) + `node_ids` | v4.0.0 migrate |
| sub_logs 旧单列索引清理 | v3.6.0-beta.1 | 把 `sub_logs` 索引从两个独立单列升级到复合 + 独立索引，drop 旧索引 | v4.0.0 migrate |
| users.email 索引清理 | v3.6.1-beta.6 | `idx_users_email` 是无用索引（无 `WHERE email=?` 等值查询），纯写放大；删除 | v4.0.0 migrate |

**操作员视角**：每段都会在 PSP 启动时自动跑（幂等），新升级的部署看到对应 `[cleanupLegacyState]` 日志行就说明命中了；旧部署再启动就什么都不做。**不需要手动操作 DB**。

**v4.0.0 实施者视角**：发 v4 时把上面这几段代码从 `cleanupLegacyState` 移除，原样复制进 [internal/migrate/](../internal/migrate/) 的 v3→v4 迁移流程开头。除本表外，v4.0.0 还需要正式删除遗留的 `user_xui_clients` 归属表 / `ports.OwnershipRepo` / 旧 `sub_client_rules`+`sub_import_clients` 兼容代码，完整清单见 [migration/v3-to-v4-cleanup.md](migration/v3-to-v4-cleanup.md)。

---

## 18. 实施路线图

### M0 骨架 ✅ 已完成

- [x] go.mod / 项目目录 / 主入口
- [x] 配置加载
- [x] MySQL + SQLite 双驱动
- [x] GORM AutoMigrate

### M1 本地账号 + 基础订阅 ✅ 已完成

- [x] 本地账号登录 + JWT
- [x] 3X-UI Client 封装
- [x] SyncSvc + 归属表护栏
- [x] 用户 CRUD API + UUID 派生多协议密码
- [x] 订阅渲染器（mihomo）
- [x] 前端：登录 + 用户列表 + 新增

### M2 SSO + 节点管理 ✅ 已完成

- [x] SAML SP 集成
- [x] OIDC 集成
- [x] IdP metadata 自动拉取 + 刷新
- [x] role 由 SAML/OIDC group 决定
- [x] 节点 CRUD（含 inbound 协议表单）
- [x] 节点详情页 + 未纳管 client 认领

### M3 分组 + 高级渲染 ✅ 已完成

- [x] 分组 CRUD
- [x] tag_filter 解析
- [x] layout 编辑器（拖拽节点 + 加分隔符）
- [x] 规则集 / 模板编辑
- [x] 用户自助页
- [x] sing-box 渲染支持

### M4 流量与可观测 ✅ 已完成

- [x] TrafficSvc + 周期重置
- [x] 超限自动暂停服务
- [x] 流量看板（排行）
- [x] AuditSvc + 审计页
- [x] ReconcileSvc + 对账报告
- [x] 订阅日志

### M5 部署与硬化 ✅ 已完成

- [x] Dockerfile + docker-compose
- [x] systemd unit + Nginx 反代样例
- [x] 限流 + CORS 收紧
- [x] SSO Cookie Secure 标志
- [x] return_to 开放重定向防护

### M6 功能增强 ✅ 已完成

- [x] 订阅客户端检测与阻止
- [x] 邮件通知系统（到期/停用/公告）
- [x] 紧急访问功能
- [x] 站点品牌定制
- [x] 同步任务队列
- [x] 订阅访问日志
- [x] 品牌化订阅名称

### M7 可观测与体验 ✅ 已完成

- [x] 流量统计图表（ECharts）
- [x] 流量历史查询 API（含小时聚合）
- [x] 节点状态监控（health_state + ConfigSyncState）
- [x] 多语言 i18n（zh-CN / en-US，react-i18next）

### M8 账户安全 ✅ 已完成（v3.7.0）

- [x] TOTP 两步验证
- [x] Passkey / WebAuthn
- [x] 邮箱验证码 2FA
- [x] 账户锁定（登录失败限流）
- [x] CAPTCHA（图形验证码）
- [x] 自助找回密码
- [x] AES-GCM 加密静态密钥统一治理

### M9 分组化管理 ✅ 已完成（v3.8.0）

- [x] 全局默认 + 各 Group 覆盖两级设置模型（`scope_settings`）
- [x] 后台 Workspace 式导航重组（Dashboard / Directory / Infrastructure / Subscription / Reporting / Settings）
- [x] 自助注册（邮箱验证 + 落地组）
- [x] Email 可满足强制 2FA（`TwoFAEmailAsFactor`，实现为 `twofa_allow_email`）

### M10 共享 client 模型 ✅ 已完成（v3.9.0）

- [x] `psp_clients` / `psp_client_inbounds` 一等公民模型
- [x] 凭据全对称存储（render 不再现算派生）
- [x] 静默全量迁移（自动 + sync_task 兜底，无需人工操作）
- [x] 流量按 email 聚合读取（消除多 inbound 双重计数）
- [x] 账号状态 / 服务状态拆分
- [x] 3X-UI 最低版本升到 3.3.0（v3.9.0 shared client），再升到 3.4.2（v3.9.1 节点侧 REALITY 扫描）

---

## 19. Future Scope

### 19.1 Canvas LMS 联动

| 形式 | 方向 |
|---|---|
| LTI 1.3 工具 | 老师在 Canvas 课程嵌入"代理订阅"工具，学生点开自动 SSO 跳到面板 |
| 自动开户 | Canvas course enrollment webhook → 自动建用户 / 续期 |
| Canvas 用户身份联动 | Canvas SIS ID / login_id 映射到面板 user.upn |

当 Canvas 联动需求成熟后单独立项设计。**当前不实现**，但架构上保留扩展点：
- AuthSvc 接口设计成 pluggable（SAML / OIDC / LTI 各自实现 Provider）
- `sso_provider` 字段已是自由字符串（`local` / `saml:*` / `oidc:*`），未来扩展 `lti:*` 无需改 schema

### 19.2 v4.0.0 候选项（需要破坏性 schema 变更时才批量做）

- 删除遗留 `user_xui_clients` 归属表 / `ports.OwnershipRepo` / `domain.XUIClientEntry`（完整清单见 [migration/v3-to-v4-cleanup.md](migration/v3-to-v4-cleanup.md)）
- 删除 `sub_client_rules` / `sub_import_clients` 一次性兼容折叠代码（`sub_clients_legacy.go`）
- `internal/migrate/` 从 v2.5.x→v3 迁移逻辑改写为 v3.x→v4 迁移逻辑

### 19.3 有意保持现状（非待办，已在设计评审中定为非目标）

- **登录前策略（`lockout_*` / `captcha_*` / `login_mode`）不做 per-Group 覆盖**：识别用户身份前无法安全查询其分组，强行做会引入用户名枚举面，见 §6.3 与 [v3.8.0-group-scoped-admin.md §10-1](v3.8.0-group-scoped-admin.md)
- **注册策略保持全局**：`RegistrationEnabled` 等字段若允许某 Group 覆盖会出现"用哪个 Group 的策略判断新用户能否注册"的鸡生蛋问题，新用户落地组由全局 `RegistrationDefaultGroupID` 指定

### 19.4 其他候选

- 节点健康巡检 + 自动剔除
- 订阅 URL 二维码邮件 / Slack / Telegram 通知
- Web 端在线测试节点延迟
- 品牌/门户装饰性设置（Logo/公告/快捷链接）做 per-Group 覆盖（`RP-ID`/`issuer` 相关的品牌字段仍须全局，见 [v3.8.0-group-scoped-admin.md §10-6](v3.8.0-group-scoped-admin.md)）

---

## 20. 术语表

| 术语 | 含义 |
|---|---|
| PSP | Passwall-Sub-Panel 缩写 |
| inbound | 3X-UI 的入站监听条目，对应一个代理节点 |
| client | 3X-UI 里的用户凭据条目（email + uuid/password 等）。v3.9.0 起通常一个用户一个面板对应一个"共享 client"，见下 |
| 共享 client / `psp_client` | v3.9.0 模型：一个 (user, panel, credClass) 对应一个 client，挂载到该用户所有可访问的 inbound，取代旧的"每节点一个 client" |
| sub_token | 订阅 URL 中的凭证段 |
| 纳管范围 | 面板认定"可以安全写"的 client 集合：共享 client 表 `psp_clients` + 过渡期遗留归属表 `user_xui_clients` |
| 纳管 / 未纳管 | 是否在纳管范围内 |
| 作用域设置 / scope settings | v3.8.0：全局默认 + 各 Group 可覆盖的两级设置模型；可覆盖字段是显式白名单，登录前策略等有意排除在外 |
| 账号状态 / 服务状态 | v3.9.0 拆分：账号状态（`enabled`）控制面板登录权限；服务状态（`service_disabled_reason`）独立控制代理/订阅可用性，超限/违规只影响后者 |
| UPN | User Principal Name，SSO 用户唯一标识 |
| IdP | Identity Provider，身份提供方 |
| SP | Service Provider，服务提供方（本项目=面板自身） |
| ACS | Assertion Consumer Service，SP 接收 SAML Response 的端点 |
| SAML | Security Assertion Markup Language，XML-based SSO 协议 |
| OIDC | OpenID Connect，基于 OAuth 2.0 的 SSO 协议 |
| Reality | Xray 协议下的 TLS 伪装机制 |
| tag_filter | 分组按 region/tag 过滤节点的条件 |
| layout | 分组级渲染布局（排序 + 分隔符） |
| 轴 A / 轴 B | inbound 配置层（轴 A，PSP 持续强制对齐）与 client 层（轴 B，绝不误伤）两条独立的对账/管理边界，见 §4 与 §10.4 |
