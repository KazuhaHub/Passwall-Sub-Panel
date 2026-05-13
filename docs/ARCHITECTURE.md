# Passwall-Sub-Panel 架构设计文档

| 字段 | 值 |
|---|---|
| 文档版本 | 0.2 (草案) |
| 架构迭代 | v7 |
| 最后更新 | 2026-05-11 |
| 状态 | 待最终拍板（见 §14 待决策事项） |

## 变更摘要 (v6 → v7)

| 维度 | v6 | v7 |
|---|---|---|
| 认证 | 本地账号 | **SAML SSO (Entra ID) 主 + 本地账号备** |
| 业务数据存储 | YAML 文件 | **MySQL**（用户/分组/归属表/节点元数据/流量） |
| 配置内容存储 | YAML | YAML（规则集/模板保持不变）|
| client.email 约定 | `psp_{username}` | **SSO 用户：UPN；本地用户：`local_{username}@psp.local`** |
| 节点管理 | 仅 3X-UI 后台 | **面板提供完整 inbound CRUD**（封装 3X-UI API） |
| 多协议凭证 | 各协议独立 | **统一从 UUID 派生**（含 Trojan / SS / SS-2022） |
| 流量限额 | 复用 3X-UI total_gb | **面板主动管理 + 超限自动 disable 全部归属 client** |
| 分组结构 | tag_filter 单一过滤 | **tag_filter + layout（排序权重 + 自定义分隔符占位）** |
| Canvas LMS 联动 | 未提及 | **标注 Future Scope** |

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
| **UPN (User Principal Name)** | Entra ID 用户的唯一标识，形如 `name@tenant.onmicrosoft.com` | Entra ID |
| **分组 (Group)** | 用户分组，含 tag_filter + layout | 面板 MySQL |
| **归属表 (Ownership)** | 每个用户在 3X-UI 里拥有的 client 白名单 | 面板 MySQL |
| **规则集 (Rule Set)** | Clash rules 分片 | 面板 YAML |
| **模板 (Template)** | Clash/Sing-box 配置框架 | 面板 YAML |
| **layout** | 分组级渲染布局（节点排序 + 分隔符占位） | 面板 MySQL |
| **IdP / SP** | 身份提供方 (Entra ID) / 服务提供方 (本面板) | - |
| **SAML metadata** | IdP 公布的 XML，定义证书、端点、claim 映射 | Entra ID |

### 2.1 唯一识别符体系

#### 标识符全景

| 层 | 标识符 | 类型 | 用途 |
|---|---|---|---|
| **面板用户** | `users.id` | INT auto | 面板内部主键 |
| | `users.username` | VARCHAR | 本地账号唯一名 |
| | `users.upn` | VARCHAR | SSO 用户唯一名 |
| | `users.uuid` | UUID v4 | **协议凭证**（VLESS/VMess 的 uuid；Trojan/SS 派生 password）|
| | `users.sub_token` | 32B base64url | 订阅 URL 凭证（独立于 uuid，可单独重置）|
| **3X-UI inbound** | `inbound.id` | INT | 3X-UI 内部主键 |
| | `(panel_name, inbound_id)` | 元组 | 跨多个 3X-UI 面板的全局引用 |
| **3X-UI client** | `client.id` / `uuid` | UUID 字符串 | 协议凭证 + `updateClient/:clientId` 路径键 |
| | `client.email` | VARCHAR | **跨 inbound 聚合流量的主键** |
| **面板 nodes** | `nodes.id` | INT auto | 内部主键 |
| | `(panel_name, inbound_id)` | 唯一索引 | 与 3X-UI inbound 1:1 映射 |
| **归属表 xui_clients** | `(panel_name, inbound_id, client_email)` | 唯一索引 | 面板用户 ←→ 3X-UI client 匹配 |

#### 关键设计：**email 是主匹配键，uuid 是辅助键**

| 跨系统操作 | 匹配键 | 原因 |
|---|---|---|
| 拉某用户跨所有 inbound 的流量 | **email** | 3X-UI `GET /getClientTraffics/:email` 原生跨 inbound 聚合 |
| 改某 inbound 内单个 client | uuid | 3X-UI `POST /updateClient/:clientId` 路径键是 uuid |
| 按邮箱删 client | email | 3X-UI `POST /:id/delClientByEmail/:email` |
| 在归属表反查"client 属于哪个面板用户" | (panel, inbound_id, email) | 三元组唯一索引；同一用户在所有 inbound email 相同 |

#### email 命名约定

