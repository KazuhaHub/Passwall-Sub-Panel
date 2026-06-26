// Package mailer owns SMTP delivery and reminder scheduling.
package mailer

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	htmltemplate "html/template"
	"mime"
	"net"
	"net/mail"
	"net/smtp"
	"strconv"
	"strings"
	"text/template"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/log"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/safego"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

type Service struct {
	repo     ports.MailRepo
	users    ports.UserRepo
	traffic  ports.TrafficRepo
	settings ports.ScopedSettings
	tasks    ports.SyncTaskRepo
}

type TemplatePreview struct {
	Subject string `json:"subject"`
	Body    string `json:"body"`
}

func New(repo ports.MailRepo, users ports.UserRepo, traffic ports.TrafficRepo, settings ports.ScopedSettings, tasks ports.SyncTaskRepo) *Service {
	return &Service{repo: repo, users: users, traffic: traffic, settings: settings, tasks: tasks}
}

func DefaultSettings() domain.MailSettings {
	return domain.MailSettings{
		SMTPPort:   587,
		Encryption: "starttls",
	}
}

// MailNotifyPayload is the JSON payload stored in SyncTask for mail notifications.
type MailNotifyPayload struct {
	UserID       int64  `json:"user_id"`
	TemplateKind string `json:"template_kind"`
	Reason       string `json:"reason,omitempty"`
	Detail       string `json:"detail,omitempty"`
}

// enqueueMailNotify creates a sync task for mail notification when sending fails.
func (s *Service) enqueueMailNotify(ctx context.Context, userID int64, kind, reason, detail string) {
	if s.tasks == nil {
		return
	}
	// Check if there's already an active task for this user+kind.
	if _, err := s.tasks.GetActiveByTarget(ctx, domain.SyncTaskMailNotify, fmt.Sprintf("user:%s", kind), userID); err == nil {
		return // Already queued.
	} else if !errors.Is(err, domain.ErrNotFound) {
		log.Warn("mail enqueue check", "user_id", userID, "err", err)
		return
	}

	payload := MailNotifyPayload{
		UserID:       userID,
		TemplateKind: kind,
		Reason:       reason,
		Detail:       detail,
	}
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		log.Warn("mail enqueue marshal", "user_id", userID, "err", err)
		return
	}

	if err := s.tasks.Create(ctx, &domain.SyncTask{
		Type:       domain.SyncTaskMailNotify,
		Status:     domain.SyncTaskPending,
		TargetType: fmt.Sprintf("user:%s", kind),
		TargetID:   userID,
		Summary:    fmt.Sprintf("send %s notification to user %d", kind, userID),
		Payload:    string(payloadBytes),
		NextRunAt:  time.Now(),
	}); err != nil {
		log.Warn("mail enqueue create", "user_id", userID, "err", err)
	}
}

// ProcessDueMailTasks processes pending mail notification tasks.
func (s *Service) ProcessDueMailTasks(ctx context.Context, limit int) error {
	if s.tasks == nil {
		return nil
	}
	tasks, err := s.tasks.ListDue(ctx, time.Now(), limit)
	if err != nil {
		return err
	}
	for _, task := range tasks {
		if task.Type != domain.SyncTaskMailNotify {
			continue
		}
		claimed, err := s.tasks.MarkRunning(ctx, task.ID)
		if err != nil {
			log.Warn("mail task mark-running", "task_id", task.ID, "err", err)
			continue
		}
		if !claimed {
			// Canceled (or claimed by another runner) between ListDue and here — skip.
			continue
		}
		if err := s.processMailTask(ctx, task); err != nil {
			// Terminal failures stop re-running forever (the pre-fix bug: MarkRetry
			// never set a terminal status, so a broken SMTP relay / bad payload
			// produced an immortal sync_task re-attempted every hourly loop). Cancel
			// on a permanent error (bad payload / unknown template kind) or once the
			// retry cap is hit, mirroring the user/node processors.
			if errors.Is(err, domain.ErrValidation) || task.Attempts+1 >= maxMailTaskAttempts {
				log.Warn("mail task gave up",
					"task_id", task.ID, "attempts", task.Attempts+1, "last_err", err.Error())
				if markErr := s.tasks.Cancel(ctx, task.ID); markErr != nil {
					log.Warn("mail task cancel", "task_id", task.ID, "err", markErr)
				}
				continue
			}
			next := time.Now().Add(mailTaskBackoff(task.Attempts + 1))
			if markErr := s.tasks.MarkRetry(ctx, task.ID, err.Error(), next); markErr != nil {
				log.Warn("mail task mark-retry", "task_id", task.ID, "err", markErr)
			}
			continue
		}
		if err := s.tasks.MarkSucceeded(ctx, task.ID); err != nil {
			log.Warn("mail task mark-succeeded", "task_id", task.ID, "err", err)
		}
	}
	return nil
}

func (s *Service) processMailTask(ctx context.Context, task *domain.SyncTask) error {
	var payload MailNotifyPayload
	if err := json.Unmarshal([]byte(task.Payload), &payload); err != nil {
		// Permanent: a payload that won't unmarshal now never will. Classify as
		// validation so ProcessDueMailTasks cancels instead of retrying forever.
		return fmt.Errorf("%w: invalid mail payload: %v", domain.ErrValidation, err)
	}
	u, err := s.users.GetByID(ctx, payload.UserID)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return nil // User deleted, skip.
		}
		return err
	}
	switch payload.TemplateKind {
	case "account_disabled":
		return s.SendAccountDisabledNotification(ctx, u, payload.Reason, payload.Detail)
	case "account_enabled":
		return s.SendAccountEnabledNotification(ctx, u, payload.Reason, payload.Detail)
	case "service_suspended":
		return s.SendServiceSuspendedNotification(ctx, u, payload.Reason, payload.Detail)
	case "service_restored":
		return s.SendServiceRestoredNotification(ctx, u)
	default:
		// Permanent: an unknown template kind is a code/enqueue bug, never
		// recoverable by retrying. Classify as validation so it's cancelled.
		return fmt.Errorf("%w: unknown template kind: %s", domain.ErrValidation, payload.TemplateKind)
	}
}

// maxMailTaskAttempts caps mail-notification retries, mirroring
// maxUserTaskAttempts / maxNodeTaskAttempts. Without it a permanently-failing
// SMTP relay produces an immortal sync_task that re-runs every hourly loop and
// is never pruned (MarkRetry never sets a terminal status).
const maxMailTaskAttempts = 100

// mailTaskBackoff returns retry interval for mail tasks.
func mailTaskBackoff(attempt int) time.Duration {
	// Exponential backoff: 1m, 2m, 4m, 8m, 16m, capped at 30m. Clamp the shift
	// so a large attempt count can't overflow int64 (1<<(attempt-1) wraps
	// negative around attempt 29+, making the cap check false and the task
	// immediately-due forever). attempt>=6 already saturates the 30m cap.
	shift := attempt - 1
	if shift < 0 {
		shift = 0
	}
	if shift > 5 {
		shift = 5
	}
	interval := time.Minute * time.Duration(int64(1)<<uint(shift))
	if interval > 30*time.Minute {
		interval = 30 * time.Minute
	}
	return interval
}

func DefaultTemplates() []*domain.MailTemplate {
	loginRow := emailRow("登录名", "{{.UPN}}", false)
	return []*domain.MailTemplate{
		{
			Kind:    domain.MailReminderExpireBefore,
			Enabled: true,
			Subject: "订阅将在 {{.ExpireBeforeDays}} 天内到期",
			Body: defaultHTMLTemplate(
				"订阅即将到期",
				"你的订阅将在 {{.ExpireAt}} 到期。请及时联系管理员完成续期，避免服务中断。",
				loginRow+emailRow("到期时间", "{{.ExpireAt}}", true)+emailRow("生成时间", "{{.GeneratedAt}}", true),
			),
		},
		{
			Kind:    domain.MailReminderExpired,
			Enabled: true,
			Subject: "订阅已到期",
			Body: defaultHTMLTemplate(
				"订阅已到期",
				"你的订阅已在 {{.ExpireAt}} 到期。如需继续使用，请联系管理员续期。",
				loginRow+emailRow("到期时间", "{{.ExpireAt}}", true)+emailRow("生成时间", "{{.GeneratedAt}}", true),
			),
		},
		{
			Kind:    domain.MailReminderTrafficLow,
			Enabled: true,
			Subject: "剩余流量低于 {{.TrafficRemainPercent}}%",
			Body: defaultHTMLTemplate(
				"剩余流量不足",
				"你的本周期剩余流量已低于 {{.TrafficRemainPercent}}%。请合理安排使用，或联系管理员调整套餐。",
				loginRow+emailRow("剩余流量", "{{.TrafficRemainGB}} GB", true)+emailRow("生成时间", "{{.GeneratedAt}}", true),
			),
		},
		{
			Kind:    domain.MailReminderTrafficExhausted,
			Enabled: true,
			Subject: "本期流量已用完",
			Body: defaultHTMLTemplate(
				"流量已用完",
				"你的本期流量已用完，代理服务已暂停（账号未被禁用，仍可登录面板查看 / 处理）。服务将在下个计费周期开始时自动恢复；如需立即继续使用，可以在面板申请紧急访问或联系管理员。",
				loginRow+
					emailRow("已用流量", "{{.PeriodUsedGB}} / {{.TrafficLimitGB}} GB", true)+
					emailRow("暂停时间", "{{.GeneratedAt}}", true),
			),
		},
		{
			Kind:    domain.MailReminderAccountDisable,
			Enabled: true,
			Subject: "账号已被停用",
			Body: defaultHTMLTemplate(
				"账号已停用",
				"你的账号已被停用。",
				loginRow+`{{if .DisableDetail}}`+emailRow("停用原因", "{{.DisableDetail}}", true)+`{{end}}`+emailRow("停用时间", "{{.GeneratedAt}}", true),
			),
		},
		{
			Kind:    domain.MailReminderAccountEnable,
			Enabled: true,
			Subject: "账号已恢复正常",
			Body: defaultHTMLTemplate(
				"账号已恢复",
				"你的账号已恢复正常，可以继续使用订阅服务。",
				loginRow+`{{if .EnableDetail}}`+emailRow("备注", "{{.EnableDetail}}", true)+`{{end}}`+emailRow("恢复时间", "{{.GeneratedAt}}", true),
			),
		},
		{
			Kind:    domain.MailReminderServiceSuspended,
			Enabled: true,
			Subject: "代理服务已暂停",
			Body: defaultHTMLTemplate(
				"服务已暂停",
				"你的代理 / 订阅服务已被暂停，订阅暂时无法使用。你仍然可以登录面板查看原因或自助处理 —— 账号本身没有被禁用。",
				loginRow+
					emailRow("暂停原因", "{{.SuspendReason}}", false)+
					`{{if .SuspendDetail}}`+emailRow("详情", "{{.SuspendDetail}}", true)+`{{end}}`+
					emailRow("暂停时间", "{{.GeneratedAt}}", true),
			),
		},
		{
			Kind:    domain.MailReminderServiceRestored,
			Enabled: true,
			Subject: "代理服务已恢复",
			Body: defaultHTMLTemplate(
				"服务已恢复",
				"你的代理 / 订阅服务已恢复，可以继续使用订阅。",
				loginRow+emailRow("恢复时间", "{{.GeneratedAt}}", true),
			),
		},
		{
			Kind:    domain.MailReminderAnnouncement,
			Enabled: true,
			Subject: "{{.AnnouncementTitle}}",
			Body: defaultHTMLTemplate(
				"{{.AnnouncementTitle}}",
				"{{.AnnouncementBodyHTML}}",
				loginRow+emailRow("公告时间", "{{.GeneratedAt}}", true),
			),
		},
		{
			Kind:    domain.MailReminderBlockedClient,
			Enabled: true,
			Subject: "检测到不被允许的客户端",
			Body: defaultHTMLTemplate(
				"客户端不被允许",
				"我们检测到你在使用不被允许 / 不受支持的客户端「{{.ClientName}}」拉取订阅，本次请求已被拒绝。请改用推荐的客户端；若反复使用被禁客户端，账号可能会被临时停用。",
				loginRow+emailRow("客户端", "{{.ClientName}}", false)+emailRow("时间", "{{.GeneratedAt}}", true),
			),
		},
		{
			Kind:    domain.MailReminderPasswordReset,
			Enabled: true,
			Subject: "重置你的密码",
			Body:    defaultPasswordResetTemplate(),
		},
		{
			Kind:    domain.MailReminderEmailVerify,
			Enabled: true,
			Subject: "验证你的邮箱地址",
			Body:    defaultEmailVerifyTemplate(),
		},
		{
			Kind:    domain.MailReminderLogin2FA,
			Enabled: true,
			Subject: "你的登录验证码",
			Body:    defaultLogin2FATemplate(),
		},
	}
}

