# Passwall-Sub-Panel 架构设计文档

| 字段 | 值 |
|---|---|
| 文档版本 | 3.0.0 |
| 最后更新 | 2026-05-18 |
| 状态 | 活跃维护 |

## v3.0.0 数据库重构

> 升级到 v3.0.0 必读：[docs/UPGRADE-v3.0.0.md](UPGRADE-v3.0.0.md)。重构不可原地升级，需运行一次性迁移程序 `cmd/migrate-db-v2/`。

| 维度 | 重构前 (≤ v2.5.x) | 重构后 (v3.0.0+) |
|---|---|---|
| 配置存储 | 4 张专用表（`ui_settings` 30+ 列宽表 + `mail_settings` + `saml_config` + `oidc_config`） | **统一 KV `settings` 表**（type/name/value/encrypted）合并原 `ui_settings` + 邮件触发阈值；mail/saml/oidc 保留单行专用表为未来多账号/多 IdP 留扩展，并统一改名 `_settings` 后缀 |
| ownership 表 | `xui_clients`（字面误导，实际是 user↔panel client 占有映射） | **`user_xui_clients`**（语义清晰的 join 表命名） |
| panel_name 冗余 | 三张表（`nodes` / `xui_clients` / `client_traffic_snapshots`）冗余存，admin 改 panel 名后历史快照永远显示旧名 | **彻底删除冗余列**；service 层从 in-memory panel pool 按 `panel_id` 实时查询，admin 改名立即生效 |
| `rule_sets` DB 表 | 存在但实际未被注入（死代码） | **删除**；规则集真实来源是 `config/rulesets/*.yaml` |
| 流量保留策略 | 三张 snapshot 表（`traffic_snapshots` / `client_traffic_snapshots` / `node_traffic_snapshots`）**无 retention 清理**，默认 5 分钟轮询下 `client_traffic_snapshots` 年增长数千万行 | **新增 `traffic_snapshot_retention_days`（默认 180 天）**，cron 自动清理 |
| client lifetime | 仅在 `users` / `nodes` 层维护；per-client 历史用量需扫 snapshot 表 | **`user_xui_clients` 加 `lifetime_*_bytes` + `last_raw_*_bytes`**，对称于 users/nodes；admin 可直接 `ORDER BY lifetime_total_bytes` 看 client 历史 |
| snapshot 语义 | `traffic_snapshots` / `node_traffic_snapshots` 存 lifetime，但 `client_traffic_snapshots` 存 raw counter（语义不一致） | **三表统一存 lifetime**；raw counter 作为 baseline 收纳进 `user_xui_clients.last_raw_*_bytes` |
| period 用量计算 | `traffic.Service.periodUsage` + `mailer.Service.periodUsage` 各自 `LastBefore(period_start)` 随机点查（50 user 即 50 次 query/poll） | **`users.period_baseline_bytes` + `User.PeriodUsed()`**：lifetime - baseline，O(1) 内存计算；mailer 重复实现合并 |
| 空 delta snapshot | 即使 client 没新流量也每轮写入 | **零 delta 跳过**：用户离线时 client snapshot 写入量约降至 1/3 |
| 升级模式 | 原地 ALTER / DROP | **side-by-side 新库**（Cloudreve `drive`/`drive_v2` 模式）；v3.0.0 主程序只识别新 schema，旧库永久 backup；迁移由 `cmd/migrate-db-v2/` 一次性脚本完成（跑完即删） |

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
| client.email 约定 | `psp_{username}` | **统一格式 `u{userID}-n{nodeID}@{domain}`** |
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
| **核心** | SSO 登录（Entra ID SAML），保留本地账号作为 fallback |
| **核心** | 严格管理边界，**绝不**误伤非纳管资源 |
| **核心** | 流量限额由面板主动管理，超限自动断开所有归属客户端 |
| 次要 | 用户自助页（到期、流量、订阅 URL、改密码） |
| 次要 | 多客户端格式（mihomo / Sing-box） |
| 次要 | 流量历史曲线（日/月/永久） |
| 次要 | 审计日志 |
| **Future** | Canvas LMS 联动（LTI 1.3 / 外部工具） |

### 1.3 非目标

- 不做企业级机场系统（无支付、分销、邀请、工单）
- 不做高可用（单机部署，朋友圈量级，备份足矣）
- 不做客户端 App

---

## 2. 核心概念

| 概念 | 说明 | 真相源 |
|---|---|---|
| **节点 (Node)** | 一个 3X-UI inbound = 一个节点 | 3X-UI（协议参数）+ 面板（展示元数据 region/tag/sort）|
| **客户端 (Client)** | 3X-UI inbound 内的一个 client 条目 | 3X-UI |
| **用户 (User)** | 面板侧逻辑用户，对应 3X-UI 里多个 client | 面板 MySQL |
| **UPN (User Principal Name)** | 所有用户的唯一标识（本地用户和 SSO 用户统一） | 面板 MySQL |
| **分组 (Group)** | 用户分组，含 tag_filter + layout | 面板 MySQL |
| **归属表 (Ownership)** | 每个用户在 3X-UI 里拥有的 client 白名单 | 面板 MySQL |
| **规则集 (Rule Set)** | Clash/sing-box rules 分片 + 策略组顺序 | 面板 YAML (`config/rulesets/*.yaml`) |
| **模板 (Template)** | Clash/Sing-box 配置框架 | 面板 YAML (`config/templates/*.yaml`) |
| **layout** | 分组级渲染布局（节点排序 + 分隔符占位） | 面板 MySQL |
| **同步任务 (Sync Task)** | 异步可重试的 3X-UI 操作 | 面板 MySQL |
| **订阅客户端规则** | UA 检测 + 白名单过滤 | 面板 MySQL (UISettings) |
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
| **3X-UI client** | `client.id` / `uuid` | 字符串 | VLESS/VMess 的协议凭证；SS/SS-2022 可能为空，仅作辅助键 |
| | `client.email` | VARCHAR | **跨 inbound 聚合流量的主键** |
| **面板 nodes** | `nodes.id` | INT auto | 内部主键 |
| | `(panel_id, inbound_id)` | 唯一索引 | 与 3X-UI inbound 1:1 映射 |
| **归属表 user_xui_clients** | `(panel_id, inbound_id, client_email)` | 唯一索引 | 面板用户 ←→ 3X-UI client 匹配（v3.0.0 前叫 `xui_clients`）|

#### 关键设计：**email 是主匹配键，uuid 是辅助键**

| 跨系统操作 | 匹配键 | 原因 |
|---|---|---|
| 拉某用户跨所有 inbound 的流量 | **email** | 3X-UI `GET /getClientTraffics/:email` 原生跨 inbound 聚合 |
| 改某 inbound 内单个 client | email 优先，id 辅助 | 封装层读取当前 inbound，按 id 或 email 定位后回写完整 inbound；兼容 SS 空 id |
| 按邮箱删 client | email 优先，id 辅助 | 当前 client 有 id 时可按 id 删除；SS 空 id 时按 email 删除 |
| 在归属表反查"client 属于哪个面板用户" | (panel, inbound_id, email) | 三元组唯一索引；同一用户在所有 inbound email 相同 |

#### email 命名约定

所有用户（本地 + SSO）统一格式：

| 格式 | 例 |
|---|---|
| `u{userID}-n{nodeID}@{domain}` | `u42-n5@psp.local` |

- `domain` 来自 UISettings.EmailDomain（默认 `psp.local`）
- 包含 nodeID 是为了区分同一用户在不同 inbound 的 client
- 历史导入的 client **保留原 email** 不强制改名

#### 协议凭证派生表（同一 uuid 派生）

| 协议 | 字段 | 值 |
|---|---|---|
| VLESS | uuid | `user.uuid` |
| VMess | uuid | `user.uuid` |
| Trojan | password | `user.uuid` |
| SS legacy | password | `user.uuid` |
| SS-2022 | password | `base64(SHA-256(user.uuid))` |

UUID 重置 → 所有协议密码同步刷新（封装层 `proxyPassword(user, protocol)` 在 sync 和渲染时保持一致）。

---

## 3. 整体架构

```
┌──────────────────────────────────────────────────────────┐
│         React 18 + MUI (Material Design 3) 管理后台 SPA    │
│                                                           │
│  /login          [SSO 登录] 或 [本地账号登录]              │
│  /admin/dashboard        仪表盘                            │
│  /admin/users            用户 CRUD + 流量/到期             │
│  /admin/servers          3X-UI 服务器管理                  │
│  /admin/nodes            节点 CRUD（封装 inbound API）     │
│  /admin/groups           分组 CRUD + tag_filter + layout   │
│  /admin/rules            规则集编辑                        │
│  /admin/template         模板编辑 + 预览                   │
│  /admin/traffic          流量看板                          │
│  /admin/logs             日志管理（审计 + 订阅）           │
│  /admin/sync-tasks       同步任务管理                      │
│  /admin/settings         系统设置                          │
│  /user/me                用户自助                          │
└─────────────────────────────┬────────────────────────────┘
                              │ REST + JWT
                              ▼
┌──────────────────────────────────────────────────────────┐
│                   Go 后端 (Gin + RBAC)                   │
│                                                           │
│  HTTP 层：                                                 │
│  ├─ /api/auth/*              认证（本地/SAML/OIDC）       │
│  ├─ /api/admin/*             管理 API (role=admin)        │
│  ├─ /api/user/me/*           用户自助 API                  │
│  ├─ /sub/:token              订阅渲染（公开，动态路径）    │
│  └─ /health                  健康检查                      │
│                                                           │
│  Service 层：                                              │
│  ├─ AuthSvc       SAML/OIDC + 本地账号 + JWT 签发         │
│  ├─ UserSvc       用户 CRUD、改组联动 sync                 │
│  ├─ NodeSvc       inbound CRUD（封装 3X-UI API）          │
│  ├─ GroupSvc      分组 CRUD（含 layout）                   │
│  ├─ RenderSvc     订阅渲染（mihomo / sing-box）           │
│  ├─ SyncSvc       所有写 3X-UI 操作的统一入口 + 护栏       │
│  ├─ TrafficSvc    cron 拉取 + 快照 + 超限自动 disable     │
│  ├─ MailerSvc     邮件通知（到期/停用/公告）               │
│  ├─ AuditSvc      所有写操作记审计                          │
│  └─ ReconcileSvc  周期对账                                  │
└──────┬─────────────────────────────────────┬────────────┘
       ▼                                     ▼
┌─────────────────────┐              ┌─────────────────────┐
│       MySQL         │              │    3X-UI HTTP API   │
│  业务/运行时配置表    │              │   (N 个面板)         │
└─────────────────────┘              └─────────────────────┘
       ▲
       │
┌──────┴───────────────────────────────────────────────────┐
│      文件系统 (config/)                                  │
│  config.yaml          主配置（最小化：listen/jwt/db）    │
│  rulesets/*.yaml      规则集 + 策略组顺序                 │
│  templates/*.yaml     订阅模板                           │
└──────────────────────────────────────────────────────────┘
```

