/**
 * The variables a mail template can use, with the key of each one's description (settings.mail.tpl_vars.<key>).
 * Most edits get the syntax wrong on the first try ("{{ExpireAt}}" for "{{.ExpireAt}}"), so the list is shown
 * beside the editor instead of left to the source.
 */
export const MAIL_VARS: readonly (readonly [code: string, key: string])[] = [
  ['{{.UserID}}', 'user_id'],
  ['{{.UPN}}', 'upn'],
  ['{{.DisplayName}}', 'display_name'],
  ['{{.Email}}', 'email'],
  ['{{.ExpireAt}}', 'expire_at'],
  ['{{.ExpireBeforeDays}}', 'expire_before_days'],
  ['{{.TrafficRemainPercent}}', 'traffic_remain_percent'],
  ['{{.TrafficRemainGB}}', 'traffic_remain_gb'],
  ['{{.PeriodUsedGB}}', 'period_used_gb'],
  ['{{.TrafficLimitGB}}', 'traffic_limit_gb'],
  ['{{.DisableReason}}', 'disable_reason'],
  ['{{.DisableDetail}}', 'disable_detail'],
  ['{{.EnableReason}}', 'enable_reason'],
  ['{{.EnableDetail}}', 'enable_detail'],
  ['{{.SuspendReason}}', 'suspend_reason'],
  ['{{.SuspendReasonCode}}', 'suspend_reason_code'],
  ['{{.SuspendDetail}}', 'suspend_detail'],
  ['{{.AnnouncementTitle}}', 'announcement_title'],
  ['{{.AnnouncementBody}}', 'announcement_body'],
  ['{{.AnnouncementBodyHTML}}', 'announcement_body_html'],
  ['{{.ClientName}}', 'client_name'],
  ['{{.PanelURL}}', 'panel_url'],
  ['{{.SiteTitle}}', 'site_title'],
  ['{{.LogoURL}}', 'logo_url'],
  ['{{.GeneratedAt}}', 'generated_at'],
  ['{{.OTPCode}}', 'otp_code'],
  ['{{.ResetLink}}', 'reset_link'],
  ['{{.VerifyLink}}', 'verify_link'],
  ['{{.ExpireMinutes}}', 'expire_minutes'],
]