// defaultEmailVerifyTemplate renders either a verification-link button (link
// delivery) or a prominent OTP code (otp delivery) for self-registration.
func defaultEmailVerifyTemplate() string {
	return `<!doctype html>
<html>
<body style="margin:0;background:#f4f6fb;padding:32px 16px;font-family:-apple-system,BlinkMacSystemFont,'Segoe UI',Arial,sans-serif;color:#111827;">
  <div style="max-width:640px;margin:0 auto;background:#ffffff;border:1px solid #e5e7eb;border-radius:12px;overflow:hidden;">
    <div style="padding:28px 32px;border-bottom:1px solid #eef2f7;background:#0f172a;">
      {{if .LogoURL}}<img src="{{.LogoURL}}" alt="{{.SiteTitle}}" style="height:42px;max-width:220px;object-fit:contain;display:block;margin-bottom:18px;">{{end}}
      <div style="font-size:13px;color:#94a3b8;letter-spacing:.08em;text-transform:uppercase;">{{.SiteTitle}}</div>
      <h1 style="margin:8px 0 0;font-size:24px;line-height:1.3;color:#ffffff;font-weight:700;">验证邮箱</h1>
    </div>
    <div style="padding:30px 32px;">
      <p style="margin:0 0 16px;font-size:15px;line-height:1.7;color:#374151;">{{if .DisplayName}}你好 {{.DisplayName}}，{{else}}你好，{{end}}</p>
      <p style="margin:0 0 24px;font-size:15px;line-height:1.7;color:#374151;">感谢注册。{{if .VerifyLink}}点击下方按钮即可完成邮箱验证并激活账号。{{else}}请在验证页面输入下面的验证码以完成验证。{{end}}此请求 {{.ExpireMinutes}} 分钟内有效。</p>
      {{if .VerifyLink}}
      <a href="{{.VerifyLink}}" style="display:inline-block;background:#2563eb;color:#ffffff;text-decoration:none;border-radius:8px;padding:12px 18px;font-size:14px;font-weight:700;">验证邮箱</a>
      <p style="margin:24px 0 0;font-size:12px;line-height:1.6;color:#6b7280;">如果按钮无法打开，请复制以下链接：<br><span style="word-break:break-all;color:#374151;">{{.VerifyLink}}</span></p>
      {{else}}
      <div style="margin:0 0 8px;font-size:32px;font-weight:700;letter-spacing:.3em;color:#111827;background:#f8fafc;border:1px solid #e5e7eb;border-radius:10px;padding:18px 0;text-align:center;">{{.OTPCode}}</div>
      {{end}}
      <p style="margin:24px 0 0;font-size:12px;line-height:1.6;color:#6b7280;">如果这不是你本人的操作，请忽略此邮件。</p>
    </div>
  </div>
</body>
</html>`
}

// defaultLogin2FATemplate renders the one-time login code emailed when the admin
// enables email as an alternative 2FA factor. Code-only (no link).
func defaultLogin2FATemplate() string {
	return `<!doctype html>
<html>
<body style="margin:0;background:#f4f6fb;padding:32px 16px;font-family:-apple-system,BlinkMacSystemFont,'Segoe UI',Arial,sans-serif;color:#111827;">
  <div style="max-width:640px;margin:0 auto;background:#ffffff;border:1px solid #e5e7eb;border-radius:12px;overflow:hidden;">
    <div style="padding:28px 32px;border-bottom:1px solid #eef2f7;background:#0f172a;">
      {{if .LogoURL}}<img src="{{.LogoURL}}" alt="{{.SiteTitle}}" style="height:42px;max-width:220px;object-fit:contain;display:block;margin-bottom:18px;">{{end}}
      <div style="font-size:13px;color:#94a3b8;letter-spacing:.08em;text-transform:uppercase;">{{.SiteTitle}}</div>
      <h1 style="margin:8px 0 0;font-size:24px;line-height:1.3;color:#ffffff;font-weight:700;">登录验证码</h1>
    </div>
    <div style="padding:30px 32px;">
      <p style="margin:0 0 16px;font-size:15px;line-height:1.7;color:#374151;">{{if .DisplayName}}你好 {{.DisplayName}}，{{else}}你好，{{end}}</p>
      <p style="margin:0 0 24px;font-size:15px;line-height:1.7;color:#374151;">我们收到了你的登录请求，请在登录页面输入下面的验证码以完成两步验证。此验证码 {{.ExpireMinutes}} 分钟内有效，仅可使用一次。</p>
      <div style="margin:0 0 8px;font-size:32px;font-weight:700;letter-spacing:.3em;color:#111827;background:#f8fafc;border:1px solid #e5e7eb;border-radius:10px;padding:18px 0;text-align:center;">{{.OTPCode}}</div>
      <p style="margin:24px 0 0;font-size:12px;line-height:1.6;color:#6b7280;">如果这不是你本人的操作，请立即修改密码——可能有人已经掌握了你的密码。</p>
    </div>
  </div>
</body>
</html>`
}

// defaultPasswordResetTemplate renders either a reset-link button (link
// delivery) or a prominent OTP code (otp delivery) depending on which template
// variable is populated. Built separately from defaultHTMLTemplate because its
// call-to-action is the reset action, not the generic "open panel" button.
func defaultPasswordResetTemplate() string {
	return `<!doctype html>
<html>
<body style="margin:0;background:#f4f6fb;padding:32px 16px;font-family:-apple-system,BlinkMacSystemFont,'Segoe UI',Arial,sans-serif;color:#111827;">
  <div style="max-width:640px;margin:0 auto;background:#ffffff;border:1px solid #e5e7eb;border-radius:12px;overflow:hidden;">
    <div style="padding:28px 32px;border-bottom:1px solid #eef2f7;background:#0f172a;">
      {{if .LogoURL}}<img src="{{.LogoURL}}" alt="{{.SiteTitle}}" style="height:42px;max-width:220px;object-fit:contain;display:block;margin-bottom:18px;">{{end}}
      <div style="font-size:13px;color:#94a3b8;letter-spacing:.08em;text-transform:uppercase;">{{.SiteTitle}}</div>
      <h1 style="margin:8px 0 0;font-size:24px;line-height:1.3;color:#ffffff;font-weight:700;">重置密码</h1>
    </div>
    <div style="padding:30px 32px;">
      <p style="margin:0 0 16px;font-size:15px;line-height:1.7;color:#374151;">{{if .DisplayName}}你好 {{.DisplayName}}，{{else}}你好，{{end}}</p>
      <p style="margin:0 0 24px;font-size:15px;line-height:1.7;color:#374151;">我们收到了重置你账号密码的请求。{{if .ResetLink}}点击下方按钮即可设置新密码。{{else}}请在重置页面输入下面的验证码以设置新密码。{{end}}此请求 {{.ExpireMinutes}} 分钟内有效。</p>
      {{if .ResetLink}}
      <a href="{{.ResetLink}}" style="display:inline-block;background:#2563eb;color:#ffffff;text-decoration:none;border-radius:8px;padding:12px 18px;font-size:14px;font-weight:700;">重置密码</a>
      <p style="margin:24px 0 0;font-size:12px;line-height:1.6;color:#6b7280;">如果按钮无法打开，请复制以下链接：<br><span style="word-break:break-all;color:#374151;">{{.ResetLink}}</span></p>
      {{else}}
      <div style="margin:0 0 8px;font-size:32px;font-weight:700;letter-spacing:.3em;color:#111827;background:#f8fafc;border:1px solid #e5e7eb;border-radius:10px;padding:18px 0;text-align:center;">{{.OTPCode}}</div>
      {{end}}
      <p style="margin:24px 0 0;font-size:12px;line-height:1.6;color:#6b7280;">如果这不是你本人的操作，请忽略此邮件，你的密码不会被更改。</p>
    </div>
  </div>
</body>
</html>`
}

