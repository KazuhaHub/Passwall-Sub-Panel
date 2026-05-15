package xui

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

// Client implements ports.XUIClient for a single 3X-UI panel.
//
// Auth priority:
//  1. If APIToken is non-empty, requests use Authorization: Bearer.
//  2. Otherwise the client logs in with username/password once and reuses
//     the cookie session; on 401 it re-logs in transparently.
//
// All client-write operations (AddClient/UpdateClient) follow a
// read-modify-write pattern: fetch the current Inbound, mutate
// settings.clients[], then post the modified object back. This is the only
// way to avoid clobbering pre-existing clients that the panel does not
// manage (e.g. the operator's personal clients).
type Client struct {
	panelName string
	baseURL   string
	apiToken  string
	username  string
	password  string

	http   *http.Client
	mu     sync.Mutex
	jar    http.CookieJar
	authed bool
}

// New constructs a Client for the given 3X-UI panel.
func New(p *domain.XUIPanel) (*Client, error) {
	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil, err
	}
	return &Client{
		panelName: p.Name,
		baseURL:   strings.TrimRight(p.URL, "/"),
		apiToken:  p.APIToken,
		username:  p.Username,
		password:  p.Password,
		http: &http.Client{
			Timeout: 30 * time.Second,
			Jar:     jar,
		},
		jar: jar,
	}, nil
}

// --- Auth ---

func (c *Client) ensureAuth(ctx context.Context) error {
	if c.apiToken != "" {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.authed {
		return nil
	}
	return c.loginLocked(ctx)
}

func (c *Client) loginLocked(ctx context.Context) error {
	form := url.Values{}
	form.Set("username", c.username)
	form.Set("password", c.password)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.baseURL+"/login", strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	var r genericResponse
	if err := json.Unmarshal(b, &r); err != nil {
		return fmt.Errorf("login: %w (raw: %s)", err, string(b))
	}
	if !r.Success {
		return fmt.Errorf("login failed: %s", r.Msg)
	}
	c.authed = true
	return nil
}

func (c *Client) doJSON(ctx context.Context, method, path string, body any, out any) error {
	if err := c.ensureAuth(ctx); err != nil {
		return err
	}
	var reader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, reader)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.apiToken != "" {
		req.Header.Set("Authorization", "Bearer "+c.apiToken)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	// On 401 in cookie mode, drop the cached session and retry once.
	if resp.StatusCode == http.StatusUnauthorized && c.apiToken == "" {
		c.mu.Lock()
		c.authed = false
		c.mu.Unlock()
		if err := c.ensureAuth(ctx); err != nil {
			return err
		}
		return c.doJSON(ctx, method, path, body, out)
	}

	b, _ := io.ReadAll(resp.Body)
	trimmed := strings.TrimSpace(string(b))

	// Distinguish common 3X-UI failure shapes from a real JSON parse error
	// so the operator gets an actionable message instead of "unexpected end
	// of JSON input".
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("%s %s: HTTP %d (body: %s)",
			method, path, resp.StatusCode, snippet(trimmed, 200))
	}
	if trimmed == "" {
		hint := "verify URL and api_token / username+password — 3X-UI returns an empty body when auth is wrong"
		if c.apiToken == "" {
			hint = "verify username/password — 3X-UI returns an empty body when cookie auth is wrong"
		}
		return fmt.Errorf("%s %s: empty response body (HTTP %d) — %s",
			method, path, resp.StatusCode, hint)
	}
	if trimmed[0] != '{' && trimmed[0] != '[' {
		return fmt.Errorf("%s %s: non-JSON response (HTTP %d) — likely an auth redirect or wrong endpoint (preview: %s)",
			method, path, resp.StatusCode, snippet(trimmed, 120))
	}

	var r genericResponse
	if err := json.Unmarshal(b, &r); err != nil {
		return fmt.Errorf("%s %s: parse: %w (raw: %s)", method, path, err, snippet(trimmed, 200))
	}
	if !r.Success {
		return fmt.Errorf("%s %s: %s", method, path, r.Msg)
	}
	if out != nil && len(r.Obj) > 0 {
		if err := json.Unmarshal(r.Obj, out); err != nil {
			return fmt.Errorf("%s %s: decode obj: %w", method, path, err)
		}
	}
	return nil
}

