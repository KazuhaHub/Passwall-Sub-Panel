// Package sui implements the portable panel contract against S-UI's token
// authenticated /apiv2 API.
package sui

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/safehttp"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

type Client struct {
	panelName string
	baseURL   string
	token     string
	http      *http.Client
	writeMu   sync.Mutex
}

func New(p *domain.Panel) (*Client, error) {
	if p == nil {
		return nil, fmt.Errorf("S-UI panel definition is nil")
	}
	if strings.TrimSpace(p.URL) == "" {
		return nil, fmt.Errorf("S-UI panel URL is required")
	}
	if strings.TrimSpace(p.APIToken) == "" {
		return nil, fmt.Errorf("S-UI /apiv2 requires an API token")
	}
	if p.AuthMethod == domain.XUIAuthPassword {
		return nil, fmt.Errorf("S-UI adapter supports API token authentication only")
	}
	return &Client{
		panelName: p.Name,
		baseURL:   strings.TrimRight(p.URL, "/"),
		token:     p.APIToken,
		http:      safehttp.NewClientTLS(30*time.Second, p.InsecureSkipVerify),
	}, nil
}

func (c *Client) Capabilities() []ports.PanelCapability {
	return []ports.PanelCapability{
		ports.CapabilityInboundRead,
		ports.CapabilityInboundCreate,
		ports.CapabilityInboundUpdate,
		ports.CapabilityInboundDelete,
		ports.CapabilityClientRead,
		ports.CapabilityClientWrite,
		ports.CapabilityTrafficRead,
		ports.CapabilityStatusRead,
	}
}

type response struct {
	Success bool            `json:"success"`
	Msg     string          `json:"msg"`
	Obj     json.RawMessage `json:"obj"`
}

func (c *Client) do(ctx context.Context, method, path string, form url.Values, out any) error {
	var body io.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+"/apiv2/"+strings.TrimLeft(path, "/"), body)
	if err != nil {
		return err
	}
	req.Header.Set("Token", c.token)
	if form != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("[%s] %w", c.panelName, err)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("[%s] %s %s: HTTP %d: %s", c.panelName, method, path, resp.StatusCode, snippet(b, 240))
	}
	var envelope response
	if err := json.Unmarshal(b, &envelope); err != nil {
		return fmt.Errorf("[%s] %s %s: decode response: %w", c.panelName, method, path, err)
	}
	if !envelope.Success {
		return fmt.Errorf("[%s] %s %s: %s", c.panelName, method, path, envelope.Msg)
	}
	if out != nil && len(envelope.Obj) > 0 && !bytes.Equal(envelope.Obj, []byte("null")) {
		if err := json.Unmarshal(envelope.Obj, out); err != nil {
			return fmt.Errorf("[%s] %s %s: decode obj: %w", c.panelName, method, path, err)
		}
	}
	return nil
}

func snippet(b []byte, max int) string {
	s := strings.TrimSpace(string(b))
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}

type clientModel struct {
	ID         int                       `json:"id,omitempty"`
	Enable     bool                      `json:"enable"`
	Name       string                    `json:"name"`
	Config     map[string]map[string]any `json:"config,omitempty"`
	Inbounds   []int                     `json:"inbounds"`
	Links      json.RawMessage           `json:"links,omitempty"`
	Volume     int64                     `json:"volume"`
	Expiry     int64                     `json:"expiry"`
	Down       int64                     `json:"down"`
	Up         int64                     `json:"up"`
	Desc       string                    `json:"desc"`
	Group      string                    `json:"group"`
	Remark     string                    `json:"remark,omitempty"`
	CreatedAt  int64                     `json:"createdAt,omitempty"`
	OnlineAt   int64                     `json:"onlineAt,omitempty"`
	DelayStart bool                      `json:"delayStart,omitempty"`
	AutoReset  bool                      `json:"autoReset,omitempty"`
	ResetDays  int                       `json:"resetDays,omitempty"`
	NextReset  int64                     `json:"nextReset,omitempty"`
	TotalUp    int64                     `json:"totalUp,omitempty"`
	TotalDown  int64                     `json:"totalDown,omitempty"`
}