// emailRow builds one <tr> for the metric table at the bottom of each email.
// hasTopBorder draws the divider that separates a row from the one above it;
// the first row in the table should pass false.
func emailRow(label, valueHTML string, hasTopBorder bool) string {
	border := ""
	if hasTopBorder {
		border = "border-top:1px solid #e5e7eb;"
	}
	return fmt.Sprintf(
		`<tr><td style="padding:14px 18px;color:#64748b;font-size:13px;%s">%s</td><td style="padding:14px 18px;text-align:right;font-size:14px;font-weight:600;color:#111827;%s">%s</td></tr>`,
		border, label, border, valueHTML,
	)
}

func defaultHTMLTemplate(title, message, rowsHTML string) string {
	return `<!doctype html>
<html>
<body style="margin:0;background:#f4f6fb;padding:32px 16px;font-family:-apple-system,BlinkMacSystemFont,'Segoe UI',Arial,sans-serif;color:#111827;">
  <div style="max-width:640px;margin:0 auto;background:#ffffff;border:1px solid #e5e7eb;border-radius:12px;overflow:hidden;">
    <div style="padding:28px 32px;border-bottom:1px solid #eef2f7;background:#0f172a;">
      {{if .LogoURL}}<img src="{{.LogoURL}}" alt="{{.SiteTitle}}" style="height:42px;max-width:220px;object-fit:contain;display:block;margin-bottom:18px;">{{end}}
      <div style="font-size:13px;color:#94a3b8;letter-spacing:.08em;text-transform:uppercase;">{{.SiteTitle}}</div>
      <h1 style="margin:8px 0 0;font-size:24px;line-height:1.3;color:#ffffff;font-weight:700;">` + title + `</h1>
    </div>
    <div style="padding:30px 32px;">
      <p style="margin:0 0 16px;font-size:15px;line-height:1.7;color:#374151;">{{if .DisplayName}}你好 {{.DisplayName}}，{{else}}你好，{{end}}</p>
      <p style="margin:0 0 24px;font-size:15px;line-height:1.7;color:#374151;">` + message + `</p>
      <table role="presentation" style="width:100%;border-collapse:collapse;margin:0 0 26px;background:#f8fafc;border:1px solid #e5e7eb;border-radius:10px;overflow:hidden;">` + rowsHTML + `</table>
      <a href="{{.PanelURL}}" style="display:inline-block;background:#2563eb;color:#ffffff;text-decoration:none;border-radius:8px;padding:12px 18px;font-size:14px;font-weight:700;">打开面板</a>
      {{if .PanelURL}}<p style="margin:24px 0 0;font-size:12px;line-height:1.6;color:#6b7280;">如果按钮无法打开，请复制以下链接：<br><span style="word-break:break-all;color:#374151;">{{.PanelURL}}</span></p>{{end}}
    </div>
  </div>
</body>
</html>`
}

func (s *Service) LoadSettings(ctx context.Context) (domain.MailSettings, error) {
	return s.repo.LoadSettings(ctx, DefaultSettings())
}

func (s *Service) SaveSettings(ctx context.Context, settings domain.MailSettings) error {
	settings.SMTPHost = strings.TrimSpace(settings.SMTPHost)
	settings.SMTPUsername = strings.TrimSpace(settings.SMTPUsername)
	settings.FromEmail = strings.TrimSpace(settings.FromEmail)
	settings.FromName = strings.TrimSpace(settings.FromName)
	settings.Encryption = strings.ToLower(strings.TrimSpace(settings.Encryption))
	switch settings.Encryption {
	case "", "none", "starttls", "tls":
	default:
		return fmt.Errorf("%w: encryption must be none, starttls, or tls", domain.ErrValidation)
	}
	if settings.Encryption == "" {
		settings.Encryption = "starttls"
	}
	if settings.SMTPPort <= 0 {
		settings.SMTPPort = 587
	}
	// Notify thresholds (ExpireBeforeDays / TrafficRemainPercent) now live in
	// the settings KV table under type='notify' — admins edit them via the
	// global settings page and validation runs there.
	return s.repo.SaveSettings(ctx, settings)
}

func (s *Service) ListTemplates(ctx context.Context) ([]*domain.MailTemplate, error) {
	existing, err := s.repo.ListTemplates(ctx)
	if err != nil {
		return nil, err
	}
	byKind := map[domain.MailReminderKind]*domain.MailTemplate{}
	for _, t := range existing {
		byKind[t.Kind] = t
	}
	out := DefaultTemplates()
	for i, d := range out {
		if t := byKind[d.Kind]; t != nil {
			out[i] = t
		}
	}
	return out, nil
}

// ResetTemplate overwrites the stored template for `kind` with the default
// shipped in DefaultTemplates(). Useful when an admin saved a template under
// an older panel version and wants to pull in newer copy / structure without
// editing the body by hand.
func (s *Service) ResetTemplate(ctx context.Context, kind domain.MailReminderKind) (*domain.MailTemplate, error) {
	if err := validateTemplateKind(kind); err != nil {
		return nil, err
	}
	for _, d := range DefaultTemplates() {
		if d.Kind != kind {
			continue
		}
		tpl := &domain.MailTemplate{
			Kind:    d.Kind,
			Subject: d.Subject,
			Body:    d.Body,
			Enabled: d.Enabled,
		}
		if err := s.repo.SaveTemplate(ctx, tpl); err != nil {
			return nil, err
		}
		return tpl, nil
	}
	return nil, fmt.Errorf("%w: no default template for kind %q", domain.ErrNotFound, kind)
}

func (s *Service) SaveTemplate(ctx context.Context, tpl *domain.MailTemplate) error {
	if tpl == nil {
		return fmt.Errorf("%w: template required", domain.ErrValidation)
	}
	if err := validateTemplateKind(tpl.Kind); err != nil {
		return err
	}
	tpl.Subject = strings.TrimSpace(tpl.Subject)
	if tpl.Subject == "" {
		return fmt.Errorf("%w: subject required", domain.ErrValidation)
	}
	if _, err := template.New("subject").Parse(tpl.Subject); err != nil {
		return fmt.Errorf("%w: subject template: %v", domain.ErrValidation, err)
	}
	// Body is rendered with html/template (see renderHTMLTemplate), so validate
	// it with the same engine — catches html/template-specific parse errors.
	if _, err := htmltemplate.New("body").Parse(tpl.Body); err != nil {
		return fmt.Errorf("%w: body template: %v", domain.ErrValidation, err)
	}
	return s.repo.SaveTemplate(ctx, tpl)
}

func (s *Service) PreviewTemplate(ctx context.Context, tpl *domain.MailTemplate) (*TemplatePreview, error) {
	if tpl == nil {
		return nil, fmt.Errorf("%w: template required", domain.ErrValidation)
	}
	if err := validateTemplateKind(tpl.Kind); err != nil {
		return nil, err
	}
	subject := strings.TrimSpace(tpl.Subject)
	if subject == "" {
		return nil, fmt.Errorf("%w: subject required", domain.ErrValidation)
	}
	settings := DefaultSettings()
	if s != nil && s.repo != nil {
		if loaded, err := s.LoadSettings(ctx); err == nil {
			settings = loaded
		}
	}
	data := s.previewTemplateData(ctx, settings, tpl.Kind)
	renderedSubject, err := renderTemplate("preview_subject", subject, data)
	if err != nil {
		return nil, fmt.Errorf("%w: subject template: %v", domain.ErrValidation, err)
	}
	renderedBody, err := renderHTMLTemplate("preview_body", tpl.Body, data)
	if err != nil {
		return nil, fmt.Errorf("%w: body template: %v", domain.ErrValidation, err)
	}
	return &TemplatePreview{Subject: renderedSubject, Body: renderedBody}, nil
}

func validateTemplateKind(kind domain.MailReminderKind) error {
	switch kind {
	case domain.MailReminderExpireBefore, domain.MailReminderExpired, domain.MailReminderTrafficLow, domain.MailReminderTrafficExhausted, domain.MailReminderAccountDisable, domain.MailReminderAccountEnable, domain.MailReminderAnnouncement, domain.MailReminderBlockedClient, domain.MailReminderPasswordReset, domain.MailReminderEmailVerify, domain.MailReminderLogin2FA:
		return nil
	default:
		return fmt.Errorf("%w: invalid template kind", domain.ErrValidation)
	}
}

func (s *Service) SendTest(ctx context.Context, to string) error {
	settings, err := s.LoadSettings(ctx)
	if err != nil {
		return err
	}
	to = strings.TrimSpace(to)
	if to == "" {
		return fmt.Errorf("%w: recipient required", domain.ErrValidation)
	}
	return sendSMTP(ctx, settings, to, "Passwall 邮件测试", "这是一封来自 Passwall Sub Panel 的测试邮件。")
}

