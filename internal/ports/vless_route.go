package ports

import (
	"context"
	"encoding/json"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

// RouteOutbound is the only outbound metadata exposed to administrators.
// Complete settings/credentials stay inside the adapter/service boundary.
type RouteOutbound struct {
	Tag      string `json:"tag"`
	Protocol string `json:"protocol"`
}

type RouteTemplate struct {
	Config    json.RawMessage
	TestURL   string
	Outbounds []RouteOutbound
}

// VlessRouteAdapter is optional: other panel kinds do not implement it.
// The upstream API is whole-template read/modify/write, not a rule patch API.
type VlessRouteAdapter interface {
	ReadRouteTemplate(context.Context) (RouteTemplate, error)
	WriteRouteTemplate(context.Context, RouteTemplate) error
}

// NodeRouteStore isolates route-state CAS and a cross-process backend lease
// from ordinary node metadata writes. The lease outlives the bounded operation.
type NodeRouteStore interface {
	SaveRouteState(context.Context, int64, int64, *domain.NodeRouteState) error
	AcquireRouteLease(context.Context, string, string, time.Time) (bool, error)
	ReleaseRouteLease(context.Context, string, string) error
}