### 3.1 存储分层

| 数据 | 存储 | 理由 |
|---|---|---|
| 业务数据（users/groups/nodes/traffic/audit 等） | **MySQL** | 频繁读写、需事务、需 JOIN |
| 配置数据（settings KV / mail_settings / saml_settings / oidc_settings / xui_panels） | **MySQL** | 运行时可编辑，管理员通过 UI 修改 |
| 规则集内容 | **YAML 文件** | 本地配置资产，支持 Monaco 编辑器和版本发布默认值 |
| 订阅模板 | **YAML 文件** | 本地配置资产，支持 Monaco 编辑器和版本发布默认值 |
| 主配置 (config.yaml) | **YAML 文件** | 启动时加载，最小化配置（listen/jwt/db） |

**注意**：3X-UI 面板凭证、SAML/OIDC、邮件、UI 设置等运行时配置存 MySQL；规则集和订阅模板仍以 YAML 文件为真相源，不做数据库迁移。

### 3.2 本地优先与远端异步一致性

用户、分组、节点元数据等面板状态以本地 MySQL 为真相源。管理员操作先提交本地状态，再尝试同步 3X-UI；远端失败（面板离线、单节点错误、SS 空 client id 等）不回滚本地操作，而是写入 `sync_tasks` 异步重试。

| 场景 | 本地处理 | 远端处理 |
|---|---|---|
| 修改用户启用、到期、分组 | 先更新 `users` / `groups` | 逐节点同步；失败进入 `user_push_config` 或 `user_resync` |
| 紧急访问、重置订阅/协议凭证 | 先写新凭证或新到期时间 | 推 3X-UI 失败时进入 `user_resync` |
| 节点元数据启停 | 先更新 `nodes.enabled` 等本地字段 | 推 inbound enable 失败时进入节点同步任务 |
| 新建 inbound | 需要先拿到 3X-UI 返回的 `inbound_id` | 远端创建失败时仅保留 `node_create` 任务，不创建无法映射的本地节点 |
| 修改 inbound 协议参数 | 本地只保存展示元数据 | 协议参数以 3X-UI 为真相源，远端失败进入 `node_update` |

---

## 4. 管理边界（核心安全约束）

⚠️ **本节决定面板"绝不误伤"用户私人资源的能力，是整个系统最重要的设计。**

### 4.1 现状观察

3X-UI 实际部署中，单个 inbound 内部混杂了维护者私人客户端与朋友客户端（例如 inbound 1 同时有 `Kazuha Home`、`Clash Private Subscribe` 等私人 client 和 `wxid: xxx` 等朋友 client）。**inbound 级别无法干净划分纳管/非纳管**，必须下沉到 client 级别。

### 4.2 边界规则

| 层 | 是否过滤 | 识别方式 |
|---|---|---|
| inbound（读 / 列表） | 不过滤，全部可见 | - |
| inbound（写：修改） | 允许，但提示"含未纳管 client" | 见 §4.4 |
| inbound（写：删除） | **必须 inbound 内全部 client 都在归属表内** | 见 §4.4 |
| client（读 / 列表） | 不过滤，UI 区分纳管/未纳管 | - |
| **client（写）** | **必须命中归属表** | 见 §4.3 |

### 4.3 client 写护栏

```go
func (s *SyncSvc) ensureClientOwned(userID int, inboundID int, email string) error {
    exists, _ := s.repo.XUIClientExists(userID, inboundID, email)
    if !exists {
        return ErrClientNotOwnedByPanel
    }
    return nil
}
```

所有调 3X-UI 写 client API（AddClient / DelClient / UpdateClient）入口都过此 guard。未命中归属表 → 拒绝执行。

### 4.4 inbound 写护栏

```go
func (s *SyncSvc) ensureInboundDeletable(inboundID int) error {
    clients, _ := s.xui.ListClientsOf(inboundID)
    for _, c := range clients {
        if !s.repo.IsManagedClient(inboundID, c.Email) {
            return ErrInboundHasUnmanagedClients
        }
    }
    return nil
}
```

`UpdateInbound` 允许（含未纳管 client 也能改），但 UI 提示"该 inbound 内有 N 个未纳管 client，修改会影响他们"。`DeleteInbound` 必须全部 client 纳管。

### 4.5 UI 中的区分

| 场景 | 表现 |
|---|---|
| 节点详情页 client 列表 | 纳管标蓝色 + 可操作；未纳管灰色不可点 |
| 流量看板 | 仅统计纳管 client |
| 对账任务 | 仅扫描归属表内的 client，其他完全不感知 |

---

## 5. 数据模型

### 5.1 MySQL Schema (v3.0.0+)

主要表通过 GORM AutoMigrate 自动创建。规则集/模板由 YAML 仓储读取，不以 MySQL 表为真相源。

**总览（17 张表，按职责分 4 类）**：

| 分类 | 表 | 说明 |
|---|---|---|
| **配置 (4)** | `settings` | KV 主配置（type/name/value/encrypted/updated_at），类型分组：site / auth / sub / security / runtime / notice / notify。替代了 v3.0.0 前的 `ui_settings` 30+ 列宽表 |
| | `mail_settings` | SMTP 连接（单行）；瘦身后 8 字段。未来扩展为 `mail_accounts` 多行表 |
| | `saml_settings` | SAML SSO（单行）；改名自 `saml_config`。未来扩展为 `saml_sp` + `saml_idps` |
| | `oidc_settings` | OIDC SSO（单行）；改名自 `oidc_config`。未来扩展为 `oidc_providers` |
| **业务实体 (6)** | `users` | 含 `lifetime_*_bytes` + `period_baseline_bytes` + `lifetime_baseline_at` 用于 O(1) 用量计算 |
| | `groups_` | 用户分组（`groups` 在某些 MySQL 版本是关键字） |
| | `nodes` | 3X-UI inbound 在面板侧的元数据；**panel_name 列已删**（v9） |
| | `xui_panels` | 下游 3X-UI 面板凭据 |
| | `user_xui_clients` | 改名自 `xui_clients`；本地 user ↔ panel client 占有映射；新增 `lifetime_*` + `last_raw_*` 字段；**panel_name 列已删** |
| | `mail_templates` | 邮件模板（按 kind 主键，多行） |
| **时序快照 (3)** | `traffic_snapshots` | user 级流量（lifetime 语义） |
| | `client_traffic_snapshots` | per-client × inbound 流量（v3.0.0 改为 lifetime 语义）；**panel_name 列已删** |
| | `node_traffic_snapshots` | node 级流量（lifetime 语义） |
| **日志/事件 (4)** | `audit_log` | admin 操作审计 |
| | `sub_logs` | 订阅访问日志 |
| | `mail_sent` | 发件历史 + 幂等键 (unique user_id,kind,window_key) |
| | `sync_tasks` | 同步任务（带状态/重试） |

**v3.0.0 升级迁移**：通过独立的 `cmd/migrate-db-v2/` 一次性程序完成。v3.0.0 主程序**完全不识别旧 schema**；旧库由 admin 手工保留作永久 backup，无原地 ALTER。详见 [docs/UPGRADE-v3.0.0.md](UPGRADE-v3.0.0.md) 与 `cmd/migrate-db-v2/README.md`。

**SQL DDL（v9，节选关键字段，详见 [internal/adapters/mysql/schema.go](../internal/adapters/mysql/schema.go) 与 [settings_kv_repo.go](../internal/adapters/mysql/settings_kv_repo.go)）**：