// SendAccountDisabledNotification sends an email notification when an account is disabled.
func (s *Service) SendAccountDisabledNotification(ctx context.Context, u *domain.User, disableReason, disableDetail string) error {
	settings, err := s.LoadSettings(ctx)
	if err != nil {
		return err
	}
	if !settings.Enabled {
		return nil
	}
	templates, err := s.ListTemplates(ctx)
	if err != nil {
		return err
	}
	// Pick the most specific template for this disable. When the reason is
	// traffic_exceeded AND a dedicated traffic_exhausted template is enabled,
	// use that — its copy is tuned for quota events ("流量已用完，下周期自动
	// 恢复"). Falls back to the generic account_disabled template otherwise.
	preferred := domain.MailReminderAccountDisable
	if disableReason == string(domain.DisabledTrafficExceeded) {
		for _, t := range templates {
			if t.Kind == domain.MailReminderTrafficExhausted && t.Enabled {
				preferred = domain.MailReminderTrafficExhausted
				break
			}
		}
	}
	var tpl *domain.MailTemplate
	for _, t := range templates {
		if t.Kind == preferred && t.Enabled {
			tpl = t
			break
		}
	}
	if tpl == nil {
		return nil // Template not found or disabled.
	}
	to := reminderAddress(u)
	if to == "" {
		return nil // No email address.
	}
	// Throttle: a disable can be triggered from several places (admin toggle,
	// traffic poll, blocked-client) and a double-click or a quick retry could
	// re-fire for the same event. Dedup per (user, reason, minute) so genuine
	// later state changes still notify but accidental duplicates don't. Window
	// is minute-grained so an SMTP-failure retry (minutes later) still sends.
	windowKey := eventWindowKey(disableReason)
	uiCfg, uiErr := s.settings.Load(ctx, ports.UISettings{})
	if uiErr != nil {
		// Log and proceed with zero-value uiCfg — templateData has
		// fallback constants for app_title / notify thresholds, so the
		// email still goes out, just without admin's branding overrides.
		log.Warn("mailer settings.Load", "err", uiErr)
	}
	data := s.templateData(ctx, settings, uiCfg, u)
	data["DisableReason"] = disableReason
	data["DisableDetail"] = disableDetail
	subject, err := renderTemplate("account_disabled_subject", tpl.Subject, data)
	if err != nil {
		return err
	}
	body, err := renderHTMLTemplate("account_disabled_body", tpl.Body, data)
	if err != nil {
		return err
	}
	// Atomically claim the per-(user, reason, minute) slot BEFORE sending. A
	// disable can fire concurrently from the traffic poll and the retry
	// processor within the same minute; the old HasSent-then-RecordSent pair
	// was racy (both observe HasSent=false → double-send). Render-first (above)
	// so a broken template can't consume the slot. Mirrors maybeSend /
	// SendBlockedClientWarning.
	won, err := s.repo.ReserveSentSlot(ctx, u.ID, domain.MailReminderAccountDisable, windowKey, to)
	if err != nil {
		return err
	}
	if !won {
		return nil
	}
	if err := sendSMTP(ctx, settings, to, subject, body); err != nil {
		// Enqueue for retry. The retry recomputes a fresh minute-grained
		// windowKey, so it reserves a new slot and sends once SMTP recovers.
		s.enqueueMailNotify(ctx, u.ID, "account_disabled", disableReason, disableDetail)
		return err
	}
	return nil
}

// eventWindowKey builds a per-minute dedup key for event-driven (non-periodic)
// notifications like account disable/enable. The reason prefix keeps distinct
// causes (traffic vs blocked-client vs admin) from masking one another, and the
// minute bucket lets a legitimate later state change re-notify.
func eventWindowKey(reason string) string {
	return reason + "@" + time.Now().UTC().Format("2006-01-02T15:04")
}

// SendAccountDisabledToUser is a convenience wrapper for sending disable notification.
func (s *Service) SendAccountDisabledToUser(ctx context.Context, userID int64, disableReason, disableDetail string) error {
	u, err := s.users.GetByID(ctx, userID)
	if err != nil {
		return err
	}
	return s.SendAccountDisabledNotification(ctx, u, disableReason, disableDetail)
}

// SendAccountEnabledNotification sends an email notification when an account is re-enabled.
func (s *Service) SendAccountEnabledNotification(ctx context.Context, u *domain.User, enableReason, enableDetail string) error {
	settings, err := s.LoadSettings(ctx)
	if err != nil {
		return err
	}
	if !settings.Enabled {
		return nil
	}
	templates, err := s.ListTemplates(ctx)
	if err != nil {
		return err
	}
	// Find the account_enabled template.
	var tpl *domain.MailTemplate
	for _, t := range templates {
		if t.Kind == domain.MailReminderAccountEnable && t.Enabled {
			tpl = t
			break
		}
	}
	if tpl == nil {
		return nil // Template not found or disabled.
	}
	to := reminderAddress(u)
	if to == "" {
		return nil // No email address.
	}
	// Same per-minute dedup as the disable path (see eventWindowKey).
	windowKey := eventWindowKey(enableReason)
	uiCfg, uiErr := s.settings.Load(ctx, ports.UISettings{})
	if uiErr != nil {
		// Log and proceed with zero-value uiCfg — templateData has
		// fallback constants for app_title / notify thresholds, so the
		// email still goes out, just without admin's branding overrides.
		log.Warn("mailer settings.Load", "err", uiErr)
	}
	data := s.templateData(ctx, settings, uiCfg, u)
	data["EnableReason"] = enableReason
	data["EnableDetail"] = enableDetail
	subject, err := renderTemplate("account_enabled_subject", tpl.Subject, data)
	if err != nil {
		return err
	}
	body, err := renderHTMLTemplate("account_enabled_body", tpl.Body, data)
	if err != nil {
		return err
	}
	// Atomically claim the slot BEFORE sending (see SendAccountDisabledNotification
	// for the double-send race this closes). Render-first so a broken template
	// can't consume the slot.
	won, err := s.repo.ReserveSentSlot(ctx, u.ID, domain.MailReminderAccountEnable, windowKey, to)
	if err != nil {
		return err
	}
	if !won {
		return nil
	}
	if err := sendSMTP(ctx, settings, to, subject, body); err != nil {
		// Enqueue for retry (a fresh minute-grained windowKey reserves anew).
		s.enqueueMailNotify(ctx, u.ID, "account_enabled", enableReason, enableDetail)
		return err
	}
	return nil
}

// SendAccountEnabledToUser is a convenience wrapper for sending enable notification.
func (s *Service) SendAccountEnabledToUser(ctx context.Context, userID int64, enableReason, enableDetail string) error {
	u, err := s.users.GetByID(ctx, userID)
	if err != nil {
		return err
	}
	return s.SendAccountEnabledNotification(ctx, u, enableReason, enableDetail)
}

// serviceSuspendReasonText maps a service-suspension reason code to user-facing
// copy for the suspend email. Falls back to the raw code for unknown reasons.
func serviceSuspendReasonText(reason string) string {
	switch reason {
	case string(domain.DisabledTrafficExceeded):
		return "本期流量已用完"
	case string(domain.DisabledBlockedClient):
		return "使用了被禁止的客户端"
	case string(domain.DisabledServiceManual):
		return "管理员手动暂停服务"
	case string(domain.DisabledExpired):
		return "订阅已到期"
	case "":
		return "服务暂停"
	default:
		return reason
	}
}

// SendServiceSuspendedNotification emails the user that their proxy / subscription
// SERVICE was suspended — distinct from an account disable: they can still log in
// to see why and self-rescue. Carries the reason. A quota suspension prefers the
// richer traffic_exhausted template (it shows used/limit GB); every other reason
// uses the generic service_suspended template with the humanized reason + detail.
func (s *Service) SendServiceSuspendedNotification(ctx context.Context, u *domain.User, reason, detail string) error {
	settings, err := s.LoadSettings(ctx)
	if err != nil {
		return err
	}
	if !settings.Enabled {
		return nil
	}
	templates, err := s.ListTemplates(ctx)
	if err != nil {
		return err
	}
	// Quota suspensions keep their tuned copy (used/limit GB) when that template
	// is enabled; otherwise fall back to the generic service_suspended template.
	preferred := domain.MailReminderServiceSuspended
	if reason == string(domain.DisabledTrafficExceeded) {
		for _, t := range templates {
			if t.Kind == domain.MailReminderTrafficExhausted && t.Enabled {
				preferred = domain.MailReminderTrafficExhausted
				break
			}
		}
	}
	var tpl *domain.MailTemplate
	for _, t := range templates {
		if t.Kind == preferred && t.Enabled {
			tpl = t
			break
		}
	}
	if tpl == nil {
		return nil // Template not found or disabled.
	}
	to := reminderAddress(u)
	if to == "" {
		return nil // No email address.
	}
	// Per-(user, reason, minute) dedup — same backstop as the account-disable
	// path. The reason prefix keeps a manual-suspend mail from being masked by a
	// same-minute quota mail, and the minute bucket lets a real later state change
	// re-notify (see eventWindowKey).
	windowKey := eventWindowKey(reason)
	uiCfg, uiErr := s.settings.Load(ctx, ports.UISettings{})
	if uiErr != nil {
		log.Warn("mailer settings.Load", "err", uiErr)
	}
	data := s.templateData(ctx, settings, uiCfg, u)
	data["SuspendReason"] = serviceSuspendReasonText(reason)
	data["SuspendReasonCode"] = reason
	data["SuspendDetail"] = detail
	// Compat for the traffic_exhausted template's existing placeholders.
	data["DisableReason"] = reason
	data["DisableDetail"] = detail
	subject, err := renderTemplate("service_suspended_subject", tpl.Subject, data)
	if err != nil {
		return err
	}
	body, err := renderHTMLTemplate("service_suspended_body", tpl.Body, data)
	if err != nil {
		return err
	}
	won, err := s.repo.ReserveSentSlot(ctx, u.ID, domain.MailReminderServiceSuspended, windowKey, to)
	if err != nil {
		return err
	}
	if !won {
		return nil
	}
	if err := sendSMTP(ctx, settings, to, subject, body); err != nil {
		s.enqueueMailNotify(ctx, u.ID, "service_suspended", reason, detail)
		return err
	}
	return nil
}

// SendServiceSuspendedToUser is a convenience wrapper that loads the user first.
func (s *Service) SendServiceSuspendedToUser(ctx context.Context, userID int64, reason, detail string) error {
	u, err := s.users.GetByID(ctx, userID)
	if err != nil {
		return err
	}
	return s.SendServiceSuspendedNotification(ctx, u, reason, detail)
}

