package xui

import (
	"context"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/metrics"
)

// Operation tagging for the RTT histograms.
//
// The label is carried on the context rather than passed down as an
// argument because doJSON already sits behind three wrappers and every
// adapter method would otherwise grow a parameter it does nothing with.
// It is read at the one place that performs the HTTP exchange, so each
// round trip is counted exactly once under exactly one label — including
// the nested case (UpdateInbound's internal GetInbound re-tags for the
// duration of the inner call, then the outer request records under the
// outer op).

type opKey struct{}

// withOp tags ctx so every HTTP exchange it drives records under op.
// Call at the top of a public adapter method.
func withOp(ctx context.Context, op string) context.Context {
	return context.WithValue(ctx, opKey{}, op)
}

// opOf returns the operation tag, or "other" for a request issued from an
// untagged path. "other" is deliberately a real bucket rather than a
// dropped sample: a growing "other" count is the signal that a hot path
// was added without a tag.
func opOf(ctx context.Context) string {
	if op, ok := ctx.Value(opKey{}).(string); ok && op != "" {
		return op
	}
	return "other"
}

// recordOp records one HTTP exchange against its operation label. The
// duration spans the full doJSONRetry, so a transparent re-login (or the
// single 401 retry) is included — that is latency the caller genuinely
// waits on, and folding it in keeps the histogram honest about what a
// call costs rather than about what a happy path costs.
func recordOp(ctx context.Context, start time.Time, err error) {
	op := opOf(ctx)
	metrics.PanelOpTotal.With(op).Inc()
	metrics.PanelRTT.With(op).ObserveSince(start)
	if err != nil {
		metrics.PanelOpErrorTotal.With(op).Inc()
	}
}
