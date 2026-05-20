# Changelog

Format inspired by [Keep a Changelog](https://keepachangelog.com/en/1.1.0/);
semver per `feedback_semver` (major = refactor, minor = feature, patch = fix +
small improvement).

## v3.2.1-beta.1 — 2026-05-20

### Added
- 用户编辑界面重做为 Cloudreve 风格的左右双栏：左栏聚合身份信息（头像 / 角色 /
  SSO / 状态徽章、ID/UUID、流量用量条 + 本周期已用 / 上限 / Lifetime 总量及上下行
  明细、创建时间、复制订阅），右栏为可编辑表单网格。用户列表 DTO 新增
  `lifetime_{up,down,total}_bytes`（只读，永不被周期重置清零）。

### Fixed
- 流量上限与紧急访问每窗口配额现在接受小数 GB（>=0，如 0.3）。此前后端
  `TrafficLimitGB` / `EmergencyAccessQuotaGB` 是整型，提交 0.3 之类的值会被
  JSON 解析直接拒掉；改为 `float64` 并在转字节时 `int64()` 截断，KV 设置用
  `floatField` 序列化，前端数字输入框 `step="any"` 且校验放宽为仅「>=0」。

## v3.2.0 — 2026-05-20

正式版。汇总 v3.2.0-beta.1 → rc.2 的全部改动（PostgreSQL 支持、到期日按面板
时区锚定、未纳管标签页按服务器查询、Shadowsocks 订阅 SIP022 修正、VLESS flow
统一、Hysteria2 多用户同步、依赖 CVE 升级等），并在 rc.2 之后补入下列收尾修复。
完整逐项见下方各 pre-release 段落。

### Security
- operator 角色的前端按钮门控收口：operator 能进的页面（节点 / 分组 / 规则库 /
  配置方案 / 日志 / 同步任务）上，admin-only 的写操作按钮（新建 / 编辑 / 删除 /
  导入 / 认领 / 清空 / 清理保留期 / 清理任务）以及对 admin/operator 账户的删除 /
  启停 / 重置 / 解绑等，现在对 operator **直接隐藏**，不再"有按钮、一点报错"。
  保留 operator 实际有权的操作（普通用户 CRUD、流量、节点启停、任务重试/取消）。
  新增前端能力层 `web-react/src/utils/permissions.ts`（`useCan(capability)`）作为
  自定义角色 / 细粒度权限的预留扩展点；详见 docs/ARCHITECTURE.md §6.3。

### Fixed
- TLS `allowInsecure` 现在会渲染进客户端配置：Clash 的 `skip-cert-verify`、
  sing-box 的 `insecure`、URI 的 `allowInsecure=1`，Hysteria2 也接上。此前面板
  能在创建节点时勾选 allowInsecure，但订阅完全不下发，自签证书的 TLS 节点客户端
  会因校验失败而连不上。
- 客户端删除统一走 `delClientByEmail`：3X-UI 的 `delClient/:id` 只认 UUID /
  password（VLESS/VMess 按 UUID、Trojan 按 password），不认 Shadowsocks(email)
  / Hysteria2(auth)，导致 SS 客户端按存储的 UUID 删除时报 “Client Not Found In
  Inbound For ID”、用户重同步的 DEL 任务无限重试。改为始终按 email 删除，对所有
  协议生效（取代 rc.1 的 by-id+回退方案）。
- 修正 `copyClients` 的请求字段：3X-UI 读 `clientEmails`，面板此前发的是
  `emails`（被忽略），会把"选择性复制"静默变成"复制全部"。

## v3.2.0-rc.2 — 2026-05-20

### Fixed
- Hysteria2 多用户同步打通：3X-UI 按 `auth` 字段识别 Hysteria2 客户端（auth 即
  其「client id」，为空会被拒「empty client ID」），但面板此前的 `ClientSpec` /
  `buildClientSpec` / `buildClientJSON` 完全没有 `auth`，同步出去的 Hysteria2
  客户端没有凭证、3X-UI 拒收或无法认证。现在 `ClientSpec` 新增 `Auth`，
  Hysteria2 客户端的 `auth` 设为用户 UUID（与订阅渲染用的 HY2 密码一致），
  序列化与回读都带上。VLESS/VMess/Trojan/SS 不受影响；删除走 email 路径已覆盖。

## v3.2.0-rc.1 — 2026-05-20

### Fixed
- 同步删除（Shadowsocks）：删 SS 客户端时按 settings 的 `id`(UUID) 调
  `delClient` 会被 3X-UI 拒为 “Client Not Found In Inbound For ID”，导致用户
  重同步的 DEL 任务无限重试（观察到 131 次）。现在 id 删失败后回退到
  `delClientByEmail`（3X-UI 跨协议稳定的删除键），删成功即移除归属、任务正常
  完成。VLESS / VMess / Trojan 的按-id 删除原本就有效，不受影响。
- 用户门户「本周期用量」旁的重置周期显示成原始 key `reset_period.monthly`：
  MeView 漏了 `profile.` 命名空间前缀，已修正。
- 创建 / 续期用户的到期口径与编辑统一：创建表单改为日期选择器（发
  `expire_date`，后端按面板时区 end-of-day 解析）；续期发的裸 `expire_at` 也
  锚定到面板时区当天结束。此前创建 / 续期按“现在 + N 天”的浏览器钟点存，跨
  时区会与编辑显示差一天、且在当天钟点而非当天结束过期。
- PostgreSQL 列表 / 图表排序补确定性 tiebreaker（`id`）：审计 / 订阅访问 /
  已发邮件分页此前按非唯一时间戳排序，PG 上同值行跨页可能重复或漏；流量图表
  分桶取“桶内最后一行”在 tie 时也不确定。
- SQLite 连接池上限改为 1（写串行化），消除高写竞争下的 “database is locked”；
  MySQL / Postgres 保持原有连接池。

### Security
- 升级存在可达 CVE 的依赖：`golang-jwt/jwt` v5.2.2（JWT header 解析内存放大
  DoS）、`golang.org/x/net` v0.53.0、`russellhaering/goxmldsig` v1.6.0。
  govulncheck 复查 0 命中。

### Internal
- 清理 staticcheck（U1000）标记的未使用代码：多处 `toDomain`、legacy 行类型、
  `settingsClients`、`panelName`、`isInboundNotFoundErr`、`activeLoginMode`。

## v3.2.0-beta.2 — 2026-05-19

### Fixed
- 节点「未纳管」标签页的服务器选择标签 / 占位、空状态提示、加载失败提示在
  英文界面下显示为中文：相关 i18n 键此前 zh-CN / en-US 都缺失，退回了中文
  兜底。已补齐两个 locale。

## v3.2.0-beta.1 — 2026-05-19

### Added
- **PostgreSQL 支持**：除默认 SQLite / MySQL 外新增 PostgreSQL（pgx 驱动）。
  用 `postgres:` 块的离散字段（host/port/user/password/database/sslmode）配置，
  或在 `mysql.dsn` 填 `postgres://` URL；环境变量 `PSP_POSTGRES_DSN` 可覆盖。
  建表仍由 GORM AutoMigrate 按方言生成，无需迁移脚本。
- 当浏览器时区 ≠ 面板时区时，管理端 / 用户端在到期日处显示小提示，说明该
  日期以哪个时区为准，消除歧义。

### Changed
- 节点「未纳管」标签页改为先选服务器再查询，仅请求所选面板的 inbound（一次
  `ListInbounds`），不再每次加载同步扫描全部 3X-UI 面板；某个面板慢或不可达
  时错误只限于所选服务器并提供重试，进入标签页前不再访问任何面板。

### Fixed
- 管理员用户搜索改为大小写不敏感（`LOWER(col) LIKE`）。此前 PostgreSQL 的
  `LIKE` 大小写敏感，搜 “john” 匹配不到 “John”；SQLite / MySQL 不受影响。
- 管理端设置 / 显示的到期日改为**按面板时区**解释与渲染。此前编辑弹窗用
  `new Date("YYYY-MM-DD")`（按 UTC 解析）再 setHours 本地小时，UTC 以西的
  时区会让到期早一天。用户端到期仍按浏览器本地时区显示（设计如此）。
- 审计 `before_json` / `after_json` 列由 `json` 改为 `text`：审计会写入空
  字符串（新建无 before 状态），而 PostgreSQL 的 json 列拒绝空串。

## v3.1.1-rc.3 — 2026-05-19

### Fixed
- 编辑节点弹窗的 `Flow` 字段现在仅对 VLESS 节点显示（此前对 SS / VMess /
  Trojan / Hysteria2 也显示）。为支持这点，节点新增缓存 `protocol`（schema
  加列，AutoMigrate 自动添加、无需 backfill）：import / create 时写入，编辑
  inbound 时回填，列表 / 详情 API 带出。已有旧节点 protocol 为空时按「未知」
  处理、仍显示 Flow，下次重新 import 或编辑 inbound 会自愈。

## v3.1.1-rc.2 — 2026-05-19

### Added
- 新建节点 / 导入 inbound 弹窗的 `Address` 字段现在按所属 3X-UI 服务器的
  URL 主机名预填（仅取 hostname，丢弃 scheme / 管理端口 / 路径）。这是
  可编辑的默认值——切换服务器时若地址未被手动改过会跟着更新，手动改过则
  保留。

### Fixed
- 订阅 URI 列表里 SS-2022（`2022-blake3-*`）的 `ss://` 链接拼接修正为
  SIP022 形式 `ss://method:serverPSK:userPSK@host:port`，PSK 内的 base64
  特殊字符（`+ / =`）走 percent-encoding；不再把整段 userinfo 用标准
  base64 包装。旧拼法会让 sing-box / shadowsocks-rust / Shadowrocket 无法
  解析 2022 节点。普通 SS 仍保持 SIP002 的 base64url userinfo。
- 统一 VLESS `flow` 的渲染：Clash / URI / sing-box 三种订阅一律按节点存储的
  flow 原样输出，空就留空。此前 Clash / URI 在 REALITY 且 flow 为空时会擅自
  补 `xtls-rprx-vision`，与显式选"无"、ws/grpc 传输或纯 reality 服务端冲突，
  且和 sing-box（一直按原值）行为不一致。

### Changed
- 导入 inbound 弹窗：`Flow` 选择器仅在源 inbound 为 VLESS 时显示，
  SS / VMess / Trojan / Hysteria2 不再出现该字段，提交时也不会为非 VLESS
  协议写入 flow。

### Maintenance
- `go mod tidy`：`coreos/go-oidc/v3`、`golang.org/x/oauth2` 从 `// indirect`
  归类为直接依赖（它们被 OIDC 登录代码直接 import），消除 go.mod 过期标记。

## v3.0.0 — 2026-05-18

正式版。基于一系列 V3 发布前的代码审查（后端 / 安全 / 前端 / DB / 构建 /
测试），完成了进程稳定性、HTTP 基础安全、数据完整性和发布卫生四个方向的硬化。

### Breaking
- 数据库 schema 重构（KV `settings` / `xui_clients` → `user_xui_clients` /
  retention 字段重组）。详见 [docs/UPGRADE-v3.0.0.md](docs/UPGRADE-v3.0.0.md)。
  老库由 `psp migrate` 子命令旁车迁移；主程序不会原地升级。
- 移除根目录历史构建残留（`migrate-db-v2.exe`、`reset-admin-password.exe`、
  `material-demo.html`、`user_*.yaml`），构建产物统一在 `local-build/`。

### Security
- HTTP 全局加入 HSTS / X-Frame-Options / X-Content-Type-Options /
  Referrer-Policy / 最小 CSP。
- `http.Server` 加 `ReadTimeout`、`WriteTimeout`、`IdleTimeout`、
  `MaxHeaderBytes`；请求体硬限 1 MiB，避免 audit 中间件的 `io.ReadAll` 被
  恶意大 body 打 OOM。
- SSO 注销时清 cookie 的 `secure` 标志与设置时一致 — 修复 HTTPS 部署下旧
  cookie 不被实际删除导致 access token 仍在 TTL 内可用的问题。
- JWT 加入 `tv` token_version claim；管理员停用 / 改角色 / 重置密码 /
  用户自助改密都会 bump 版本号，立即吊销旧 token。
- SAML Assertion 重放缓存：同一个 Assertion ID 在 `NotOnOrAfter` 窗口内
  只接受一次。
- SAML IdP metadata 获取加 15 s 超时 + 4 MiB body 上限，挡掉 SSRF/DoS。
- `isHTTPS()` 只在请求来自受信代理时才信任 `X-Forwarded-Proto` /
  `X-Forwarded-Ssl`，避免攻击者通过伪造头降级 cookie 安全标志。
- 启动时审计 `xui_panels`、`saml_settings`、`oidc_settings`、`mail_settings`
  里的密文列；存在明文行时 WARN 提示设 `PSP_SECRET_KEY_MATERIAL` 并在 UI
  重新保存。

### Reliability
- 新增 `internal/pkg/safego`：所有后台 goroutine（traffic / reconcile /
  health / mail / sync / audit cleanup / saml metadata refresh / 异步邮件 /
  扇出 worker）全部包 recover。任一 panic 不再打死整个面板。
- `App.Shutdown` 用 `sync.WaitGroup` 等后台 worker 退出，按 caller 设定的
  deadline 限时。
- mailer SMTP 拨号 ctx 超时时启动 reaper goroutine 收尾，不再泄漏 TCP/TLS
  连接。
- handler 异步邮件改用 `AsyncDispatcher` 接口，在面板 bg 上下文派生 —
  停服时干净退出，不再悬挂在 `context.Background()` 上。
- `render` 订阅渲染做 inbound 批量预取（每个 3X-UI 面板一次
  `ListInbounds`），消除原先每节点单独 `GetInbound` 的 N+1。
- `emergencyMu` 现在也覆盖 traffic poll 写入 `EmergencyUntil` /
  `EmergencyBaselineBytes` 的路径，封堵与 `UseEmergencyAccess` 的竞态。

### Data integrity
- 新增 `sub_logs` 自动 prune cron（接 audit cleanup loop），不再无界增长。
- `XUIPanelRepo.Delete` 加级联保护：仍有 nodes / user_xui_clients 引用时
  拒绝删除，避免幽灵外键。

### Frontend
- axios 401 拦截器接入 `/auth/refresh` 单飞重试，access TTL 过期不再粗暴
  踢回登录页。
- axios 错误差异化：网络异常 / 超时 / 5xx / 429 / 4xx 文案分离；同一错误
  在 1.5 s 内去重，避免 `Promise.allSettled` 批量失败刷屏 toast。
- 剪贴板新增 `document.execCommand` 回退路径，HTTP 部署也能复制订阅 URL；
  失败时 warning toast。
- `useAuthStore` 加 `hasToken` 字段 + `storage` 事件监听：多 tab 登出
  会同步触发其他 tab 的路由守卫重新评估。
- i18n：补齐 `nodes.field.{enabled,separator_*}` 等 4 个 zh-CN 缺失 key。
- 新增 `common.errors.{network,timeout,server,rate_limited,copy_*}` 文案。

### Operations
- 新增 `internal/version`；启动日志、`psp version` 子命令、`/api/version`
  端点共用 build identity。CI 用 `-ldflags="-X .../version.Version=..."`
  注入 release tag。
- systemd unit：`ReadWritePaths` 补 `/opt/psp/config`（修首启写不进
  config.yaml 的 P0），加 `StandardOutput=journal`、`MemoryMax`、
  `SystemCallFilter`、`RestrictAddressFamilies` 等硬化项；新增
  `deploy/systemd/env.example`。
- backend `POST /api/auth/refresh` 端点（与登录共用 PerIP 限流）。
