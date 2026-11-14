// Package destcatalog is the built-in catalog of ready-made rule groups an
// admin adds with one click ("BitTorrent", "Finance", "Adult content", ...):
// an embedded, versioned document that maps each group onto v2fly geosite
// categories, a protocol, ports or the private-address match.
//
// The catalog is read-only. A group's name and description may be edited on the
// policy it created, but its mapping may not: to change what it matches, copy
// it into a custom list. Nothing in it is enabled by default — adding a group
// asks the admin for its state.
//
// Categories v2fly does not have (a generic "education", "payment" or
// "gambling" group) are listed too, as UNAVAILABLE with the reason, and create
// nothing: an Iranian category passed off as a generic one would give an
// admin the impression of coverage they do not have.
package destcatalog

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

//go:embed catalog.json
var embedded []byte

// Locales the catalog carries text for. zh-CN is the default.
const (
	LocaleZH = "zh-CN"
	LocaleEN = "en-US"
)

// Localized is a text in each supported locale.
type Localized map[string]string

func (l Localized) clone() Localized {
	out := make(Localized, len(l))
	for k, v := range l {
		out[k] = v
	}
	return out
}

func (l Localized) pick(locale string) string {
	if v := l[locale]; v != "" {
		return v
	}
	if strings.HasPrefix(strings.ToLower(locale), "en") {
		return l[LocaleEN]
	}
	return l[LocaleZH]
}

// Template is one built-in rule group.
type Template struct {
	Key         string    `json:"key"`
	Name        Localized `json:"name"`
	Description Localized `json:"description"`
	// Geosite are the v2fly categories it covers; Protocols, Ports, Network
	// and Private are the policy's own built-in match.
	Geosite   []string `json:"geosite,omitempty"`
	Protocols []string `json:"protocols,omitempty"`
	Ports     string   `json:"ports,omitempty"`
	Network   string   `json:"network,omitempty"`
	Private   bool     `json:"private,omitempty"`
}

// Unavailable is a group the admin may look for and cannot have, with why.
type Unavailable struct {
	Key    string    `json:"key"`
	Name   Localized `json:"name"`
	Reason Localized `json:"reason"`
}

// Catalog is the embedded document.
type Catalog struct {
	Version     int           `json:"catalog_version"`
	Templates   []Template    `json:"templates"`
	Unavailable []Unavailable `json:"unavailable"`
}

var (
	keyRE   = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)
	portsRE = regexp.MustCompile(`^\d{1,5}(-\d{1,5})?(,\d{1,5}(-\d{1,5})?)*$`)
)

// Load parses and validates the embedded catalog.
func Load() (*Catalog, error) { return parse(embedded) }

func parse(doc []byte) (*Catalog, error) {
	var c Catalog
	dec := json.NewDecoder(bytes.NewReader(doc))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&c); err != nil {
		return nil, fmt.Errorf("destcatalog: %w", err)
	}
	if c.Version < 1 {
		return nil, errors.New("destcatalog: catalog_version is missing")
	}
	seen := map[string]bool{}
	text := func(what string, l Localized, max int) error {
		for _, loc := range []string{LocaleZH, LocaleEN} {
			if v := l[loc]; v == "" || len(v) > max {
				return fmt.Errorf("destcatalog: %s[%s] is empty or over %d bytes", what, loc, max)
			}
		}
		return nil
	}
	for _, t := range c.Templates {
		switch {
		case !keyRE.MatchString(t.Key) || len(t.Key) > domain.DestTemplateKeyMax:
			return nil, fmt.Errorf("destcatalog: bad template key %q", t.Key)
		case seen[t.Key]:
			return nil, fmt.Errorf("destcatalog: template key %q is listed twice", t.Key)
		case len(t.Geosite) == 0 && len(t.Protocols) == 0 && t.Ports == "" && !t.Private:
			return nil, fmt.Errorf("destcatalog: template %q matches nothing", t.Key)
		case t.Ports != "" && !portsRE.MatchString(t.Ports):
			return nil, fmt.Errorf("destcatalog: template %q: bad ports %q", t.Key, t.Ports)
		case t.Network != "" && t.Network != "tcp" && t.Network != "udp":
			return nil, fmt.Errorf("destcatalog: template %q: bad network %q", t.Key, t.Network)
		}
		seen[t.Key] = true
		for _, p := range t.Protocols {
			if p != "bittorrent" {
				return nil, fmt.Errorf("destcatalog: template %q: unknown protocol %q", t.Key, p)
			}
		}
		if err := text(t.Key+" name", t.Name, domain.DestNameMax); err != nil {
			return nil, err
		}
		if err := text(t.Key+" description", t.Description, domain.DestDescriptionMax); err != nil {
			return nil, err
		}
	}
	for _, u := range c.Unavailable {
		if !keyRE.MatchString(u.Key) || seen[u.Key] {
			return nil, fmt.Errorf("destcatalog: bad or duplicate unavailable key %q", u.Key)
		}
		seen[u.Key] = true
		if err := text(u.Key+" name", u.Name, domain.DestNameMax); err != nil {
			return nil, err
		}
		if err := text(u.Key+" reason", u.Reason, 1024); err != nil {
			return nil, err
		}
	}
	return &c, nil
}

// Template returns the template with key.
func (c *Catalog) Template(key string) (Template, bool) {
	for _, t := range c.Templates {
		if t.Key == key {
			return t, true
		}
	}
	return Template{}, false
}

func (c *Catalog) unavailable(key string) (Unavailable, bool) {
	for _, u := range c.Unavailable {
		if u.Key == key {
			return u, true
		}
	}
	return Unavailable{}, false
}