```sql
-- 用户（含 lifetime 累加 + period 基线 + emergency 配额）
CREATE TABLE users (
  id                       BIGINT AUTO_INCREMENT PRIMARY KEY,
  upn                      VARCHAR(255) UNIQUE NOT NULL,
  sso_provider             VARCHAR(64) NOT NULL DEFAULT 'local',
  sso_subject              VARCHAR(255) NOT NULL DEFAULT '',
  email                    VARCHAR(255) INDEX,
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
  period_baseline_bytes    BIGINT DEFAULT 0,   -- v3.0.0 新增：O(1) periodUsage 基线
  lifetime_baseline_at     DATETIME,
  display_name             VARCHAR(128),
  remark                   VARCHAR(255),
  enabled                  BOOLEAN NOT NULL,
  auto_disabled_reason     VARCHAR(32),
  disable_detail           TEXT,
  block_violation_count    INT DEFAULT 0,
  emergency_used_count     INT,
  emergency_until          DATETIME,
  emergency_baseline_bytes BIGINT DEFAULT 0,
  created_at               DATETIME,
  updated_at               DATETIME,
  INDEX idx_user_sso (sso_provider, sso_subject)
);

CREATE TABLE groups_ (
  id BIGINT AUTO_INCREMENT PRIMARY KEY, slug VARCHAR(64) UNIQUE NOT NULL,
  name VARCHAR(128) NOT NULL, tag_filter JSON, layout JSON,
  remark VARCHAR(255), created_at DATETIME
);

-- nodes：v3.0.0 删除 panel_name 列
CREATE TABLE nodes (
  id BIGINT AUTO_INCREMENT PRIMARY KEY,
  panel_id BIGINT NOT NULL INDEX, inbound_id INT NOT NULL,
  display_name VARCHAR(255) NOT NULL, server_address VARCHAR(255),
  flow VARCHAR(64), region VARCHAR(16) NOT NULL, tags JSON,
  sort_order INT DEFAULT 0, enabled BOOLEAN DEFAULT TRUE,
  kind VARCHAR(16) DEFAULT 'real',
  lifetime_up_bytes BIGINT DEFAULT 0, lifetime_down_bytes BIGINT DEFAULT 0,
  lifetime_total_bytes BIGINT DEFAULT 0,
  last_traffic_up_bytes BIGINT DEFAULT 0, last_traffic_down_bytes BIGINT DEFAULT 0,
  last_traffic_total_bytes BIGINT DEFAULT 0,
  health_state VARCHAR(32) DEFAULT '', health_checked_at DATETIME,
  health_detail VARCHAR(512) DEFAULT '',
  created_at DATETIME,
  UNIQUE KEY uk_panel_inbound (panel_id, inbound_id)
);

-- v3.0.0 改名 xui_clients → user_xui_clients；新增 lifetime + last_raw；删除 panel_name
CREATE TABLE user_xui_clients (
  id BIGINT AUTO_INCREMENT PRIMARY KEY,
  user_id BIGINT NOT NULL INDEX, panel_id BIGINT NOT NULL INDEX,
  inbound_id INT NOT NULL, client_email VARCHAR(255) NOT NULL,
  client_uuid VARCHAR(36) NOT NULL, created_at DATETIME,
  -- v3.0.0 新增：per-client lifetime counters (mirrors users/nodes)
  lifetime_up_bytes BIGINT DEFAULT 0, lifetime_down_bytes BIGINT DEFAULT 0,
  lifetime_total_bytes BIGINT DEFAULT 0,
  -- baseline for next-poll monotonicDelta computation
  last_raw_up_bytes BIGINT DEFAULT 0, last_raw_down_bytes BIGINT DEFAULT 0,
  last_raw_total_bytes BIGINT DEFAULT 0,
  UNIQUE KEY uk_owner_match (panel_id, inbound_id, client_email)
);

-- 流量时序：三表语义统一为 lifetime（v9）
CREATE TABLE traffic_snapshots (
  id BIGINT AUTO_INCREMENT PRIMARY KEY, user_id BIGINT NOT NULL,
  up_bytes BIGINT, down_bytes BIGINT, total_bytes BIGINT,  -- lifetime cumulative
  captured_at DATETIME NOT NULL,
  KEY idx_user_time (user_id, captured_at)
);

CREATE TABLE client_traffic_snapshots (
  id BIGINT AUTO_INCREMENT PRIMARY KEY,
  user_id BIGINT NOT NULL, panel_id BIGINT NOT NULL,
  inbound_id INT NOT NULL, client_email VARCHAR(255) NOT NULL,
  up_bytes BIGINT, down_bytes BIGINT, total_bytes BIGINT,  -- v3.0.0: 改为 lifetime 语义
  captured_at DATETIME NOT NULL,
  KEY idx_client_time (user_id, panel_id, inbound_id, client_email, captured_at)
);

CREATE TABLE node_traffic_snapshots (
  id BIGINT AUTO_INCREMENT PRIMARY KEY, node_id BIGINT NOT NULL,
  up_bytes BIGINT, down_bytes BIGINT, total_bytes BIGINT,  -- lifetime
  captured_at DATETIME NOT NULL,
  KEY idx_node_time (node_id, captured_at)
);

CREATE TABLE xui_panels (
  id BIGINT AUTO_INCREMENT PRIMARY KEY, name VARCHAR(128) UNIQUE NOT NULL,
  url VARCHAR(512) NOT NULL,
  api_token TEXT,    -- AES-GCM 密文
  username VARCHAR(255),
  password TEXT,     -- AES-GCM 密文
  remark VARCHAR(255), created_at DATETIME, updated_at DATETIME
);

CREATE TABLE audit_log (
  id BIGINT AUTO_INCREMENT PRIMARY KEY,
  actor VARCHAR(255) NOT NULL, action VARCHAR(64) NOT NULL,
  target VARCHAR(255), before_json JSON, after_json JSON,
  ip VARCHAR(64), at DATETIME INDEX
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

-- 配置 KV 主表（v3.0.0 新增；替代旧版 ui_settings 宽表）
CREATE TABLE settings (
  id BIGINT AUTO_INCREMENT PRIMARY KEY,
  type VARCHAR(32) NOT NULL,           -- 分组：site/auth/sub/security/runtime/notice/notify
  name VARCHAR(128) NOT NULL,          -- 字段名（snake_case）
  value TEXT,                          -- string / 数字串 / JSON
  encrypted BOOLEAN NOT NULL DEFAULT FALSE,  -- 透明 AES-GCM enc/dec 标记
  updated_at DATETIME,
  UNIQUE KEY uk_setting_kv (type, name),
  INDEX idx_setting_type (type)
);

-- 瘦身后的 mail_settings：仅 SMTP 连接（旧版 mail_settings 的 expire_before_days/traffic_remain_percent 已搬至 settings.type='notify'）
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

-- saml_settings / oidc_settings 字段不变，仅改名（saml_config / oidc_config → *_settings）
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
```

**`settings` KV 表的字段分组**（type）：

| type | 用途 | 字段示例 |
|---|---|---|
| `site` | 站点品牌 | site_title, app_title, logo_url, footer_text, theme_color, email_domain, sub_base_url |
| `auth` | JWT + 登录策略 | login_mode, jwt_issuer, jwt_access_ttl_minutes, disallow_user_local_login |
| `sub` | 订阅渲染 | sub_path, sub_clients (JSON, 检测族→导入App 两层注册表；v3.3.0 合并自 sub_client_rules + sub_import_clients，旧字段一次性迁移代码 sub_clients_legacy.go 计划 v4.0.0（下个 major）删), sub_update_interval_hours |
| `security` | 限流 / 留存 / 应急 | sub_per_ip_per_min, audit_retention_days, **traffic_snapshot_retention_days** (v3.0.0 新增), emergency_access_* |
| `runtime` | Cron / 性能 / 时区 | timezone, cron_traffic_pull_minutes, cron_reconcile_minutes, max_panel_concurrency |
| `notice` | 用户视图 | quick_links (JSON), global_announcement (JSON) |
| `notify` | 邮件触发阈值（v3.0.0 从 mail_settings 搬出） | expire_before_days, traffic_remain_percent |

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

**注意**：3X-UI 面板凭证、SAML/OIDC 配置、系统设置等通过管理后台写入 MySQL。规则集和模板通过管理后台编辑，但最终落盘到 YAML 文件。

`rulesets/*.yaml` 的关键字段：

```yaml
slug: default
name: 默认规则
enabled: true
proxy_group_order:
  - 节点选择
  - 中国大陆
rules:
  - DOMAIN-SUFFIX,example.com,节点选择
```

---

## 6. 后台页面 × 操作矩阵

### 6.1 管理员后台 (role=admin)

| 页面 | 列表显示 | 操作 |
|---|---|---|
| **总览 (Dashboard)** | 统计概览 | - |
| **用户管理** | UPN / 显示名 / 分组 / 到期 / 配额 / 已用 / 状态 | 新增、编辑、改组、改到期、改配额、重置 UUID、禁用(含原因)、启用(含原因)、重置 sub_token、重置密码、删除、批量延期、批量改组 |
| **服务器** | 3X-UI 面板列表 | 新增、编辑、删除、连接测试 |
| **节点管理** | 显示名 / 协议 / 端口 / region / tags / 启用 | 新增 inbound、编辑、改 region/tag/sort、启用/禁用、删除、导入未纳管 inbound、认领 client |
| **分组** | 分组名 / slug / 成员数 | 新增、编辑、编辑 layout、删除 |
| **规则库** | 规则集列表 | 新增、编辑、删除、启用/禁用 |
| **配置方案** | 模板列表 | 新增、编辑、删除、设为默认 |
| **订阅管理** | - | 公网基地址、订阅路径、客户端规则、导入客户端、日志保留、违规自动停用 |
| **流量统计** | Top-N 排行 | 查看详情、手动设置用量 |
| **日志管理** | 订阅日志 / 审计日志 | 清空、按策略清理、查看详情 |
| **同步任务** | 任务列表 | 重试、取消、清理已完成 |
| **系统设置** | - | 基本设置、邮件提醒、站点品牌、SSO 认证 |

### 6.2 用户自助页 (role=user)

| 内容 | 操作 |
|---|---|
| 到期倒计时 / 流量进度条 / 订阅 URL + 二维码 | 改密码、重置 sub_token、紧急访问、查看订阅客户端导入 |
| 快捷链接 | 管理员配置的外部链接（如客户端下载、教程等） |
| 全局公告 | 管理员发布的置顶公告 |

### 6.3 operator 角色 + 权限扩展点（预留接口，勿绕过）

**operator（日常运营）** 是介于 admin 与 user 之间的受限管理角色：

- **能做**：登录后台；普通用户（role=user）CRUD；流量查看 / 手动设用量 / 紧急访问；节点启用/禁用；同步任务重试/取消；查看分组 / 规则库 / 配置方案 / 日志 / 审计。
- **不能做**：3X-UI 服务器（面板凭据）、系统设置、邮件 SMTP、SSO（SAML/OIDC）；节点 / 分组 / 规则库 / 配置方案 / 分隔符的增改删；日志清空与保留期；同步任务清理；操作 admin/operator 账户或分配 admin/operator 角色。

