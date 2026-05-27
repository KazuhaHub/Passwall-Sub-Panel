// Package config loads the main YAML configuration plus mandatory env vars.
package config

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// Config is the top-level panel configuration. Only bootstrap-required
// values live here — anything an admin might tune (cron cadence, JWT TTLs,
// rate limits, login mode, branding, sub_base_url, etc.) is stored in the
// settings table and edited through Admin → Settings.
type Config struct {
	Listen        string `yaml:"listen"`
	JWTSecret     string `yaml:"jwt_secret"`
	EncryptionKey string `yaml:"encryption_key"`

	MySQL    MySQLConfig    `yaml:"mysql"`
	Postgres PostgresConfig `yaml:"postgres"`
	HTTP     HTTPConfig     `yaml:"http"`

	ConfigDir string `yaml:"config_dir"`
	DataDir   string `yaml:"data_dir"`

	// LogLevel sets the global slog level (debug / info / warn / error,
	// case-insensitive). Lives in the boot config because it has to take
	// effect BEFORE the DB is reachable — settings-table edits would be
	// useless for early-boot diagnostics like PollOnce per-stage timing.
	// Empty = keep the default (info). Override order: --debug flag >
	// PSP_LOG_LEVEL env > this field > default.
	LogLevel string `yaml:"log_level"`
}

// HTTPConfig groups reverse-proxy-aware request-handling settings.
//
// Default behavior is "zero-config behind any reverse proxy": the panel
// trusts all upstream IPs and reads the real client IP from
// CF-Connecting-IP / X-Real-IP / X-Forwarded-For (in that order). This
// matches the common case — panel sits behind nginx / caddy / traefik on
// the same machine OR behind a CDN like Cloudflare — without forcing the
// admin to enumerate CIDRs.
//
// The zero-config default trusts ALL upstreams, which is only safe while the
// listen port isn't directly reachable. RECOMMENDED for any real deployment:
// set TrustedProxies to your reverse proxy's IP (e.g. "127.0.0.1,::1"). Then
// Gin verifies the TCP peer IS that proxy before believing X-Forwarded-For —
// so a direct attacker can't forge XFF to spoof their IP and thereby bypass
// the per-IP rate limiter (/login, /sub abuse protection) or poison the IP
// recorded in audit / subscription logs. (This is stricter than what Gin's
// TrustedPlatform single-header approach can express, since it pins the
// proxy by address, not just by which header it sets.)
type HTTPConfig struct {
	// TrustedProxies controls which upstream IPs Gin will believe when
	// computing ClientIP. Comma-separated CIDRs or IPs. Special tokens:
	//   ""        → loopback only (127.0.0.1/32, ::1/128). Safe default
	//               for a direct listen. Behind a reverse proxy / CDN
	//               every request looks like the proxy's IP — fix by
	//               setting one of the explicit values below.
	//   "all"/"*" → trust every source (pre-v3.6.1-beta.2 default).
	//               Use ONLY when the listen port can't be reached
	//               from outside (Docker network, UNIX socket, etc.).
	//               Boot logs WARN when this mode is active.
	//   "none"    → disable the trust list entirely; ClientIP returns
	//               the raw TCP peer.
	//   "<cidr>[,<cidr>]" → trust only the listed networks. Recommended
	//                       for production behind Cloudflare / nginx /
	//                       Caddy — name the proxy's IP ranges.
	TrustedProxies string `yaml:"trusted_proxies"`
}