// snippet truncates s to n chars with an ellipsis, suitable for embedding
// in an error message without dumping a huge HTML body to logs.
func snippet(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// --- Inbound ---

func (c *Client) ListInbounds(ctx context.Context) ([]ports.Inbound, error) {
	var raws []rawInbound
	if err := c.doJSON(ctx, http.MethodGet, "/panel/api/inbounds/list", nil, &raws); err != nil {
		return nil, err
	}
	out := make([]ports.Inbound, len(raws))
	for i := range raws {
		out[i] = rawToInbound(&raws[i])
	}
	return out, nil
}

func (c *Client) GetInbound(ctx context.Context, id int) (*ports.Inbound, error) {
	var raw rawInbound
	if err := c.doJSON(ctx, http.MethodGet, "/panel/api/inbounds/get/"+strconv.Itoa(id), nil, &raw); err != nil {
		return nil, err
	}
	in := rawToInbound(&raw)
	return &in, nil
}

func (c *Client) AddInbound(ctx context.Context, spec ports.InboundSpec) (int, error) {
	body := specToRaw(&spec, 0)
	var out rawInbound
	if err := c.doJSON(ctx, http.MethodPost, "/panel/api/inbounds/add", body, &out); err != nil {
		return 0, err
	}
	return out.ID, nil
}

func (c *Client) UpdateInbound(ctx context.Context, id int, spec ports.InboundSpec) error {
	mergedSpec := spec
	settings, err := c.settingsWithCurrentClients(ctx, id, spec.Settings)
	if err != nil {
		return err
	}
	mergedSpec.Settings = settings
	body := specToRaw(&mergedSpec, id)
	return c.doJSON(ctx, http.MethodPost, "/panel/api/inbounds/update/"+strconv.Itoa(id), body, nil)
}

func (c *Client) DelInbound(ctx context.Context, id int) error {
	return c.doJSON(ctx, http.MethodPost, "/panel/api/inbounds/del/"+strconv.Itoa(id), nil, nil)
}

func (c *Client) SetInboundEnable(ctx context.Context, id int, enable bool) error {
	body := map[string]any{"enable": enable}
	return c.doJSON(ctx, http.MethodPost, "/panel/api/inbounds/setEnable/"+strconv.Itoa(id), body, nil)
}

// --- Client (read-modify-write) ---

// AddClient appends spec to inbound.settings.clients[] without disturbing any
// existing entry. The 3X-UI /addClient endpoint accepts an Inbound object
// whose settings.clients[] contains only the rows to be added; the server
// merges them into the live config.
func (c *Client) AddClient(ctx context.Context, inboundID int, spec ports.ClientSpec) error {
	clientJSON, err := buildClientJSON(spec)
	if err != nil {
		return err
	}
	settings := map[string]any{
		"clients": []json.RawMessage{clientJSON},
	}
	settingsJSON, _ := json.Marshal(settings)
	body := map[string]any{
		"id":       inboundID,
		"settings": string(settingsJSON),
	}
	return c.doJSON(ctx, http.MethodPost, "/panel/api/inbounds/addClient", body, nil)
}

// UpdateClient replaces the client identified by clientUUID with the values
// in spec. clientUUID is the value of client.id / uuid.
func (c *Client) UpdateClient(ctx context.Context, inboundID int, clientUUID string, spec ports.ClientSpec) error {
	inb, err := c.GetInbound(ctx, inboundID)
	if err != nil {
		return err
	}
	settings, err := updateClientInSettings(inb.Settings, clientUUID, spec)
	if err != nil {
		return err
	}
	inboundSpec := ports.InboundSpec{
		Remark:         inb.Remark,
		Enable:         inb.Enable,
		Listen:         inb.Listen,
		Port:           inb.Port,
		Protocol:       inb.Protocol,
		Settings:       settings,
		StreamSettings: inb.StreamSettings,
		Sniffing:       inb.Sniffing,
		Allocate:       inb.Allocate,
		ExpiryTime:     inb.ExpiryTime,
	}
	body := specToRaw(&inboundSpec, inboundID)
	return c.doJSON(ctx, http.MethodPost, "/panel/api/inbounds/update/"+strconv.Itoa(inboundID), body, nil)
}

func (c *Client) DelClient(ctx context.Context, inboundID int, clientUUID string) error {
	path := fmt.Sprintf("/panel/api/inbounds/%d/delClient/%s", inboundID, clientUUID)
	return c.doJSON(ctx, http.MethodPost, path, nil, nil)
}

func (c *Client) DelClientByEmail(ctx context.Context, inboundID int, email string) error {
	path := fmt.Sprintf("/panel/api/inbounds/%d/delClientByEmail/%s", inboundID, url.PathEscape(email))
	return c.doJSON(ctx, http.MethodPost, path, nil, nil)
}

func (c *Client) CopyClients(ctx context.Context, srcInboundID, dstInboundID int, emails []string) error {
	body := map[string]any{
		"sourceInboundId": srcInboundID,
		"emails":          emails,
	}
	path := fmt.Sprintf("/panel/api/inbounds/%d/copyClients", dstInboundID)
	return c.doJSON(ctx, http.MethodPost, path, body, nil)
}

// --- Traffic ---

func (c *Client) GetClientTraffic(ctx context.Context, email string) ([]ports.ClientTraffic, error) {
	var raw json.RawMessage
	path := "/panel/api/inbounds/getClientTraffics/" + url.PathEscape(email)
	if err := c.doJSON(ctx, http.MethodGet, path, nil, &raw); err != nil {
		return nil, err
	}
	raws, err := decodeTrafficObj(raw)
	if err != nil {
		return nil, fmt.Errorf("decode client traffic: %w", err)
	}
	return rawTrafficsToPorts(raws), nil
}

func (c *Client) GetInboundTraffics(ctx context.Context, id int) ([]ports.ClientTraffic, error) {
	var raw json.RawMessage
	path := "/panel/api/inbounds/getClientTrafficsById/" + strconv.Itoa(id)
	if err := c.doJSON(ctx, http.MethodGet, path, nil, &raw); err != nil {
		return nil, err
	}
	raws, err := decodeTrafficObj(raw)
	if err != nil {
		return nil, fmt.Errorf("decode inbound traffic: %w", err)
	}
	return rawTrafficsToPorts(raws), nil
}

func (c *Client) ResetClientTraffic(ctx context.Context, inboundID int, email string) error {
	path := fmt.Sprintf("/panel/api/inbounds/%d/resetClientTraffic/%s", inboundID, url.PathEscape(email))
	return c.doJSON(ctx, http.MethodPost, path, nil, nil)
}

// GetInboundClients fetches the inbound and decodes settings.clients[] into
// a normalised slice. Returns empty if the inbound has no clients defined.
func (c *Client) GetInboundClients(ctx context.Context, inboundID int) ([]ports.ClientDetail, error) {
	inb, err := c.GetInbound(ctx, inboundID)
	if err != nil {
		return nil, err
	}
	if inb.Settings == "" {
		return nil, nil
	}
	var s struct {
		Clients []struct {
			ID         string `json:"id"`
			Email      string `json:"email"`
			Enable     *bool  `json:"enable"`
			Flow       string `json:"flow"`
			Password   string `json:"password"`
			ExpiryTime int64  `json:"expiryTime"`
			TotalGB    int64  `json:"totalGB"`
		} `json:"clients"`
	}
	if err := json.Unmarshal([]byte(inb.Settings), &s); err != nil {
		return nil, fmt.Errorf("decode inbound settings: %w", err)
	}
	out := make([]ports.ClientDetail, len(s.Clients))
	for i, src := range s.Clients {
		enable := true
		if src.Enable != nil {
			enable = *src.Enable
		}
		out[i] = ports.ClientDetail{
			ID:         src.ID,
			Email:      src.Email,
			Enable:     enable,
			Flow:       src.Flow,
			Password:   src.Password,
			ExpiryTime: src.ExpiryTime,
			TotalGB:    src.TotalGB,
		}
	}
	return out, nil
}

// --- helpers ---

func rawToInbound(r *rawInbound) ports.Inbound {
	return ports.Inbound{
		ID:             r.ID,
		Up:             r.Up,
		Down:           r.Down,
		Total:          r.Total,
		Remark:         r.Remark,
		Enable:         r.Enable,
		ExpiryTime:     r.ExpiryTime,
		Listen:         r.Listen,
		Port:           r.Port,
		Protocol:       r.Protocol,
		Settings:       r.Settings,
		StreamSettings: r.StreamSettings,
		Tag:            r.Tag,
		Sniffing:       r.Sniffing,
		Allocate:       r.Allocate,
		ClientStats:    rawTrafficsToPorts(r.ClientStats),
	}
}

func specToRaw(s *ports.InboundSpec, id int) map[string]any {
	return map[string]any{
		"id":             id,
		"remark":         s.Remark,
		"enable":         s.Enable,
		"listen":         s.Listen,
		"port":           s.Port,
		"protocol":       s.Protocol,
		"settings":       s.Settings,
		"streamSettings": s.StreamSettings,
		"sniffing":       s.Sniffing,
		"allocate":       s.Allocate,
		"expiryTime":     s.ExpiryTime,
	}
}

func rawTrafficsToPorts(raws []rawClientTraffic) []ports.ClientTraffic {
	out := make([]ports.ClientTraffic, len(raws))
	for i, r := range raws {
		out[i] = ports.ClientTraffic{
			ID:         r.ID,
			InboundID:  r.InboundID,
			Email:      r.Email,
			Up:         r.Up,
			Down:       r.Down,
			Total:      r.Total,
			Enable:     r.Enable,
			ExpiryTime: r.ExpiryTime,
			Reset:      r.Reset,
		}
	}
	return out
}

func decodeTrafficObj(raw json.RawMessage) ([]rawClientTraffic, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return nil, nil
	}
	if trimmed[0] == '[' {
		var raws []rawClientTraffic
		if err := json.Unmarshal(trimmed, &raws); err != nil {
			return nil, err
		}
		return raws, nil
	}
	var one rawClientTraffic
	if err := json.Unmarshal(trimmed, &one); err != nil {
		return nil, err
	}
	if one.Email == "" && one.ID == 0 && one.InboundID == 0 && one.Up == 0 && one.Down == 0 && one.Total == 0 {
		return nil, nil
	}
	return []rawClientTraffic{one}, nil
}

