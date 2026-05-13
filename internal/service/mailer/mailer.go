// Package mailer owns SMTP delivery and reminder scheduling.
package mailer

import (
	"bytes"
	"context"
	"crypto/tls"
	"fmt"
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
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

type Service struct {
	repo     ports.MailRepo
	users    ports.UserRepo
	traffic  ports.TrafficRepo
	settings ports.SettingsRepo
}

func New(repo ports.MailRepo, users ports.UserRepo, traffic ports.TrafficRepo, settings ports.SettingsRepo) *Service {
	return &Service{repo: repo, users: users, traffic: traffic, settings: settings}
}

func DefaultSettings() domain.MailSettings {
	return domain.MailSettings{
		SMTPPort:             587,
		Encryption:           "starttls",
		ExpireBeforeDays:     3,
		TrafficRemainPercent: 10,
	}
}

func DefaultTemplates() []*domain.MailTemplate {
	return []*domain.MailTemplate{
		{
			Kind:    domain.MailReminderExpireBefore,
			Enabled: true,
			Subject: "订阅将在 {{.ExpireBeforeDays}} 天内到期",
			Body:    defaultHTMLTemplate("订阅即将到期", "你的订阅将在 {{.ExpireAt}} 到期。请及时联系管理员完成续期，避免服务中断。", "到期时间", "{{.ExpireAt}}"),
		},
		{
			Kind:    domain.MailReminderExpired,
			Enabled: true,
			Subject: "订阅已到期",
			Body:    defaultHTMLTemplate("订阅已到期", "你的订阅已在 {{.ExpireAt}} 到期。如需继续使用，请联系管理员续期。", "到期时间", "{{.ExpireAt}}"),
		},
		{
			Kind:    domain.MailReminderTrafficLow,
			Enabled: true,
			Subject: "剩余流量低于 {{.TrafficRemainPercent}}%",
			Body:    defaultHTMLTemplate("剩余流量不足", "你的本周期剩余流量已低于 {{.TrafficRemainPercent}}%。请合理安排使用，或联系管理员调整套餐。", "剩余流量", "{{.TrafficRemainGB}} GB"),
		},
		{
			Kind:    domain.MailReminderAccountDisable,
			Enabled: true,
			Subject: "账号已被停用",
			Body:    defaultHTMLTemplate("账号已停用", "你的账号已被停用。{{.DisableReason}}", "停用原因", "{{.DisableDetail}}"),
		},
		{
			Kind:    domain.MailReminderAccountEnable,
			Enabled: true,
			Subject: "账号已恢复正常",
			Body:    defaultHTMLTemplate("账号已恢复", "你的账号已恢复正常，可以继续使用订阅服务。{{.EnableReason}}", "备注", "{{.EnableDetail}}"),
		},
	}
}

func defaultHTMLTemplate(title, message, metricLabel, metricValue string) string {
	return `<!doctype html>
<html>
<body style="margin:0;background:#f4f6fb;padding:32px 16px;font-family:-apple-system,BlinkMacSystemFont,'Segoe UI',Arial,sans-serif;color:#111827;">
  <div style="max-width:640px;margin:0 auto;background:#ffffff;border:1px solid #e5e7eb;border-radius:12px;overflow:hidden;">
    <div style="padding:28px 32px;border-bottom:1px solid #eef2f7;background:#0f172a;">
      <img src="{{.LogoURL}}" alt="{{.SiteTitle}}" style="height:42px;max-width:220px;object-fit:contain;display:block;margin-bottom:18px;">
      <div style="font-size:13px;color:#94a3b8;letter-spacing:.08em;text-transform:uppercase;">{{.SiteTitle}}</div>
      <h1 style="margin:8px 0 0;font-size:24px;line-height:1.3;color:#ffffff;font-weight:700;">` + title + `</h1>
    </div>
    <div style="padding:30px 32px;">
      <p style="margin:0 0 18px;font-size:16px;line-height:1.7;">你好 {{.DisplayName}}，</p>
      <p style="margin:0 0 24px;font-size:15px;line-height:1.7;color:#374151;">` + message + `</p>
      <table role="presentation" style="width:100%;border-collapse:collapse;margin:0 0 26px;background:#f8fafc;border:1px solid #e5e7eb;border-radius:10px;overflow:hidden;">
        <tr>
          <td style="padding:14px 18px;color:#64748b;font-size:13px;">UPN</td>
          <td style="padding:14px 18px;text-align:right;font-size:14px;font-weight:600;color:#111827;">{{.UPN}}</td>
        </tr>
        <tr>
          <td style="padding:14px 18px;color:#64748b;font-size:13px;border-top:1px solid #e5e7eb;">` + metricLabel + `</td>
          <td style="padding:14px 18px;text-align:right;font-size:14px;font-weight:600;color:#111827;border-top:1px solid #e5e7eb;">` + metricValue + `</td>
        </tr>
        <tr>
          <td style="padding:14px 18px;color:#64748b;font-size:13px;border-top:1px solid #e5e7eb;">生成时间</td>
          <td style="padding:14px 18px;text-align:right;font-size:14px;font-weight:600;color:#111827;border-top:1px solid #e5e7eb;">{{.GeneratedAt}}</td>
        </tr>
      </table>
      <a href="{{.SubURL}}" style="display:inline-block;background:#2563eb;color:#ffffff;text-decoration:none;border-radius:8px;padding:12px 18px;font-size:14px;font-weight:700;">查看我的订阅</a>
      <p style="margin:24px 0 0;font-size:12px;line-height:1.6;color:#6b7280;">如果按钮无法打开，请复制以下链接：<br><span style="word-break:break-all;color:#374151;">{{.SubURL}}</span></p>
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
	if settings.ExpireBeforeDays <= 0 {
		settings.ExpireBeforeDays = 3
	}
	if settings.TrafficRemainPercent <= 0 {
		settings.TrafficRemainPercent = 10
	}
	if settings.TrafficRemainPercent > 100 {
		return fmt.Errorf("%w: traffic remain percent must be <= 100", domain.ErrValidation)
	}
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

func (s *Service) SaveTemplate(ctx context.Context, tpl *domain.MailTemplate) error {
	if tpl == nil {
		return fmt.Errorf("%w: template required", domain.ErrValidation)
	}
	switch tpl.Kind {
	case domain.MailReminderExpireBefore, domain.MailReminderExpired, domain.MailReminderTrafficLow, domain.MailReminderAccountDisable, domain.MailReminderAccountEnable:
	default:
		return fmt.Errorf("%w: invalid template kind", domain.ErrValidation)
	}
	tpl.Subject = strings.TrimSpace(tpl.Subject)
	if tpl.Subject == "" {
		return fmt.Errorf("%w: subject required", domain.ErrValidation)
	}
	if _, err := template.New("subject").Parse(tpl.Subject); err != nil {
		return fmt.Errorf("%w: subject template: %v", domain.ErrValidation, err)
	}
	if _, err := template.New("body").Parse(tpl.Body); err != nil {
		return fmt.Errorf("%w: body template: %v", domain.ErrValidation, err)
	}
	return s.repo.SaveTemplate(ctx, tpl)
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
	// Find the account_disabled template.
	var tpl *domain.MailTemplate
	for _, t := range templates {
		if t.Kind == domain.MailReminderAccountDisable && t.Enabled {
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
	data := s.templateData(ctx, settings, u)
	data["DisableReason"] = disableReason
	data["DisableDetail"] = disableDetail
	subject, err := renderTemplate("account_disabled_subject", tpl.Subject, data)
	if err != nil {
		return err
	}
	body, err := renderTemplate("account_disabled_body", tpl.Body, data)
	if err != nil {
		return err
	}
	return sendSMTP(ctx, settings, to, subject, body)
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
	data := s.templateData(ctx, settings, u)
	data["EnableReason"] = enableReason
	data["EnableDetail"] = enableDetail
	subject, err := renderTemplate("account_enabled_subject", tpl.Subject, data)
	if err != nil {
		return err
	}
	body, err := renderTemplate("account_enabled_body", tpl.Body, data)
	if err != nil {
		return err
	}
	return sendSMTP(ctx, settings, to, subject, body)
}

// SendAccountEnabledToUser is a convenience wrapper for sending enable notification.
func (s *Service) SendAccountEnabledToUser(ctx context.Context, userID int64, enableReason, enableDetail string) error {
	u, err := s.users.GetByID(ctx, userID)
	if err != nil {
		return err
	}
	return s.SendAccountEnabledNotification(ctx, u, enableReason, enableDetail)
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
	if _, err := template.New("announcement_subject").Parse(in.Subject); err != nil {
		return nil, fmt.Errorf("%w: subject template: %v", domain.ErrValidation, err)
	}
	if _, err := template.New("announcement_body").Parse(in.Body); err != nil {
		return nil, fmt.Errorf("%w: body template: %v", domain.ErrValidation, err)
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
			data := s.templateData(ctx, settings, u)
			subject, err := renderTemplate("announcement_subject", in.Subject, data)
			if err != nil {
				result.Failed++
				result.Errors = append(result.Errors, announcementError(u, to, err))
				continue
			}
			body, err := renderTemplate("announcement_body", in.Body, data)
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
			if err := s.processUser(ctx, settings, byKind, u, now); err != nil {
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

func (s *Service) processUser(ctx context.Context, settings domain.MailSettings, templates map[domain.MailReminderKind]*domain.MailTemplate, u *domain.User, now time.Time) error {
	to := reminderAddress(u)
	if to == "" {
		return nil
	}
	data := s.templateData(ctx, settings, u)
	if u.ExpireAt != nil {
		window := strconv.FormatInt(u.ExpireAt.Unix(), 10)
		if !u.ExpireAt.After(now) {
			if err := s.maybeSend(ctx, settings, templates[domain.MailReminderExpired], u, to, domain.MailReminderExpired, window, data); err != nil {
				return err
			}
		} else if u.ExpireAt.Sub(now) <= time.Duration(settings.ExpireBeforeDays)*24*time.Hour {
			if err := s.maybeSend(ctx, settings, templates[domain.MailReminderExpireBefore], u, to, domain.MailReminderExpireBefore, window, data); err != nil {
				return err
			}
		}
	}
	if u.TrafficLimitBytes > 0 && settings.TrafficRemainPercent > 0 {
		used, err := s.periodUsage(ctx, u)
		if err == nil {
			remain := u.TrafficLimitBytes - used
			if remain < 0 {
				remain = 0
			}
			if remain*100 <= int64(settings.TrafficRemainPercent)*u.TrafficLimitBytes {
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
	sent, err := s.repo.HasSent(ctx, u.ID, kind, windowKey)
	if err != nil || sent {
		return err
	}
	subject, err := renderTemplate("subject", tpl.Subject, data)
	if err != nil {
		return err
	}
	body, err := renderTemplate("body", tpl.Body, data)
	if err != nil {
		return err
	}
	if err := sendSMTP(ctx, settings, to, subject, body); err != nil {
		return err
	}
	return s.repo.RecordSent(ctx, u.ID, kind, windowKey, to)
}

func (s *Service) templateData(ctx context.Context, settings domain.MailSettings, u *domain.User) map[string]any {
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
	siteTitle := "Passwall"
	logoURL := "/images/logo+title-circle.png"
	subURL := "/sub/" + u.SubToken
	if s.settings != nil {
		st, err := s.settings.Load(ctx, ports.UISettings{
			SiteTitle: "Passwall",
			AppTitle:  "Passwall",
			LogoURL:   "/images/logo+title-circle.png",
		})
		if err == nil {
			if st.SiteTitle != "" {
				siteTitle = st.SiteTitle
			}
			if st.LogoURL != "" {
				logoURL = st.LogoURL
			}
			base := strings.TrimRight(st.SubBaseURL, "/")
			if base != "" {
				subURL = base + "/sub/" + u.SubToken
				logoURL = absoluteURL(base, logoURL)
			}
		}
	}
	return map[string]any{
		"UserID":               u.ID,
		"UPN":                  u.UPN,
		"DisplayName":          name,
		"Email":                u.Email,
		"SiteTitle":            siteTitle,
		"LogoURL":              logoURL,
		"GeneratedAt":          time.Now().Format("2006-01-02 15:04"),
		"ExpireAt":             expireAt,
		"ExpireBeforeDays":     settings.ExpireBeforeDays,
		"TrafficRemainPercent": settings.TrafficRemainPercent,
		"PeriodUsedGB":         periodUsedGB,
		"TrafficLimitGB":       trafficLimitGB,
		"TrafficRemainGB":      trafficRemainGB,
		"SubURL":               subURL,
	}
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

func (s *Service) periodUsage(ctx context.Context, u *domain.User) (int64, error) {
	latest, err := s.traffic.LatestForUser(ctx, u.ID)
	if err != nil || latest == nil {
		return 0, err
	}
	if u.TrafficPeriodStart == nil {
		return latest.TotalBytes, nil
	}
	base, err := s.traffic.LastBefore(ctx, u.ID, *u.TrafficPeriodStart)
	if err != nil || base == nil {
		return latest.TotalBytes, nil
	}
	used := latest.TotalBytes - base.TotalBytes
	if used < 0 {
		return latest.TotalBytes, nil
	}
	return used, nil
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

func gb(bytes int64) string {
	return strconv.FormatFloat(float64(bytes)/1024/1024/1024, 'f', 2, 64)
}

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
		return ctx.Err()
	case result = <-ch:
	}
	if result.err != nil {
		return result.err
	}
	c, err := smtp.NewClient(result.conn, settings.SMTPHost)
	if err != nil {
		_ = result.conn.Close()
		return err
	}
	defer c.Close()
	if settings.Encryption == "starttls" {
		if err := c.StartTLS(&tls.Config{ServerName: settings.SMTPHost, MinVersion: tls.VersionTLS12}); err != nil {
			return err
		}
	}
	if settings.SMTPUsername != "" {
		if err := c.Auth(smtp.PlainAuth("", settings.SMTPUsername, settings.SMTPPassword, settings.SMTPHost)); err != nil {
			return err
		}
	}
	if err := c.Mail(settings.FromEmail); err != nil {
		return err
	}
	if err := c.Rcpt(to); err != nil {
		return err
	}
	w, err := c.Data()
	if err != nil {
		return err
	}
	msg := buildMessage(fromAddr.String(), to, subject, body)
	if _, err := w.Write([]byte(msg)); err != nil {
		_ = w.Close()
		return err
	}
	if err := w.Close(); err != nil {
		return err
	}
	return c.Quit()
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