// MySQLConfig is the database selection block. Two ways to configure:
//
//  1. Discrete fields (Host/Port/User/Password/Database/Params): readable,
//     idiomatic YAML. The panel assembles a DSN at startup.
//  2. DSN: a raw go-sql-driver MySQL DSN, OR "sqlite:./path.db" to force
//     SQLite, OR empty to fall through to embedded SQLite at
//     <DataDir>/panel.db. When DSN is non-empty it overrides the discrete
//     fields entirely.
//
// Env PSP_MYSQL_DSN, when set, replaces whatever this block produces —
// useful for keeping the password out of the config file.
type MySQLConfig struct {
	// Raw DSN escape hatch. Empty unless you need a connection-string form
	// the discrete fields don't model (e.g. unix socket, TLS).
	DSN string `yaml:"dsn"`

	// Discrete connection fields. Host being non-empty is the trigger that
	// switches the panel from SQLite (default) to MySQL.
	Host     string `yaml:"host"`
	Port     int    `yaml:"port"`
	User     string `yaml:"user"`
	Password string `yaml:"password"`
	Database string `yaml:"database"`
	// go-sql-driver query string appended to the assembled DSN. Defaults to
	// "parseTime=true&charset=utf8mb4&loc=UTC" when blank — parseTime is
	// REQUIRED or GORM hands back []byte for DATETIME columns. loc=UTC is
	// the canonical "store UTC, render local at the edges" pattern: every
	// DATETIME column ends up holding a UTC wall-clock string regardless
	// of where the panel runs. Pair with TZ=UTC on the panel process
	// (Dockerfile already sets it) so Go's time.Local also matches.
	Params string `yaml:"params"`
}

// PostgresConfig is the PostgreSQL selection block, parallel to MySQLConfig
// but assembling a "postgres://" URL DSN instead of a go-sql-driver one.
// Two ways to configure:
//
//  1. Discrete fields (Host/Port/User/Password/Database/SSLMode/Params):
//     readable YAML; the panel assembles the URL for you.
//  2. DSN: a raw connection string (URL "postgres://…" or libpq keyword
//     form), which overrides the discrete fields entirely.
//
// Setting either DSN or Host here selects the Postgres driver. Env
// PSP_POSTGRES_DSN, when set, replaces whatever this block produces.
type PostgresConfig struct {
	// Raw DSN escape hatch. Empty unless you need a form the discrete fields
	// don't model. Accepts both the URL and libpq keyword styles (pgx reads
	// either).
	DSN string `yaml:"dsn"`

	Host     string `yaml:"host"`
	Port     int    `yaml:"port"`
	User     string `yaml:"user"`
	Password string `yaml:"password"`
	Database string `yaml:"database"`
	// SSLMode maps to the libpq sslmode query param. Blank defaults to
	// "disable" — the pragmatic choice for a panel reusing a PG instance on
	// localhost / a trusted LAN. Set "require" / "verify-full" when the DB is
	// reached over an untrusted network.
	SSLMode string `yaml:"sslmode"`
	// Params is an extra "k=v&k2=v2" query string merged into the assembled
	// URL (e.g. "connect_timeout=10"). sslmode here is overridden by SSLMode.
	Params string `yaml:"params"`
}

// assembleDSN builds a "postgres://user:pass@host:port/db?sslmode=…" URL from
// the discrete fields. net/url handles percent-encoding so passwords with
// special characters survive intact.
func (p PostgresConfig) assembleDSN() string {
	port := p.Port
	if port <= 0 {
		port = 5432
	}
	sslmode := strings.TrimSpace(p.SSLMode)
	if sslmode == "" {
		sslmode = "disable"
	}
	q := url.Values{}
	if extra := strings.TrimSpace(p.Params); extra != "" {
		if vals, err := url.ParseQuery(extra); err == nil {
			for k, vs := range vals {
				for _, v := range vs {
					q.Add(k, v)
				}
			}
		}
	}
	q.Set("sslmode", sslmode) // SSLMode wins over any sslmode in Params
	userInfo := url.User(strings.TrimSpace(p.User))
	if p.Password != "" {
		userInfo = url.UserPassword(strings.TrimSpace(p.User), p.Password)
	}
	u := url.URL{
		Scheme:   "postgres",
		User:     userInfo,
		Host:     net.JoinHostPort(strings.TrimSpace(p.Host), strconv.Itoa(port)),
		Path:     "/" + strings.TrimSpace(p.Database),
		RawQuery: q.Encode(),
	}
	return u.String()
}

