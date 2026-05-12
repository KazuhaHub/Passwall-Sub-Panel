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

	// Write-guard errors enforced by SyncSvc (see §4 management boundary).
	ErrClientNotOwnedByPanel      = errors.New("client not owned by panel")
	ErrInboundHasUnmanagedClients = errors.New("inbound has unmanaged clients")
)