**授权实现（四处，必须保持一致）：**

| 层 | 位置 | 机制 |
|---|---|---|
| 后端路由 | `internal/transport/http/router.go` | `staffGroup`（admin+operator）vs `adminGroup`（仅 admin）两组；`middleware.RequireRole(...)` 按角色放行 |
| 后端 handler | `internal/transport/http/handler/admin_user.go` | `ensureOperatorAllowed`（operator 不可动 admin/operator 账户）、`guardOperatorRoleAssignment`（operator 只能分配 role=user） |
| 前端路由 | `web-react/src/router/RequireAuth.tsx` + `router/home.ts` `ADMIN_ONLY_ROUTES` | servers / settings 对非 admin 重定向到 dashboard |
| 前端按钮 | `web-react/src/utils/permissions.ts` | **能力层** `useCan('config.write' \| 'users.elevate' \| ...)`，各 View 按能力隐藏操作按钮 |

**预留扩展点 —— 自定义角色 / 细粒度权限（重点，别忘）：**

- **前端所有操作门控只走能力层** `utils/permissions.ts`：调用点一律 `useCan(capability)`，**绝不内联 `role === 'admin'`**。将来支持自定义角色或后端下发权限集时，**只改 `permissions.ts` 的 `ROLE_CAPS` / `roleCan`**（例如改为按角色名查后端返回的权限表），所有 `useCan(...)` 调用点保持不变 —— 这就是"留好的口，不把路封死"。
- **后端**新增角色：在 `domain.Role` 加枚举值，并纳入 `router.go` 的 staffGroup/adminGroup（或将来更细的分组）；`ensureOperatorAllowed` 这类守卫按需推广为基于能力的检查。
- **前后端能力定义必须对齐**：`permissions.ts` 的 `ROLE_CAPS` 与 `router.go` 的路由分组是同一套规则的两端，改一处要同步另一处。

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
  ⑥ 查 MySQL users WHERE upn=?：
     - 找到 → 更新信息
     - 未找到 → 自动建用户（role=auto判定, group=默认组）
  ⑦ 对比 admin_group_ids，命中 → role=admin
  ⑧ 签发 JWT (HS256, access 2h + refresh 7d)，HttpOnly cookie
  ⑨ 302 跳回 /admin 或 /user/me（按 role）
```

### 7.2 本地账号登录（备用）

```
用户访问 /login → 输入 UPN 和密码
  ① 后端查 users WHERE upn=?
  ② bcrypt 验证 password_hash
  ③ 签发 JWT
  ④ 跳转

注：本地账号仅供以下场景：
  - 初次启动建第一个 admin
  - SSO 故障 fallback
  - 不能用 SSO 登录的特殊用户
```

### 7.3 新建本地用户

```
UI: 用户管理 → 新增 → source=local, username, group, expire_at, traffic_limit_gb → 提交

后端:
  1. 校验 username 唯一
  2. 生成 uuid (v4)、sub_token、bcrypt(初始密码)
  3. 先 INSERT users（本地用户立即生效）
  4. 派生多协议密码:
       vless_uuid = user.uuid
       vmess_uuid = user.uuid
       trojan_pwd = user.uuid
       ss_legacy_pwd = user.uuid
       ss2022_psk = base64(SHA-256(user.uuid))  -- 32 字节 PSK
  5. 查 group.tag_filter → 拉 3X-UI inbound list → 过滤匹配
  6. 对每个匹配 inbound 调 SyncSvc.AddClient:
       email = "u{userID}-n{nodeID}@{domain}"
       根据 inbound 协议选 uuid / password 字段
       expiry_time = users.expire_at 对应毫秒时间戳（永久为 0）
       total_gb = 0 (面板侧管)
       远端失败时不回滚用户，写 sync_tasks 后续重试
  7. 写 user_xui_clients 表（只记录面板纳管的 client）
  8. 返回 {sub_url, initial_password}
```

### 7.4 重置用户 UUID

```
UI: 用户编辑 → 重置 UUID（带确认对话框，提示"所有协议密码会同步换，朋友现有客户端要重新拉订阅"）

后端:
  1. 生成新 uuid (v4)
  2. 先更新 users.uuid / sub_token（本地订阅立即使用新凭证）
  3. 派生新的各协议密码
  4. 对每个 user_xui_clients 条目 → SyncSvc.UpdateClient:
       新 uuid / 新 password
       远端失败时写 user_resync 任务，不回滚本地凭证
  5. AuditLog（before/after 不存 uuid 明文，存哈希值 + 操作者）
  6. 返回成功
```

### 7.5 节点纳入面板（两种路径）

⚠️ **默认走路径 A（导入现有 inbound），路径 B 仅在确实需要建新 inbound 时使用。维护者已有的 inbound 全部走 A。**

#### 7.5.1 路径 A：导入现有 inbound（推荐 ⭐）

适用：3X-UI 后台已经存在 inbound（含混杂的私人 + 朋友 client），只需在面板登记元数据。

```
UI: 节点管理 → "未纳管的 inbound" tab → 列出所有 3X-UI inbound 但不在面板 nodes 表内的
    → 选择某条 → "纳入管理" → 填 display_name / region / tags / sort_order → 保存

后端:
  1. 校验：(panel_name, inbound_id) 不在 nodes 表
  2. **不调 3X-UI 任何写 API**
  3. INSERT nodes 表（仅展示元数据）
  4. AuditLog

inbound 内现有 client（维护者私人 + 老朋友）完全不感知，保持原样工作。
后续给面板用户加权限时（§7.5.3），addClient 是追加语义，仍不影响现有 client。
```

#### 7.5.2 路径 B：从面板新建 inbound（可选）

适用：希望完全在面板里走流程，不去 3X-UI 后台填表。

```
UI: 节点管理 → 新增 → 选协议 (VLESS+Reality 等) → 填地址/端口/Reality 参数 → 设 region/tags → 保存

后端:
  1. 校验协议参数合法
  2. 调 XUIClient.AddInbound 在 3X-UI 创建新 inbound（settings.clients[] 为空）
  3. 拿到 3X-UI 返回的 inbound_id
  4. INSERT nodes 表
  5. AuditLog

如果 3X-UI 返回端口冲突等永久错误，任务标记失败并展示错误；如果是临时网络/认证故障，写 `node_create` 异步任务。新建 inbound 需要远端返回 `inbound_id`，因此此路径不能先创建没有映射关系的本地节点。

后续：根据 group.tag_filter 自动补 client（见 §7.5.3）。
```

#### 7.5.3 给用户加权限到节点（A/B 路径都用同一流程）

```
触发：用户改组、分组扩容、新建用户。

SyncSvc.AddClient(user_id, node_id):
  1. ensureClientNotExists(user_id, panel, inbound_id, email)
  2. XUIClient.AddClient(inbound_id, ClientSpec{
       email = "u{userID}-n{nodeID}@{domain}"
       uuid = user.uuid
       password 派生:
         - VLESS / VMess: 用 uuid
         - Trojan: 用 uuid
         - SS legacy: 用 uuid
         - SS-2022: base64(SHA-256(uuid))
       enable = true
       expiry_time = users.expire_at 对应毫秒时间戳（永久为 0）
       total_gb = 0     (面板侧维护)
     })
     封装层内部"读-改-写":
       a. GET /get/:inbound_id 读现有 Inbound 完整 state
       b. settings.clients[] **末尾追加**新 client
       c. POST /addClient 回写
  3. INSERT user_xui_clients (归属表)
  4. AuditLog

现有所有 client 原封不动。
```

### 7.6 节点编辑

```
UI: 节点详情 → 改 region 或协议参数

仅改 region/tags/sort → 只写 nodes 表，不动 3X-UI
改协议参数 → 调 XUIClient.UpdateInbound + 提示影响范围；远端失败写 node_update 异步任务
```

### 7.7 节点删除

```
UI: 节点详情 → 删除按钮

后端:
  1. 先标记 nodes.enabled=false，避免新订阅继续下发该节点
  2. 写 node_delete 异步任务，由 worker 调 XUIClient.GetInbound → 列所有 client
  3. 校验：所有 client 都在 user_xui_clients 表内？
     - 是 → 继续
     - 否 → 任务失败并记录未纳管 client，UI 在任务详情提示"请先处理未纳管 client"
  4. 对每个纳管 client → SyncSvc.DelOwnedClient + 删 user_xui_clients 条目
  5. 调 XUIClient.DelInbound
  6. 删 nodes 表条目
  7. AuditLog
```

### 7.8 导入已有 client

```
UI: 节点详情 → 未纳管 client 列表 → 点击 "wxid: Hard2BeSober" → "认领"对话框
   → 选已有面板用户 OR 创建新用户 → 提交

后端:
  1. 选已有用户:
     - 记录该用户对当前 client 的 ownership
     - 若 client 有 id/uuid，可记录为辅助键；SS/SS-2022 允许为空
  2. 新建用户:
     - source=local, username 自动建议（清洗 email），group=默认
     - 生成面板用户 uuid；认领动作本身不改 3X-UI client
  3. 写 user_xui_clients 表，匹配键为 (panel, inbound_id, client_email)
  4. 不调 3X-UI 任何写 API（无缝迁移）
  5. AuditLog