// SendServiceRestoredNotification emails the user that their proxy / subscription
// service is back (the service-axis counterpart of SendAccountEnabledNotification).
func (s *Service) SendServiceRestoredNotification(ctx context.Context, u *domain.User) error {
	settings, err := s.LoadSettings(ctx)
	if err != nil {
		return err
	}
	if !settings.Enabled {
		return nil
	}
	templates, err := s.ListTemplates(ctx)
	if err != nil {
		return err
	}
	var tpl *domain.MailTemplate
	for _, t := range templates {
		if t.Kind == domain.MailReminderServiceRestored && t.Enabled {
			tpl = t
			break
		}
	}
	if tpl == nil {
		return nil
	}
	to := reminderAddress(u)
	if to == "" {
		return nil
	}
	windowKey := eventWindowKey("service_restored")
	uiCfg, uiErr := s.settings.Load(ctx, ports.UISettings{})
	if uiErr != nil {
		log.Warn("mailer settings.Load", "err", uiErr)
	}
	data := s.templateData(ctx, settings, uiCfg, u)
	subject, err := renderTemplate("service_restored_subject", tpl.Subject, data)
	if err != nil {
		return err
	}
	body, err := renderHTMLTemplate("service_restored_body", tpl.Body, data)
	if err != nil {
		return err
	}
	won, err := s.repo.ReserveSentSlot(ctx, u.ID, domain.MailReminderServiceRestored, windowKey, to)
	if err != nil {
		return err
	}
	if !won {
		return nil
	}
	if err := sendSMTP(ctx, settings, to, subject, body); err != nil {
		s.enqueueMailNotify(ctx, u.ID, "service_restored", "", "")
		return err
	}
	return nil
}

// SendServiceRestoredToUser is a convenience wrapper that loads the user first.
func (s *Service) SendServiceRestoredToUser(ctx context.Context, userID int64) error {
	u, err := s.users.GetByID(ctx, userID)
	if err != nil {
		return err
	}
	return s.SendServiceRestoredNotification(ctx, u)
}

// SendBlockedClientWarning emails the user that they used a blocked /
// unsupported client. The caller (sub handler) has already gated on
// SubBlockNotifyUser and passes the loaded UI settings in, so this never
// re-reads them. Capped at SubBlockNotifyMaxPerDay per user per day.
//
// Cap is enforced insert-first to stay race-safe: concurrent blocked fetches
// would otherwise both read the same count, both send, then collide on the
// same window_key (OnConflict DoNothing drops the second) — over-sending while
// under-recording. Instead we reserve the next "YYYY-MM-DD#seq" slot via an
// atomic insert and only send if we won it; a lost race simply doesn't send
// (we prefer under- to over-send for a soft notice). Best-effort: NOT enqueued
// for retry — the client re-fetches on its own schedule.
func (s *Service) SendBlockedClientWarning(ctx context.Context, u *domain.User, clientName string, uiCfg ports.UISettings) error {
	if !uiCfg.SubBlockNotifyUser {
		return nil
	}
	settings, err := s.LoadSettings(ctx)
	if err != nil || !settings.Enabled {
		return err
	}
	maxPerDay := uiCfg.SubBlockNotifyMaxPerDay
	if maxPerDay <= 0 {
		maxPerDay = 1
	}
	to := reminderAddress(u)
	if to == "" {
		return nil
	}
	today := time.Now().Format("2006-01-02")
	sent, err := s.repo.CountSentInWindow(ctx, u.ID, domain.MailReminderBlockedClient, today)
	if err != nil {
		return err
	}
	if sent >= int64(maxPerDay) {
		return nil // daily cap already reached
	}
	// Resolve the template before reserving a slot so a missing/disabled
	// template doesn't consume the day's quota.
	var tpl *domain.MailTemplate
	templates, err := s.ListTemplates(ctx)
	if err != nil {
		return err
	}
	for _, t := range templates {
		if t.Kind == domain.MailReminderBlockedClient && t.Enabled {
			tpl = t
			break
		}
	}
	if tpl == nil {
		return nil
	}
	// Reserve this slot atomically. If we lost the race, another goroutine
	// already claimed it and will (or did) send — we stay silent.
	windowKey := today + "#" + strconv.FormatInt(sent, 10)
	won, err := s.repo.ReserveSentSlot(ctx, u.ID, domain.MailReminderBlockedClient, windowKey, to)
	if err != nil {
		return err
	}
	if !won {
		return nil
	}
	data := s.templateData(ctx, settings, uiCfg, u)
	data["ClientName"] = clientName
	subject, err := renderTemplate("blocked_client_subject", tpl.Subject, data)
	if err != nil {
		return err
	}
	body, err := renderHTMLTemplate("blocked_client_body", tpl.Body, data)
	if err != nil {
		return err
	}
	return sendSMTP(ctx, settings, to, subject, body)
}

// SendPasswordReset delivers a self-service password-reset email. Exactly one of
// link / code is non-empty (the recovery service picks per the delivery
// setting). Returns an error when SMTP isn't configured so the caller can log
// it; the recovery flow itself stays silent to the end user (no enumeration).
func (s *Service) SendPasswordReset(ctx context.Context, to, displayName, link, code string, expireMinutes int) error {
	settings, err := s.LoadSettings(ctx)
	if err != nil {
		return err
	}
	if !settings.Enabled {
		return fmt.Errorf("%w: smtp not configured", domain.ErrValidation)
	}
	to = strings.TrimSpace(to)
	if to == "" {
		return fmt.Errorf("%w: recipient required", domain.ErrValidation)
	}
	// password_reset is transactional and gated by PasswordRecoveryEnabled, so
	// it ignores the template's Enabled flag — ListTemplates always surfaces it
	// (DefaultTemplates merge), with the admin's edits if any.
	var tpl *domain.MailTemplate
	templates, err := s.ListTemplates(ctx)
	if err != nil {
		return err
	}
	for _, t := range templates {
		if t.Kind == domain.MailReminderPasswordReset {
			tpl = t
			break
		}
	}
	if tpl == nil {
		return fmt.Errorf("password_reset template unavailable")
	}
	uiCfg, _ := s.settings.Load(ctx, ports.UISettings{})
	base := strings.TrimRight(uiCfg.SubBaseURL, "/")
	data := map[string]any{
		"DisplayName":   displayName,
		"Email":         to,
		"SiteTitle":     uiCfg.BrandName(),
		"LogoURL":       resolveLogoURL(base, uiCfg.LogoURL, uiCfg.LogoURLDark),
		"PanelURL":      base,
		"ResetLink":     link,
		"OTPCode":       code,
		"ExpireMinutes": expireMinutes,
		"GeneratedAt":   time.Now().Format("2006-01-02 15:04"),
	}
	subject, err := renderTemplate("password_reset_subject", tpl.Subject, data)
	if err != nil {
		return err
	}
	body, err := renderHTMLTemplate("password_reset_body", tpl.Body, data)
	if err != nil {
		return err
	}
	return sendSMTP(ctx, settings, to, subject, body)
}

// SendEmailVerification delivers a self-registration email-verify message.
// Exactly one of link / code is non-empty. Returns an error when SMTP isn't
// configured so the caller can log it.
func (s *Service) SendEmailVerification(ctx context.Context, to, displayName, link, code string, expireMinutes int) error {
	settings, err := s.LoadSettings(ctx)
	if err != nil {
		return err
	}
	if !settings.Enabled {
		return fmt.Errorf("%w: smtp not configured", domain.ErrValidation)
	}
	to = strings.TrimSpace(to)
	if to == "" {
		return fmt.Errorf("%w: recipient required", domain.ErrValidation)
	}
	var tpl *domain.MailTemplate
	templates, err := s.ListTemplates(ctx)
	if err != nil {
		return err
	}
	for _, t := range templates {
		if t.Kind == domain.MailReminderEmailVerify {
			tpl = t
			break
		}
	}
	if tpl == nil {
		return fmt.Errorf("email_verify template unavailable")
	}
	uiCfg, _ := s.settings.Load(ctx, ports.UISettings{})
	base := strings.TrimRight(uiCfg.SubBaseURL, "/")
	data := map[string]any{
		"DisplayName":   displayName,
		"Email":         to,
		"SiteTitle":     uiCfg.BrandName(),
		"LogoURL":       resolveLogoURL(base, uiCfg.LogoURL, uiCfg.LogoURLDark),
		"PanelURL":      base,
		"VerifyLink":    link,
		"OTPCode":       code,
		"ExpireMinutes": expireMinutes,
		"GeneratedAt":   time.Now().Format("2006-01-02 15:04"),
	}
	subject, err := renderTemplate("email_verify_subject", tpl.Subject, data)
	if err != nil {
		return err
	}
	body, err := renderHTMLTemplate("email_verify_body", tpl.Body, data)
	if err != nil {
		return err
	}
	return sendSMTP(ctx, settings, to, subject, body)
}

// SendLogin2FACode delivers a one-time login code for the email-as-2FA factor.
// Code-only (no link). Returns an error when SMTP isn't configured so the caller
// can surface it.
func (s *Service) SendLogin2FACode(ctx context.Context, to, displayName, code string, expireMinutes int) error {
	settings, err := s.LoadSettings(ctx)
	if err != nil {
		return err
	}
	if !settings.Enabled {
		return fmt.Errorf("%w: smtp not configured", domain.ErrValidation)
	}
	to = strings.TrimSpace(to)
	if to == "" {
		return fmt.Errorf("%w: recipient required", domain.ErrValidation)
	}
	var tpl *domain.MailTemplate
	templates, err := s.ListTemplates(ctx)
	if err != nil {
		return err
	}
	for _, t := range templates {
		if t.Kind == domain.MailReminderLogin2FA {
			tpl = t
			break
		}
	}
	if tpl == nil {
		return fmt.Errorf("login_2fa template unavailable")
	}
	uiCfg, _ := s.settings.Load(ctx, ports.UISettings{})
	base := strings.TrimRight(uiCfg.SubBaseURL, "/")
	data := map[string]any{
		"DisplayName":   displayName,
		"Email":         to,
		"SiteTitle":     uiCfg.BrandName(),
		"LogoURL":       resolveLogoURL(base, uiCfg.LogoURL, uiCfg.LogoURLDark),
		"PanelURL":      base,
		"OTPCode":       code,
		"ExpireMinutes": expireMinutes,
		"GeneratedAt":   time.Now().Format("2006-01-02 15:04"),
	}
	subject, err := renderTemplate("login_2fa_subject", tpl.Subject, data)
	if err != nil {
		return err
	}
	body, err := renderHTMLTemplate("login_2fa_body", tpl.Body, data)
	if err != nil {
		return err
	}
	return sendSMTP(ctx, settings, to, subject, body)
}

