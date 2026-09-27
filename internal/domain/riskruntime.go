package domain

import "time"

// The risk worker's fleet-wide knobs: what used to be constants in the
// worker loop, the fetch window, the login read and the bell, and is now an
// admin setting with the constant as its shipped default.
//
// Fleet-wide, not per group, each for a reason that does not depend on who
// is looking: the loop runs once for the whole fleet on one cadence, the
// fetch window is streamed once per run for every account together, the
// login log is read in one keyset pass, the bell counts the fleet,
// usage_shift reads one fleet series whose days every account's series must
// share, connection_history and flag_records are each pruned by one hourly
// pass over every account's rows, and the risk center's live view is one
// snapshot of the fleet with one refresh button. A group value would be
// stored, shown and never read, so none of these is in
// ports.OverridableScopeKeys.
// (login_country's warm-up and hold, and usage_shift's warm-up and
// over-days, ARE per group: they are judging thresholds, in RiskPolicy.)
//
// The bounds below are not policy. They keep a knob inside the range where
// it still means what its name says, and each one is argued at the
// constant. A stored value outside them is clamped when read, never rejected
// at the form: this file is the one place a stored value is interpreted,
// the same bargain GeoRuntimeFromSettings makes.
const (
	// RiskDefaultRefreshMinutes is how often the signals are recomputed.
	// The signals are day-scale (a week of fetches, weeks of traffic), so
	// an hour is fresh enough. RiskRefreshMinMinutes is a DB-cost bound:
	// every run streams a week of sub_logs and reads weeks of hourly rows
	// per account. RiskRefreshMaxMinutes is a day, so a flag is never more
	// than a day stale.
	RiskDefaultRefreshMinutes = 60
	RiskRefreshMinMinutes     = 10
	RiskRefreshMaxMinutes     = 1440
	// RiskDefaultFirstDelayMinutes is how long after start the first run
	// waits, so it does not compete with the boot probes and the first
	// infrastructure refresh. RiskFirstDelayMaxMinutes: a longer wait leaves
	// a fresh install's risk view empty for over an hour.
	RiskDefaultFirstDelayMinutes = 2
	RiskFirstDelayMaxMinutes     = 60
	// RiskDefaultAlertFreshnessHours is how long a flag nobody re-judged
	// keeps the bell lit. RiskAlertFreshnessMaxHours is a month: an older
	// latch is history, not something to look at now.
	RiskDefaultAlertFreshnessHours = 24
	RiskAlertFreshnessMaxHours     = 720
	// RiskDefaultWindowDays is the fetch window a fresh install reads. Its
	// ceiling is RiskWindowDays, which is structural, not policy: every day
	// mask is a uint8.
	RiskDefaultWindowDays = 7
	// RiskLoginLookbackMinDays is the shipped hold: a lookback shorter than
	// the recent days would read a recent login with no history behind it.
	// RiskLoginLookbackMaxDays is a year, which bounds what one run holds
	// in memory. RiskLoginLookbackDays (90) stays the default.
	RiskLoginLookbackMinDays = 7
	RiskLoginLookbackMaxDays = 365
	// RiskLoginWarmupMaxLogins bounds login_country's per-group warm-up:
	// past it, a signal that expects rare panel logins never finishes
	// learning, and "learning" would silently mean "off".
	RiskLoginWarmupMaxLogins = 50
	// usage_shift's series lengths; RiskUsageBaselineDays (28) and
	// RiskUsageRecentDays (7) are the defaults. The baseline is at least two
	// weeks — two of every weekday, so a median is not one weekend's — and
	// at most eight, which bounds the hourly rows one run reads per account.
	// The recent days are at least three, room for the over-day floor of two
	// to mean "more than a download", and at most two weeks: a flag is
	// about what the account does now.
	RiskUsageBaselineMinDays = 14
	RiskUsageBaselineMaxDays = 56
	RiskUsageRecentMinDays   = 3
	RiskUsageRecentMaxDays   = 14
	// How long connection_history keeps a source after it was last seen.
	// That table is the one place new code stores an IP address, so its
	// retention is a privacy promise: a week by default, and at most
	// RiskConnectionRetentionMaxDays, a bound an owner who needs longer has
	// to raise in code. Unset or negative is the week, never "keep
	// forever" — the deliberate opposite of sub_log_retention_days, whose 0
	// keeps fetches for ever: an IP-bearing table must always age out.
	RiskDefaultConnectionRetentionDays = 7
	RiskConnectionRetentionMaxDays     = 90
	// How long flag_records keeps an attention change. The history is what
	// an admin reads to answer "when was this account flagged, and why", so
	// it outlasts the connection history: 90 days by default. It holds no
	// address, so RiskFlagRecordRetentionMaxDays (ten years) bounds growth
	// rather than exposure. Unset or negative is the default, never "keep
	// forever", for the connection history's reason: everything this
	// detector writes ages out.
	RiskDefaultFlagRecordRetentionDays = 90
	RiskFlagRecordRetentionMaxDays     = 3650
	// The risk center's live view (实时连接).
	//
	// RiskDefaultLiveSnapshotStaleMinutes is how old the snapshot on show
	// may get before the view says so: three default polls. The view floors
	// it at two poll intervals on top (SnapshotStaleAfter), since a poll
	// snapshot is only replaced by the next poll. RiskLiveSnapshotStaleMaxMinutes
	// is a day: an older reading is not "live" by any reading of the word.
	RiskDefaultLiveSnapshotStaleMinutes = 15
	RiskLiveSnapshotStaleMaxMinutes     = 1440
	// RiskDefaultLiveRefreshCooldownSeconds is how long one 立即刷新 holds
	// off the next, for everyone: a refresh reads every panel's live
	// connections, so it is rationed fleet-wide, never per admin or per
	// account. RiskLiveRefreshCooldownMinSeconds protects the panels: five
	// seconds is half of 3X-UI's own ten-second rescan, and clicking faster
	// mostly reads the same scan again. RiskLiveRefreshCooldownMaxSeconds is
	// an hour, past which the button is decoration.
	RiskDefaultLiveRefreshCooldownSeconds = 30
	RiskLiveRefreshCooldownMinSeconds     = 5
	RiskLiveRefreshCooldownMaxSeconds     = 3600
	// RiskDefaultDeviceInferHours is how far back the view looks in the
	// fetch log for the fetches it infers a connection's device from: the
	// same account fetching from the same source. A day covers a client
	// that refreshes its subscription daily. RiskDeviceInferMaxHours is a
	// week, the fetch window's own ceiling; a shorter sub-log retention
	// shortens it where the log is read, never here.
	RiskDefaultDeviceInferHours = 24
	RiskDeviceInferMaxHours     = 168
)

