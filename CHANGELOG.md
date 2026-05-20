# Changelog

Format inspired by [Keep a Changelog](https://keepachangelog.com/en/1.1.0/);
semver per `feedback_semver` (major = refactor, minor = feature, patch = fix +
small improvement).

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
