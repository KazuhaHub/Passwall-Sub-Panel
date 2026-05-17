package domain

import "errors"

// Sentinel domain errors. Specific business errors should wrap one of these
// via fmt.Errorf("%w: ...", ErrXxx); callers use errors.Is to classify.
var (
	ErrNotFound      = errors.New("not found")
	ErrAlreadyExists = errors.New("already exists")
	ErrValidation    = errors.New("validation failed")
	ErrUnauthorized  = errors.New("unauthorized")
	ErrForbidden     = errors.New("forbidden")
	ErrConflict      = errors.New("conflict")

	// ErrSSONoAccount is returned by EnsureSSO when a non-admin SSO principal
	// has no pre-provisioned account. The caller should redirect the user to a
	// "contact your administrator" page rather than auto-creating an account.
	ErrSSONoAccount = errors.New("sso: no matching account")

	// ErrSSOAccountConflict is returned by EnsureSSO when a UPN matches an
	// existing panel row that is already bound to a DIFFERENT SSO
	// (provider, subject) than the one currently logging in. The strict
	// policy refuses silent rebinding — admin must clear the old SSO
	// linkage before the new identity can take over. See EnsureSSO for
	// the full layered lookup.
	ErrSSOAccountConflict = errors.New("sso: account already bound to a different SSO identity")

	// Write-guard errors enforced by SyncSvc (see §4 management boundary).
	ErrClientNotOwnedByPanel      = errors.New("client not owned by panel")
	ErrInboundHasUnmanagedClients = errors.New("inbound has unmanaged clients")
)