// RiskRuntimeSettings is the flat, storage-shaped form: what the admin form
// saves, with 0 (or a negative number) meaning "never configured".
// ports.UISettings.RiskRuntimeSettings is the one mapping into it.
type RiskRuntimeSettings struct {
	RefreshIntervalMinutes, FirstDelayMinutes, AlertFreshnessHours,
	WindowDays, LoginLookbackDays, UsageBaselineDays, UsageRecentDays int
	ConnectionRetentionDays, FlagRecordRetentionDays                       int
	LiveSnapshotStaleMinutes, LiveRefreshCooldownSeconds, DeviceInferHours int
}

// RiskRuntime is the sanitised form every reader uses. Each field is inside
// its bounds; nothing downstream asks whether a value was meant.
//
// It carries the CONFIGURED values. The sub-log and auth-event retentions
// shorten the fetch window and the login lookback where the logs are read,
// never here: RiskPolicy.Bounded clamps a group's min_days to WindowDays,
// and clamping it to a retention-shortened window would hide
// retention_short — the code that exists to say the logs are too short for
// min_days.
type RiskRuntime struct {
	// RefreshInterval: the worker's cadence. 10..1440 min.
	RefreshInterval time.Duration
	// FirstDelay: how long after start the first run waits. 1..60 min.
	FirstDelay time.Duration
	// AlertFreshness: how long a flag nobody re-judged keeps the bell lit.
	// 1..720 h, and never less than two RefreshIntervals (see
	// RiskRuntimeFromSettings); the geo entry is floored by the poll
	// interval on top (GeoBellFreshness).
	AlertFreshness time.Duration
	// WindowDays: the fetch window the place and device signals read, in
	// panel-local days. 1..RiskWindowDays.
	WindowDays int
	// LoginLookbackDays: how far back login_country reads the login log.
	// RiskLoginLookbackMinDays..RiskLoginLookbackMaxDays.
	LoginLookbackDays int
	// UsageBaselineDays and UsageRecentDays: usage_shift's series, the days
	// an account's median is taken over and the days after them that are
	// judged. Fleet-wide because the worker reads one fleet series per run
	// and every account's fleet factor comes from it. 14..56 and 3..14.
	UsageBaselineDays, UsageRecentDays int
	// ConnectionRetentionDays: how many days connection_history keeps a
	// source after its LAST sighting. 1..RiskConnectionRetentionMaxDays.
	// Fleet-wide because one hourly pass prunes every account's rows.
	ConnectionRetentionDays int
	// FlagRecordRetentionDays: how many days flag_records keeps a record.
	// 1..RiskFlagRecordRetentionMaxDays. Fleet-wide for the same reason.
	FlagRecordRetentionDays int
	// LiveSnapshotStale: how old the live view's snapshot may get before
	// the view warns. 1..1440 min; floored by the poll interval where it is
	// read (SnapshotStaleAfter). Fleet-wide: there is one snapshot.
	LiveSnapshotStale time.Duration
	// LiveRefreshCooldown: how long one on-demand refresh holds off the
	// next, fleet-wide. 5..3600 s.
	LiveRefreshCooldown time.Duration
	// DeviceInferWindow: how far back the view reads the fetch log to infer
	// a connection's device. 1..168 h; a shorter sub-log retention shortens
	// it where the log is read.
	DeviceInferWindow time.Duration
}