func (c *Client) listClients(ctx context.Context) ([]clientModel, error) {
	var obj struct {
		Clients []clientModel `json:"clients"`
	}
	if err := c.do(ctx, http.MethodGet, "clients", nil, &obj); err != nil {
		return nil, err
	}
	return obj.Clients, nil
}

func (c *Client) getClientModel(ctx context.Context, name string) (*clientModel, error) {
	items, err := c.listClients(ctx)
	if err != nil {
		return nil, err
	}
	for i := range items {
		if items[i].Name != name {
			continue
		}
		var obj struct {
			Clients []clientModel `json:"clients"`
		}
		if err := c.do(ctx, http.MethodGet, "clients?id="+strconv.Itoa(items[i].ID), nil, &obj); err != nil {
			return nil, err
		}
		if len(obj.Clients) == 0 {
			return nil, nil
		}
		return &obj.Clients[0], nil
	}
	return nil, nil
}

func (c *Client) saveInto(ctx context.Context, object, action string, data any, out any) error {
	return c.saveIntoWithInitialUsers(ctx, object, action, data, "", out)
}

func (c *Client) saveIntoWithInitialUsers(ctx context.Context, object, action string, data any, initialUsers string, out any) error {
	raw, err := json.Marshal(data)
	if err != nil {
		return err
	}
	form := url.Values{
		"object": {object},
		"action": {action},
		"data":   {string(raw)},
	}
	if initialUsers != "" {
		form.Set("initUsers", initialUsers)
	}
	return c.do(ctx, http.MethodPost, "save", form, out)
}

func (c *Client) save(ctx context.Context, object, action string, data any) error {
	return c.saveInto(ctx, object, action, data, nil)
}

func configFromSpec(spec ports.ClientSpec) map[string]map[string]any {
	name := spec.Email
	password := spec.Password
	if password == "" {
		password = spec.Auth
	}
	return map[string]map[string]any{
		"vmess":         {"name": name, "uuid": spec.ID, "alterId": 0},
		"vless":         {"name": name, "uuid": spec.ID, "flow": spec.Flow},
		"trojan":        {"name": name, "password": password},
		"shadowsocks":   {"name": name, "password": password},
		"shadowsocks16": {"name": name, "password": password},
		"shadowsocks32": {"name": name, "password": password},
		"hysteria2":     {"name": name, "password": password},
	}
}

func modelFromSpec(spec ports.ClientSpec, inboundIDs []int) clientModel {
	expiry := spec.ExpiryTime
	if expiry > 0 {
		expiry /= 1000 // PSP uses ms; S-UI stores unix seconds.
	}
	return clientModel{
		Enable: spec.Enable, Name: spec.Email, Config: configFromSpec(spec),
		Inbounds: inboundIDs, Volume: spec.TotalGB, Expiry: expiry,
		Desc: "Managed by Passwall Sub Panel", Group: "PSP",
	}
}

func applySpec(model *clientModel, spec ports.ClientSpec) {
	model.Enable = spec.Enable
	model.Name = spec.Email
	model.Config = configFromSpec(spec)
	model.Volume = spec.TotalGB
	model.Expiry = spec.ExpiryTime
	if model.Expiry > 0 {
		model.Expiry /= 1000
	}
}

func (c *Client) AddClient(ctx context.Context, inboundID int, spec ports.ClientSpec) error {
	return c.AddClientToInbounds(ctx, []int{inboundID}, spec)
}

func (c *Client) AddClientToInbounds(ctx context.Context, inboundIDs []int, spec ports.ClientSpec) error {
	if spec.Email == "" || len(inboundIDs) == 0 {
		return fmt.Errorf("S-UI add client requires name and at least one inbound")
	}
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	return c.save(ctx, "clients", "new", modelFromSpec(spec, inboundIDs))
}

