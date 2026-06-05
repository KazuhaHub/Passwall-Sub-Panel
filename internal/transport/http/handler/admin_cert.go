package handler

import (
	"net/http"
	"sort"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/KazuhaHub/passwall-sub-panel/internal/adapters/acme"
	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/cert"
)

// AdminCertHandler exposes PSP-managed certificate + DNS-credential CRUD under
// /api/admin/certs and /api/admin/dns-credentials (adminGroup — these touch
// ACME private keys / DNS provider secrets). Responses NEVER carry the cert
// private key or the DNS credential values.
type AdminCertHandler struct {
	cert *cert.Service
}

func NewAdminCertHandler(c *cert.Service) *AdminCertHandler { return &AdminCertHandler{cert: c} }

type certDTO struct {
	ID              int64      `json:"id"`
	Name            string     `json:"name"`
	Domains         []string   `json:"domains"`
	Status          string     `json:"status"`
	DNSCredentialID int64      `json:"dns_credential_id"`
	NotBefore       *time.Time `json:"not_before"`
	NotAfter        *time.Time `json:"not_after"`
	Fingerprint     string     `json:"fingerprint"`
	AutoRenew       bool       `json:"auto_renew"`
	LastError       string     `json:"last_error"`
	CreatedAt       time.Time  `json:"created_at"`
}

func toCertDTO(c *domain.TLSCertificate) certDTO {
	status := string(c.Status)
	// Derived display state: an active cert past its NotAfter shows as expired
	// (never stored — purely a presentation status).
	if c.Status == domain.CertStatusActive && c.NotAfter != nil && c.NotAfter.Before(time.Now()) {
		status = string(domain.CertStatusExpired)
	}
	return certDTO{
		ID: c.ID, Name: c.Name, Domains: c.Domains, Status: status,
		DNSCredentialID: c.DNSCredentialID, NotBefore: c.NotBefore, NotAfter: c.NotAfter,
		Fingerprint: c.Fingerprint, AutoRenew: c.AutoRenew, LastError: c.LastError, CreatedAt: c.CreatedAt,
	}
}

// certTaskDTO is the in-flight issue/renew sync-task surfaced on a pending cert's
// detail view as its "progress" (the closest thing to progress, since lego's
// Obtain is a single blocking call).
type certTaskDTO struct {
	Status    string     `json:"status"` // pending / running
	Attempts  int        `json:"attempts"`
	NextRunAt *time.Time `json:"next_run_at"`
	LastError string     `json:"last_error"`
}

func (h *AdminCertHandler) List(c *gin.Context) {
	certs, err := h.cert.ListCerts(c.Request.Context())
	if err != nil {
		mapServerError(c, err)
		return
	}
	out := make([]certDTO, 0, len(certs))
	for _, x := range certs {
		out = append(out, toCertDTO(x))
	}
	c.JSON(http.StatusOK, gin.H{"certs": out})
}

func (h *AdminCertHandler) Get(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}
	x, err := h.cert.GetCert(c.Request.Context(), id)
	if err != nil {
		mapServerError(c, err)
		return
	}
	resp := gin.H{"cert": toCertDTO(x)}
	// Surface the in-flight issue/renew task so the UI can show "in progress"
	// detail (state / attempts / next retry / last error) for a pending cert.
	if task, terr := h.cert.ActiveTask(c.Request.Context(), id); terr == nil && task != nil {
		nr := task.NextRunAt
		resp["task"] = certTaskDTO{Status: string(task.Status), Attempts: task.Attempts, NextRunAt: &nr, LastError: task.LastError}
	}
	c.JSON(http.StatusOK, resp)
}

type createCertRequest struct {
	Name            string   `json:"name"`
	Domains         []string `json:"domains"`
	DNSCredentialID int64    `json:"dns_credential_id"`
	AutoRenew       bool     `json:"auto_renew"`
}

func (h *AdminCertHandler) Create(c *gin.Context) {
	var req createCertRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	cert := &domain.TLSCertificate{
		Name: req.Name, Domains: req.Domains,
		DNSCredentialID: req.DNSCredentialID, AutoRenew: req.AutoRenew,
	}
	if err := h.cert.CreateCert(c.Request.Context(), cert); err != nil {
		mapServerError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"cert": toCertDTO(cert)})
}

func (h *AdminCertHandler) Delete(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}
	if err := h.cert.DeleteCert(c.Request.Context(), id); err != nil {
		mapServerError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

func (h *AdminCertHandler) Renew(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}
	if err := h.cert.ManualRenew(c.Request.Context(), id); err != nil {
		mapServerError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

// Download returns the full certificate chain + private key PEM so an admin can
// deploy the managed cert elsewhere. AdminGroup-only and an EXPLICIT action — the
// list and detail DTOs never carry PEM material; this dedicated endpoint is the
// single place a private key leaves PSP.
func (h *AdminCertHandler) Download(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}
	x, err := h.cert.GetCert(c.Request.Context(), id)
	if err != nil {
		mapServerError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"name": x.Name, "cert_pem": x.CertPEM, "key_pem": x.KeyPEM})
}

