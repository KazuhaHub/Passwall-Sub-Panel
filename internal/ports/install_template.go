package ports

import (
	"context"
	"errors"
)

// NodeInstallTemplate renders the installation script for a registered node
// identity and a published Passwall Node release.
//
// IT IS A PORT RATHER THAN A LIBRARY CALL. The template belongs to the Passwall
// Node project, which publishes it as a signed release asset; the panel fetches,
// verifies and renders it. A caller that reached for a template directly would be
// compiling another project's install logic into this one, which is the dependency
// this boundary exists to keep out — and it is why this interface says nothing
// about where the bytes came from.
//
// Failure is refuse-rather-than-degrade: a caller that cannot get a template is
// told, never handed a partial script. The rendered string carries a credential.
type NodeInstallTemplate interface {
	// Validate reports whether the request could be rendered, without fetching
	// anything — for callers that need the answer and not the script.
	Validate(InstallTemplateRequest) error
	Render(context.Context, InstallTemplateRequest) (string, error)
}

// InstallTemplateRequest names an already registered identity and an already
// published release. Credential is secret: never log a request or its result.
type InstallTemplateRequest struct {
	Endpoint   string
	AgentID    string
	Credential string
	Version    string
}

// THE FAILURES ARE TWO KINDS, and a caller has to tell them apart: one is the
// request it sent, the other is the publication it could not obtain or trust. The
// first is the caller's to fix; the second is not, and answering "invalid request"
// to a signature failure would send an operator to check their own input while the
// publication is what is wrong.
var (
	// ErrInstallTemplateRequest means the request cannot be rendered at all — an
	// unknown version, an endpoint that is not the sync path, a credential outside
	// the shared envelope. Decided before anything is fetched.
	ErrInstallTemplateRequest = errors.New("install template: the request cannot be rendered")
	// ErrInstallTemplateSource means the published template could not be obtained,
	// or was obtained and could not be trusted.
	ErrInstallTemplateSource = errors.New("install template: the published template could not be obtained")
)
