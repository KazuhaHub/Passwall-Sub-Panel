package migrate

// Minimal GORM struct definitions matching the legacy (≤ v2.5.x) schema.
// They live in this cmd-local package so the main panel program stays clean
// of any old-schema knowledge — this file gets deleted along with the rest
// of cmd/migrate-db-v2/ once the migration is signed off.
//
// We only declare the fields we actually copy or read. Other v2 columns
// (e.g. legacy panel_name on nodes/xui_clients/client_traffic_snapshots) are
// listed so SELECT * works on those tables, but the migration code below
// simply doesn't copy them forward into v3.

// ---- legacyUISettingsRow: the 30+ field "ui_settings" wide table ----

type legacyUISettingsRow struct {
	ID                         int64 `gorm:"primaryKey"`
	LoginMode                  string
	SiteTitle                  string
	AppTitle                   string
	IconURL                    string
	LogoURL                    string
	LogoURLDark                string
	EmailDomain                string
	AuditRetentionDays         int
	SubBaseURL                 string
	Timezone                   string
	CronTrafficPullMinutes     int
	CronReconcileMinutes       int
	MaxPanelConcurrency        int
	JWTAccessTTLMinutes        int
	JWTRefreshTTLMinutes       int
	JWTIssuer                  string
	SubPerIPPerMin             int
	LoginPerIPPerMin           int
	SyncTaskRetentionDays      int
	DisallowUserLocalLogin     bool
	DisallowUserPasswordChange bool
	AllowUserPersonalRules     bool
	EmergencyAccessEnabled     bool
	EmergencyAccessHours       int
	EmergencyAccessMaxCount    int
	EmergencyAccessQuotaGB     int
	SubPath                    string
	SubClientRules             string `gorm:"type:json"`
	SubImportClients           string `gorm:"type:json"`
	SubImportTutorialURL       string
	SubLogRetentionDays        int
	SubBlockAutoDisable        bool
	SubBlockAutoDisableCount   int
	SubUpdateIntervalHours     int
	SubRegionFlagPrefix        bool
	QuickLinks                 string `gorm:"type:json"`
	GlobalAnnouncement         string `gorm:"type:json"`
	FooterText                 string
	ThemeColor                 string
	UpdatedAt                  string // opaque shuttle
}

func (legacyUISettingsRow) TableName() string { return "ui_settings" }

// ---- legacyMailSettingsRow: still has expire_before_days + traffic_remain_percent ----

type legacyMailSettingsRow struct {
	ID                   int64 `gorm:"primaryKey"`
	Enabled              bool
	SMTPHost             string
	SMTPPort             int
	SMTPUsername         string
	SMTPPassword         string
	FromEmail            string
	FromName             string
	Encryption           string
	ExpireBeforeDays     int
	TrafficRemainPercent int
	UpdatedAt            string // opaque shuttle — written back to dst verbatim
}

func (legacyMailSettingsRow) TableName() string { return "mail_settings" }

// ---- legacySAMLConfigRow / legacyOIDCConfigRow: renamed in v3 ----

type legacySAMLConfigRow struct {
	ID                            int64 `gorm:"primaryKey"`
	Enabled                       bool
	Mode                          string
	SPEntityID                    string
	SPACSURL                      string
	SPCertPEM                     string
	SPKeyPEM                      string
	IDPMetadataURL                string
	IDPMetadataRefreshSec         int64
	AttrUPN                       string
	AttrEmail                     string
	AttrDisplayName               string
	AttrGroups                    string
	RoleRules                     string `gorm:"type:json"`
	DefaultGroupSlug              string
	AllowAutoCreate               bool
	NewUserExpireDays             int
	NewUserTrafficLimitBytes      int64
	NewUserTrafficResetPeriod     string
	UpdatedAt                     string // opaque shuttle
}

func (legacySAMLConfigRow) TableName() string { return "saml_config" }

type legacyOIDCConfigRow struct {
	ID                            int64 `gorm:"primaryKey"`
	Enabled                       bool
	IssuerURL                     string
	ClientID                      string
	ClientSecret                  string
	RedirectURL                   string
	Scopes                        string `gorm:"type:json"`
	AttrUsername                  string
	AttrEmail                     string
	AttrDisplayName               string
	AttrGroups                    string
	RoleRules                     string `gorm:"type:json"`
	DefaultGroupSlug              string
	AllowAutoCreate               bool
	NewUserExpireDays             int
	NewUserTrafficLimitBytes      int64
	NewUserTrafficResetPeriod     string
	UpdatedAt                     string // opaque shuttle
}

func (legacyOIDCConfigRow) TableName() string { return "oidc_config" }

// ---- legacyOwnershipRow: pre-rename xui_clients with panel_name ----

type legacyOwnershipRow struct {
	ID          int64 `gorm:"primaryKey;autoIncrement"`
	UserID      int64
	PanelID     int64
	PanelName   string // dropped in v3
	InboundID   int
	ClientEmail string
	ClientUUID  string
	CreatedAt   string // opaque shuttle
}

func (legacyOwnershipRow) TableName() string { return "xui_clients" }

// ---- legacyClientTrafficRow: still keyed by raw counters; used for LastRaw seeding ----

type legacyClientTrafficRow struct {
	ID          int64 `gorm:"primaryKey;autoIncrement"`
	UserID      int64
	PanelID     int64
	InboundID   int
	ClientEmail string
	UpBytes     int64
	DownBytes   int64
	TotalBytes  int64
	CapturedAt  string // opaque shuttle (we read up/down/total only)
}

func (legacyClientTrafficRow) TableName() string { return "client_traffic_snapshots" }