type certEventDTO struct {
	ID        int64     `json:"id"`
	CertID    int64     `json:"cert_id"`
	CertName  string    `json:"cert_name"`
	Kind      string    `json:"kind"`
	Success   bool      `json:"success"`
	Message   string    `json:"message"`
	CreatedAt time.Time `json:"created_at"`
}

// ListEvents returns the paginated cert issuance/renewal activity log (newest
// first) for the Logs page's Certificates tab.
func (h *AdminCertHandler) ListEvents(c *gin.Context) {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "50"))
	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 200 {
		pageSize = 50
	}
	events, total, err := h.cert.ListEvents(c.Request.Context(), pageSize, (page-1)*pageSize)
	if err != nil {
		mapServerError(c, err)
		return
	}
	out := make([]certEventDTO, 0, len(events))
	for _, e := range events {
		out = append(out, certEventDTO{
			ID: e.ID, CertID: e.CertID, CertName: e.CertName, Kind: string(e.Kind),
			Success: e.Success, Message: e.Message, CreatedAt: e.CreatedAt,
		})
	}
	c.JSON(http.StatusOK, gin.H{"events": out, "total": total})
}

// ---- DNS credentials ----

// dnsCredDTO returns only the credential KEY NAMES (e.g. "CF_DNS_API_TOKEN"),
// never the values — those are write-only secrets.
type dnsCredDTO struct {
	ID       int64    `json:"id"`
	Name     string   `json:"name"`
	Provider string   `json:"provider"`
	Keys     []string `json:"keys"`
}

func toDNSCredDTO(c *domain.DNSCredential) dnsCredDTO {
	keys := make([]string, 0, len(c.Credentials))
	for k := range c.Credentials {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return dnsCredDTO{ID: c.ID, Name: c.Name, Provider: c.Provider, Keys: keys}
}

type dnsCredRequest struct {
	Name        string            `json:"name"`
	Provider    string            `json:"provider"`
	Credentials map[string]string `json:"credentials"`
}

func (h *AdminCertHandler) ListDNSCreds(c *gin.Context) {
	creds, err := h.cert.ListDNSCreds(c.Request.Context())
	if err != nil {
		mapServerError(c, err)
		return
	}
	out := make([]dnsCredDTO, 0, len(creds))
	for _, x := range creds {
		out = append(out, toDNSCredDTO(x))
	}
	c.JSON(http.StatusOK, gin.H{"credentials": out})
}

func (h *AdminCertHandler) CreateDNSCred(c *gin.Context) {
	var req dnsCredRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	cred := &domain.DNSCredential{Name: req.Name, Provider: req.Provider, Credentials: req.Credentials}
	if err := h.cert.CreateDNSCred(c.Request.Context(), cred); err != nil {
		mapServerError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"credential": toDNSCredDTO(cred)})
}

func (h *AdminCertHandler) UpdateDNSCred(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}
	var req dnsCredRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	cred := &domain.DNSCredential{ID: id, Name: req.Name, Provider: req.Provider, Credentials: req.Credentials}
	if err := h.cert.UpdateDNSCred(c.Request.Context(), cred); err != nil {
		mapServerError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"credential": toDNSCredDTO(cred)})
}

func (h *AdminCertHandler) DeleteDNSCred(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}
	if err := h.cert.DeleteDNSCred(c.Request.Context(), id); err != nil {
		mapServerError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

// ListProviders returns the curated DNS provider catalog — each provider's code,
// label, and credential field schema — so the create-credential dialog can render
// labeled inputs for a built-in vendor and fall back to a free-form KEY/VALUE
// editor only for the generic exec/httpreq providers (Custom=true).
func (h *AdminCertHandler) ListProviders(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"providers": acme.SupportedProviderInfos()})
}

type setNodeCertSourceRequest struct {
	Source string `json:"source"` // ""/manual/from_panel/psp_managed
	CertID int64  `json:"cert_id"`
}

// SetNodeCertSource records a node's certificate source and, for psp_managed,
// deploys the bound cert. Lives on the cert handler because the binding drives a
// deploy through the cert service. Routed under /admin/nodes/:id/cert-source.
func (h *AdminCertHandler) SetNodeCertSource(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}
	var req setNodeCertSourceRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := h.cert.SetNodeCertSource(c.Request.Context(), id, domain.CertSource(req.Source), req.CertID); err != nil {
		mapServerError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}