// Errors an instantiation can end in.
var (
	// ErrUnknownTemplate is a key that is neither a template nor listed as
	// unavailable (HTTP 404).
	ErrUnknownTemplate = fmt.Errorf("%w: unknown catalog template", domain.ErrNotFound)
	// ErrTemplateUnavailable is a listed group that cannot be added (HTTP 400).
	ErrTemplateUnavailable = fmt.Errorf("%w: this catalog group is not available", domain.ErrValidation)
)

// GeositeLists is what instantiation needs of the list service.
type GeositeLists interface {
	EnsureGeositeData(ctx context.Context) error
	// LockGeosite excludes a geosite update/rollback until the returned
	// function is called.
	LockGeosite(ctx context.Context) (unlock func(), err error)
	GeositeListSpec(ctx context.Context, category string, attrs []string) (domain.DestList, error)
}

// Instantiator creates a template's lists and policy in one transaction,
// reusing identical lists (sqlstore.DestRepos).
type Instantiator interface {
	InstantiateTemplate(ctx context.Context, lists []domain.DestList, p *domain.DestPolicy) (*domain.DestPolicy, error)
}

// PolicyLister reads the policies, to mark the templates already added.
type PolicyLister interface {
	List(ctx context.Context) ([]*domain.DestPolicy, error)
}

// Service instantiates catalog templates.
type Service struct {
	catalog  *Catalog
	lists    GeositeLists
	store    Instantiator
	policies PolicyLister
}

// New builds the service over the embedded catalog.
func New(lists GeositeLists, store Instantiator, policies PolicyLister) (*Service, error) {
	c, err := Load()
	if err != nil {
		return nil, err
	}
	return &Service{catalog: c, lists: lists, store: store, policies: policies}, nil
}

// TemplateView is a template as the page shows it.
type TemplateView struct {
	Template
	// Added says a policy was created from it (by template_key, so editing
	// the policy's name does not lose the link).
	Added       bool             `json:"added"`
	PolicyID    int64            `json:"policy_id,omitempty"`
	PolicyState domain.DestState `json:"policy_state,omitempty"`
}

// View is the whole catalog as the page shows it.
type View struct {
	Version     int            `json:"catalog_version"`
	Templates   []TemplateView `json:"templates"`
	Unavailable []Unavailable  `json:"unavailable"`
}

// View lists the templates, marking those already added. The result shares no
// memory with the catalog.
func (s *Service) View(ctx context.Context) (*View, error) {
	policies, err := s.policies.List(ctx)
	if err != nil {
		return nil, err
	}
	byKey := map[string]*domain.DestPolicy{}
	for _, p := range policies {
		if p.TemplateKey != "" {
			byKey[p.TemplateKey] = p
		}
	}
	v := &View{Version: s.catalog.Version, Templates: make([]TemplateView, 0, len(s.catalog.Templates)), Unavailable: make([]Unavailable, 0, len(s.catalog.Unavailable))}
	for _, t := range s.catalog.Templates {
		tv := TemplateView{Template: cloneTemplate(t)}
		if p := byKey[t.Key]; p != nil {
			tv.Added, tv.PolicyID, tv.PolicyState = true, p.ID, p.State
		}
		v.Templates = append(v.Templates, tv)
	}
	for _, u := range s.catalog.Unavailable {
		v.Unavailable = append(v.Unavailable, Unavailable{Key: u.Key, Name: u.Name.clone(), Reason: u.Reason.clone()})
	}
	return v, nil
}

func cloneTemplate(t Template) Template {
	t.Name, t.Description = t.Name.clone(), t.Description.clone()
	t.Geosite, t.Protocols = slices.Clone(t.Geosite), slices.Clone(t.Protocols)
	return t
}

// Instantiate adds a template: it prepares the geosite lists the template
// covers (downloading the geosite data first if there is none), then creates
// the lists — reusing identical ones — and the policy in one transaction. The
// admin chooses the state (off, report or on); there is no default. A group listed as unavailable
// creates nothing.
func (s *Service) Instantiate(ctx context.Context, key, locale string, state domain.DestState) (*domain.DestPolicy, error) {
	if _, ok := s.catalog.unavailable(key); ok {
		return nil, fmt.Errorf("%w: %s", ErrTemplateUnavailable, key)
	}
	t, ok := s.catalog.Template(key)
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrUnknownTemplate, key)
	}
	if !state.Valid() {
		return nil, fmt.Errorf("%w: the state must be off, report or on", domain.ErrValidation)
	}
	var specs []domain.DestList
	if len(t.Geosite) > 0 {
		// Download first, outside the lock (the update takes it itself), then
		// derive and store under it: an update between the two would swap the
		// data and re-derive every list, and the lists created here would be
		// left on the old release.
		if err := s.lists.EnsureGeositeData(ctx); err != nil {
			return nil, err
		}
		unlock, err := s.lists.LockGeosite(ctx)
		if err != nil {
			return nil, err
		}
		defer unlock()
		for _, cat := range t.Geosite {
			spec, err := s.lists.GeositeListSpec(ctx, cat, nil)
			if err != nil {
				return nil, fmt.Errorf("catalog group %s: %w", key, err)
			}
			specs = append(specs, spec)
		}
	}
	p := &domain.DestPolicy{
		Name: t.Name.pick(locale), Description: t.Description.pick(locale), Action: domain.DestActionBlock,
		State: state, Scope: domain.DestScopeAll, TemplateKey: t.Key,
		Inline: domain.DestPolicyInline{Ports: t.Ports, Network: t.Network, Protocols: slices.Clone(t.Protocols), Private: t.Private},
	}
	return s.store.InstantiateTemplate(ctx, specs, p)
}