| 用户来源 | email 格式 | 例 |
|---|---|---|
| 本地账号 | `local_{username}@psp.local` | `local_friend_a@psp.local` |
| SSO（Entra ID） | 直接用 UPN | `friend_a@tenant.onmicrosoft.com` |
| §7.8 历史导入 | **保留原 email** 不强制改名 | `wxid: Hard2BeSober` |

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
│            Vue 3 + Element Plus  管理后台 SPA             │
│                                                           │
│  /login          [SSO 登录] 或 [本地账号登录]              │
│  /admin/users           用户 CRUD + 流量/到期             │
│  /admin/nodes           节点 CRUD（封装 inbound API）     │
│  /admin/nodes/:id       节点详情 + client 列表 + 认领     │
│  /admin/groups          分组 CRUD + tag_filter + layout   │
│  /admin/rules           规则集 Monaco 编辑                 │
│  /admin/template        模板 Monaco 编辑 + 预览           │
│  /admin/traffic         流量看板                          │
│  /admin/panels          3X-UI 面板凭证                    │
│  /admin/sso             SAML 配置（IdP metadata URL）     │
│  /admin/audit           审计日志                          │
│  /user/me               用户自助                          │
└─────────────────────────────┬────────────────────────────┘
                              │ REST + JWT
                              ▼