// LoadOrGenerate loads config from path. If the file does not exist a default
// config (with a random JWT secret) is written there first, so the panel can
// start without any manual setup.
func LoadOrGenerate(path string) (*Config, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	if _, err := os.Stat(abs); errors.Is(err, os.ErrNotExist) {
		if err := writeDefaultConfig(abs); err != nil {
			return nil, fmt.Errorf("generate default config %s: %w", abs, err)
		}
	}
	return Load(path)
}

func writeDefaultConfig(abs string) error {
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return err
	}
	secret, err := randomBase64(32)
	if err != nil {
		return err
	}
	const tpl = `# Auto-generated by passwall-sub-panel on first run.
# This file only stores what the binary needs to BOOT: the listen address,
# the JWT signing key, where data goes on disk, and how to reach the DB.
# Everything else (login mode, branding, sub_base_url, email domains, cron
# cadence, JWT TTLs, rate limits, audit retention, SAML/OIDC, …) is managed
# from Admin → Settings and stored in the database.

# ---- Networking ----
listen: ":8788"                            # bind address; ":port" listens on all interfaces

# ---- Secrets ----
# JWT signing key. Generated randomly on first run. Rotate by replacing the
# value (invalidates every existing session). Env override: PSP_JWT_SECRET.
jwt_secret: "%s"

# Database secret encryption key. Generated randomly on first run. It protects
# 3X-UI credentials, SAML/OIDC secrets, and SMTP passwords at rest.
# Env override: PSP_ENCRYPTION_KEY.
encryption_key: "%s"

# ---- Reverse proxy / real client IP ----
# Default (unset): loopback only (127.0.0.1/32, ::1/128). Safe default
# for a direct listen — proxy headers (CF-Connecting-IP /
# X-Forwarded-For etc.) are accepted only from loopback. Behind a real
# proxy this means every client looks like the proxy's IP; fix by
# setting one of the values below.
#
# Tokens:
#   trusted_proxies: "127.0.0.1,::1"      # your reverse proxy (same host)
#   trusted_proxies: "10.0.0.0/8"         # the proxy's subnet / CDN range
#   trusted_proxies: "173.245.48.0/20,..." # Cloudflare-style explicit list
#   trusted_proxies: "all"                # trust every source — ONLY when the
#                                         # listen port is unreachable from
#                                         # outside (Docker network, UNIX
#                                         # socket). Boot logs WARN in this mode.
#   trusted_proxies: "none"               # disable trust list entirely; the
#                                         # raw TCP peer is the client IP.
#
# Gin verifies the TCP peer IS in the trust list before believing
# X-Forwarded-For etc., so a direct attacker can't forge XFF to spoof
# their IP and bypass the per-IP rate limiter (/login, /sub) or poison
# audit / subscription log IPs.
#
# http:
#   trusted_proxies: "127.0.0.1,::1"
#
# Env override: PSP_TRUSTED_PROXIES.

# ---- Logging ----
# Global log level. Empty (or unset) keeps the default of "info".
# Has to live here (or env/flag) because it takes effect BEFORE the DB is
# reachable — anything controlled by the settings table can't help diagnose
# boot-time problems. Setting "debug" unlocks the per-stage timing markers
# in PollOnce and similar diagnostic logs; leave commented for production.
#
# Override priority (most specific wins):
#   --debug flag > PSP_LOG_LEVEL env > log_level (this field) > default (info)
#
# log_level: "debug"                       # debug | info | warn | error

# ---- Filesystem ----
config_dir: "./config"                     # runtime configs (templates, etc.)
data_dir: "./data"                         # SQLite panel.db lives here when no MySQL is set

# ---- Database ----
# Recommended: a real MySQL 5.7+ / MariaDB 10.5+ server. Fill in the discrete
# fields below — the panel assembles the DSN for you. Uncomment the mysql:
# block to enable.
#
# mysql:
#   host: "127.0.0.1"                      # MySQL server hostname or IP
#   port: 3306                             # MySQL TCP port (3306 by default)
#   user: "psp"                            # MySQL account
#   password: "CHANGE_ME"                  # MySQL password — keep this file 0600
#   database: "passwall"                   # database/schema name (must already exist)
#   params: ""                             # extra DSN params; blank uses the
#                                          # safe default below — only override
#                                          # if you know you need something else.
#                                          # Default: parseTime=true&charset=utf8mb4&loc=UTC
#
# Env override: PSP_MYSQL_DSN, if set, replaces whatever this block produces.
# Recommended for production so secrets stay out of the config file.
#
# Advanced / escape hatch: set "dsn:" to a raw go-sql-driver string when the
# discrete fields don't fit (unix socket, TLS, multi-host, etc.). When set,
# dsn wins over the discrete fields above.
#   dsn: "user:pw@unix(/tmp/mysql.sock)/db?parseTime=true"
#
# PostgreSQL: use the postgres: block below instead (discrete fields, same
# style as mysql:). Setting postgres.host or postgres.dsn selects the PG
# driver. Default port 5432; sslmode defaults to "disable" (set "require" /
# "verify-full" when the DB is reached over an untrusted network).
#
# postgres:
#   host: "127.0.0.1"                      # PostgreSQL server hostname or IP
#   port: 5432                             # PG TCP port (5432 by default)
#   user: "psp"                            # PG role
#   password: "CHANGE_ME"                  # PG password — keep this file 0600
#   database: "passwall"                   # database name (must already exist)
#   sslmode: "disable"                     # disable | require | verify-full | ...
#   # params: "connect_timeout=10"         # extra &k=v query params (optional)
#   # dsn: "postgres://psp:pw@127.0.0.1:5432/passwall?sslmode=disable"  # raw override
#
# Env override: PSP_POSTGRES_DSN, if set, replaces whatever the postgres block
# produces. Recommended for production so secrets stay out of the config file.
#
# Dev/single-user fallback: leave the mysql and postgres blocks commented out
# and the panel uses embedded SQLite at <data_dir>/panel.db. You can also force
# a specific SQLite path via the mysql dsn field:
#   dsn: "sqlite:./data/panel.db"
`
	encKey, err := randomBase64(32)
	if err != nil {
		return err
	}
	return os.WriteFile(abs, []byte(fmt.Sprintf(tpl, secret, encKey)), 0o600)
}