type AnnouncementInput struct {
	Subject     string
	Body        string
	OnlyEnabled bool
}

type AnnouncementResult struct {
	Total   int                 `json:"total"`
	Sent    int                 `json:"sent"`
	Skipped int                 `json:"skipped"`
	Failed  int                 `json:"failed"`
	Errors  []AnnouncementError `json:"errors,omitempty"`
}

type AnnouncementError struct {
	UserID int64  `json:"user_id"`
	UPN    string `json:"upn"`
	Email  string `json:"email"`
	Error  string `json:"error"`
}

// AlertAdmins emails every enabled admin a one-off subject/body, deduped per
// (admin, kind, windowKey) via mail_sent so a persistently-failing job doesn't
// spam. No-op when mail is disabled. Used by the cert service to notify admins
// of certificate issuance/renewal failures.
func (s *Service) AlertAdmins(ctx context.Context, kind domain.MailReminderKind, windowKey, subject, body string) (int, error) {
	settings, err := s.LoadSettings(ctx)
	if err != nil {
		return 0, err
	}
	if !settings.Enabled {
		return 0, nil
	}
	ctx = context.WithoutCancel(ctx)
	sent := 0
	page := 1
	const pageSize = 100
	for {
		users, total, err := s.users.List(ctx, ports.UserFilter{Pagination: ports.Pagination{Page: page, PageSize: pageSize}})
		if err != nil {
			return sent, err
		}
		for _, u := range users {
			if u.Role != domain.RoleAdmin || !u.Enabled {
				continue
			}
			to := reminderAddress(u)
			if to == "" {
				continue
			}
			// Atomic at-most-once per (admin, kind, windowKey).
			won, rerr := s.repo.ReserveSentSlot(ctx, u.ID, kind, windowKey, to)
			if rerr != nil || !won {
				continue
			}
			if serr := sendSMTP(ctx, settings, to, subject, body); serr != nil {
				log.Warn("alert admins send", "user_id", u.ID, "err", serr)
				continue
			}
			sent++
		}
		if int64(page*pageSize) >= total {
			break
		}
		page++
	}
	return sent, nil
}

func (s *Service) SendAnnouncement(ctx context.Context, in AnnouncementInput) (*AnnouncementResult, error) {
	settings, err := s.LoadSettings(ctx)
	if err != nil {
		return nil, err
	}
	in.Subject = strings.TrimSpace(in.Subject)
	if in.Subject == "" {
		return nil, fmt.Errorf("%w: subject required", domain.ErrValidation)
	}
	if strings.TrimSpace(in.Body) == "" {
		return nil, fmt.Errorf("%w: body required", domain.ErrValidation)
	}
	templates, err := s.ListTemplates(ctx)
	if err != nil {
		return nil, err
	}
	var tpl *domain.MailTemplate
	for _, t := range templates {
		if t.Kind == domain.MailReminderAnnouncement {
			tpl = t
			break
		}
	}
	if tpl == nil || !tpl.Enabled {
		return nil, fmt.Errorf("%w: announcement template is disabled", domain.ErrValidation)
	}

	// Decouple the broadcast from the request context. A fan-out to thousands of
	// users takes minutes; if it rode the request ctx, the admin's browser timing
	// out or navigating away would cancel it mid-list — every remaining sendSMTP
	// fails on the cancelled ctx, half the users go un-notified, and the result is
	// lost. WithoutCancel keeps request-scoped values but drops the cancellation.
	ctx = context.WithoutCancel(ctx)

	// Hoist the UISettings load out of the per-user loop — broadcast can
	// fan out to thousands of users and the settings KV is N row reads each
	// time. One Load per broadcast, then templateData is pure CPU per user.
	uiCfg, uiErr := s.settings.Load(ctx, ports.UISettings{})
	if uiErr != nil {
		// Log and proceed with zero-value uiCfg — templateData has
		// fallback constants for app_title / notify thresholds, so the
		// email still goes out, just without admin's branding overrides.
		log.Warn("mailer settings.Load", "err", uiErr)
	}

	result := &AnnouncementResult{}
	page := 1
	const pageSize = 100
	for {
		users, total, err := s.users.List(ctx, ports.UserFilter{Pagination: ports.Pagination{Page: page, PageSize: pageSize}})
		if err != nil {
			return nil, err
		}
		for _, u := range users {
			result.Total++
			if in.OnlyEnabled && !u.Enabled {
				result.Skipped++
				continue
			}
			to := reminderAddress(u)
			if to == "" {
				result.Skipped++
				continue
			}
			data := s.templateData(ctx, settings, uiCfg, u)
			data["AnnouncementTitle"] = in.Subject
			data["AnnouncementBody"] = in.Body
			// Pre-rendered + already escaped by announcementBodyHTML; mark it
			// safe so the html/template body doesn't double-escape its <br>s.
			data["AnnouncementBodyHTML"] = htmltemplate.HTML(announcementBodyHTML(in.Body))
			subject, err := renderTemplate("announcement_subject", tpl.Subject, data)
			if err != nil {
				result.Failed++
				result.Errors = append(result.Errors, announcementError(u, to, err))
				continue
			}
			body, err := renderHTMLTemplate("announcement_body", tpl.Body, data)
			if err != nil {
				result.Failed++
				result.Errors = append(result.Errors, announcementError(u, to, err))
				continue
			}
			if err := sendSMTP(ctx, settings, to, subject, body); err != nil {
				result.Failed++
				result.Errors = append(result.Errors, announcementError(u, to, err))
				continue
			}
			result.Sent++
		}
		if int64(page*pageSize) >= total {
			break
		}
		page++
	}
	return result, nil
}

func announcementBodyHTML(body string) string {
	escaped := html.EscapeString(strings.TrimSpace(body))
	escaped = strings.ReplaceAll(escaped, "\r\n", "\n")
	escaped = strings.ReplaceAll(escaped, "\r", "\n")
	return strings.ReplaceAll(escaped, "\n", "<br>")
}

func announcementError(u *domain.User, to string, err error) AnnouncementError {
	return AnnouncementError{
		UserID: u.ID,
		UPN:    u.UPN,
		Email:  to,
		Error:  err.Error(),
	}
}

func (s *Service) ProcessReminders(ctx context.Context) error {
	settings, err := s.LoadSettings(ctx)
	if err != nil {
		return err
	}
	if !settings.Enabled {
		return nil
	}
	// Notify thresholds live in the settings KV table (type='notify') since the
	// v3 schema split — load them alongside SMTP settings so processUser has
	// everything it needs without a second DB round-trip per user.
	uiCfg, err := s.settings.Load(ctx, ports.UISettings{})
	if err != nil {
		return err
	}
	templates, err := s.ListTemplates(ctx)
	if err != nil {
		return err
	}
	byKind := make(map[domain.MailReminderKind]*domain.MailTemplate, len(templates))
	for _, tpl := range templates {
		byKind[tpl.Kind] = tpl
	}

	page := 1
	const pageSize = 100
	now := time.Now()
	for {
		users, total, err := s.users.List(ctx, ports.UserFilter{Pagination: ports.Pagination{Page: page, PageSize: pageSize}})
		if err != nil {
			return err
		}
		for _, u := range users {
			if err := s.processUser(ctx, settings, uiCfg, byKind, u, now); err != nil {
				log.Warn("mail reminder user", "user_id", u.ID, "err", err)
			}
		}
		if int64(page*pageSize) >= total {
			break
		}
		page++
	}
	return nil
}