// DefaultRiskRuntime is the runtime a fresh install runs with: exactly the
// constants these knobs replaced, so an upgrade changes nothing — plus the
// connection history's week and the flag records' 90 days, tables no earlier
// build had.
func DefaultRiskRuntime() RiskRuntime {
	return RiskRuntime{
		RefreshInterval:   RiskDefaultRefreshMinutes * time.Minute,
		FirstDelay:        RiskDefaultFirstDelayMinutes * time.Minute,
		AlertFreshness:    RiskDefaultAlertFreshnessHours * time.Hour,
		WindowDays:        RiskDefaultWindowDays,
		LoginLookbackDays: RiskLoginLookbackDays,
		UsageBaselineDays: RiskUsageBaselineDays,
		UsageRecentDays:   RiskUsageRecentDays,

		ConnectionRetentionDays: RiskDefaultConnectionRetentionDays,
		FlagRecordRetentionDays: RiskDefaultFlagRecordRetentionDays,

		LiveSnapshotStale:   RiskDefaultLiveSnapshotStaleMinutes * time.Minute,
		LiveRefreshCooldown: RiskDefaultLiveRefreshCooldownSeconds * time.Second,
		DeviceInferWindow:   RiskDefaultDeviceInferHours * time.Hour,
	}
}

// RiskRuntimeFromSettings turns stored settings into a runtime that is safe
// to run with: unset is the default, anything else is clamped to its bounds.
//
// Unset has to be the default and not the zero, for the reason
// GeoRuntimeFromSettings gives: a fresh install stores nothing, and every
// one of these read literally is broken — a zero-length ticker (which
// panics), a first run racing the boot probes, a bell that forgets a flag
// the moment it is written, a window of no days, a login log read over no
// time, a usage median over no baseline and no days judged, and a
// connection history or a flag history pruned to nothing (or, read the way
// the other retention settings read 0, kept for ever), a live view stale the
// moment it is taken, a refresh button with no cooldown, and a device
// inference over no fetches.
//
// The bell's freshness is raised to two refresh intervals. The worker
// rewrites every row once per run, so a window shorter than two runs would
// drop a latched flag off the bell between one run and the next and bring
// it back — a lit, dark, lit bell for an account nothing changed about. The
// raised value IS the value in effect, for both bell entries: one knob, one
// number an admin can check.
func RiskRuntimeFromSettings(s RiskRuntimeSettings) RiskRuntime {
	refresh := time.Duration(settingOr(s.RefreshIntervalMinutes, RiskDefaultRefreshMinutes, RiskRefreshMinMinutes, RiskRefreshMaxMinutes)) * time.Minute
	fresh := time.Duration(settingOr(s.AlertFreshnessHours, RiskDefaultAlertFreshnessHours, 1, RiskAlertFreshnessMaxHours)) * time.Hour
	return RiskRuntime{
		RefreshInterval:   refresh,
		FirstDelay:        time.Duration(settingOr(s.FirstDelayMinutes, RiskDefaultFirstDelayMinutes, 1, RiskFirstDelayMaxMinutes)) * time.Minute,
		AlertFreshness:    max(fresh, 2*refresh),
		WindowDays:        settingOr(s.WindowDays, RiskDefaultWindowDays, 1, RiskWindowDays),
		LoginLookbackDays: settingOr(s.LoginLookbackDays, RiskLoginLookbackDays, RiskLoginLookbackMinDays, RiskLoginLookbackMaxDays),
		UsageBaselineDays: settingOr(s.UsageBaselineDays, RiskUsageBaselineDays, RiskUsageBaselineMinDays, RiskUsageBaselineMaxDays),
		UsageRecentDays:   settingOr(s.UsageRecentDays, RiskUsageRecentDays, RiskUsageRecentMinDays, RiskUsageRecentMaxDays),

		ConnectionRetentionDays: settingOr(s.ConnectionRetentionDays, RiskDefaultConnectionRetentionDays, 1, RiskConnectionRetentionMaxDays),
		FlagRecordRetentionDays: settingOr(s.FlagRecordRetentionDays, RiskDefaultFlagRecordRetentionDays, 1, RiskFlagRecordRetentionMaxDays),

		LiveSnapshotStale: time.Duration(settingOr(s.LiveSnapshotStaleMinutes, RiskDefaultLiveSnapshotStaleMinutes, 1, RiskLiveSnapshotStaleMaxMinutes)) * time.Minute,
		LiveRefreshCooldown: time.Duration(settingOr(s.LiveRefreshCooldownSeconds, RiskDefaultLiveRefreshCooldownSeconds,
			RiskLiveRefreshCooldownMinSeconds, RiskLiveRefreshCooldownMaxSeconds)) * time.Second,
		DeviceInferWindow: time.Duration(settingOr(s.DeviceInferHours, RiskDefaultDeviceInferHours, 1, RiskDeviceInferMaxHours)) * time.Hour,
	}
}

// GeoBellFreshness is the concurrent-location bell entry's window: the
// configured freshness, raised to two traffic-poll intervals. The poll
// re-judges every latched account once per poll, so a shorter window would
// flicker a latch off the bell between two polls. The poll interval has no
// ceiling (cron_traffic_pull_minutes is checked only for < 0), which is why
// the floor is computed here and not assumed below the freshness.
func (rt RiskRuntime) GeoBellFreshness(poll time.Duration) time.Duration {
	return max(rt.AlertFreshness, 2*poll)
}

// SnapshotStaleAfter is the age past which the live view calls its snapshot
// stale: the configured staleness, raised to two traffic-poll intervals. A
// poll snapshot is replaced only by the next poll, so at a 30-minute poll a
// 20-minute-old snapshot is simply the latest one; warning about it would
// teach an admin to skip the warning that means the poll has stopped.
func (rt RiskRuntime) SnapshotStaleAfter(poll time.Duration) time.Duration {
	return max(rt.LiveSnapshotStale, 2*poll)
}