func randomBase64(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func Load(path string) (*Config, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	b, err := os.ReadFile(abs)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", abs, err)
	}
	var c Config
	if err := yaml.Unmarshal(b, &c); err != nil {
		return nil, fmt.Errorf("parse %s: %w", abs, err)
	}
	c.applyDefaults()

	// env overrides
	if dsn := os.Getenv("PSP_MYSQL_DSN"); dsn != "" {
		c.MySQL.DSN = dsn
	}
	if dsn := os.Getenv("PSP_POSTGRES_DSN"); dsn != "" {
		c.Postgres.DSN = dsn
	}
	if sec := os.Getenv("PSP_JWT_SECRET"); sec != "" {
		c.JWTSecret = sec
	}
	if key := os.Getenv("PSP_ENCRYPTION_KEY"); key != "" {
		c.EncryptionKey = key
	}
	if tp := os.Getenv("PSP_TRUSTED_PROXIES"); tp != "" {
		c.HTTP.TrustedProxies = tp
	}

	if err := c.validate(); err != nil {
		return nil, err
	}
	return &c, nil
}

// SecretKeyMaterial returns stable key material used to derive the AES-GCM key
// for database secrets. Existing configs without encryption_key fall back to
// jwt_secret for compatibility; new generated configs include a separate key.
func (c *Config) SecretKeyMaterial() string {
	if strings.TrimSpace(c.EncryptionKey) != "" {
		return c.EncryptionKey
	}
	return c.JWTSecret
}