func (c *Client) UpdateClient(ctx context.Context, _ int, _ string, spec ports.ClientSpec) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	model, err := c.getClientModel(ctx, spec.Email)
	if err != nil {
		return err
	}
	if model == nil {
		return fmt.Errorf("%w: S-UI client %q not found", domain.ErrValidation, spec.Email)
	}
	applySpec(model, spec)
	return c.save(ctx, "clients", "edit", model)
}

func (c *Client) UpdateClientWithInbound(ctx context.Context, inb *ports.Inbound, clientUUID string, spec ports.ClientSpec) error {
	if inb == nil {
		return fmt.Errorf("S-UI update client: inbound is nil")
	}
	return c.UpdateClient(ctx, inb.ID, clientUUID, spec)
}

func (c *Client) DelClientByEmail(ctx context.Context, _ int, email string) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	model, err := c.getClientModel(ctx, email)
	if err != nil || model == nil {
		return err
	}
	return c.save(ctx, "clients", "del", model.ID)
}

func (c *Client) GetClient(ctx context.Context, email string) (*ports.ClientDetail, error) {
	model, err := c.getClientModel(ctx, email)
	if err != nil || model == nil {
		return nil, err
	}
	detail := &ports.ClientDetail{Email: model.Name, Enable: model.Enable, InboundIDs: append([]int(nil), model.Inbounds...)}
	for _, key := range []string{"vless", "vmess"} {
		if cfg := model.Config[key]; cfg != nil {
			detail.ID, _ = cfg["uuid"].(string)
			if key == "vless" {
				detail.Flow, _ = cfg["flow"].(string)
			}
			if detail.ID != "" {
				break
			}
		}
	}
	for _, key := range []string{"trojan", "shadowsocks", "shadowsocks16", "shadowsocks32"} {
		if cfg := model.Config[key]; cfg != nil {
			if value, _ := cfg["password"].(string); value != "" {
				detail.Password = value
				break
			}
		}
	}
	if cfg := model.Config["hysteria2"]; cfg != nil {
		detail.Auth, _ = cfg["password"].(string)
	}
	if model.Expiry > 0 {
		detail.ExpiryTime = model.Expiry * 1000
	}
	detail.TotalGB = model.Volume
	return detail, nil
}

func (c *Client) ListClientInbounds(ctx context.Context) (map[string][]int, error) {
	items, err := c.listClients(ctx)
	if err != nil {
		return nil, err
	}
	out := make(map[string][]int, len(items))
	for _, item := range items {
		out[item.Name] = append([]int(nil), item.Inbounds...)
	}
	return out, nil
}

func (c *Client) BulkDelByEmail(ctx context.Context, emails []string) (int, error) {
	if len(emails) == 0 {
		return 0, nil
	}
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	items, err := c.listClients(ctx)
	if err != nil {
		return 0, err
	}
	wanted := make(map[string]bool, len(emails))
	for _, email := range emails {
		wanted[email] = true
	}
	ids := make([]int, 0, len(emails))
	for _, item := range items {
		if wanted[item.Name] {
			ids = append(ids, item.ID)
		}
	}
	if len(ids) == 0 {
		return 0, nil
	}
	if err := c.save(ctx, "clients", "delbulk", ids); err != nil {
		return 0, err
	}
	return len(ids), nil
}

