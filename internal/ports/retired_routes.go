package ports

import "context"

// RetiredRouteCleanup names the rules removable from a specific 3X-UI snapshot.
type RetiredRouteCleanup struct {
	RuleTags []string `json:"rule_tags"`
	Revision string   `json:"revision"`
}

// RetiredRouteCleaner is optional. An empty revision previews rules without
// changing anything. A supplied revision must match the current template.
type RetiredRouteCleaner interface {
	RetiredRouteCleanup(ctx context.Context, revision string) (RetiredRouteCleanup, error)
}
