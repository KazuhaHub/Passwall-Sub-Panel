<p align="center">
  <img src="web-react/public/images/logo-title-circle.png" alt="Passwall Sub Panel" width="200">
</p>

<h1 align="center">Passwall Sub Panel</h1>

<p align="center">
  在 3X-UI 之上的多用户订阅管理面板，动态渲染 Clash / Mihomo、sing-box、V2rayN 订阅
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

Passwall Sub Panel 是一个基于 Go + React 的代理订阅管理系统，通过与 [3X-UI](https://github.com/MHSanaei/3x-ui) 面板集成，提供完整的用户管理、订阅生成、流量监控，以及 SSO 单点登录（SAML / OIDC）、本地登录的 2FA / Passkey、操作审计、多数据库（SQLite / MySQL / PostgreSQL）与非 root 运行等能力。

**适用场景**：从个人 / 朋友圈、小团队，到需要单点登录、操作审计、合规留痕与非 root 部署的中大型组织 —— 同一套单文件二进制都能覆盖，按需开启。

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

## 角色与权限

三个内置角色。后端按路由组授权（`staffGroup` = admin+operator，`adminGroup` = 仅 admin），前端按**能力（capability）**显示/隐藏操作按钮 —— operator 无权的按钮**直接隐藏**，不会出现"点了报错"。

| 能力 / 操作 | admin | operator | user |
|---|:--:|:--:|:--:|
| 登录管理后台 | ✓ | ✓ | — |
| 用户 CRUD（普通用户，role=user） | ✓ | ✓ | — |
| 操作 admin/operator 账户、分配 admin/operator 角色 | ✓ | — | — |
| 流量查看 / 手动设置用量 / 紧急访问 | ✓ | ✓ | — |
| 节点：查看、启用/禁用 | ✓ | ✓ | — |
| 节点：新建 / 编辑 / 删除 / 导入 / 认领、分隔符增改删 | ✓ | — | — |
| 分组 / 规则库 / 配置方案：查看 | ✓ | ✓ | — |
| 分组 / 规则库 / 配置方案：新建 / 编辑 / 删除 | ✓ | — | — |
| 日志：查看 | ✓ | ✓ | — |
| 日志：清空 / 清理 / 改保留期 | ✓ | — | — |
| 同步任务：查看、重试、取消 | ✓ | ✓ | — |
| 同步任务：清理已完成 | ✓ | — | — |
| 3X-UI 服务器（面板凭据）管理 | ✓ | — | — |
| 系统设置 / 邮件 SMTP / SSO（SAML / OIDC） | ✓ | — | — |
| 用户自助页（`/user/me`：订阅、改密、紧急访问、个人规则） | — | — | ✓ |

> **operator**＝日常运营：管普通用户与流量，但碰不到面板凭据 / 系统设置 / SSO，也不能动 admin/operator 账户或把谁提升为 admin/operator。服务器、系统设置页对 operator 直接重定向到 Dashboard。
>
> 自定义角色 / 细粒度权限是**预留扩展点**，实现与扩展方式见 [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md) §6.3。

## 快速开始

### 环境要求

- **运行时**：Linux（推荐）或任何支持 Go 的系统
- **数据库**：内置 SQLite（默认，零配置）或 MySQL 8.0+
- **3X-UI**：**≥ 3.1.0**，已部署且可访问，准备好 API token 或管理员账号（3.1.0 起 `/inbounds/list` 把 settings 改成了嵌套对象，PSP 适配的就是这一版的响应格式）
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

> 你**完全**不需要写这个文件——首次启动二进制时它会自动生成（含随机 secrets）。手动写只是想精确控制路径或预置 MySQL DSN 时用。

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

最简方式——**不用 clone 仓库**，下载一份 `docker-compose.yml` 就能跑：

```bash
mkdir -p /opt/Passwall-Sub-Panel && cd /opt/Passwall-Sub-Panel
curl -O https://raw.githubusercontent.com/KazuhaHub/Passwall-Sub-Panel/main/docker-compose.yml
docker compose up -d
docker compose logs -f psp
```

完事。首启后**当前目录会出现 `./config/`**，里面是：

```
/opt/Passwall-Sub-Panel/
├── docker-compose.yml
└── config/
    ├── config.yaml          # 自动生成，含随机 secrets
    ├── rulesets/            # Clash 分流规则集
    └── templates/           # 订阅渲染模板
```

访问 `http://<host-ip>:8788`，首登 `admin / admin`。

### 默认行为说明

仓库 [`docker-compose.yml`](docker-compose.yml) 的关键决策：

| 项 | 默认 | 原因 |
|---|---|---|
| 网络模式 | `network_mode: host` | 让 PSP 容器以 `127.0.0.1:<port>` 直访同宿主机的 3X-UI，无需 `extra_hosts` |
| 镜像 | `ghcr.io/kazuhahub/passwall-sub-panel:latest`，`pull_policy: always` | `:latest` = 最新**稳定版**，每次 `up -d` 自动重拉；想跟测试版（beta）见下「镜像渠道」 |
| 配置 | **bind mount `./config:/app/config`** | 宿主机直接 `nano` 编辑，所见即所得；首启 entrypoint 从镜像内 `/app/defaults/` 把模板/规则集种到空目录里 |
| 数据 | 命名 volume `psp-data` | SQLite 数据库 + 运行时数据，不需要宿主机直接访问 |

所有运维设置（含 secrets、MySQL DSN）都改 `./config/config.yaml`——**不用** `.env`、**不用** 一堆 `PSP_*` 环境变量。

### 镜像渠道（稳定版 / 测试版）

发布分两条滚动通道，与面板内版本号颜色（绿 = 稳定、黄 = 测试）一致：

| Tag | 跟踪 | 适合 |
|---|---|---|
| `:latest` | 最新**稳定版**（默认） | 生产 / 日常 |
| `:beta` | **最前沿**——任何最新发布（预发布**或**稳定版） | 想一直用最新 |
| `:v3.7.0` / `:v3.7.0-beta.17` | 钉死某个确切版本 | 不想自动滚动 |

切换通道只改 `docker-compose.yml` 里那一行 `image`，然后 `docker compose up -d`（`pull_policy: always` 会自动重拉）：

```yaml
services:
  psp:
    image: ghcr.io/kazuhahub/passwall-sub-panel:beta   # latest → beta
```

> `:beta` 是「永远最新」而非「只有 beta」：在 `:beta` 上的部署，等稳定版（如 `v3.7.0`）发布后会**自动滚上稳定版**，之后再滚到下一个 beta。想固定在稳定轨就用 `:latest`，想钉死不动就用 `:vX.Y.Z`。
>
> 此外面板会在**管理员通知**里检测并提示 PSP 新稳定版（只提示稳定版、不催 beta），3X-UI 面板低于 PSP 已测最高支持版本时也会提示。

### 改配置：直接编辑宿主机文件

```bash
cd /opt/Passwall-Sub-Panel
nano config/config.yaml          # 用任何编辑器
nano config/templates/clash.yaml # 模板也是
docker compose restart psp       # 改完重启生效
```

不用 `docker compose exec`、不用进容器、不用临时装编辑器。

### 3X-UI 联通

宿主机上跑着 3X-UI（比如监听 `127.0.0.1:2053`）→ 登录 PSP 后，「服务器」页面 URL 填 `http://127.0.0.1:2053`。

### （可选）切回桥接网络

如果不能用 host 网络（比如和外部 MySQL 容器走自定义 bridge）：

```yaml
services:
  psp:
    # ... 其他不变
    # network_mode: host           # 注释掉
    ports:
      - "127.0.0.1:8788:8788"      # 启用端口映射
    extra_hosts:
      - "host.docker.internal:host-gateway"
```

然后「服务器」页面里 3X-UI URL 改成 `http://host.docker.internal:<3xui_port>`。

### （可选）本地构建镜像

不想用 GHCR 上的预构建版（自定义代码、网络拉不到 GHCR）：

```yaml
services:
  psp:
    # image: ghcr.io/kazuhahub/passwall-sub-panel:latest    # 注释掉
    build: .                                                # 用仓库根的 Dockerfile
```

然后 `git clone` 仓库后在仓库根目录 `docker compose build && docker compose up -d`。

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

日常运维**只改 config.yaml**。代码里保留几个环境变量做应急逃生口，绝大多数人用不到：

| 变量 | 何时才用 |
|---|---|
| `PSP_CONFIG` | 改 config.yaml 文件路径（Docker 镜像里默认指 `/app/config/config.yaml`） |
| `PSP_TRUSTED_PROXIES` | 反向代理**不在** loopback 时填代理 CIDR；`none` = 忽略所有 X-Forwarded-For |

`PSP_JWT_SECRET` / `PSP_ENCRYPTION_KEY` / `PSP_MYSQL_DSN` 也仍然能覆盖对应 yaml 字段，但**不推荐**——会让"一个 config.yaml 看到所有配置"的心智模型破掉。

### 管理后台「系统设置」

| 分类 | 说明 |
|---|---|
| 公网基地址 | 订阅 URL 生成的 base，必填（Nginx 模式下填 `https://domain`） |
| 面板路径前缀 | 可选，留空时面板位于域名根路径；填 `/panel` 后管理页面、API 与 SSO 回调均位于 `https://domain/panel/`，订阅链接仍保持在根路径。修改后需同步更新身份提供商登记的 OIDC/SAML 回调地址 |
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

## 许可证

本项目采用 [GNU AGPL v3](LICENSE) 许可证。

这是带有"网络条款"的强 copyleft 协议:任何人修改本项目后，无论是分发二进制还是**部署成对外的网络服务**，都必须向使用者提供完整的对应源码。详见根目录 [LICENSE](LICENSE) 全文。

Copyright (C) 2026 Kazuha Mo
