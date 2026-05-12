// Package config loads the main YAML configuration plus mandatory env vars.
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Config is the top-level panel configuration. All intervals are stored in
// minutes for human-friendliness; Duration accessors convert to time.Duration.
type Config struct {
	Listen     string `yaml:"listen"`
	JWTSecret  string `yaml:"jwt_secret"`
	SubBaseURL string `yaml:"sub_base_url"`

	MySQL MySQLConfig `yaml:"mysql"`

	ConfigDir string `yaml:"config_dir"`
	DataDir   string `yaml:"data_dir"`

	Cron CronConfig `yaml:"cron"`

	// Paths to side configs that contain sensitive data; kept in separate
	// files so they can have stricter file permissions.
	XUIPanelsFile string `yaml:"xui_panels_file"`
	SAMLFile      string `yaml:"saml_file"`

	JWT       JWTConfig       `yaml:"jwt"`
	RateLimit RateLimitConfig `yaml:"rate_limit"`

	NewUserDefaults NewUserDefaults `yaml:"new_user_defaults"`
}

type MySQLConfig struct {
	// DSN drives database selection. Three accepted shapes:
	//
	//   - Empty / unset       → SQLite at <DataDir>/panel.db (zero-config default)
	//   - "sqlite:./path.db"  → SQLite at the given path
	//   - "user:pw@tcp(...)"  → MySQL via the standard go-sql-driver DSN
	//
	// May also be supplied via env PSP_MYSQL_DSN, which takes precedence.
	// The field is named "MySQL" for historical reasons; SQLite is fully
	// supported via the same setting.
	DSN string `yaml:"dsn"`
}

type CronConfig struct {
	TrafficPullMinutes int `yaml:"traffic_pull_minutes"` // L2 default 5
	ReconcileMinutes   int `yaml:"reconcile_minutes"`    // L3 default 15
}

type JWTConfig struct {
	AccessTTLMinutes  int    `yaml:"access_ttl_minutes"`  // default 120
	RefreshTTLMinutes int    `yaml:"refresh_ttl_minutes"` // default 10080 (7d)
	Issuer            string `yaml:"issuer"`              // default "passwall-sub-panel"
}

type RateLimitConfig struct {
	SubPerIPPerMin   int `yaml:"sub_per_ip_per_min"`   // default 60
	LoginPerIPPerMin int `yaml:"login_per_ip_per_min"` // default 5
}

type NewUserDefaults struct {
	ExpireDays         int    `yaml:"expire_days"`           // default 30; 0 = permanent
	TrafficLimitGB     int64  `yaml:"traffic_limit_gb"`      // default 0 (unlimited)
	TrafficResetPeriod string `yaml:"traffic_reset_period"`  // default "monthly"
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
	if sec := os.Getenv("PSP_JWT_SECRET"); sec != "" {
		c.JWTSecret = sec
	}

	if err := c.validate(); err != nil {
		return nil, err
	}
	return &c, nil
}

func (c *Config) applyDefaults() {
	if c.Listen == "" {
		c.Listen = ":8787"
	}
	if c.ConfigDir == "" {
		c.ConfigDir = "./config"
	}
	if c.DataDir == "" {
		c.DataDir = "./data"
	}
	if c.XUIPanelsFile == "" {
		c.XUIPanelsFile = filepath.Join(c.ConfigDir, "xui_panels.yaml")
	}
	if c.SAMLFile == "" {
		c.SAMLFile = filepath.Join(c.ConfigDir, "saml.yaml")
	}
	if c.Cron.TrafficPullMinutes <= 0 {
		c.Cron.TrafficPullMinutes = 5
	}
	if c.Cron.ReconcileMinutes <= 0 {
		c.Cron.ReconcileMinutes = 15
	}
	if c.JWT.AccessTTLMinutes <= 0 {
		c.JWT.AccessTTLMinutes = 120
	}
	if c.JWT.RefreshTTLMinutes <= 0 {
		c.JWT.RefreshTTLMinutes = 60 * 24 * 7
	}
	if c.JWT.Issuer == "" {
		c.JWT.Issuer = "passwall-sub-panel"
	}
	if c.RateLimit.SubPerIPPerMin <= 0 {
		c.RateLimit.SubPerIPPerMin = 60
	}
	if c.RateLimit.LoginPerIPPerMin <= 0 {
		c.RateLimit.LoginPerIPPerMin = 5
	}
	if c.NewUserDefaults.ExpireDays == 0 {
		c.NewUserDefaults.ExpireDays = 30
	}
	if c.NewUserDefaults.TrafficResetPeriod == "" {
		c.NewUserDefaults.TrafficResetPeriod = "monthly"
	}
}

func (c *Config) validate() error {
	if c.JWTSecret == "" {
		return fmt.Errorf("jwt_secret must be set (in config or env PSP_JWT_SECRET)")
	}
	// DSN is optional; empty falls back to SQLite at <DataDir>/panel.db.
	return nil
}

// DBKind returns the active database driver: "mysql" or "sqlite".
func (c *Config) DBKind() string {
	dsn := strings.TrimSpace(c.MySQL.DSN)
	if dsn == "" || strings.HasPrefix(dsn, "sqlite:") {
		return "sqlite"
	}
	return "mysql"
}

// DBDSN returns the driver-specific connection string:
//   - For SQLite, a filesystem path (config "sqlite:..." prefix stripped, or
//     <DataDir>/panel.db when unset).
//   - For MySQL, the configured DSN verbatim.
func (c *Config) DBDSN() string {
	dsn := strings.TrimSpace(c.MySQL.DSN)
	if dsn == "" {
		return filepath.Join(c.DataDir, "panel.db")
	}
	if strings.HasPrefix(dsn, "sqlite:") {
		return strings.TrimPrefix(dsn, "sqlite:")
	}
	return dsn
}

func (c *Config) TrafficPullInterval() time.Duration {
	return time.Duration(c.Cron.TrafficPullMinutes) * time.Minute
}

func (c *Config) ReconcileInterval() time.Duration {
	return time.Duration(c.Cron.ReconcileMinutes) * time.Minute
}

func (c *Config) AccessTTL() time.Duration {
	return time.Duration(c.JWT.AccessTTLMinutes) * time.Minute
}

func (c *Config) RefreshTTL() time.Duration {
	return time.Duration(c.JWT.RefreshTTLMinutes) * time.Minute
}
