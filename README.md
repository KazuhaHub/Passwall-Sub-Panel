<p align="center">
  <img src="web/public/images/logo+title-circle.png" alt="Passwall Sub Panel" width="200">
</p>

<h1 align="center">Passwall Sub Panel</h1>

<p align="center">
  一个轻量级的代理订阅管理面板，专为小型团队和朋友圈设计
</p>

<p align="center">
  <a href="#功能特性">功能特性</a> •
  <a href="#快速开始">快速开始</a> •
  <a href="#部署">部署</a> •
  <a href="#配置">配置</a> •
  <a href="#api">API</a> •
  <a href="#许可证">许可证</a>
</p>

<p align="center">
  <a href="README_EN.md">English</a>
</p>

---

## 简介

Passwall Sub Panel 是一个基于 Go + Vue 3 的代理订阅管理系统，通过与 [3X-UI](https://github.com/MHSanaei/3x-ui) 面板集成，提供完整的用户管理、订阅生成、流量监控等功能。

**适用场景**：小型团队、朋友圈、个人使用，不是企业级机场系统。

## 功能特性

### 核心功能
- **订阅管理** - 动态生成 Clash/Sing-box 配置，支持多种客户端
- **用户管理** - 用户 CRUD、分组管理、到期时间、流量限额
- **节点管理** - 通过面板管理 3X-UI inbound，支持多面板
- **客户端检测** - UA 自动识别客户端类型，支持白名单过滤
- **自动停用** - 多次使用禁用客户端后自动停用账号

### 认证方式
- **本地账号** - UPN/密码登录
- **SAML SSO** - 支持 Entra ID 等 SAML IdP
- **OIDC SSO** - 支持 OpenID Connect

### 邮件通知
- 到期提醒
- 流量不足提醒
- 账号停用/恢复通知
- 失败自动重试（指数退避）

### 其他功能
- 流量统计与历史记录
- 审计日志
- 同步任务队列
- 多语言客户端支持
- 暗色/亮色主题

## 快速开始

### 环境要求

- Go 1.21+
- Node.js 18+
- MySQL 8.0+ 或 SQLite

### 从源码构建

```bash
# 克隆项目
git clone https://github.com/KazuhaHub/Passwall-Sub-Panel.git
cd Passwall-Sub-Panel

# 构建前端
cd web
npm install
npm run build
cd ..

# 构建后端
go build -o psp ./cmd/panel

# 运行
./psp
```

### 使用 Docker

```bash
docker-compose up -d
```

## 部署

### 配置文件

首次运行会生成 `config.yaml`，主要配置项：

```yaml
listen: ":8788"          # 监听地址
db_kind: "sqlite"        # 数据库类型：sqlite 或 mysql
db_dsn: "data/panel.db"  # 数据库连接
jwt_secret: "your-secret" # JWT 密钥
```

### 反向代理 (Nginx)

```nginx
server {
    listen 443 ssl;
    server_name your-domain.com;

    location / {
        proxy_pass http://127.0.0.1:8788;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
    }
}
```

### systemd 服务

```bash
sudo cp deploy/systemd/passwall-sub-panel.service /etc/systemd/system/
sudo systemctl enable --now passwall-sub-panel
```

## 配置

### 系统设置

登录管理后台后，在「系统设置」中配置：

| 设置项 | 说明 |
|---|---|
| 登录模式 | SSO 优先 / 双形态 / 仅本地 |
| 公网基地址 | 面板的公网访问地址 |
| 订阅路径 | 订阅 URL 路径前缀，默认 `sub` |
| 客户端规则 | 配置允许/禁止的客户端类型 |
| 邮件提醒 | SMTP 配置和邮件模板 |

### 客户端规则

在「系统设置 → 订阅管理」中配置：

- **名称** - 客户端显示名称
- **关键词** - UA 匹配关键词（逗号分隔）
- **渲染格式** - mihomo 或 sing-box
- **状态** - 启用/禁用

### 邮件模板

支持以下模板类型：

| 类型 | 说明 |
|---|---|
| `expire_before` | 到期前提醒 |
| `expired` | 到期提醒 |
| `traffic_low` | 流量不足提醒 |
| `account_disabled` | 账号停用通知 |
| `account_enabled` | 账号恢复通知 |

## API

### 公开端点

```
GET /health              # 健康检查
GET /sub/:token          # 获取订阅
```

### 认证端点

```
POST /api/auth/local/login   # 本地登录
GET  /api/auth/saml/login    # SAML 登录
GET  /api/auth/oidc/login    # OIDC 登录
```

### 管理端点

```
GET/POST   /api/admin/users           # 用户管理
GET/POST   /api/admin/nodes           # 节点管理
GET/POST   /api/admin/groups          # 分组管理
GET/PUT    /api/admin/settings/ui     # 系统设置
GET/POST   /api/admin/sub-logs        # 订阅日志
```

## 技术栈

| 层 | 技术 |
|---|---|
| 后端 | Go 1.21+, Gin, GORM |
| 前端 | Vue 3, TypeScript, Element Plus |
| 数据库 | MySQL 8.0 / SQLite |
| 认证 | JWT, SAML 2.0, OIDC |

## 许可证

MIT License