┌──────────────────────────────────────────────────────────┐
│                   Go 后端 (Gin + RBAC)                   │
│                                                           │
│  HTTP 层：                                                 │
│  ├─ /api/auth/saml/login     SAML AuthnRequest 发起       │
│  ├─ /api/auth/saml/acs       SAML 回调（ACS endpoint）    │
│  ├─ /api/auth/saml/metadata  SP metadata (供 IdP 配置)    │
│  ├─ /api/auth/local/login    本地账号登录                  │
│  ├─ /api/admin/*             管理 API (role=admin)        │
│  ├─ /api/user/me/*           用户自助 API                  │
│  └─ /sub/:token              订阅渲染（公开，token 凭证）  │
│                                                           │
│  Service 层：                                              │
│  ├─ AuthSvc       SAML SP 实现 + 本地账号 + JWT 签发      │
│  ├─ UserSvc       用户 CRUD、改组联动 sync                 │
│  ├─ NodeSvc       inbound CRUD（封装 3X-UI API）          │
│  ├─ GroupSvc      分组 CRUD（含 layout）                   │
│  ├─ RuleSvc       规则集 CRUD                              │
│  ├─ TmplSvc       模板 CRUD + 预览渲染                     │
│  ├─ RenderSvc     订阅渲染                                  │
│  ├─ XUIClient     3X-UI HTTP API 封装                     │
│  ├─ SyncSvc       所有写 3X-UI 操作的统一入口 + 护栏       │
│  ├─ TrafficSvc    cron 拉取 + 快照 + 超限自动 disable     │
│  ├─ AuditSvc      所有写操作记审计                          │
│  └─ ReconcileSvc  周期对账                                  │
└──────┬─────────────────────────────────────┬────────────┘
       ▼                                     ▼
┌─────────────────────┐              ┌─────────────────────┐
│       MySQL         │              │    3X-UI HTTP API   │
│  users / groups     │              │   (N 个面板)         │
│  nodes / xui_clients│              │                     │
│  traffic_snapshots  │              └─────────────────────┘
│  audit_log          │                       ▲
│  sub_logs           │                       │
└─────────────────────┘                       │
       ▲                                      │
       │                                      │
┌──────┴───────────────────────────────────────┴──────────┐
│      文件系统 (config/)                                  │
│  config.yaml          主配置                              │
│  xui_panels.yaml      3X-UI 凭证（AES-GCM 加密字段）      │
│  saml.yaml            SAML IdP 配置                       │
│  rule_sets/*.yaml     规则集                              │
│  templates/*.yaml     模板                                │
└──────────────────────────────────────────────────────────┘
```

### 3.1 存储分层

| 数据 | 存储 | 理由 |
|---|---|---|
| users / groups / nodes / xui_clients / traffic / audit / sub_logs | **MySQL** | 频繁读写、需事务、需 JOIN |
| 规则集 / 模板内容 | **YAML 文件** | 长文本，配 Monaco 编辑器适合文件式存取 |
| 主配置 / 3X-UI 凭证 / SAML 配置 | **YAML 文件** | 启动时加载，少变更，含敏感字段加密 |

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

### 5.1 MySQL Schema

```sql
-- 用户
CREATE TABLE users (
  id              BIGINT AUTO_INCREMENT PRIMARY KEY,
  username        VARCHAR(64) UNIQUE NOT NULL,
  upn             VARCHAR(255) UNIQUE,        -- SSO 用户的 UPN；本地用户为 NULL
  source          ENUM('sso','local') NOT NULL,
  password_hash   VARCHAR(255),               -- bcrypt；本地账号才有
  role            ENUM('admin','user') NOT NULL DEFAULT 'user',
  sub_token       VARCHAR(64) UNIQUE NOT NULL,
  uuid            CHAR(36) NOT NULL,          -- v4
  group_id        BIGINT NOT NULL,
  enabled_rule_sets JSON,                     -- ["ad_block","ai",...]
  personal_rules  TEXT,
  expire_at       DATETIME,                   -- 面板侧维护
  traffic_limit_bytes BIGINT DEFAULT 0,       -- 0 = 不限
  traffic_reset_period ENUM('never','monthly','quarterly') DEFAULT 'never',
  traffic_period_start DATETIME,              -- 当前计费周期起点
  remark          VARCHAR(255),
  enabled         TINYINT(1) DEFAULT 1,
  auto_disabled_reason VARCHAR(64),           -- 'traffic_exceeded' / 'expired' / NULL
  created_at      DATETIME DEFAULT CURRENT_TIMESTAMP,
  updated_at      DATETIME DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  KEY idx_group (group_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

-- 分组
CREATE TABLE groups (
  id              BIGINT AUTO_INCREMENT PRIMARY KEY,
  slug            VARCHAR(64) UNIQUE NOT NULL,
  name            VARCHAR(128) NOT NULL,
  tag_filter      JSON NOT NULL,              -- "*" 或 ["region:TW","tag:reality"]
  layout          JSON,                       -- 见 §5.2
  remark          VARCHAR(255),
  created_at      DATETIME DEFAULT CURRENT_TIMESTAMP
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

-- 节点元数据（与 3X-UI inbound 1:1，存展示信息）
CREATE TABLE nodes (
  id              BIGINT AUTO_INCREMENT PRIMARY KEY,
  panel_name      VARCHAR(64) NOT NULL,
  inbound_id      INT NOT NULL,
  display_name    VARCHAR(255) NOT NULL,      -- 订阅里 proxies[].name 用这个
  region          VARCHAR(16) NOT NULL,       -- TW / US / CN / HK / JP / SG / ...
  tags            JSON,                       -- ["reality","global"]
  sort_order      INT DEFAULT 0,
  enabled         TINYINT(1) DEFAULT 1,
  created_at      DATETIME DEFAULT CURRENT_TIMESTAMP,
  UNIQUE KEY uk_panel_inbound (panel_name, inbound_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

-- 归属表
CREATE TABLE xui_clients (
  id              BIGINT AUTO_INCREMENT PRIMARY KEY,
  user_id         BIGINT NOT NULL,
  panel_name      VARCHAR(64) NOT NULL,
  inbound_id      INT NOT NULL,
  client_email    VARCHAR(255) NOT NULL,
  client_uuid     CHAR(36) NOT NULL,
  created_at      DATETIME DEFAULT CURRENT_TIMESTAMP,
  UNIQUE KEY uk_inbound_email (panel_name, inbound_id, client_email),
  KEY idx_user (user_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

-- 流量快照（5min 一条）
CREATE TABLE traffic_snapshots (
  id              BIGINT AUTO_INCREMENT PRIMARY KEY,
  user_id         BIGINT NOT NULL,
  up_bytes        BIGINT NOT NULL,
  down_bytes      BIGINT NOT NULL,
  total_bytes     BIGINT NOT NULL,
  captured_at     DATETIME NOT NULL,
  KEY idx_user_time (user_id, captured_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

-- 订阅访问日志
CREATE TABLE sub_logs (
  id              BIGINT AUTO_INCREMENT PRIMARY KEY,
  user_id         BIGINT,
  ip              VARCHAR(64),
  ua              VARCHAR(255),
  client_type     VARCHAR(32),
  accessed_at     DATETIME DEFAULT CURRENT_TIMESTAMP,
  KEY idx_user_time (user_id, accessed_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

-- 审计日志
CREATE TABLE audit_log (
  id              BIGINT AUTO_INCREMENT PRIMARY KEY,
  actor           VARCHAR(255) NOT NULL,
  action          VARCHAR(64) NOT NULL,
  target          VARCHAR(255),
  before_json     JSON,
  after_json      JSON,
  ip              VARCHAR(64),
  at              DATETIME DEFAULT CURRENT_TIMESTAMP,
  KEY idx_time (at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
```

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
| `config.yaml` | 监听端口、JWT 密钥、MySQL DSN、订阅基地址、cron 间隔 |
| `config/xui_panels.yaml` | 3X-UI 面板凭证（`api_token` 优先，`password` 兜底；敏感字段 AES-GCM 加密）|
| `config/saml.yaml` | IdP metadata URL、SP entity ID、admin group ID 列表、attribute 映射 |
| `config/rule_sets/*.yaml` | 规则集分片，每文件含 slug/name/sort/content |
| `config/templates/*.yaml` | Clash/Sing-box 模板 |

---

## 6. 后台页面 × 操作矩阵

### 6.1 管理员后台 (role=admin)

| 页面 | 列表显示 | 操作 |
|---|---|---|
| **用户管理** | 用户名 / 来源(SSO/本地) / 分组 / 到期 / 配额 / 已用 / 状态 | 新增（仅本地）、编辑、改组、改到期、改配额、改重置周期、重置 UUID、禁用、启用、重置 sub_token、重置密码（仅本地）、删除、批量延期、批量改组、导出 CSV |
| **节点管理** | 显示名 / 协议 / 端口 / region / tags / 启用 / client 数（纳/未纳） | **新增 inbound**（VLESS/VMess/Trojan/SS/SS-2022 协议表单 + Reality 参数）、编辑、改 region/tag/sort、启用/禁用、删除（受护栏保护）、从 3X-UI 导入未纳管 inbound |
| **节点详情** | 该 inbound 内全部 client（纳管/未纳管标记） | 认领未纳管 client、查看实时流量、踢出在线连接 |
| **分组管理** | 组名 / 节点数 / 用户数 | 新增、编辑（tag_filter）、**编辑 layout（拖拽节点排序 + 加分隔符占位）**、复制、删除（无成员才允许）|
| **规则集** | slug / 名称 / 行数 / 修改时间 | 新增、Monaco 编辑、排序、启用/禁用、删除 |
| **模板** | 名称 / 客户端类型 / 是否默认 | Monaco 编辑、**实时预览**（选用户预览）、切默认 |
| **3X-UI 面板** | name / URL / 状态 | 新增、编辑、测连接、删除 |
| **SSO 配置** | IdP entity ID / metadata URL / 同步状态 | 配 metadata URL（自动拉取 + 定期刷新）、配 admin group ID、配 attribute 映射、测试登录 |
| **流量看板** | 今日 / 本月总量 + Top 10 | 单用户折线图 30 天、按节点聚合、CSV 导出 |
| **审计日志** | 时间 / 操作者 / 动作 / 对象 / diff | 筛选、时间范围、导出 |
| **系统设置** | JWT 有效期 / 订阅基地址 / cron 间隔 | 编辑 |

### 6.2 用户自助页 (role=user)

| 内容 | 操作 |
|---|---|
| 到期倒计时 / 流量进度条 / 订阅 URL + 二维码 | 改密码（本地账号）、重置 sub_token、切换订阅格式、查看 30 天流量曲线 |

---

## 7. 关键数据流（核心场景演练）

### 7.1 SAML SSO 登录

```
朋友访问 /login → 点 "SSO 登录"
  ① 后端生成 SAML AuthnRequest，302 跳转 IdP
  ② IdP (Entra ID) 完成登录，POST SAML Response 到 /api/auth/saml/acs
  ③ 后端验签（用 IdP metadata 里的证书）
  ④ 解析 SAML attributes：
       upn       = "friend_a@tenant.onmicrosoft.com"
       groups    = ["group-uuid-1","group-uuid-2",...]
       email     = ...
       display_name = ...
  ⑤ 查 MySQL users WHERE upn=?:
       - 找到 → 更新 last_login、对照 SAML groups 重算 role
       - 未找到 → 自动建用户（source=sso, role=auto判定, group=默认组）
  ⑥ 比对 saml.yaml 里的 admin_group_ids，命中任意 → role=admin，否则 user
  ⑦ 签发 JWT (HS256, access 2h + refresh 7d)，HttpOnly cookie
  ⑧ 302 跳回 /admin 或 /user/me（按 role）

新用户首次 SSO 登录的同步:
  ① UserSvc.CreateFromSSO(upn, display_name, groups)
  ② 生成 uuid (v4)、sub_token (32B base64url)
  ③ 默认分到 group_id = config.default_group_id
  ④ 解析 default_group.tag_filter → 拉 inbound list → 过滤
  ⑤ 对每个匹配 inbound 调 SyncSvc.AddClient:
       email = upn                      ← 关键：直接用 UPN 作为 email
       uuid = user.uuid
       expiry_time = 0 (无限期，可后续在面板改)
       total_gb = 0 (无限，由面板 traffic_limit 强制)
  ⑥ 写 xui_clients 表
  ⑦ AuditLog
```

### 7.2 本地账号登录（备用）

```
朋友访问 /login → "本地账号" tab → 输入用户名密码
  ① 后端查 users WHERE source='local' AND username=?
  ② bcrypt 验证 password_hash
  ③ 签发 JWT
  ④ 跳转

注：本地账号仅供以下场景：
  - 初次启动建第一个 admin（环境变量或 CLI 工具）
  - SSO 故障 fallback
  - 不能用 Entra ID 登录的特殊用户
```

### 7.3 新建本地用户

```
UI: 用户管理 → 新增 → source=local, username, group, expire_at, traffic_limit_gb → 提交

后端:
  1. 校验 username 唯一
  2. 生成 uuid (v4)、sub_token、bcrypt(初始密码)
  3. 派生多协议密码:
       vless_uuid = user.uuid
       vmess_uuid = user.uuid
       trojan_pwd = user.uuid
       ss_legacy_pwd = user.uuid
       ss2022_psk = base64(SHA-256(user.uuid))  -- 32 字节 PSK
  4. 查 group.tag_filter → 拉 3X-UI inbound list → 过滤匹配
  5. 对每个匹配 inbound 调 SyncSvc.AddClient:
       email = "local_" + username + "@psp.local"
       根据 inbound 协议选 uuid / password 字段
       expiry_time = 0 (面板侧管)
       total_gb = 0 (面板侧管)
  6. 写 xui_clients 表
  7. 返回 {sub_url, initial_password}
```

### 7.4 重置用户 UUID

```
UI: 用户编辑 → 重置 UUID（带确认对话框，提示"所有协议密码会同步换，朋友现有客户端要重新拉订阅"）

后端:
  1. 生成新 uuid (v4)
  2. 派生新的各协议密码
  3. 对每个 xui_clients 条目 → SyncSvc.UpdateClient:
       新 uuid / 新 password
  4. 更新 users.uuid
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

后续：根据 group.tag_filter 自动补 client（见 §7.5.3）。
```

#### 7.5.3 给用户加权限到节点（A/B 路径都用同一流程）

```
触发：用户改组、分组扩容、新建用户。

SyncSvc.AddClient(user_id, node_id):
  1. ensureClientNotExists(user_id, panel, inbound_id, email)
  2. XUIClient.AddClient(inbound_id, ClientSpec{
       email = local_{username}@psp.local  或  UPN（SSO 用户）
       uuid = user.uuid
       password 派生:
         - VLESS / VMess: 用 uuid
         - Trojan: 用 uuid
         - SS legacy: 用 uuid
         - SS-2022: base64(SHA-256(uuid))
       enable = true
       expiry_time = 0  (面板侧维护)
       total_gb = 0     (面板侧维护)
     })
     封装层内部"读-改-写":
       a. GET /get/:inbound_id 读现有 Inbound 完整 state
       b. settings.clients[] **末尾追加**新 client
       c. POST /addClient 回写
  3. INSERT xui_clients (归属表)
  4. AuditLog

现有所有 client 原封不动。
```

### 7.6 节点编辑

```
UI: 节点详情 → 改 region 或协议参数

仅改 region/tags/sort → 只写 nodes 表，不动 3X-UI
改协议参数 → 调 XUIClient.UpdateInbound + 提示影响范围
```

### 7.7 节点删除

```
UI: 节点详情 → 删除按钮

后端:
  1. 调 XUIClient.GetInbound → 列所有 client
  2. 校验：所有 client 都在 xui_clients 表内？
     - 是 → 继续
     - 否 → 返回 409 + 未纳管 client 列表，UI 提示"请先处理未纳管 client"
  3. 对每个纳管 client → SyncSvc.DelClient + 删 xui_clients 条目
  4. 调 XUIClient.DelInbound
  5. 删 nodes 表条目
  6. AuditLog
```

### 7.8 导入已有 client

```
UI: 节点详情 → 未纳管 client 列表 → 点击 "wxid: Hard2BeSober" → "认领"对话框
   → 选已有面板用户 OR 创建新用户 → 提交

后端:
  1. 选已有用户:
     - 检查该用户的 uuid 是否与 client.uuid 一致
     - 不一致警告（继续会导致后续 sync 把 client 改成用户的 uuid）
  2. 新建用户:
     - source=local, username 自动建议（清洗 email），group=默认
     - 用户 uuid 直接采用 client.uuid（保持现有连接不中断）
  3. 写 xui_clients 表
  4. 不调 3X-UI 任何写 API（无缝迁移）
  5. AuditLog
```

### 7.9 流量超限自动 disable

```
TrafficSvc cron 每 5min:
  1. 拉所有 3X-UI 面板的 client traffic
  2. 按 xui_clients 表反查 user_id，聚合该用户的 (up + down) 之和
  3. INSERT traffic_snapshots
  4. 算当前计费周期已用量:
       周期内已用 = current_total - period_start_snapshot.total
  5. 若 user.traffic_limit_bytes > 0 且 已用 > limit:
       - users.enabled = false
       - users.auto_disabled_reason = 'traffic_exceeded'
       - 对该用户所有 xui_clients 调 SyncSvc.UpdateClient(enable=false)
       - AuditLog
  6. 重置周期触发（每月 1 号 / 每季首日）:
       - period_start = now
       - 如果是 auto_disabled_reason='traffic_exceeded'，自动恢复:
           enabled = true, auto_disabled_reason = NULL
           对所有 xui_clients 调 UpdateClient(enable=true)
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
     f. {{ rules_common }} 拼接启用的 rule_sets
     g. {{ rules_personal }} 插入 user.personal_rules
  6. 写 sub_logs
  7. 写 Subscription-Userinfo header（流量 + 到期 + 限额）
  8. 返回 yaml
```

---

## 8. 订阅渲染管线

### 8.1 模板占位符

| 占位符 | 展开内容 |
|---|---|
| `{{ proxies }}` | 用户授权节点的 Clash proxy block 列表 + group.layout 应用 |
| `{{ rules_common }}` | user.enabled_rule_sets 对应规则集按 sort 拼接 |
| `{{ rules_personal }}` | user.personal_rules 原文 |
| `@all` (在 proxy-groups.proxies) | 该用户所有授权节点名（按 layout 顺序，含分隔符） |
| `@region:TW` | region=TW 的节点名 |
| `@tag:reality` | tags 含 reality 的节点名 |
| `@region:TW+tag:reality` | AND 组合 |

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
| POST | `/updateClient/:clientId` | 更新（clientId 是 uuid） |
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

`internal/xui/client.go`：

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
func (c *Client) UpdateClient(ctx, inboundID, uuid, spec ClientSpec) error
func (c *Client) DelClient(ctx, inboundID, uuid) error
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
2. **add/update client 是读-改-写**：3X-UI 的 `/addClient` 和 `/updateClient` body 都是完整 Inbound 对象。封装层内部：
   - `GET /get/:id` → 拿到当前 Inbound
   - 修改 `settings.clients[]`（JSON 字符串，需先 Unmarshal）
   - `POST /addClient` 或 `/updateClient/:uuid` 回写
   - 加 retry：如果 update 期间被其他人改过（错误返回），重试一次
3. **错误码**：3X-UI 未认证返回 404（混淆探测），需要靠 response body 区分。
4. **每个 3X-UI 面板一个 Client 实例**，按 `panel_name` 路由。

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

**仅遍历归属表 `xui_clients`**（L1 只扫被写的那条；L2/L3/L4 扫全部）。归属表外的 client（维护者私人 + 未纳管老朋友）**完全不感知、不读、不写**。

#### 9.4.2 检查项（按 xui_clients 逐条）

L2 只跑 #1、#3；L1/L3/L4 跑全部。

| # | 检查项 | 漂移场景 | 修复动作 |
|---|---|---|---|
| 1 | **client 存在性** | 面板有记录，3X-UI 该 inbound 内找不到该 email | `AddClient` 恢复 |
| 2 | **uuid 一致性** | 面板 `user.uuid` ≠ 3X-UI `client.id` | `UpdateClient` 把 uuid 写回面板值 |
| 3 | **enable 一致性** | 面板 `user.enabled` ≠ 3X-UI `client.enable` | `UpdateClient` 以面板为准；例外：3X-UI 主动 disable 因流量耗尽时不强制开启 |
| 4 | **多协议 password 一致性** | 派生公式 `proxyPassword(user, protocol)` ≠ 3X-UI 当前值 | `UpdateClient` 写回派生值 |
| 5 | **3X-UI 端"不该设"字段** | `client.totalGB > 0` 或 `client.expiryTime > 0`（这俩面板自管，3X-UI 端应保持 0） | `UpdateClient(totalGB=0, expiryTime=0)` |

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

面板所有写 client 操作（add / del / update）入口都过 §4.3 的护栏：**目标 `(panel, inbound_id, email)` 必须在 xui_clients 表内**。

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
| t1 面板 `AddClient(friend_a)` | 上面 N 条 **+ `local_friend_a@psp.local`**（末尾追加） |
| t2 friend_a 流量超限自动 disable | N 条不动；`local_friend_a@psp.local` 标记 enable=false |
| t3 管理员从面板**删除** friend_a 用户 | N 条不动；`local_friend_a@psp.local` 被 DelClient 移除 |
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

实现：`users.traffic_period_start` 记录当前周期起点。周期内已用 = 最新 total - 周期起点最近 snapshot.total。

### 10.3 超限自动 disable

| 触发 | 动作 |
|---|---|
| 周期内已用 > traffic_limit_bytes | users.enabled=false + auto_disabled_reason='traffic_exceeded' + 对所有 xui_clients UpdateClient(enable=false) |
| 到达下一周期起点 | period_start 更新；若 auto_disabled_reason='traffic_exceeded' → 自动恢复 enabled=true + UpdateClient(enable=true) |
| 管理员手动改 traffic_limit_bytes 调高 | 立即重检查 → 若已不超限，自动恢复 |
| 管理员手动启用 | 清 auto_disabled_reason，UpdateClient(enable=true) |

**永远不删 client**，只是 disable。重新启用时无缝恢复。

### 10.4 看板聚合查询

| 指标 | 计算 |
|---|---|
| 永久用量 | 最新 total |
| 当前周期已用 | 最新 total - 周期起点 snapshot.total |
| 今日 | 最新 total - 今日 00:00 之前最后一条 |
| 30 天曲线 | 按日取末次 snapshot，相邻 diff |

---

## 11. SSO（SAML 2.0）详细设计

### 11.1 配置（`config/saml.yaml`）

```yaml
sp:
  entity_id: "https://sub.example.com/saml/metadata"
  acs_url: "https://sub.example.com/api/auth/saml/acs"
  # SP 证书与私钥（用于签名 AuthnRequest 和验签 Response）
  cert_pem_path: "./config/saml-sp.crt"
  key_pem_path: "./config/saml-sp.key"

idp:
  # 自动拉取 + 缓存 + 定期刷新（默认 24h）
  metadata_url: "https://login.microsoftonline.com/{tenant-id}/federationmetadata/2007-06/federationmetadata.xml"
  metadata_refresh_interval: "24h"

attribute_mapping:
  upn: "http://schemas.xmlsoap.org/ws/2005/05/identity/claims/upn"
  email: "http://schemas.xmlsoap.org/ws/2005/05/identity/claims/emailaddress"
  display_name: "http://schemas.xmlsoap.org/ws/2005/05/identity/claims/displayname"
  groups: "http://schemas.microsoft.com/ws/2008/06/identity/claims/groups"

# Entra ID 里的 Group Object ID，命中任一即视为面板管理员
admin_group_ids:
  - "00000000-0000-0000-0000-000000000001"
  - "00000000-0000-0000-0000-000000000002"

# 默认分组（首次 SSO 自动入此组）
default_group_slug: "default"

# 首次 SSO 用户的初始流量限额 / 到期日（可选）
new_user_defaults:
  traffic_limit_bytes: 107374182400   # 100GB，0 = 不限
  expire_days: 30                     # 30 天后到期，0 = 永久
  traffic_reset_period: "monthly"
```

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
| 本地账号密码泄漏 | bcrypt cost=10；登录限流 5/min/IP |
| SAML Response 伪造 / 重放 | IdP 证书验签 + InResponseTo + NotBefore/NotOnOrAfter 校验 + ID 黑名单 |
| 3X-UI 凭证落地 | xui_panels.yaml 的 password 字段 AES-GCM 加密，key 从 env 读 |
| SAML SP 私钥泄漏 | 私钥文件权限 0600；建议放 K8s secret / Vault |
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
| 前端 | Vue 3 + Vite + Element Plus + Pinia | |
| 代码编辑器 | Monaco Editor | 规则集/模板 |
| 前端打包 | `go:embed` 嵌到二进制 | 单二进制部署 |

---

## 14. 部署方案

### 14.1 依赖

- MySQL 8.0+ 实例（已存在，无需新部署）
- 域名 + HTTPS 证书（Entra ID 要求 ACS URL 必须 HTTPS）
- Linux 服务器（systemd）或 Docker 环境

### 14.2 二进制 + systemd

```
/opt/passwall-sub-panel/
├── panel                    # 二进制（含 web/dist）
├── config/
│   ├── config.yaml
│   ├── xui_panels.yaml
│   ├── saml.yaml
│   ├── saml-sp.crt + .key
│   ├── rule_sets/*.yaml
│   └── templates/*.yaml

环境变量:
  PSP_SECRET_KEY=<random-32-bytes>          # AES key
  PSP_MYSQL_DSN=user:pass@tcp(localhost:3306)/psp?parseTime=true&charset=utf8mb4

Nginx: https://sub.example.com → 127.0.0.1:8788
```

### 14.3 Docker

```yaml
services:
  panel:
    image: kazuha/passwall-sub-panel:latest
    ports: ["127.0.0.1:8788:8788"]
    volumes:
      - ./config:/app/config
    environment:
      - PSP_SECRET_KEY=${PSP_SECRET_KEY}
      - PSP_MYSQL_DSN=user:pass@tcp(host.docker.internal:3306)/psp?...
    restart: unless-stopped
```

### 14.4 初次启动 (Bootstrap)

```bash
# CLI 工具创建第一个 admin（本地账号）
./panel init-admin --username kazuha --password <生成的>

# 启动后通过 SSO 登录的用户若 group 命中 admin_group_ids 自动升为 admin
```

---

## 15. 决策记录

✓ 已决：

| # | 决策点 | 选定 |
|---|---|---|
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

## 16. 实施路线图

### M0 骨架（部分已完成）

- [x] go.mod / 项目目录 / 主入口
- [x] 配置加载
- [ ] 改用 MySQL（替换 SQLite）
- [ ] 改 model 适配 v7（含 SSO 字段、layout 字段等）

### M1 本地账号 + 基础订阅

- [ ] 本地账号登录 + JWT
- [ ] MySQL 迁移脚本
- [ ] 3X-UI Client 封装
- [ ] SyncSvc + 归属表护栏
- [ ] 用户 CRUD API + UUID 派生多协议密码
- [ ] 订阅渲染器（不含 layout，先用统一默认）
- [ ] 极简前端：登录 + 用户列表 + 新增

### M2 SSO + 节点管理

- [ ] SAML SP 集成（crewjam/saml）
- [ ] IdP metadata 自动拉取 + 刷新
- [ ] role 由 SAML group 决定
- [ ] 节点 CRUD（含 inbound 协议表单）
- [ ] 节点详情页 + 未纳管 client 认领

### M3 分组 + 高级渲染

- [ ] 分组 CRUD
- [ ] tag_filter 解析
- [ ] **layout 编辑器（拖拽节点 + 加分隔符）**
- [ ] 规则集 / 模板 Monaco 编辑
- [ ] 用户自助页

### M4 流量与可观测

- [ ] TrafficSvc + 周期重置
- [ ] 超限自动 disable
- [ ] 流量看板（曲线 + 排行）
- [ ] AuditSvc + 审计页
- [ ] ReconcileSvc + 对账报告

### M5 部署与硬化

- [ ] Dockerfile + docker-compose
- [ ] systemd unit + Nginx 反代样例
- [ ] 凭证 AES-GCM 加密
- [ ] 限流 + CORS 收紧

### M6 (可选)

- [ ] Sing-box / v2rayN 订阅格式
- [ ] 流量历史曲线扩展
- [ ] CSV 导出
- [ ] 多管理员细粒度权限

---

## 17. Future Scope

### 17.1 Canvas LMS 联动

| 形式 | 方向 |
|---|---|
| LTI 1.3 工具 | 老师在 Canvas 课程嵌入"代理订阅"工具，学生点开自动 SSO 跳到面板 |
| 自动开户 | Canvas course enrollment webhook → 自动建用户 / 续期 |
| Canvas 用户身份联动 | Canvas SIS ID / login_id 映射到面板 user.upn |

当 Canvas 联动需求成熟后单独立项设计。**当前 MVP 不实现**，但架构上保留扩展点：
- AuthSvc 接口设计成 pluggable（SAML / OIDC / LTI 各自实现 Provider）
- 用户表 source 字段已支持 `'sso' | 'local'`，未来扩展 `'lti'`

### 17.2 其他候选

- 节点健康巡检 + 自动剔除
- 订阅 URL 二维码邮件 / Slack / Telegram 通知
- Web 端在线测试节点延迟

---

## 18. 术语表

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