```

### 7.9 流量超限自动 disable

```
TrafficSvc cron 每 5min:
  1. 拉所有 3X-UI 面板的 client traffic
  2. 按 user_xui_clients 表反查 user_id，聚合该用户的 (up + down) 之和
  3. INSERT traffic_snapshots
  4. 算当前计费周期已用量:
       周期内已用 = users.lifetime_total_bytes - users.period_baseline_bytes (v3.0.0 O(1) 内存计算)
  5. 若 user.traffic_limit_bytes > 0 且 已用 > limit:
       - users.enabled = false
       - users.auto_disabled_reason = 'traffic_exceeded'
       - 对该用户所有 user_xui_clients 调 SyncSvc.UpdateClient(enable=false)
       - AuditLog
  6. 重置周期触发（每月 1 号 / 每季首日）:
       - period_start = now
       - 如果是 auto_disabled_reason='traffic_exceeded'，自动恢复:
           enabled = true, auto_disabled_reason = NULL
           对所有 user_xui_clients 调 UpdateClient(enable=true)
       - AuditLog
```

### 7.10 订阅请求

```
GET /sub/abc123 (UA: mihomo)

后端:
  1. 查 users WHERE sub_token=? → user
  2. 检查 user.enabled && now < user.expire_at
  3. 查 user.group_id → group (tag_filter + layout)
  4. 拉 nodes 表 + 应用 tag_filter → 过滤匹配节点
  5. RenderSvc:
     a. 加载默认模板（按 UA 或 ?client 参数选）
     b. 应用 group.layout:
        - 节点按 layout.sort 排序，未列入的按 default_sort_strategy
        - 在 layout.separators 指定位置插入分隔符
     c. 拉 3X-UI inbound 协议骨架，注入用户凭证:
        - VLESS / VMess: uuid = user.uuid
        - Trojan: password = user.uuid
        - SS legacy: password = user.uuid
        - SS-2022: password = base64(SHA-256(user.uuid))
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

### 8.1 公开端点

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/health` | 健康检查 |
| GET | `/{sub_path}/:token` | 订阅渲染（动态路径，默认 /sub/） |

### 8.2 认证端点 (`/api/auth`)

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/methods` | 返回可用的登录方式列表 |
| POST | `/local/login` | 本地账号登录 |
| GET | `/saml/login` | SAML 登录发起 |
| POST | `/saml/acs` | SAML 断言消费 |
| GET | `/saml/metadata` | SP metadata |
| GET | `/oidc/login` | OIDC 登录发起 |
| GET | `/oidc/callback` | OIDC 回调 |
| GET | `/sso-complete` | SSO 登录完成 |

### 8.3 用户自助端点 (`/api/user/me`)

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `` | 个人资料 |
| GET | `/traffic` | 流量报告 |
| GET | `/rules` | 个人规则 |
| PUT | `/rules` | 更新个人规则 |
| POST | `/emergency-access` | 紧急访问 |
| POST | `/reset-credentials` | 重置凭证 |
| POST | `/change-password` | 修改密码 |

### 8.4 管理端点 (`/api/admin`)

**用户管理：**

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/users` | 用户列表 |
| POST | `/users` | 创建用户 |
| GET | `/users/:id` | 用户详情 |
| PUT | `/users/:id` | 更新用户 |
| DELETE | `/users/:id` | 删除用户 |
| POST | `/users/:id/reset-credentials` | 重置凭证 |
| POST | `/users/:id/reset-emergency-usage` | 重置紧急访问计数 |
| POST | `/users/:id/set-enabled` | 启用/禁用用户 |
| GET | `/users/:id/rules` | 获取用户规则 |
| PUT | `/users/:id/rules` | 更新用户规则 |

**节点管理：**

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/nodes` | 节点列表 |
| POST | `/nodes` | 创建 inbound |
| GET | `/nodes/:id` | 节点详情 |
| POST | `/nodes/import` | 导入现有 inbound |
| PUT | `/nodes/:id/metadata` | 更新元数据 |
| PUT | `/nodes/:id/inbound` | 更新 inbound 配置 |
| POST | `/nodes/:id/set-enabled` | 启用/禁用节点 |
| DELETE | `/nodes/:id` | 删除节点 |
| GET | `/nodes/unmanaged` | 未纳管 inbound 列表 |
| POST | `/nodes/:id/claim` | 认领 client |
| POST | `/nodes/generate-reality-keypair` | 生成 Reality 密钥对 |

**分组管理：**

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/groups` | 分组列表 |
| POST | `/groups` | 创建分组 |
| GET | `/groups/:id` | 分组详情 |
| PUT | `/groups/:id` | 更新分组 |
| PUT | `/groups/:id/layout` | 更新分组布局 |
| DELETE | `/groups/:id` | 删除分组 |

**其他管理：**

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/rules` | 规则集列表 |
| GET/PUT/DELETE | `/rules/:slug` | 规则集 CRUD |
| GET | `/templates` | 模板列表 |
| GET/PUT/DELETE | `/templates/:slug` | 模板 CRUD |
| GET | `/servers` | 3X-UI 服务器列表 |
| POST/PUT/DELETE | `/servers/:id` | 服务器 CRUD |
| POST | `/servers/probe` | 测试连接 |
| GET | `/traffic/top` | 流量排行 |
| GET/PUT | `/traffic/user/:id` | 用户流量报告/设置 |
| GET | `/audit` | 审计日志 |
| DELETE | `/audit` | 清空审计日志 |
| GET | `/sub-logs` | 订阅日志 |
| DELETE | `/sub-logs` | 清空订阅日志 |
| POST | `/sub-logs/purge` | 按策略清理 |
| GET | `/sync-tasks` | 同步任务列表 |
| POST | `/sync-tasks/:id/retry` | 重试任务 |
| POST | `/sync-tasks/:id/cancel` | 取消任务 |
| POST | `/sync-tasks/purge` | 清理已完成 |
| POST | `/reconcile/run` | 手动对账 |

**系统设置：**

| 方法 | 路径 | 说明 |
|---|---|---|
| GET/PUT | `/settings/ui` | UI 设置 |
| GET/PUT | `/settings/mail` | 邮件设置 |
| PUT | `/settings/mail/templates/:kind` | 邮件模板 |
| POST | `/settings/mail/test` | 测试邮件 |
| POST | `/settings/mail/announcement` | 发送公告 |
| GET/PUT | `/settings/saml` | SAML 配置 |
| GET/PUT | `/settings/oidc` | OIDC 配置 |

---

## 9. 订阅渲染管线

### 8.1 模板占位符

| 占位符 | 展开内容 |
|---|---|
| `{{ proxies }}` | 用户授权节点的 Clash proxy block 列表 + group.layout 应用 |
| `{{ rules_common }}` | 当前模板 `rule_sets` 绑定的规则集内容拼接 |
| `{{ rules_personal }}` | user.personal_rules 原文 |
| `@all` (在 proxy-groups.proxies) | 该用户所有授权节点名（按 layout 顺序，含分隔符） |
| `@region:TW` | region=TW 的节点名 |
| `@tag:reality` | tags 含 reality 的节点名 |
| `@region:TW+tag:reality` | AND 组合 |

规则集内的 `proxy_group_order` 是 mihomo `proxy-groups` 和 sing-box selector outbounds 的默认展示顺序。模板负责声明使用哪些规则集；规则集负责声明策略组顺序和规则内容。

### 8.2 分组级 layout（v7 新）

替代了 v6 的"按 region 自动派生分隔符"。每个 group 可以独立配置：

- **节点排序**：拖拽 UI 调整权重
- **分隔符插入**：在排序后的节点序列任意位置插一行"占位节点"（127.0.0.1:1 假节点，仅用于 Clash UI 视觉分组）
- **分隔符文字**：完全自定义（"🇹🇼 高级线路" / "----- TW -----" / "TIER 1" 都行）

UI 实现：分组详情 → "渲染布局" tab → 左侧节点池（可拖入）+ 右侧"渲染结果"（可拖排序、可加分隔符行、可双击改分隔符文字）。

### 8.3 用户级凭证注入

| 协议 | 字段 | 值来源 |
|---|---|---|
| VLESS | uuid | user.uuid |
| VMess | uuid | user.uuid |
| Trojan | password | user.uuid |
| Shadowsocks (legacy) | password | user.uuid |
| Shadowsocks 2022 | password | `base64(SHA-256(user.uuid))` |

派生函数 `proxyPassword(user, protocol)` 在渲染时和 sync 推 3X-UI 时**一致使用**，保证两端值相同。

---

## 9. 3X-UI 集成

### 9.1 真实 API 清单

Base path: `/panel/api/inbounds` | 鉴权：`Authorization: Bearer <api-token>` 优先，session cookie 兜底。

**Inbound CRUD：**

| Method | Path | 用途 |
|---|---|---|
| GET | `/list` | 列出所有 inbound（含 `clientStats` 流量） |
| GET | `/get/:id` | 单 inbound 详情 + 客户端 + 流量 |
| POST | `/add` | 新建（body = 完整 Inbound 对象） |
| POST | `/update/:id` | 更新（body = 完整 Inbound 对象） |
| POST | `/del/:id` | 删除 |
| POST | `/setEnable/:id` | 切启用（body = `{enable: bool}`） |
| POST | `/import` | JSON 导入 |

**Client CRUD：**

| Method | Path | 用途 |
|---|---|---|
| POST | `/addClient` | 添加（body = 包含新 client 的 Inbound 对象） |
| POST | `/updateClient/:clientId` | 3X-UI 原生更新接口；面板封装不把它作为唯一更新路径 |
| POST | `/:id/delClient/:clientId` | 删除 |
| POST | `/:id/delClientByEmail/:email` | 按 email 删 |
| POST | `/:id/copyClients` | 批量复制 client 到另一 inbound |

**Traffic：**

| Method | Path | 用途 |
|---|---|---|
| GET | `/getClientTraffics/:email` | **按 email 跨 inbound 聚合** |
| GET | `/getClientTrafficsById/:id` | 某 inbound 全部 client 流量 |
| POST | `/:id/resetClientTraffic/:email` | 重置单 client |

**其他：**

| Method | Path | 用途 |
|---|---|---|
| GET | `/getClientLinks/:id/:email` | 单 client 连接 URL |
| POST | `/onlines` | 当前在线列表 |

