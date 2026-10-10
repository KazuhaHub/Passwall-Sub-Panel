package app

import (
	"context"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/service/destpolicy"
)

// Use the same current default templates as subscription rendering, including
// operator changes. Seed examples are never the runtime source of suggestions.
func (a *App) destinationGroupBaseHosts(ctx context.Context) ([]string, error) {
	if a == nil || a.repos.Template == nil {
		return nil, domain.ErrUnavailable
	}
	ctx, release, err := a.operationGate.Read(ctx)
	if err != nil {
		return nil, err
	}
	defer release()
	var templates []domain.Template
	for _, client := range []domain.ClientType{domain.ClientMihomo, domain.ClientSingBox} {
		template, err := a.repos.Template.GetDefault(ctx, client)
		if err != nil {
			return nil, err
		}
		if template == nil {
			return nil, domain.ErrUnavailable
		}
		templates = append(templates, *template)
	}
	return destpolicy.ProxiedDNSHosts(templates...)
}