func (c *Config) applyDefaults() {
	if c.Listen == "" {
		c.Listen = ":8788"
	}
	if c.ConfigDir == "" {
		c.ConfigDir = "./config"
	}
	if c.DataDir == "" {
		c.DataDir = "./data"
	}
}

func (c *Config) validate() error {
	if c.JWTSecret == "" {
		return fmt.Errorf("jwt_secret must be set (in config or env PSP_JWT_SECRET)")
	}
	// DSN is optional; empty falls back to SQLite at <DataDir>/panel.db.
	return nil
}

// DBKind returns the active database driver: "mysql", "postgres" or
// "sqlite".
//
// Selection rules, in order:
//  1. mysql.dsn with "sqlite:" prefix → sqlite
//  2. mysql.dsn with "postgres://" / "postgresql://" prefix → postgres
//  3. Any other non-empty mysql.dsn → mysql
//  4. postgres.dsn or postgres.host set → postgres (discrete fields)
//  5. mysql.host set → mysql (discrete fields)
//  6. Otherwise → sqlite (embedded zero-config fallback)
//
// The mysql.dsn field doubles as the universal raw-DSN escape hatch (it also
// carries sqlite: / postgres:// strings and the PSP_MYSQL_DSN env override),
// so it is checked before the discrete postgres block. If you populate both
// the postgres and mysql discrete blocks, postgres wins.
func (c *Config) DBKind() string {
	dsn := strings.TrimSpace(c.MySQL.DSN)
	switch {
	case strings.HasPrefix(dsn, "sqlite:"):
		return "sqlite"
	case strings.HasPrefix(dsn, "postgres://"), strings.HasPrefix(dsn, "postgresql://"):
		return "postgres"
	case dsn != "":
		return "mysql"
	case strings.TrimSpace(c.Postgres.DSN) != "" || strings.TrimSpace(c.Postgres.Host) != "":
		return "postgres"
	case strings.TrimSpace(c.MySQL.Host) != "":
		return "mysql"
	default:
		return "sqlite"
	}
}

// DBDSN returns the driver-specific connection string:
//   - For SQLite, a filesystem path (config "sqlite:..." prefix stripped, or
//     <DataDir>/panel.db when unset).
//   - For Postgres, the raw "postgres://..." URL passed through unchanged
//     (GORM's pgx driver consumes it directly).
//   - For MySQL, either the raw DSN or one assembled from the discrete
//     Host/Port/User/Password/Database/Params fields.
func (c *Config) DBDSN() string {
	dsn := strings.TrimSpace(c.MySQL.DSN)
	if strings.HasPrefix(dsn, "sqlite:") {
		return strings.TrimPrefix(dsn, "sqlite:")
	}
	if dsn != "" {
		// Raw mysql DSN, or a postgres:// URL passed through unchanged.
		return dsn
	}
	if pgDSN := strings.TrimSpace(c.Postgres.DSN); pgDSN != "" {
		return pgDSN
	}
	if strings.TrimSpace(c.Postgres.Host) != "" {
		return c.Postgres.assembleDSN()
	}
	if strings.TrimSpace(c.MySQL.Host) != "" {
		port := c.MySQL.Port
		if port <= 0 {
			port = 3306
		}
		params := strings.TrimSpace(c.MySQL.Params)
		if params == "" {
			params = "parseTime=true&charset=utf8mb4&loc=UTC"
		}
		return fmt.Sprintf("%s:%s@tcp(%s:%d)/%s?%s",
			c.MySQL.User, c.MySQL.Password,
			strings.TrimSpace(c.MySQL.Host), port,
			strings.TrimSpace(c.MySQL.Database), params)
	}
	return filepath.Join(c.DataDir, "panel.db")
}