### 9.2 Go 客户端封装

`internal/adapters/xui/client.go`：

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
func (c *Client) GetInbound(ctx, id) (*Inbound, error)        // 含 client list + stats
func (c *Client) AddInbound(ctx, spec InboundSpec) (int, error)
func (c *Client) UpdateInbound(ctx, id, spec InboundSpec) error
func (c *Client) DelInbound(ctx, id) error
func (c *Client) SetInboundEnable(ctx, id, enable bool) error

// client (注意 add/update 是"读-改-写"模式：内部先 GET inbound、改 settings.clients[]、再 POST)
func (c *Client) AddClient(ctx, inboundID, spec ClientSpec) error
func (c *Client) UpdateClient(ctx, inboundID, clientKey, spec ClientSpec) error
func (c *Client) DelClient(ctx, inboundID, clientID) error
func (c *Client) DelClientByEmail(ctx, inboundID, email) error
func (c *Client) CopyClients(ctx, srcInboundID, dstInboundID, emails []string) error

// traffic
func (c *Client) GetClientTraffic(ctx, email) (*ClientTraffic, error)  // 跨 inbound 聚合
func (c *Client) GetInboundTraffics(ctx, id) ([]ClientTraffic, error)
func (c *Client) ResetClientTraffic(ctx, inboundID, email) error

// session 管理（仅当 api_token 未配置时使用）
func (c *Client) Login(ctx) error
```

**关键实现细节：**

1. **鉴权优先级**：`api_token` 配置存在 → 直接走 `Authorization: Bearer`，**不需要 Login**；否则 fallback 走 cookie session（自动登录 + 过期重登）。
2. **add/update client 是读-改-写**：封装层不依赖 `updateClient/:uuid` 作为唯一更新路径，避免 SS/SS-2022 `client.id` 为空时失败。封装层内部：
   - `GET /get/:id` → 拿到当前 Inbound
   - 修改 `settings.clients[]`（JSON 字符串，需先 Unmarshal），按 `id` 或 `email` 定位现有 client
   - `POST /addClient` 或 `POST /update/:id` 回写完整 inbound
   - `UpdateInbound` 修改协议参数时会保留当前 `settings.clients[]`，避免覆盖已有 client
   - 加 retry：如果 update 期间被其他人改过（错误返回），重试一次
3. **错误码**：3X-UI 未认证返回 404（混淆探测），需要靠 response body 区分。
4. **每个 3X-UI 面板一个 Client 实例**，按 `panel_id` 路由（v3.0.0 前用 `panel_name`，但 panel pool 实际上一直按 ID 索引；DB 层的 panel_name 冗余列在 v3.0.0 已删，name 由 service 层在渲染时从 pool 现查）。

### 9.3 节点元数据存储

| 字段 | 存储位置 | 理由 |
|---|---|---|
| 协议、地址、端口、TLS/Reality 参数 | 3X-UI（面板调 API 读写） | 复用 3X-UI 真相能力 |
| display_name | 面板 `nodes.display_name` | 与 3X-UI remark 解耦 |
| region | 面板 `nodes.region` | 面板侧分类 |
| tags | 面板 `nodes.tags` | 灵活组合过滤（含 `server:xxx` 表达"同物理机"） |
| sort_order | 面板 `nodes.sort_order` | 全局默认排序，可被 group.layout 覆盖 |

**v6 的 `#region:` `#tag:` remark 约定取消**。所有展示元数据存面板 MySQL，不再依赖 3X-UI remark。

### 9.4 分层对账（详细）

目标：检测并修复"面板期望状态 vs 3X-UI 实际状态"的漂移；最坏漂移时间控制在分钟级而非小时级。

#### 9.4.0 对账分层

| 层 | 频率（默认） | 检查范围 | 触发 |
|---|---|---|---|
| **L1 写后即时验证** | 实时 | 仅被写的那一个 client 字段 | 每次 `SyncSvc.AddClient / UpdateClient / DelClient` 完成后立即 `GET /get/:inboundID` 校验生效 |
| **L2 流量采集顺便快查** | **5 min** | 检查项 1 (存在性) + 检查项 3 (enable) | TrafficSvc cron 本来就在拉 ListInbounds，顺便对比 |
| **L3 完整周期对账** | **15 min** | 全部 7 项检查 | 独立 cron |
| **L4 管理员手动** | 任意 | 同 L3 | dashboard "立即对账" 按钮 |

**最坏漂移修复时间：** 存在性 / enable → 5 min；uuid / password / 字段强制 / inbound 存在性 → 15 min；写操作问题 → 实时。

所有层共用同一套检查项实现（见 §9.4.2），只是触发频率和扫描范围不同。频率全部可在 `config.yaml` 里调。

#### 9.4.1 扫描范围

**仅遍历归属表 `user_xui_clients`**（L1 只扫被写的那条；L2/L3/L4 扫全部）。归属表外的 client（维护者私人 + 未纳管老朋友）**完全不感知、不读、不写**。

#### 9.4.2 检查项（按 user_xui_clients 逐条）

L2 只跑 #1、#3；L1/L3/L4 跑全部。

| # | 检查项 | 漂移场景 | 修复动作 |
|---|---|---|---|
| 1 | **client 存在性** | 面板有记录，3X-UI 该 inbound 内找不到该 email | `AddClient` 恢复 |
| 2 | **凭证一致性** | 面板 `user.uuid` / 派生 password ≠ 3X-UI 当前 client | `UpdateClient` 把协议凭证写回面板值；SS 空 id 时按 email 定位 |
| 3 | **enable 一致性** | 面板 `user.enabled` ≠ 3X-UI `client.enable` | `UpdateClient` 以面板为准；例外：3X-UI 主动 disable 因流量耗尽时不强制开启 |
| 4 | **多协议 password 一致性** | 派生公式 `proxyPassword(user, protocol)` ≠ 3X-UI 当前值 | `UpdateClient` 写回派生值 |
| 5 | **3X-UI 端字段强制** | `client.totalGB > 0` 或 `client.expiryTime` 不等于 `users.expire_at` 对应毫秒时间戳（永久为 0） | `UpdateClient(totalGB=0, expiryTime=expected)` |

#### 9.4.3 节点元数据对账（nodes 表）

| # | 检查项 | 漂移场景 | 修复动作 |
|---|---|---|---|
| 6 | **inbound 存在性** | 面板 `nodes` 表有记录，3X-UI 找不到对应 inbound | 标记 `nodes.enabled=false` + 写告警到 dashboard。**不删 nodes 行**（等管理员决定是不是真的删了 / 改了 ID） |
| 7 | **inbound 启用状态** | 面板 `nodes.enabled=true` 但 3X-UI `inbound.enable=false` | 不修复，只记录（3X-UI 是协议参数真相源，inbound 启用状态由那边决定）|

#### 9.4.4 输出

- 每条修复动作写 `audit_log`，`action='reconcile'`，含 before/after JSON
- 漂移摘要写 dashboard："今日检测到 N 项漂移，自动修复 K 项，M 项需人工介入"

#### 9.4.5 绝对不做的事 🚫

- ❌ **删除任何 3X-UI client**（即使归属表外的私人 client 也不动；即使归属表内"面板已删但 3X-UI 残留"也不在对账里删 —— 那种情况由 §7.X 的"延迟清理"独立机制处理 + 管理员确认）
- ❌ **修改 inbound 协议参数**（addr / port / TLS / Reality / settings 都是 3X-UI 单源真相）
- ❌ **新建 inbound**（管理员显式动作）
- ❌ **重命名 client.email**（即使发现命名不符合 §2.1 约定也不改 —— 老朋友导入时保留原 email 是有意为之）

### 9.5 现有 inbound 复用与共存机制 ⭐

面板默认假设 3X-UI 里**已经有现成的 inbound** 在跑（且内部混杂着维护者私人 client 和朋友 client）。"不冲突 + 复用现有 inbound" 靠下面三个机制：

#### 9.5.1 inbound 协议参数零变更

§7.5.1 导入路径**只写面板侧** `nodes` 表的展示元数据（display_name / region / tags / sort_order），**完全不调 3X-UI 任何写 API**。inbound 的协议、端口、TLS、Reality、`settings.clients[]` 全部保持原样。

维护者已经分发出去的旧 yaml 中朋友配置 → 继续生效，无需任何客户端侧改动。

#### 9.5.2 addClient 是"追加"语义，不是"替换"

3X-UI 的 `POST /addClient` body 是完整 Inbound 对象。封装层 `XUIClient.AddClient` 采用"读-改-写"，**只追加不删改**：

```
GET /get/:inbound_id        → 拿到完整 Inbound state（含全部现有 client）
settings.clients[].append(newClient)
POST /addClient             → 回写
```

所有现有 client（维护者私人 `Kazuha Home` / `Clash Private Subscribe`，以及尚未导入面板纳管的老朋友 `wxid: xxx`）**原封保留**。

#### 9.5.3 归属表是写护栏的最后防线

面板所有写 client 操作（add / del / update）入口都过 §4.3 的护栏：**目标 `(panel, inbound_id, email)` 必须在 user_xui_clients 表内**。

归属表只记录两种来源的条目：
- 路径 B 新建 inbound 后面板主动 sync 加的
- §7.8 显式认领（"导入已有 client"流程）加的

即使代码有 bug、即使内部 API 被恶意调用，都**无法**触达：
- 维护者私人 client（`Kazuha Home` 等）
- 老朋友但还没认领的 client（`wxid: xxx` 等）

#### 9.5.4 实例 walkthrough：在你现有的 inbound 1 上加新朋友

参照截图实际情况：inbound 1 (Vless+Reality:443) 当前有 4 个私人 client + 多个 `wxid:` / `@xxx` 老朋友 client。现在你通过面板创建 friend_a 并将其加入 group full（含 inbound 1）。