func (c *Client) settingsWithCurrentClients(ctx context.Context, inboundID int, nextSettings string) (string, error) {
	if strings.TrimSpace(nextSettings) == "" {
		return nextSettings, nil
	}
	inb, err := c.GetInbound(ctx, inboundID)
	if err != nil {
		return "", err
	}
	return replaceSettingsClients(nextSettings, inb.Settings)
}

func replaceSettingsClients(nextSettings, currentSettings string) (string, error) {
	currentClients, err := clientsFromSettings(currentSettings)
	if err != nil {
		return "", err
	}
	if len(currentClients) == 0 {
		return nextSettings, nil
	}
	var next map[string]any
	if err := json.Unmarshal([]byte(nextSettings), &next); err != nil {
		return "", fmt.Errorf("decode inbound settings: %w", err)
	}
	next["clients"] = currentClients
	b, err := json.Marshal(next)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func updateClientInSettings(settingsJSON, clientUUID string, spec ports.ClientSpec) (string, error) {
	var settings map[string]any
	if err := json.Unmarshal([]byte(settingsJSON), &settings); err != nil {
		return "", fmt.Errorf("decode inbound settings: %w", err)
	}
	clients, err := clientsFromSettings(settingsJSON)
	if err != nil {
		return "", err
	}
	if len(clients) == 0 {
		return "", fmt.Errorf("client not found in inbound settings: email=%s id=%s", spec.Email, clientUUID)
	}
	clientJSON, err := buildClientJSON(spec)
	if err != nil {
		return "", err
	}
	var nextClient map[string]any
	if err := json.Unmarshal(clientJSON, &nextClient); err != nil {
		return "", err
	}
	for i, existing := range clients {
		if !clientMatches(existing, clientUUID, spec.Email) {
			continue
		}
		merged := make(map[string]any, len(existing)+len(nextClient))
		for k, v := range existing {
			merged[k] = v
		}
		for k, v := range nextClient {
			merged[k] = v
		}
		clients[i] = merged
		settings["clients"] = clients
		b, err := json.Marshal(settings)
		if err != nil {
			return "", err
		}
		return string(b), nil
	}
	return "", fmt.Errorf("client not found in inbound settings: email=%s id=%s", spec.Email, clientUUID)
}

func clientsFromSettings(settingsJSON string) ([]map[string]any, error) {
	if strings.TrimSpace(settingsJSON) == "" {
		return nil, nil
	}
	var settings struct {
		Clients []map[string]any `json:"clients"`
	}
	if err := json.Unmarshal([]byte(settingsJSON), &settings); err != nil {
		return nil, fmt.Errorf("decode inbound settings: %w", err)
	}
	return settings.Clients, nil
}

func clientMatches(client map[string]any, id, email string) bool {
	if id != "" && stringValue(client["id"]) == id {
		return true
	}
	return email != "" && stringValue(client["email"]) == email
}

func stringValue(v any) string {
	switch x := v.(type) {
	case string:
		return x
	default:
		return fmt.Sprint(x)
	}
}

// buildClientJSON serializes a ClientSpec into the JSON shape 3X-UI expects.
// Field names follow the 3X-UI XrayClient model:
// id / email / enable / flow / limitIp / totalGB / expiryTime / subId / tgId / reset / password / method
func buildClientJSON(s ports.ClientSpec) (json.RawMessage, error) {
	obj := map[string]any{
		"email":      s.Email,
		"enable":     s.Enable,
		"limitIp":    s.LimitIP,
		"totalGB":    s.TotalGB,
		"expiryTime": s.ExpiryTime,
		"subId":      s.SubID,
		"tgId":       s.TgID,
		"reset":      s.Reset,
	}
	if s.ID != "" {
		obj["id"] = s.ID
	}
	if s.Flow != "" {
		obj["flow"] = s.Flow
	}
	if s.Password != "" {
		obj["password"] = s.Password
	}
	if s.Method != "" {
		obj["method"] = s.Method
	}
	return json.Marshal(obj)
}
