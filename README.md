<p align="center">
  <img src="web-react/public/images/logo+title-circle.png" alt="Passwall Sub Panel" width="200">
</p>

<h1 align="center">Passwall Sub Panel</h1>

<p align="center">
  一个轻量级的代理订阅管理面板，专为小型团队和朋友圈设计
</p>

<p align="center">
  <a href="#功能特性">功能特性</a> •
  <a href="#快速开始">快速开始</a> •
  <a href="#二进制部署">二进制部署</a> •
  <a href="#docker-compose-部署">Docker Compose 部署</a> •
  <a href="#配置">配置</a> •
  <a href="#api">API</a>
</p>

<p align="center">
  <a href="README_EN.md">English</a>
</p>

---

## 简介

Passwall Sub Panel 是一个基于 Go + React 的代理订阅管理系统，通过与 [3X-UI](https://github.com/MHSanaei/3x-ui) 面板集成，提供完整的用户管理、订阅生成、流量监控等功能。

**适用场景**：小型团队、朋友圈、个人使用，不是企业级机场系统。

**部署形态**：单文件 Go 二进制（前端 SPA 通过 `go:embed` 嵌入），可直接 `./psp` 启动，也提供 Docker 镜像。

## 功能特性

### 核心功能
- **订阅管理** — 动态生成 Clash/Mihomo、Sing-box、V2rayN URI list 配置
- **用户管理** — 用户 CRUD、分组管理（tag filter + 渲染布局）、到期时间、流量限额
- **节点管理** — 通过面板封装管理 3X-UI inbound，支持多面板，新建/导入双流程
- **客户端检测** — UA 自动识别客户端类型，支持白名单/黑名单
- **自动停用** — 多次使用禁用客户端后自动停用账号；超量/到期自动同步 disable

### 认证方式
- **本地账号** — UPN/密码登录，bcrypt 哈希
- **SAML 2.0 SSO** — 支持 Entra ID 等 SAML IdP
- **OIDC SSO** — 支持 OpenID Connect

### 邮件通知
- 到期前提醒、到期通知
- 流量低剩余提醒
- 账号停用 / 恢复通知
- 失败自动重试（指数退避）

### 其他
- 流量统计与历史曲线（用户级 + 节点级）
- 审计日志、订阅访问日志
- 同步任务队列（异步可重试的 3X-UI 写操作）
- 多语言（中文 / 英文）、暗色/亮色主题
- RBAC：admin / operator / user 三角色

## 快速开始

### 环境要求

- **运行时**：Linux（推荐）或任何支持 Go 的系统
- **数据库**：内置 SQLite（默认，零配置）或 MySQL 8.0+
- **3X-UI**：已部署且可访问，准备好 API token 或管理员账号
- **构建时**（如果从源码构建）：Go 1.26+、Node.js 20+

### 从源码构建

```bash
# 克隆项目
git clone https://github.com/KazuhaHub/Passwall-Sub-Panel.git
cd Passwall-Sub-Panel

# 构建前端（输出到 internal/web/dist，会被 go:embed 嵌入）
cd web-react
npm install
npm run build
cd ..

# 构建后端（-s -w 去掉符号表与调试信息，二进制可瘦 ~25%）
go build -ldflags="-s -w" -o psp ./cmd/panel

# 运行（首次启动会生成 config.yaml 模板）
./psp
```

构建完成的 `psp` 是一个**自包含**的单二进制，不依赖任何外部文件即可启动（前端、SQLite 驱动都在里面）。

## 二进制部署

适合：单机自部署，希望最简单可靠。

### 1. 准备目录与二进制

```bash
# 假设以 root 操作
useradd -r -s /usr/sbin/nologin psp
mkdir -p /opt/psp/{config,data}
chown -R psp:psp /opt/psp

# 把构建产物放到 /opt/psp/psp
cp psp /opt/psp/psp
cp -r config/* /opt/psp/config/        # 默认 rulesets、templates
chmod +x /opt/psp/psp
```

### 2. 写最小启动配置

`/opt/psp/config/config.yaml`（首次启动会自动生成，下面是关键字段）：

```yaml
listen: ":8788"

# JWT 签名密钥（用 openssl rand -base64 36 生成；首启会自动生成）
jwt_secret: "REPLACE-ME-WITH-RANDOM-STRING"

# 加密数据库里存的 3X-UI / OIDC / SAML / SMTP 等凭据（同样首启自动生成）
# 丢了这个 key 就解不开 DB 里 enc: 前缀的密文，记得备份
encryption_key: "REPLACE-ME-WITH-ANOTHER-RANDOM-STRING"

config_dir: "/opt/psp/config"
data_dir:   "/opt/psp/data"

# 不写 mysql 块就用默认 SQLite：/opt/psp/data/panel.db
# 要用 MySQL，加上：
# mysql:
#   host: "127.0.0.1"
#   port: 3306
#   user: "psp"
#   password: "..."
#   database: "passwall"
```

完整的注释化示例见 [`config/config.yaml.example`](config/config.yaml.example)。

环境敏感变量放 `/opt/psp/.env`（chmod 600，systemd 会读）：

```bash
# 可选：覆盖 config.yaml 里的 jwt_secret / encryption_key
PSP_JWT_SECRET=$(openssl rand -base64 36)
PSP_ENCRYPTION_KEY=$(openssl rand -base64 36)
# 可选：用 MySQL 时覆盖 config.yaml 里的 dsn
# PSP_MYSQL_DSN=user:pass@tcp(127.0.0.1:3306)/psp?parseTime=true&charset=utf8mb4&loc=Local
```

### 3. systemd 接管

仓库内 [`deploy/systemd/passwall-sub-panel.service`](deploy/systemd/passwall-sub-panel.service) 已经写好（含 hardening 选项）：

```bash
sudo cp deploy/systemd/passwall-sub-panel.service /etc/systemd/system/
sudo systemctl daemon-reload
sudo systemctl enable --now passwall-sub-panel
sudo systemctl status passwall-sub-panel
```

默认监听 `:8788`。浏览器访问 `http://<server>:8788` 即可。首次启动会自动创建 `admin / admin` 账号，**登录后立刻改密码**。

### 4. （可选）Nginx 反向代理 + HTTPS

仓库提供示例 [`deploy/nginx/passwall-sub-panel.conf`](deploy/nginx/passwall-sub-panel.conf)：

```bash
sudo cp deploy/nginx/passwall-sub-panel.conf /etc/nginx/conf.d/
# 改 server_name 和证书路径
sudo nginx -t && sudo systemctl reload nginx
```

走 Nginx 时记得在管理后台「系统设置 → 公网基地址」填上 `https://your-domain` —— 订阅 URL 生成依赖这一项。

## Docker Compose 部署

适合：宿主机上已经跑着 3X-UI，希望 PSP 容器能直接以 `127.0.0.1:<port>` 回访 3X-UI。

仓库的 [`docker-compose.yml`](docker-compose.yml) **默认使用 `network_mode: host`**，理由：
- 3X-UI 通常监听宿主机本地端口（如 `127.0.0.1:2053`），无需对外暴露
- Host 网络下 PSP 容器即等同于宿主机进程，直接 `http://127.0.0.1:2053` 即可访问 3X-UI
- 省去 `extra_hosts: ["host.docker.internal:host-gateway"]` 的桥接配置

### 1. 准备配置

```bash
git clone https://github.com/KazuhaHub/Passwall-Sub-Panel.git
cd Passwall-Sub-Panel

mkdir -p config data
cp local-build/config.yaml.example config/config.yaml
# 编辑 config/config.yaml（至少改 jwt_secret，或留空靠环境变量覆盖）
```

### 2. 准备 `.env`

```bash
cat > .env <<'EOF'
PSP_JWT_SECRET=请填 openssl rand -base64 36 的输出
PSP_ENCRYPTION_KEY=请填 openssl rand -base64 36 的输出
# 用 MySQL 时填这一行；用默认 SQLite 留空即可
PSP_MYSQL_DSN=
EOF
chmod 600 .env
```

> 这两个 secret 留空也能跑——首次启动会写到 `config.yaml` 里自动生成。但通过 `.env` 注入可以让 `config.yaml` 入 git / 入镜像而不带密钥。

### 3. 启动

```bash
# 第一次：本地构建镜像（多阶段，需要几分钟）
docker compose build

# 或者直接拉 GHCR 上的预构建镜像（编辑 docker-compose.yml 注释掉 build: .）
docker compose up -d

docker compose logs -f psp
```

容器内 PSP 监听 `:8788`（由 `config.yaml` 控制）。因为是 host 网络，浏览器直接访问 `http://<host-ip>:8788`。

### 4. 关键端口与 3X-UI 联通

- **PSP 监听** —— `config.yaml` 里的 `listen: ":8788"` 决定。Host 模式下这个端口直接占用宿主机。
- **3X-UI 联通** —— 在管理后台「服务器」里添加 3X-UI，URL 填 `http://127.0.0.1:<3xui_port>`。

### 5. （可选）切回桥接网络

如果你需要把 PSP 装进自定义 bridge 网络（比如和外部 MySQL 容器联网），把 `docker-compose.yml` 改成：

```yaml
services:
  psp:
    # ... 其他配置不变
    # network_mode: host           # 注释掉
    ports:
      - "127.0.0.1:8788:8788"      # 启用端口映射
    extra_hosts:
      - "host.docker.internal:host-gateway"  # 容器内访问 3X-UI 用 host.docker.internal
```

然后管理后台里 3X-UI URL 改成 `http://host.docker.internal:<3xui_port>`。

## 配置

### config.yaml — 启动必需

`config.yaml` 只放**启动必需**的最少字段，绝大多数运维设置（公网地址、邮件、登录模式、CRON 周期、限流等）走管理后台「系统设置」存到数据库。

**首次启动**没有 config.yaml 时会自动生成一个，里面 `jwt_secret` / `encryption_key` 都是随机的，开箱即用。手动配置时参考 [`config/config.yaml.example`](config/config.yaml.example) — 这是带完整注释的官方样板。

| 字段 | 必填 | 说明 |
|---|---|---|
| `listen` | 否 | 监听地址，默认 `:8788` |
| `jwt_secret` | 是 | JWT 签名密钥（首启自动生成；轮换 = 让所有现有 session 失效） |
| `encryption_key` | 是 | DB 里 3X-UI / OIDC / SAML / SMTP 凭据的 AES-GCM 加密 key；丢了无法解密 |
| `config_dir` | 否 | rulesets、templates 所在目录，默认 `./config` |
| `data_dir` | 否 | SQLite 数据库与运行时数据，默认 `./data` |
| `mysql.*` | 否 | 留空 = 用嵌入 SQLite；填了即切到 MySQL |

环境变量覆盖（优先于 config.yaml）：

| 变量 | 用途 |
|---|---|
| `PSP_CONFIG` | 指定 config.yaml 路径 |
| `PSP_JWT_SECRET` | 覆盖 `jwt_secret` |
| `PSP_ENCRYPTION_KEY` | 覆盖 `encryption_key`（用于零密钥镜像部署） |
| `PSP_MYSQL_DSN` | 整个 MySQL DSN，覆盖 `mysql` 块 |
| `PSP_TRUSTED_PROXIES` | 反向代理 CIDR 白名单（默认仅 loopback；`none` = 忽略 X-Forwarded-For） |

### 管理后台「系统设置」

| 分类 | 说明 |
|---|---|
| 公网基地址 | 订阅 URL 生成的 base，必填（Nginx 模式下填 `https://domain`） |
| 登录模式 | SSO 优先 / 双形态（SSO + 本地）/ 仅本地 |
| 订阅路径 | URL 前缀，默认 `sub` |
| 客户端规则 | UA 匹配关键词 + 渲染格式（mihomo / sing-box）+ 白/黑名单 |
| 邮件提醒 | SMTP 配置 + 模板编辑 |
| CRON 周期 | 流量拉取、健康检查、对账等节奏 |
| RBAC | operator 角色可见范围、admin 邮件白名单 |

### 邮件模板类型

| 类型 | 触发时机 |
|---|---|
| `expire_before` | 到期前提醒（窗口可配） |
| `expired` | 已到期 |
| `traffic_low` | 流量剩余低于阈值 |
| `traffic_exhausted` | 流量已用尽 |
| `account_disabled` / `account_enabled` | 账号停用 / 恢复 |
| `announcement` | 管理员群发公告 |

## API

### 公开端点

```
GET  /health                         # 健康检查
GET  /<sub_path>/:token              # 订阅（路径前缀由系统设置控制）
GET  /api/auth/saml/login
POST /api/auth/saml/acs
GET  /api/auth/oidc/login
GET  /api/auth/oidc/callback
```

### 已认证端点（JWT Bearer）

```
GET    /api/me                       # 当前用户信息
GET    /api/me/sub-url               # 订阅 URL 与重置
PATCH  /api/me/password              # 改密
GET    /api/me/traffic               # 个人流量

# Admin / operator 视权限
GET/POST/PUT/DELETE /api/admin/users
GET/POST/PUT/DELETE /api/admin/nodes
GET/POST/PUT/DELETE /api/admin/groups
GET/POST/PUT/DELETE /api/admin/servers
GET/POST/PUT/DELETE /api/admin/rules
GET/POST/PUT/DELETE /api/admin/templates
GET/PUT             /api/admin/settings
GET                 /api/admin/sub-logs
GET                 /api/admin/audit-logs
GET                 /api/admin/sync-tasks
```

完整路由见 [`internal/transport/http/router.go`](internal/transport/http/router.go)。

## 技术栈

| 层 | 技术 |
|---|---|
| 后端 | Go 1.26, Gin, GORM |
| 前端 | React 18, TypeScript, MUI (Material Design 3), Zustand, i18next, ECharts |
| 构建 | Vite (前端), `go build -ldflags="-s -w"` (后端) |
| 数据库 | SQLite (默认，纯 Go) / MySQL 8.0+ |
| 认证 | JWT (HS256), SAML 2.0 (crewjam/saml), OIDC (coreos/go-oidc) |
| 加密 | AES-GCM (敏感字段)、bcrypt (密码) |
| 集成 | 3X-UI Bearer token 优先，username/password cookie 兜底 |

## 文档

- [架构设计](docs/ARCHITECTURE.md) — 核心概念、数据模型、模块交互
- [安全审计](docs/logic-security-review.md) — v2.0 安全加固记录

## 许可证

MIT License