| 时刻 | inbound 1 `settings.clients[]` |
|---|---|
| t0 初始 | Kazuha Home, Clash Private Subscribe, Clash Friends Subscribe, Clash Temp Subscribe, wxid: Hard2BeSober, @bad502, ...（共 N 条） |
| t1 面板 `AddClient(friend_a)` | 上面 N 条 **+ `u{userID}-n{nodeID}@{domain}`**（末尾追加） |
| t2 friend_a 流量超限自动 disable | N 条不动；该纳管 email 标记 enable=false |
| t3 管理员从面板**删除** friend_a 用户 | N 条不动；该纳管 email 被 DelOwnedClient 移除 |
| t4 假想：代码 bug 误调 `DelClient(inbound=1, email="Kazuha Home")` | 护栏拦截，返回 `ErrClientNotOwnedByPanel`，**3X-UI 不动** |

→ 你已有的所有私人和老朋友 client 在 t0 → t4 全程**零变化**。

#### 9.5.5 老朋友的渐进迁移路径

不必一次性把所有老朋友迁到面板纳管。可以按以下节奏：

1. **第一阶段**：面板上线，**仅**纳管 1-2 个新朋友，老朋友继续靠手工 yaml 服务
2. **第二阶段**：通过 §7.8 "认领"流程，按需把老朋友逐个导入（不动 3X-UI，保留原 email/uuid 无缝迁移）
3. **第三阶段**：所有朋友都进面板后，老 yaml 文件归档

---

## 10. 流量统计与限额

### 10.1 采集

`TrafficSvc` 每 N 分钟（默认 5）：

1. 遍历所有面板用户：
   - 已知 `user.uuid` 派生出的 client.email 列表（用户在不同面板可能 email 后缀不同）
   - 调 3X-UI `GET /getClientTraffics/:email` —— **API 原生跨 inbound 聚合**，免去面板手动 sum
2. 拿到该用户的 (up, down) 总和
3. INSERT `traffic_snapshots`

⚠️ 多 3X-UI 面板场景：若一个面板用户跨多个 3X-UI 面板有归属 client，对每个 panel 各调一次 `getClientTraffics`，再面板侧 sum。单面板用户走快路径。

### 10.2 计费周期

| 配置 | 行为 |
|---|---|
| `traffic_reset_period = never` | 永久累计，从用户创建至今 |
| `traffic_reset_period = monthly` | 每月 1 号 00:00 归零 |
| `traffic_reset_period = quarterly` | 每季首日归零 |

实现：`users.traffic_period_start` 记录当前周期起点；`users.period_baseline_bytes` 在 period rollover 时 freeze 为当时的 `lifetime_total_bytes`。**v3.0.0 周期内已用 = lifetime_total_bytes - period_baseline_bytes**（O(1) 内存减法，零 DB 查询；通过 `domain.User.PeriodUsed()` 暴露）。v3.0.0 之前每次查询都跑 `LastBefore(period_start)` 随机点查 traffic_snapshots。

### 10.3 超限自动 disable

| 触发 | 动作 |
|---|---|
| 周期内已用 > traffic_limit_bytes | users.enabled=false + auto_disabled_reason='traffic_exceeded' + 对所有 user_xui_clients UpdateClient(enable=false) |
| 到达下一周期起点 | period_start 更新；若 auto_disabled_reason='traffic_exceeded' → 自动恢复 enabled=true + UpdateClient(enable=true) |
| 管理员手动改 traffic_limit_bytes 调高 | 立即重检查 → 若已不超限，自动恢复 |
| 管理员手动启用 | 清 auto_disabled_reason，UpdateClient(enable=true) |

**永远不删 client**，只是 disable。重新启用时无缝恢复。

### 10.4 看板聚合查询

| 指标 | 计算 |
|---|---|
| 永久用量 | 最新 total |
| 当前周期已用 | `lifetime_total_bytes - period_baseline_bytes`（v3.0.0 O(1)） |
| 今日 | 最新 total - 今日 00:00 之前最后一条 |
| 30 天曲线 | 按日取末次 snapshot，相邻 diff |

---

## 11. SSO（SAML 2.0）详细设计

### 11.1 配置（管理后台 + MySQL）

SAML 配置通过管理后台 `/api/admin/settings/saml` 读写，落到 `saml_settings` 单行表（v3.0.0 前叫 `saml_config`）；OIDC 同理落到 `oidc_settings`。`config.yaml` 只保留启动所需的监听、数据库、JWT 等最小配置。

| 配置 | 存储 | 说明 |
|---|---|---|
| SP entity_id / ACS URL / 证书私钥 | `saml_settings` | 用于生成 SP metadata、签名和验签 |
| IdP metadata URL / 刷新间隔 | `saml_settings` | 自动拉取并缓存 metadata |
| attribute_mapping | `saml_settings` JSON | UPN、email、display_name、groups claim 映射 |
| admin_group_ids / default_group_slug | `saml_settings` JSON / 字段 | 首次登录入组与管理员识别 |
| new_user_defaults | `saml_settings` JSON | 首次 SSO 用户默认流量、到期、重置周期 |

### 11.2 SP 实现

