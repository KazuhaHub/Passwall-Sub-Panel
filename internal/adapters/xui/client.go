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
	var r genericResponse
	if err := json.Unmarshal(b, &r); err != nil {
		return fmt.Errorf("%s %s: parse: %w (raw: %s)", method, path, err, string(b))
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
	body := specToRaw(&spec, id)
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
	return c.doJSON(ctx, http.MethodPost,
		"/panel/api/inbounds/updateClient/"+clientUUID, body, nil)
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
	var raws []rawClientTraffic
	path := "/panel/api/inbounds/getClientTraffics/" + url.PathEscape(email)
	if err := c.doJSON(ctx, http.MethodGet, path, nil, &raws); err != nil {
		return nil, err
	}
	return rawTrafficsToPorts(raws), nil
}

func (c *Client) GetInboundTraffics(ctx context.Context, id int) ([]ports.ClientTraffic, error) {
	var raws []rawClientTraffic
	path := "/panel/api/inbounds/getClientTrafficsById/" + strconv.Itoa(id)
	if err := c.doJSON(ctx, http.MethodGet, path, nil, &raws); err != nil {
		return nil, err
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