func (c *Client) mutateAttachments(ctx context.Context, emails []string, inboundIDs []int, attach bool) (ports.BulkAttachResult, error) {
	result := ports.BulkAttachResult{}
	if len(emails) == 0 || len(inboundIDs) == 0 {
		return result, nil
	}
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	items, err := c.listClients(ctx)
	if err != nil {
		return result, err
	}
	wanted := make(map[string]bool, len(emails))
	for _, email := range emails {
		wanted[email] = true
	}
	changed := make([]clientModel, 0, len(emails))
	for _, summary := range items {
		if !wanted[summary.Name] {
			continue
		}
		// GET /clients intentionally omits config and links. editbulk performs a
		// full-row save, so submitting that summary would erase credentials.
		// Load the complete row before changing only its inbound bindings.
		var obj struct {
			Clients []clientModel `json:"clients"`
		}
		if err := c.do(ctx, http.MethodGet, "clients?id="+strconv.Itoa(summary.ID), nil, &obj); err != nil {
			return ports.BulkAttachResult{}, err
		}
		if len(obj.Clients) == 0 {
			result.Skipped = append(result.Skipped, summary.Name)
			continue
		}
		item := obj.Clients[0]
		set := make(map[int]bool, len(item.Inbounds)+len(inboundIDs))
		for _, id := range item.Inbounds {
			set[id] = true
		}
		before := len(set)
		if attach {
			for _, id := range inboundIDs {
				set[id] = true
			}
		} else {
			for _, id := range inboundIDs {
				delete(set, id)
			}
		}
		if len(set) == before {
			result.Skipped = append(result.Skipped, item.Name)
			continue
		}
		item.Inbounds = item.Inbounds[:0]
		for id := range set {
			item.Inbounds = append(item.Inbounds, id)
		}
		sort.Ints(item.Inbounds)
		changed = append(changed, item)
		result.Done = append(result.Done, item.Name)
	}
	if len(changed) > 0 {
		if err := c.save(ctx, "clients", "editbulk", changed); err != nil {
			return ports.BulkAttachResult{}, err
		}
	}
	return result, nil
}

func (c *Client) AttachClient(ctx context.Context, email string, inboundIDs []int) error {
	_, err := c.mutateAttachments(ctx, []string{email}, inboundIDs, true)
	return err
}

func (c *Client) DetachClient(ctx context.Context, email string, inboundIDs []int) error {
	_, err := c.mutateAttachments(ctx, []string{email}, inboundIDs, false)
	return err
}

func (c *Client) BulkAttach(ctx context.Context, emails []string, inboundIDs []int) (ports.BulkAttachResult, error) {
	return c.mutateAttachments(ctx, emails, inboundIDs, true)
}

func (c *Client) BulkDetach(ctx context.Context, emails []string, inboundIDs []int) (ports.BulkAttachResult, error) {
	return c.mutateAttachments(ctx, emails, inboundIDs, false)
}

func (c *Client) BulkCreateClients(ctx context.Context, items []ports.BulkCreateClientItem) (ports.BulkCreateResult, error) {
	models := make([]clientModel, 0, len(items))
	for _, item := range items {
		if item.Spec.Email != "" && len(item.InboundIDs) > 0 {
			models = append(models, modelFromSpec(item.Spec, item.InboundIDs))
		}
	}
	if len(models) == 0 {
		return ports.BulkCreateResult{}, nil
	}
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	if err := c.save(ctx, "clients", "addbulk", models); err != nil {
		return ports.BulkCreateResult{}, err
	}
	return ports.BulkCreateResult{Created: len(models)}, nil
}

// S-UI has no persisted per-inbound enable flag. Create/update/delete are
// implemented in inbounds_write.go, but toggling one inbound cannot be mapped
// without deleting its configuration, so that operation stays unsupported.
func unsupportedInboundWrite() error {
	return fmt.Errorf("%w: S-UI has no per-inbound enable switch", ports.ErrPanelCapabilityUnsupported)
}

func (c *Client) SetInboundEnable(context.Context, int, bool) error { return unsupportedInboundWrite() }

func (c *Client) GetServerStatus(ctx context.Context) (*ports.ServerStatus, error) {
	var obj struct {
		Sys struct {
			AppVersion string `json:"appVersion"`
		} `json:"sys"`
		SBD struct {
			Running bool `json:"running"`
		} `json:"sbd"`
	}
	if err := c.do(ctx, http.MethodGet, "status?r=sys,sbd", nil, &obj); err != nil {
		return nil, err
	}
	state := "stop"
	if obj.SBD.Running {
		state = "running"
	}
	return &ports.ServerStatus{PanelVersion: obj.Sys.AppVersion, XrayState: state}, nil
}

var (
	_ ports.PanelClient        = (*Client)(nil)
	_ ports.CapabilityProvider = (*Client)(nil)
)