使用 [`crewjam/saml`](https://github.com/crewjam/saml) Go 库：
- SP middleware 提供 AuthnRequest 发起、ACS 处理、Logout
- metadata URL 自动拉取 + 缓存 + 后台刷新
- 内置 signature 验证、replay 防护、时间窗校验

### 11.3 SP metadata 暴露

面板自身的 SP metadata 通过 `GET /api/auth/saml/metadata` 暴露 XML，方便在 Entra ID 后台一键导入 SP 配置（无需手填 ACS URL / entity ID）。

### 11.4 用户首次登录的入库逻辑

```
SAML Response 验签通过 →
  upn = response.attr.upn
  user = SELECT * FROM users WHERE upn=? AND source='sso'
  if not user:
    user = CreateFromSSO(upn, display_name, groups)
  else:
    # 每次登录重算 role（防止 admin 权限被遗忘清除）
    user.role = admin if intersect(groups, admin_group_ids) else user
    update last_login_at
```

### 11.5 退化路径

| 故障 | 备用 |
|---|---|
| IdP metadata URL 拉不到 | 用上次缓存的 metadata；超过 7 天没刷新 → 报警 + 仍允许登录直到证书过期 |
| IdP 完全不可达 | 本地账号 fallback；UI 上"本地登录"按钮始终可见 |
| Entra ID 配置变更（证书轮换） | metadata 自动刷新 24h 一次；管理员可在 SSO 设置页点"立即刷新" |

---

## 12. 安全设计

| 风险 | 缓解 |
|---|---|
| 误操作私人 client | §4 归属表护栏 |
| 订阅 URL 泄漏 | sub_token 独立于密码；可重置 |
| 本地账号密码泄漏 | bcrypt cost=10；登录限流 10/min/IP |
| SAML Response 伪造 / 重放 | IdP 证书验签 + InResponseTo + NotBefore/NotOnOrAfter 校验 + ID 黑名单 |
| 3X-UI 凭证落地 | `xui_panels` 表敏感字段 AES-GCM 加密，key 从 env 读 |
| SAML SP 私钥泄漏 | `saml_settings.sp_key_pem` AES-GCM 加密；生产建议使用受控备份与最小权限访问 |
| 暴力扫 sub_token | /sub/:token 限流 60/min/IP；token 长 32B base64url |
| 内部越权 | RBAC + JWT claim 包含 role；admin API 中间件校验 |
| UUID 暴力枚举导致协议密码泄漏 | UUID v4 = 122 bit 随机；SS-2022 用 SHA-256 派生抹平结构 |
| 操作可追溯 | 所有写操作 AuditLog（含 actor/action/before/after/IP） |
| MySQL 暴露 | 监听 127.0.0.1 + 强密码；面板与 DB 同机部署 |

---

## 13. 技术栈

| 层 | 选型 | 备注 |
|---|---|---|
| 后端 | Go 1.22 + Gin + GORM | - |
| DB | MySQL 8.0 | utf8mb4 |
| MySQL 驱动 | `gorm.io/driver/mysql` | |
| SAML | `github.com/crewjam/saml` | 生产级 SAML 2.0 SP |
| YAML | `gopkg.in/yaml.v3` | |
| 密码 | `golang.org/x/crypto/bcrypt` | cost=10 |
| JWT | `github.com/golang-jwt/jwt/v5` | HS256 |
| AES-GCM | `crypto/aes` 标准库 | 凭证加密 |
| 前端 | React 18 + Vite + MUI (Material Design 3) + Zustand | v2.0 起从 Vue 3 + Element Plus 全量重写 |
| 国际化 | i18next + react-i18next | zh-CN / en-US |
| 图表 | ECharts（按需 import）| 流量趋势 |
| 前端打包 | `go:embed` 嵌到二进制 | 单二进制部署 |

---

## 14. 部署方案

### 14.1 依赖

- MySQL 8.0+ 或 SQLite（开发环境）
- 域名 + HTTPS 证书（SSO 要求）
- Linux 服务器（systemd）或 Docker 环境

### 14.2 二进制 + systemd

```
/opt/passwall-sub-panel/
├── psp                      # 二进制（含 internal/web/dist 嵌入）
├── config/
│   ├── config.yaml
│   ├── rulesets/*.yaml
│   └── templates/*.yaml
├── data/
│   └── panel.db             # SQLite 模式时使用
```

### 14.3 Docker

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
      PSP_JWT_SECRET: ${PSP_JWT_SECRET}
volumes:
  psp-data:
```

### 14.4 初次启动

启动后自动创建默认 admin 账号（admin/admin），首次登录后建议修改密码。

---

## 15. 决策记录

| # | 决策点 | 选定 | 状态 |
|---|---|---|---|
| 1 | 数据库 | MySQL 8.0 + SQLite 双驱动 | ✅ 已实现 |
| 2 | 前端框架 | React 18 + MUI + TypeScript（v2.0 起，原 v1 为 Vue 3 + Element Plus） | ✅ 已实现 |
| 3 | SSO 方案 | SAML + OIDC 双支持 | ✅ 已实现 |
| 4 | 同步策略 | 异步任务队列 (sync_tasks) | ✅ 已实现 |
| 5 | 用户标识 | UPN 统一标识（移除 username/source） | ✅ 已实现 |
| 6 | Email 约定 | `u{userID}-n{nodeID}@{domain}` 统一格式 | ✅ 已实现 |
| 7 | 订阅格式 | mihomo + sing-box | ✅ 已实现 |
| 8 | 配置存储 | MySQL（v3.0.0 后：KV `settings` 主表 + 单行 `mail_settings`/`saml_settings`/`oidc_settings`）+ YAML（规则集/模板/启动配置） | ✅ 已实现 |
| 1 | SS-2022 密码派生 | ✓ `base64(SHA-256(uuid))` 确定性派生 |
| 2 | 流量重置周期默认 | ✓ monthly |
| 9 | 节点粒度 | ✓ inbound 为最小粒度，不引入 server 抽象层；同物理机多协议用 `server:xxx` tag 关联 |
| 10 | 3X-UI 鉴权 | ✓ Bearer api_token 优先，username/password + cookie 兜底 |

待你拍板：

| # | 决策点 | 选项 | 倾向 |
|---|---|---|---|
| 3 | 新用户默认有效期 | (a) 永久 &nbsp; (b) 30 天 &nbsp; (c) 90 天 | 建议 (b) |
| 4 | 前端框架 | (a) 自搓 Vue 3 + Element Plus &nbsp; (b) 现成 admin 框架（vue-element-admin / Naive Admin） | **(a)** 灵活 |
| 5 | 3X-UI 同步策略 | (a) 改面板即推 + 每天对账 &nbsp; (b) 手动按钮 | **(a)** |
| 6 | 多管理员 RBAC | (a) 单超管 &nbsp; (b) admin role 多人，由 SAML group 决定 | **(b)** |
| 7 | 用户自助页 | (a) 有（到期/流量/改密/重置 token） &nbsp; (b) 无 | **(a)** |
| 8 | 分组成员减少时旧 client 处理 | (a) 立即 DelClient &nbsp; (b) 仅 disable &nbsp; (c) 保留不动 | **(a)** |

---

## 16. 版本升级政策

### 16.1 大版本升级路径

**不支持跨大版本跳级升级**。例如 v3.x → v5.x 必须先升级到 v4.x 再升级到 v5.x，每次只跨一个主版本（major）。

每个 major 版本的二进制只携带 **N-1 → N** 的迁移逻辑。例：

| 当前安装 | 目标 | 升级路径 |
|---|---|---|
| v2.5.x | v3.0.0 | 直接 `psp migrate`（v3.0.0 携带 ≤ v2.5.x → v3 迁移逻辑） |
| v2.5.x | v5.0.0 | v2.5.x → **v3 → v4 → v5**，三步，每步分别跑该版本的 `psp migrate` |
| v3.2.x | v4.0.0 | 直接 `psp migrate`（v4 携带 v3.x → v4 迁移逻辑） |
| v3.2.x | v6.0.0 | v3.2.x → v4 → v5 → v6，三步 |

minor / patch 内升级**不需要**跑 migrate（按 [[feedback_semver]] 规则 minor 只加功能、patch 只修 bug，DB schema 不变）。

### 16.2 设计依据

- **代码体积可控**：迁移代码一直累加会让二进制无限膨胀；只保留 N-1 让每次发版的迁移代码量稳定在数百行级别
- **测试矩阵可控**：迁移路径只有"上一个 major → 当前"一条，CI 验证成本线性 O(1)；如果支持任意跨版本，覆盖矩阵是 O(N²)
- **每次迁移都被充分实战检验**：用户被迫逐步升级 → 每条迁移路径都被大量真实部署跑过，长尾边界 case 在升级链条中被早发现
- **同行业惯例**：PostgreSQL（pg_upgrade 只支持 N-1）、MariaDB（major→major 必须逐级）、MongoDB（major version 必须线性）、Cloudreve 都是这个政策

### 16.3 实现位置

- 迁移逻辑作为主程序的 `migrate` 子命令：`psp migrate --src=<旧库> --dst=<新库>`
- 代码位置：[internal/migrate/](../internal/migrate/) —— `runner.go` 暴露 `Run([]string) int`，被 [cmd/panel/main.go](../cmd/panel/main.go) 在 `os.Args[1] == "migrate"` 时调用
- 运行时安全：normal panel 启动路径（`psp` 无参 / `psp --config=...`）**不会** import 也不会调用 migrate 包的任何 runtime 逻辑 —— 编译期 import 但运行期零开销
- 当下一个 major（vN+1）发版时：
  - vN+1 二进制**删除** vN-1 → vN 的迁移（即本仓库的 legacy_schema.go + migrate.go）
  - vN+1 二进制**新增** vN → vN+1 的迁移
  - 在 ARCHITECTURE.md 增补一节"vN → vN+1 schema 变更"

### 16.4 同 major 内部演进：cleanupLegacyState

某些"破坏性"改动**不需要**走完整的 `psp migrate` 流水线 —— 例如把 `nodes` 表里的某一类行抽出来放到新表、或者删除某个旧列。它们在同一个 major 内部发生（beta 期间尤其常见），目标是 admin 直接换二进制重启就完成迁移，不用手动跑命令。

这类一次性清理由 [internal/adapters/mysql/schema.go](../internal/adapters/mysql/schema.go) 的 **`cleanupLegacyState`** 函数承担，在 `EnsureSchema` 的 AutoMigrate 之后跑一次。

约束（必须严格遵守）：

1. **每段必须幂等**：跑 100 次结果一样（`DELETE WHERE ...` 没有副作用、`UPDATE WHERE ...` 写回的值已经是新值的话再写也无害）
2. **每段必须挂版本注释**：开头一行 `// v3.0.0-beta.7:` 之类的标签，便于 git blame 和后续回收
3. **严格 curated，绝不"自动 DROP 不认识的表"**：admin 可能自己建了分析视图 / 缓存表 / 第三方集成中间表，自动 DROP 等于销毁用户数据
4. **打 log**：每次实际命中了清理逻辑（有行被改/删）就 `log.Warn` 一条，让 admin 在 docker logs 里能看到"哦它确实清了什么"

**大版本节点回收规则**：

vN+1 发版时，**`cleanupLegacyState` 里所有 vN.x 标签的段全部删除**。理由：
- vN-1 → vN 升级路径强制走 `psp migrate`（[§16.1](#161-大版本升级路径) 政策）
- 所以任何到达 vN+1 的部署，必然先在 vN 的某个版本上跑过那些清理段（因为 `EnsureSchema` 每次启动都跑）
- vN+1 binary 不再会遇到任何 vN.x 之前的 legacy 状态 → 那些 cleanup 段是死代码，删

这条规则同时给了 `cleanupLegacyState` 一个**自然的尺寸上限**：最多累积一个 major 内的演进，约 10 段以内 / 100 行内。不会无限增长。

**记录每一段**：当你在 `cleanupLegacyState` 里加新段时，**同时在 [`docs/CHANGELOG.md`](CHANGELOG.md) 对应版本下记一笔"legacy cleanup: ..."**（人话描述清的是什么），这样回看历史不用读代码。

---

## 17. 实施路线图

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
- [x] 超限自动 disable
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

### M7 待开发

- [ ] 流量统计图表（ECharts）
- [ ] 流量历史查询 API
- [ ] 节点状态监控
- [ ] 多语言 i18n

---

## 18. Future Scope

### 18.1 Canvas LMS 联动

| 形式 | 方向 |
|---|---|
| LTI 1.3 工具 | 老师在 Canvas 课程嵌入"代理订阅"工具，学生点开自动 SSO 跳到面板 |
| 自动开户 | Canvas course enrollment webhook → 自动建用户 / 续期 |
| Canvas 用户身份联动 | Canvas SIS ID / login_id 映射到面板 user.upn |

当 Canvas 联动需求成熟后单独立项设计。**当前 MVP 不实现**，但架构上保留扩展点：
- AuthSvc 接口设计成 pluggable（SAML / OIDC / LTI 各自实现 Provider）
- 用户表 source 字段已支持 `'sso' | 'local'`，未来扩展 `'lti'`

### 18.2 其他候选

- 节点健康巡检 + 自动剔除
- 订阅 URL 二维码邮件 / Slack / Telegram 通知
- Web 端在线测试节点延迟

---

## 19. 术语表

| 术语 | 含义 |
|---|---|
| PSP | Passwall-Sub-Panel 缩写 |
| inbound | 3X-UI 的入站监听条目，对应一个代理节点 |
| client | 3X-UI inbound 内的用户条目（email + uuid 等） |
| sub_token | 订阅 URL 中的凭证段 |
| 归属表 | 面板用户在 3X-UI 里拥有的 client 列表（白名单） |
| 纳管 / 未纳管 | 是否在归属表内 |
| UPN | User Principal Name，Entra ID 用户唯一标识 |
| IdP | Identity Provider，身份提供方（本项目=Entra ID） |
| SP | Service Provider，服务提供方（本项目=面板自身） |
| ACS | Assertion Consumer Service，SP 接收 SAML Response 的端点 |
| SAML | Security Assertion Markup Language，XML-based SSO 协议 |
| Entra ID | Microsoft Entra ID（原 Azure Active Directory） |
| Reality | Xray 协议下的 TLS 伪装机制 |
| tag_filter | 分组按 region/tag 过滤节点的条件 |
| layout | 分组级渲染布局（排序 + 分隔符） |