func (s *Service) processUser(ctx context.Context, settings domain.MailSettings, uiCfg ports.UISettings, templates map[domain.MailReminderKind]*domain.MailTemplate, u *domain.User, now time.Time) error {
	to := reminderAddress(u)
	if to == "" {
		return nil
	}
	// Per-group reminder thresholds: a group can set its own expire / traffic-low
	// warning windows. eff = global (+) this user's group overrides; non-overridable
	// fields (branding/sub) equal the global uiCfg.
	eff := uiCfg
	if s.settings != nil {
		if e, lerr := s.settings.LoadForUser(ctx, u, ports.UISettings{}); lerr == nil {
			eff = e
		}
	}
	data := s.templateData(ctx, settings, eff, u)
	if u.ExpireAt != nil {
		window := strconv.FormatInt(u.ExpireAt.Unix(), 10)
		if !u.ExpireAt.After(now) {
			if err := s.maybeSend(ctx, settings, templates[domain.MailReminderExpired], u, to, domain.MailReminderExpired, window, data); err != nil {
				return err
			}
		} else if u.ExpireAt.Sub(now) <= time.Duration(eff.ExpireBeforeDays)*24*time.Hour {
			if err := s.maybeSend(ctx, settings, templates[domain.MailReminderExpireBefore], u, to, domain.MailReminderExpireBefore, window, data); err != nil {
				return err
			}
		}
	}
	if u.TrafficLimitBytes > 0 && eff.TrafficRemainPercent > 0 {
		used, err := s.periodUsage(ctx, u)
		if err == nil {
			remain := u.TrafficLimitBytes - used
			if remain < 0 {
				remain = 0
			}
			if remain*100 <= int64(eff.TrafficRemainPercent)*u.TrafficLimitBytes {
				data["PeriodUsedGB"] = gb(used)
				data["TrafficLimitGB"] = gb(u.TrafficLimitBytes)
				data["TrafficRemainGB"] = gb(remain)
				window := "no-period"
				if u.TrafficPeriodStart != nil {
					window = strconv.FormatInt(u.TrafficPeriodStart.Unix(), 10)
				}
				window += ":" + strconv.FormatInt(u.TrafficLimitBytes, 10)
				if err := s.maybeSend(ctx, settings, templates[domain.MailReminderTrafficLow], u, to, domain.MailReminderTrafficLow, window, data); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func (s *Service) maybeSend(ctx context.Context, settings domain.MailSettings, tpl *domain.MailTemplate, u *domain.User, to string, kind domain.MailReminderKind, windowKey string, data map[string]any) error {
	if tpl == nil || !tpl.Enabled {
		return nil
	}
	// Render BEFORE reserving the slot. ReserveSentSlot's at-most-once
	// contract is deliberate for SMTP-failure-vs-spam (better to lose
	// one reminder than spam if SMTP recovers between cycles), but a
	// template-parse error is a permanent admin-fixable fault — if we
	// reserve first, a broken template silently consumes the window
	// forever (HasSent on the next cycle sees the row and skips).
	// Render-first surfaces parse errors immediately and only commits
	// the slot once we know the message can actually be built. Mirrors
	// the ordering SendBlockedClientWarning already uses.
	subject, err := renderTemplate("subject", tpl.Subject, data)
	if err != nil {
		return err
	}
	body, err := renderHTMLTemplate("body", tpl.Body, data)
	if err != nil {
		return err
	}
	// ReserveSentSlot atomically claims the (user, kind, windowKey) row
	// in a single INSERT … OnConflict DoNothing. Pre-fix this path did
	// HasSent (Count(*)) + RecordSent (Insert) — two round-trips per
	// (user, kind) per cycle, AND racy: two concurrent reminder runs
	// could both observe HasSent=false and double-send. Switching to
	// the reserve primitive collapses both to one atomic operation.
	won, err := s.repo.ReserveSentSlot(ctx, u.ID, kind, windowKey, to)
	if err != nil || !won {
		return err
	}
	return sendSMTP(ctx, settings, to, subject, body)
}

// templateData builds the merge variables for one user's reminder mail. The
// caller MUST pass the UISettings it already has in scope so we don't run a
// per-user settings.Load — the broadcast and reminder paths both call this
// inside a user loop, and each settings.Load fans out to ~40 row reads in
// the KV settings table.
func (s *Service) templateData(ctx context.Context, settings domain.MailSettings, uiCfg ports.UISettings, u *domain.User) map[string]any {
	name := u.DisplayName
	if name == "" {
		name = u.UPN
	}
	expireAt := ""
	if u.ExpireAt != nil {
		expireAt = u.ExpireAt.Format("2006-01-02 15:04")
	}
	periodUsedGB := "0.00"
	trafficLimitGB := gb(u.TrafficLimitBytes)
	trafficRemainGB := gb(u.TrafficLimitBytes)
	if u.TrafficLimitBytes <= 0 {
		trafficLimitGB = "unlimited"
		trafficRemainGB = "unlimited"
	}
	if used, err := s.periodUsage(ctx, u); err == nil {
		periodUsedGB = gb(used)
		if u.TrafficLimitBytes > 0 {
			remain := u.TrafficLimitBytes - used
			if remain < 0 {
				remain = 0
			}
			trafficRemainGB = gb(remain)
		}
	}
	base := strings.TrimRight(uiCfg.SubBaseURL, "/")
	panelURL := base
	expireBeforeDays := uiCfg.ExpireBeforeDays
	if expireBeforeDays <= 0 {
		expireBeforeDays = 3
	}
	trafficRemainPercent := uiCfg.TrafficRemainPercent
	if trafficRemainPercent <= 0 {
		trafficRemainPercent = 10
	}
	logoURL := resolveLogoURL(base, uiCfg.LogoURL, uiCfg.LogoURLDark)
	return map[string]any{
		"UserID":               u.ID,
		"UPN":                  u.UPN,
		"DisplayName":          name,
		"Email":                u.Email,
		"SiteTitle":            uiCfg.BrandName(),
		"LogoURL":              logoURL,
		"PanelURL":             panelURL,
		"GeneratedAt":          time.Now().Format("2006-01-02 15:04"),
		"ExpireAt":             expireAt,
		"ExpireBeforeDays":     expireBeforeDays,
		"TrafficRemainPercent": trafficRemainPercent,
		"PeriodUsedGB":         periodUsedGB,
		"TrafficLimitGB":       trafficLimitGB,
		"TrafficRemainGB":      trafficRemainGB,
	}
}

func (s *Service) previewTemplateData(ctx context.Context, settings domain.MailSettings, kind domain.MailReminderKind) map[string]any {
	now := time.Now()
	// Notify thresholds (ExpireBeforeDays / TrafficRemainPercent) moved out of
	// mail_settings into the settings KV table in v3 — load them once here so
	// the preview reflects what real reminders would use.
	expireBeforeDays := 3
	trafficRemainPercent := 10
	if s.settings != nil {
		if st, err := s.settings.Load(ctx, ports.UISettings{}); err == nil {
			if st.ExpireBeforeDays > 0 {
				expireBeforeDays = st.ExpireBeforeDays
			}
			if st.TrafficRemainPercent > 0 {
				trafficRemainPercent = st.TrafficRemainPercent
			}
		}
	}
	// Pick template-kind-specific sample values so the preview reflects what a
	// real recipient would actually see. The base map below holds the common
	// defaults; the switch below tweaks any field whose realistic value depends
	// on which template is being previewed.
	expireAt := now.AddDate(0, 0, expireBeforeDays).Format("2006-01-02 15:04")
	periodUsed := "80.00"
	trafficLimit := "100.00"
	trafficRemain := "20.00"
	disableReason := string(domain.DisabledTrafficExceeded)
	disableDetail := "本月流量已达到 100 GB 上限"
	enableDetail := "管理员已为你恢复账户"
	announcementTitle := "系统维护通知"
	announcementBody := "<p>今晚 23:00 - 24:00 将进行短暂维护，期间订阅服务可能波动。给你带来的不便敬请谅解。</p>"

	switch kind {
	case domain.MailReminderExpireBefore:
		// Future date, X days out matches the "{{.ExpireBeforeDays}} 天内到期" subject.
		expireAt = now.AddDate(0, 0, expireBeforeDays).Format("2006-01-02 15:04")
	case domain.MailReminderExpired:
		// Past date — show "expired yesterday" so the template's "已在 X 到期" reads right.
		expireAt = now.AddDate(0, 0, -1).Format("2006-01-02 15:04")
	case domain.MailReminderTrafficLow:
		// Show the user near (but not over) their cap so the "剩余流量" row matches
		// "low remaining" semantics rather than "exhausted".
		periodUsed = "92.50"
		trafficRemain = "7.50"
	case domain.MailReminderTrafficExhausted:
		// Quota fully consumed — period used equals limit, remain is zero.
		periodUsed = "100.00"
		trafficRemain = "0.00"
	case domain.MailReminderAccountDisable:
		// Mirror what auto-disable actually writes today; keep it concrete so
		// the admin sees the row layout they'll see in real emails.
		disableReason = string(domain.DisabledTrafficExceeded)
		disableDetail = "本期流量已达到 100 GB 上限，账号已被自动停用"
	case domain.MailReminderAccountEnable:
		enableDetail = "新周期已开始，账号已自动恢复"
	case domain.MailReminderAnnouncement:
		announcementTitle = "系统维护通知"
		announcementBody = "<p>今晚 23:00 - 24:00 将进行短暂维护，期间订阅服务可能波动。</p><p>如有疑问请联系管理员。</p>"
	}

	data := map[string]any{
		"UserID":               int64(1001),
		"UPN":                  "demo@example.com",
		"DisplayName":          "演示用户",
		"Email":                "demo@example.com",
		"SiteTitle":            "Passwall",
		"LogoURL":              "",
		"PanelURL":             "https://panel.example.com",
		"GeneratedAt":          now.Format("2006-01-02 15:04"),
		"ExpireAt":             expireAt,
		"ExpireBeforeDays":     expireBeforeDays,
		"TrafficRemainPercent": trafficRemainPercent,
		"PeriodUsedGB":         periodUsed,
		"TrafficLimitGB":       trafficLimit,
		"TrafficRemainGB":      trafficRemain,
		"DisableReason":        disableReason,
		"DisableDetail":        disableDetail,
		"EnableReason":         string(domain.DisabledNone),
		"EnableDetail":         enableDetail,
		"AnnouncementTitle":    announcementTitle,
		"AnnouncementBodyHTML": htmltemplate.HTML(announcementBody), // demo HTML — pass through unescaped, mirrors the real announcement path
		"ClientName":           "Clash 示例客户端",
		// Auth templates (password_reset / email_verify / login_2fa) render an OTP
		// code or an action link. Sample the OTP variant for preview; ResetLink /
		// VerifyLink stay empty so the {{if}} falls to the code branch.
		"OTPCode":       "123456",
		"ExpireMinutes": 30,
		"ResetLink":     "",
		"VerifyLink":    "",
	}
	var configuredLogo, configuredLogoDark, base string
	if s != nil && s.settings != nil {
		st, err := s.settings.Load(ctx, ports.UISettings{
			SiteTitle: "Passwall",
			AppTitle:  "Passwall",
		})
		if err == nil {
			data["SiteTitle"] = st.BrandName()
			base = strings.TrimRight(st.SubBaseURL, "/")
			if base != "" {
				data["PanelURL"] = base
			}
			configuredLogo = st.LogoURL
			configuredLogoDark = st.LogoURLDark
		}
	}
	data["LogoURL"] = resolveLogoURL(base, configuredLogo, configuredLogoDark)
	return data
}

func absoluteURL(base, raw string) string {
	if raw == "" || strings.HasPrefix(raw, "http://") || strings.HasPrefix(raw, "https://") || strings.HasPrefix(raw, "data:") {
		return raw
	}
	if strings.HasPrefix(raw, "/") {
		return base + raw
	}
	return base + "/" + raw
}

func isHTTPURL(raw string) bool {
	return strings.HasPrefix(raw, "http://") || strings.HasPrefix(raw, "https://")
}

// emailLogoAssetPath points at the embedded brand logo, which the SPA static
// handler serves publicly (no auth) under /images/. The email header has a
// dark background (#0f172a), so the dark-mode variant is used. The "+" is a
// literal path character (RFC 3986) — only query strings treat it as a space —
// so it's sent as-is; Gmail's image proxy fetches the exact path.
const emailLogoAssetPath = "/images/logo-title-circle-darkmode.png"

// resolveLogoURL produces an <img> src that mail clients can actually fetch.
// Gmail and most webmail clients block "data:" URIs and cannot resolve
// relative paths, so the result must be an absolute http(s) URL. Preference:
//  1. the admin's configured dark logo, then light logo — but only when it
//     resolves to an absolute http(s) URL (data: / relative candidates are
//     skipped: they render in the in-panel preview yet break in real email).
//  2. the embedded brand logo served publicly at {base}/images/... — works
//     with no logo configured as long as the panel base URL is known.
//
// When no base URL is configured there is no fetchable URL to offer, so an
// empty string is returned and the template skips the <img> rather than
// shipping a broken image.
func resolveLogoURL(base, lightConfigured, darkConfigured string) string {
	for _, candidate := range []string{darkConfigured, lightConfigured} {
		if candidate == "" || strings.HasPrefix(candidate, "data:") {
			continue
		}
		if resolved := absoluteURL(base, candidate); isHTTPURL(resolved) {
			return resolved
		}
	}
	if base != "" {
		return base + emailLogoAssetPath
	}
	return ""
}

// periodUsage now defers to domain.User.PeriodUsed — the per-cycle lifetime
// counter + PeriodBaselineBytes make this O(1) memory math. Pre-v3 mailer
// kept a near-duplicate of traffic.Service's implementation; both went
// through this helper so a single source of truth removes the drift risk.
func (s *Service) periodUsage(ctx context.Context, u *domain.User) (int64, error) {
	_ = ctx
	if u == nil {
		return 0, nil
	}
	return u.PeriodUsed(), nil
}

func reminderAddress(u *domain.User) string {
	if u == nil {
		return ""
	}
	if strings.Contains(u.Email, "@") {
		return strings.TrimSpace(u.Email)
	}
	if strings.Contains(u.UPN, "@") {
		return strings.TrimSpace(u.UPN)
	}
	return ""
}

// renderTemplate renders a PLAIN-TEXT template (the email subject). No HTML
// escaping — subjects are header text, and escaping would surface literal
// &amp;/&lt; in them. Never use this for the HTML body (see renderHTMLTemplate).
func renderTemplate(name, raw string, data map[string]any) (string, error) {
	t, err := template.New(name).Parse(raw)
	if err != nil {
		return "", err
	}
	var buf bytes.Buffer
	if err := t.Execute(&buf, data); err != nil {
		return "", err
	}
	return buf.String(), nil
}

// renderHTMLTemplate renders the HTML email body with html/template, which
// contextually escapes every data interpolation ({{.DisplayName}}, {{.UPN}}, …).
// Those fields can carry IdP-controlled values (SSO display name / UPN), so a
// text/template body let an attacker inject markup into the email. Fields that
// are legitimately pre-rendered HTML (e.g. AnnouncementBodyHTML) must be passed
// as html/template.HTML so they pass through unescaped.
func renderHTMLTemplate(name, raw string, data map[string]any) (string, error) {
	t, err := htmltemplate.New(name).Parse(raw)
	if err != nil {
		return "", err
	}
	var buf bytes.Buffer
	if err := t.Execute(&buf, data); err != nil {
		return "", err
	}
	return buf.String(), nil
}

func gb(bytes int64) string {
	return strconv.FormatFloat(float64(bytes)/1024/1024/1024, 'f', 2, 64)
}

// smtpConversationTimeout bounds the whole SMTP exchange after a successful
// dial (the dialer's own Timeout only covers connect). Generous enough for a
// slow-but-alive relay, finite so one hung server can't freeze the serial
// reminder/announcement loop forever.
const smtpConversationTimeout = 60 * time.Second

func sendSMTP(ctx context.Context, settings domain.MailSettings, to, subject, body string) error {
	if settings.SMTPHost == "" || settings.FromEmail == "" {
		return fmt.Errorf("%w: smtp host and from email are required", domain.ErrValidation)
	}
	if _, err := mail.ParseAddress(to); err != nil {
		return fmt.Errorf("%w: invalid recipient", domain.ErrValidation)
	}
	fromAddr := mail.Address{Name: settings.FromName, Address: settings.FromEmail}
	addr := net.JoinHostPort(settings.SMTPHost, strconv.Itoa(settings.SMTPPort))
	dialer := net.Dialer{Timeout: 15 * time.Second}
	type dialResult struct {
		conn net.Conn
		err  error
	}
	ch := make(chan dialResult, 1)
	go func() {
		defer safego.Recover("mailer.sendSMTP.dial")
		if settings.Encryption == "tls" {
			conn, err := tls.DialWithDialer(&dialer, "tcp", addr, &tls.Config{ServerName: settings.SMTPHost, MinVersion: tls.VersionTLS12})
			ch <- dialResult{conn: conn, err: err}
			return
		}
		conn, err := dialer.Dial("tcp", addr)
		ch <- dialResult{conn: conn, err: err}
	}()
	var result dialResult
	select {
	case <-ctx.Done():
		// Reaper: the dial goroutine may still be in-flight. Receive its
		// result in the background and close any successfully-opened conn
		// so we don't leak an established TCP/TLS session on every
		// timed-out send.
		go func() {
			defer safego.Recover("mailer.sendSMTP.dial.reap")
			leaked := <-ch
			if leaked.conn != nil {
				_ = leaked.conn.Close()
			}
		}()
		return ctx.Err()
	case result = <-ch:
	}
	if result.err != nil {
		return result.err
	}
	// Bound the entire SMTP conversation. dialer.Timeout covers only TCP/TLS
	// connect; without a conn deadline, a server that completes the handshake
	// then stalls (no greeting, mid-DATA hang, network black-hole) blocks this
	// goroutine indefinitely and wedges the serial reminder/announcement loop
	// (and ctx is only consulted during the dial select, never after). One
	// absolute deadline covers greeting→Hello→StartTLS→Auth→Mail→Rcpt→Data→Quit.
	_ = result.conn.SetDeadline(time.Now().Add(smtpConversationTimeout))
	c, err := smtp.NewClient(result.conn, settings.SMTPHost)
	if err != nil {
		_ = result.conn.Close()
		// Reading the server greeting failed. A bare io.EOF here means the
		// server accepted the TCP connection then closed it — commonly a
		// wrong port (implicit-TLS server reached without "tls" encryption),
		// or an IP/relay that isn't allowed to connect.
		return fmt.Errorf("smtp greeting: %w", err)
	}
	defer c.Close()
	// Announce ourselves with a real FQDN rather than net/smtp's default
	// "localhost". Stricter relays (Google Workspace's smtp-relay.gmail.com
	// in particular) drop the connection on a non-FQDN HELO, which surfaces
	// downstream as a bare EOF. Hello() must precede StartTLS/Auth; it sets
	// the name reused for the post-TLS re-EHLO too.
	if name := heloName(settings.FromEmail, settings.SMTPHost); name != "" {
		if err := c.Hello(name); err != nil {
			return fmt.Errorf("smtp helo: %w", err)
		}
	}
	if settings.Encryption == "starttls" {
		if err := c.StartTLS(&tls.Config{ServerName: settings.SMTPHost, MinVersion: tls.VersionTLS12}); err != nil {
			return fmt.Errorf("smtp starttls: %w", err)
		}
	}
	if settings.SMTPUsername != "" {
		if err := c.Auth(smtp.PlainAuth("", settings.SMTPUsername, settings.SMTPPassword, settings.SMTPHost)); err != nil {
			return fmt.Errorf("smtp auth: %w", err)
		}
	}
	if err := c.Mail(settings.FromEmail); err != nil {
		return fmt.Errorf("smtp mail-from: %w", err)
	}
	if err := c.Rcpt(to); err != nil {
		return fmt.Errorf("smtp rcpt-to: %w", err)
	}
	w, err := c.Data()
	if err != nil {
		return fmt.Errorf("smtp data: %w", err)
	}
	msg := buildMessage(fromAddr.String(), to, subject, body)
	if _, err := w.Write([]byte(msg)); err != nil {
		_ = w.Close()
		return fmt.Errorf("smtp write: %w", err)
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("smtp data-close: %w", err)
	}
	return c.Quit()
}

// heloName derives the FQDN to announce in EHLO/HELO. The sender domain is
// the most defensible choice (it matches MAIL FROM); the SMTP host is a
// fallback. Returns "" only when neither yields a usable name, in which case
// net/smtp falls back to "localhost".
func heloName(fromEmail, smtpHost string) string {
	if at := strings.LastIndex(fromEmail, "@"); at >= 0 {
		if domain := strings.TrimSpace(fromEmail[at+1:]); domain != "" {
			return domain
		}
	}
	return strings.TrimSpace(smtpHost)
}

func buildMessage(from, to, subject, body string) string {
	contentType := "text/plain; charset=UTF-8"
	if looksLikeHTML(body) {
		contentType = "text/html; charset=UTF-8"
	}
	headers := []string{
		"From: " + from,
		"To: " + to,
		"Subject: " + mime.QEncoding.Encode("utf-8", subject),
		"MIME-Version: 1.0",
		"Content-Type: " + contentType,
		"Content-Transfer-Encoding: 8bit",
	}
	return strings.Join(headers, "\r\n") + "\r\n\r\n" + strings.ReplaceAll(body, "\n", "\r\n")
}

func looksLikeHTML(body string) bool {
	lower := strings.ToLower(body)
	return strings.Contains(lower, "<html") || strings.Contains(lower, "<body") ||
		strings.Contains(lower, "<table") || strings.Contains(lower, "<div")
}
