<p align="center">
  <img src="web-react/public/images/logo+title-circle.png" alt="Passwall Sub Panel" width="200">
</p>

<h1 align="center">Passwall Sub Panel</h1>

<p align="center">
  A lightweight proxy subscription management panel designed for small teams and friend groups
</p>

<p align="center">
  <a href="#features">Features</a> •
  <a href="#quick-start">Quick Start</a> •
  <a href="#binary-deployment">Binary Deployment</a> •
  <a href="#docker-compose-deployment">Docker Compose Deployment</a> •
  <a href="#configuration">Configuration</a> •
  <a href="#api">API</a>
</p>

<p align="center">
  <a href="README.md">中文文档</a>
</p>

---

## Introduction

Passwall Sub Panel is a proxy subscription management system built with Go + React. It integrates with [3X-UI](https://github.com/MHSanaei/3x-ui) panels to provide complete user management, subscription generation, traffic monitoring, and more.

**Use case**: Small teams, friend groups, personal use. Not an enterprise-level proxy service.

**Deployment form**: A single Go binary (the React SPA is embedded via `go:embed`). Just run `./psp`, or deploy via Docker.

## Features

### Core
- **Subscription** — Dynamic generation of Clash/Mihomo, Sing-box, and V2rayN URI-list formats
- **User management** — CRUD, group management (tag filter + render layout), expiration, traffic quotas
- **Node management** — Wraps 3X-UI inbound APIs. Supports multi-panel, both "create new" and "import existing" flows
- **Client detection** — UA-based identification with allow/deny lists
- **Auto-disable** — Disables an account after repeated use of blocked clients; auto-syncs disable on quota/expiry

### Authentication
- **Local accounts** — UPN/password login, bcrypt hashed
- **SAML 2.0 SSO** — Supports Entra ID and other SAML IdPs
- **OIDC SSO** — OpenID Connect

### Email notifications
- Pre-expiration reminders & expired notices
- Low-traffic warnings
- Account disabled / re-enabled notifications
- Automatic retry with exponential backoff

### Other
- Traffic statistics & history curves (per user + per node)
- Audit log, subscription access log
- Sync task queue (async retryable 3X-UI writes)
- Multi-language (zh-CN / en-US), dark / light theme
- RBAC: admin / operator / user

## Quick Start

### Prerequisites

- **Runtime**: Linux (recommended) or any OS Go supports
- **Database**: Embedded SQLite (default, zero-config) or MySQL 8.0+
- **3X-UI**: Already deployed and reachable; have an API token or admin credentials ready
- **Build-time** (only if building from source): Go 1.26+, Node.js 20+

### Build from source

```bash
# Clone
git clone https://github.com/KazuhaHub/Passwall-Sub-Panel.git
cd Passwall-Sub-Panel

# Build the frontend (output goes to internal/web/dist, embedded by go:embed)
cd web-react
npm install
npm run build
cd ..

# Build the backend (-s -w strips symbols and DWARF; ~25% smaller)
go build -ldflags="-s -w" -o psp ./cmd/panel

# Run (first launch generates a config.yaml template)
./psp
```

The resulting `psp` is a fully **self-contained** single binary — no external files needed at startup (the SPA, SQLite driver, etc. are all inside).

## Binary Deployment

Best for: single-host self-hosting, simplest and most reliable.

### 1. Prepare directories and the binary

```bash
# As root
useradd -r -s /usr/sbin/nologin psp
mkdir -p /opt/psp/{config,data}
chown -R psp:psp /opt/psp

# Drop the built binary into /opt/psp/psp
cp psp /opt/psp/psp
cp -r config/* /opt/psp/config/        # default rulesets, templates
chmod +x /opt/psp/psp
```

### 2. Minimal startup config

`/opt/psp/config/config.yaml` (auto-generated on first launch; key fields shown):

```yaml
listen: ":8788"

# JWT signing key (generate with `openssl rand -base64 36`; first launch auto-generates)
jwt_secret: "REPLACE-ME-WITH-RANDOM-STRING"

# Encrypts 3X-UI / OIDC / SAML / SMTP credentials stored in the database
# (first launch auto-generates). Losing this key prevents decrypting saved secrets.
encryption_key: "REPLACE-ME-WITH-ANOTHER-RANDOM-STRING"

config_dir: "/opt/psp/config"
data_dir:   "/opt/psp/data"

# Omit the mysql block to use embedded SQLite at /opt/psp/data/panel.db
# To use MySQL:
# mysql:
#   host: "127.0.0.1"
#   port: 3306
#   user: "psp"
#   password: "..."
#   database: "passwall"
```

The fully annotated reference is [`config/config.yaml.example`](config/config.yaml.example).

Sensitive env vars go in `/opt/psp/.env` (chmod 600; systemd reads it):

```bash
# Optional: override jwt_secret / encryption_key from config.yaml
PSP_JWT_SECRET=$(openssl rand -base64 36)
PSP_ENCRYPTION_KEY=$(openssl rand -base64 36)
# Optional: replace the mysql block in config.yaml when using MySQL
# PSP_MYSQL_DSN=user:pass@tcp(127.0.0.1:3306)/psp?parseTime=true&charset=utf8mb4&loc=Local
```

### 3. Hand off to systemd

The repo ships [`deploy/systemd/passwall-sub-panel.service`](deploy/systemd/passwall-sub-panel.service) (with hardening options pre-set):

```bash
sudo cp deploy/systemd/passwall-sub-panel.service /etc/systemd/system/
sudo systemctl daemon-reload
sudo systemctl enable --now passwall-sub-panel
sudo systemctl status passwall-sub-panel
```

Listens on `:8788` by default. Open `http://<server>:8788`. First launch creates `admin / admin` — **change the password immediately after login**.

### 4. (Optional) Nginx reverse proxy + HTTPS

A sample config is provided at [`deploy/nginx/passwall-sub-panel.conf`](deploy/nginx/passwall-sub-panel.conf):

```bash
sudo cp deploy/nginx/passwall-sub-panel.conf /etc/nginx/conf.d/
# Edit server_name and certificate paths
sudo nginx -t && sudo systemctl reload nginx
```

When behind Nginx, set "Public Base URL" in the admin "System Settings" to `https://your-domain` — subscription URL generation depends on it.

## Docker Compose Deployment

Best for: 3X-UI already running on the host; you want the PSP container to reach it via `127.0.0.1:<port>` with zero networking gymnastics.

The shipped [`docker-compose.yml`](docker-compose.yml) **defaults to `network_mode: host`**, because:
- 3X-UI is typically bound to a host-local port (e.g., `127.0.0.1:2053`) and shouldn't be exposed publicly
- Under host networking, the PSP container behaves as a host process and can hit `http://127.0.0.1:2053` directly
- Avoids the `extra_hosts: ["host.docker.internal:host-gateway"]` workaround

### 1. Prepare config

```bash
git clone https://github.com/KazuhaHub/Passwall-Sub-Panel.git
cd Passwall-Sub-Panel

mkdir -p config data
cp local-build/config.yaml.example config/config.yaml
# Edit config/config.yaml (at minimum set jwt_secret, or leave it for env-var override)
```

### 2. Prepare `.env`

```bash
cat > .env <<'EOF'
PSP_JWT_SECRET=put-the-output-of-openssl-rand-base64-36-here
PSP_ENCRYPTION_KEY=put-the-output-of-openssl-rand-base64-36-here
# Fill this line if you want MySQL; leave empty for default SQLite
PSP_MYSQL_DSN=
EOF
chmod 600 .env
```

> Both secrets can be left blank — the first launch writes random values into `config.yaml` for you. Injecting via `.env` lets you ship `config.yaml` in git / a Docker image without secrets.

### 3. Start

```bash
# First time: build locally (multi-stage, takes a few minutes)
docker compose build

# Or pull a prebuilt image from GHCR (comment out `build: .` in docker-compose.yml first)
docker compose up -d

docker compose logs -f psp
```

The container listens on `:8788` (controlled by `config.yaml`). Because it's on host networking, that port is the host's port — open `http://<host-ip>:8788` directly.

### 4. Ports & talking to 3X-UI

- **PSP listens on**: whatever `listen: ":8788"` says in `config.yaml`. With host networking this directly occupies the host's port.
- **Connecting to 3X-UI**: in the admin UI "Servers" page, set the 3X-UI URL to `http://127.0.0.1:<3xui_port>`.

### 5. (Optional) Switching back to a bridge network

If you want PSP on a custom bridge network (e.g., to peer with a MySQL container), edit `docker-compose.yml`:

```yaml
services:
  psp:
    # ... rest unchanged
    # network_mode: host           # comment out
    ports:
      - "127.0.0.1:8788:8788"      # enable port mapping
    extra_hosts:
      - "host.docker.internal:host-gateway"  # so the container can reach the host
```

Then set the 3X-UI URL in the admin UI to `http://host.docker.internal:<3xui_port>`.

## Configuration

### config.yaml — boot-required only

`config.yaml` carries only the **minimum to boot**. Almost all runtime settings (public base URL, email, login mode, CRON cadence, rate limits, etc.) live in the database and are managed via the admin "System Settings" UI.

**First launch**, when no config.yaml exists, the panel writes one with randomly generated `jwt_secret` and `encryption_key` — works out of the box. For manual setup, copy [`config/config.yaml.example`](config/config.yaml.example) — the fully annotated reference.

| Field | Required | Purpose |
|---|---|---|
| `listen` | no | Bind address, default `:8788` |
| `jwt_secret` | yes | JWT signing key (auto-generated; rotating invalidates every existing session) |
| `encryption_key` | yes | AES-GCM key for 3X-UI / OIDC / SAML / SMTP credentials stored in the DB. Loss = can't decrypt. |
| `config_dir` | no | Path holding rulesets & templates, default `./config` |
| `data_dir` | no | SQLite DB + runtime data, default `./data` |
| `mysql.*` | no | Leave empty for embedded SQLite; fill it to switch to MySQL |

Environment-variable overrides (take precedence over config.yaml):

| Variable | Purpose |
|---|---|
| `PSP_CONFIG` | Path to config.yaml |
| `PSP_JWT_SECRET` | Override `jwt_secret` |
| `PSP_ENCRYPTION_KEY` | Override `encryption_key` (lets you ship a key-less image) |
| `PSP_MYSQL_DSN` | Full MySQL DSN, replaces the `mysql` block |
| `PSP_TRUSTED_PROXIES` | Reverse-proxy CIDR allow-list (default: loopback only; `none` = ignore X-Forwarded-For) |

### Admin "System Settings"

| Section | Description |
|---|---|
| Public Base URL | Base for generated subscription URLs (required; e.g. `https://domain` behind Nginx) |
| Login Mode | SSO-first / Dual (SSO + local) / Local-only |
| Subscription Path | URL prefix, default `sub` |
| Client Rules | UA keywords + render format (mihomo / sing-box) + allow/deny lists |
| Email | SMTP config + template editor |
| CRON Schedules | Traffic poll, health check, reconciliation cadence |
| RBAC | operator scope, admin-email allow-list |

### Email template kinds

| Kind | When it fires |
|---|---|
| `expire_before` | Pre-expiration reminder (configurable window) |
| `expired` | After expiration |
| `traffic_low` | Remaining traffic below threshold |
| `traffic_exhausted` | Traffic quota used up |
| `account_disabled` / `account_enabled` | Account disabled / re-enabled |
| `announcement` | Admin broadcast |

## API

### Public endpoints

```
GET  /health                         # health probe
GET  /<sub_path>/:token              # subscription (prefix is configurable)
GET  /api/auth/saml/login
POST /api/auth/saml/acs
GET  /api/auth/oidc/login
GET  /api/auth/oidc/callback
```

### Authenticated endpoints (JWT Bearer)

```
GET    /api/me                       # current user
GET    /api/me/sub-url               # subscription URL & reset
PATCH  /api/me/password              # change password
GET    /api/me/traffic               # personal traffic

# Admin / operator, gated by RBAC
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

Full route list lives in [`internal/transport/http/router.go`](internal/transport/http/router.go).

## Tech Stack

| Layer | Technology |
|---|---|
| Backend | Go 1.26, Gin, GORM |
| Frontend | React 18, TypeScript, MUI (Material Design 3), Zustand, i18next, ECharts |
| Build | Vite (frontend), `go build -ldflags="-s -w"` (backend) |
| Database | SQLite (default, pure-Go) / MySQL 8.0+ |
| Auth | JWT (HS256), SAML 2.0 (crewjam/saml), OIDC (coreos/go-oidc) |
| Crypto | AES-GCM (sensitive fields), bcrypt (passwords) |
| Integration | 3X-UI Bearer token (preferred), username/password cookie (fallback) |

## Documents

- [Architecture](docs/ARCHITECTURE.md) — core concepts, data model, module interactions (Chinese)
- [Security review](docs/logic-security-review.md) — v2.0 hardening notes

## License

MIT License
