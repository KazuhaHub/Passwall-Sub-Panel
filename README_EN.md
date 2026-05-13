<p align="center">
  <img src="web/public/images/logo+title-circle.png" alt="Passwall Sub Panel" width="200">
</p>

<h1 align="center">Passwall Sub Panel</h1>

<p align="center">
  A lightweight proxy subscription management panel designed for small teams and friend groups
</p>

<p align="center">
  <a href="#features">Features</a> •
  <a href="#quick-start">Quick Start</a> •
  <a href="#deployment">Deployment</a> •
  <a href="#configuration">Configuration</a> •
  <a href="#api">API</a> •
  <a href="#license">License</a>
</p>

<p align="center">
  <a href="README.md">中文文档</a>
</p>

---

## Introduction

Passwall Sub Panel is a proxy subscription management system built with Go + Vue 3. It integrates with [3X-UI](https://github.com/MHSanaei/3x-ui) panels to provide complete user management, subscription generation, traffic monitoring, and more.

**Use case**: Small teams, friend groups, personal use. Not an enterprise-level proxy service system.

## Features

### Core Features
- **Subscription Management** - Dynamic Clash/Sing-box config generation, multi-client support
- **User Management** - User CRUD, group management, expiration dates, traffic quotas
- **Node Management** - Manage 3X-UI inbounds through the panel, multi-panel support
- **Client Detection** - UA-based client type identification with whitelist filtering
- **Auto Disable** - Automatically disable accounts after repeated use of blocked clients

### Authentication
- **Local Accounts** - UPN/password login
- **SAML SSO** - Support for Entra ID and other SAML IdPs
- **OIDC SSO** - OpenID Connect support

### Email Notifications
- Expiration reminders
- Low traffic warnings
- Account disable/enable notifications
- Automatic retry with exponential backoff

### Additional Features
- Traffic statistics and history
- Audit logs
- Sync task queue
- Multi-language client support
- Dark/Light theme

## Quick Start

### Prerequisites

- Go 1.21+
- Node.js 18+
- MySQL 8.0+ or SQLite

### Build from Source

```bash
# Clone the project
git clone https://github.com/KazuhaHub/Passwall-Sub-Panel.git
cd Passwall-Sub-Panel

# Build frontend
cd web
npm install
npm run build
cd ..

# Build backend
go build -o psp ./cmd/panel

# Run
./psp
```

### Using Docker

```bash
docker-compose up -d
```

## Deployment

### Configuration File

A `config.yaml` will be generated on first run:

```yaml
listen: ":8788"          # Listen address
db_kind: "sqlite"        # Database type: sqlite or mysql
db_dsn: "data/panel.db"  # Database connection
jwt_secret: "your-secret" # JWT secret
```

### Reverse Proxy (Nginx)

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

### systemd Service

```bash
sudo cp deploy/systemd/passwall-sub-panel.service /etc/systemd/system/
sudo systemctl enable --now passwall-sub-panel
```

## Configuration

### System Settings

After logging into the admin panel, configure in "System Settings":

| Setting | Description |
|---|---|
| Login Mode | SSO First / Dual / Local Only |
| Public Base URL | Panel's public access URL |
| Subscription Path | Subscription URL path prefix, default `sub` |
| Client Rules | Configure allowed/blocked client types |
| Email Reminder | SMTP configuration and email templates |

### Client Rules

Configure in "System Settings → Subscription Management":

- **Name** - Client display name
- **Keywords** - UA matching keywords (comma separated)
- **Render Format** - mihomo or sing-box
- **Status** - Enable/Disable

### Email Templates

Supported template types:

| Type | Description |
|---|---|
| `expire_before` | Pre-expiration reminder |
| `expired` | Expiration reminder |
| `traffic_low` | Low traffic warning |
| `account_disabled` | Account disabled notification |
| `account_enabled` | Account re-enabled notification |

## API

### Public Endpoints

```
GET /health              # Health check
GET /sub/:token          # Get subscription
```

### Auth Endpoints

```
POST /api/auth/local/login   # Local login
GET  /api/auth/saml/login    # SAML login
GET  /api/auth/oidc/login    # OIDC login
```

### Admin Endpoints

```
GET/POST   /api/admin/users           # User management
GET/POST   /api/admin/nodes           # Node management
GET/POST   /api/admin/groups          # Group management
GET/PUT    /api/admin/settings/ui     # System settings
GET/POST   /api/admin/sub-logs        # Subscription logs
```

## Tech Stack

| Layer | Technology |
|---|---|
| Backend | Go 1.21+, Gin, GORM |
| Frontend | Vue 3, TypeScript, Element Plus |
| Database | MySQL 8.0 / SQLite |
| Auth | JWT, SAML 2.0, OIDC |

## License

MIT License
