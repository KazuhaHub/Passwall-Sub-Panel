//go:build destination_acceptance && linux

package app

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"
)

type nativeTargetRoute struct {
	Type string `json:"type"`
	Dev  string `json:"dev"`
	Dst  string `json:"dst"`
}

func exactNativeTargetRoute(routes []nativeTargetRoute, ip string) bool {
	return len(routes) == 1 && routes[0].Type == "local" && routes[0].Dev == "lo" && net.ParseIP(routes[0].Dst).Equal(net.ParseIP(ip))
}

// IPv6 address installation schedules addrconf_dad_work even with nodad.
// Its completion installs the local route; command success alone is not a
// readiness observation. Poll only the original exact guard, within ctx's
// deadline. Never install/replace routes or accept a nonlocal intermediate.
func awaitNativeTargetRoute(ctx context.Context, ip string, read func(context.Context) ([]nativeTargetRoute, error)) ([]nativeTargetRoute, error) {
	var routes []nativeTargetRoute
	for {
		if err := ctx.Err(); err != nil {
			return routes, err
		}
		current, err := read(ctx)
		if err != nil {
			return routes, err
		}
		routes = current
		if err := ctx.Err(); err != nil {
			return routes, err
		}
		if exactNativeTargetRoute(routes, ip) {
			return routes, nil
		}
		timer := time.NewTimer(25 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return routes, ctx.Err()
		case <-timer.C:
		}
	}
}

func TestNativeTargetRouteWaitRequiresExactLocalReadiness(t *testing.T) {
	const ip = "2001:db8::1234"
	valid := []nativeTargetRoute{{Type: "local", Dev: "lo", Dst: ip}}
	for _, invalid := range [][]nativeTargetRoute{
		nil,
		{{Dev: "lo", Dst: ip}},
		{{Type: "unicast", Dev: "lo", Dst: ip}},
		{{Type: "local", Dev: "eth0", Dst: ip}},
		{{Type: "local", Dev: "lo", Dst: "2001:db8::1235"}},
		{{Type: "local", Dev: "lo", Dst: ip + "/128"}},
		{valid[0], valid[0]},
	} {
		calls := 0
		ctx, cancel := context.WithTimeout(t.Context(), time.Second)
		routes, err := awaitNativeTargetRoute(ctx, ip, func(context.Context) ([]nativeTargetRoute, error) {
			calls++
			if calls == 1 {
				return invalid, nil
			}
			return valid, nil
		})
		cancel()
		if err != nil || calls != 2 || !exactNativeTargetRoute(routes, ip) {
			t.Fatal("route became ready without passing every original guard comparison")
		}
	}
}

func TestNativeTargetRouteWaitStopsOnDeadlineCancellationAndReadFailure(t *testing.T) {
	const ip = "2001:db8::1234"
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Millisecond)
	defer cancel()
	routes, err := awaitNativeTargetRoute(ctx, ip, func(context.Context) ([]nativeTargetRoute, error) {
		return []nativeTargetRoute{{Dev: "lo", Dst: ip}}, nil
	})
	if !errors.Is(err, context.DeadlineExceeded) || exactNativeTargetRoute(routes, ip) {
		t.Fatal("persistent nonlocal route was accepted or wait was unbounded")
	}
	canceled, stop := context.WithCancel(t.Context())
	stop()
	if _, err := awaitNativeTargetRoute(canceled, ip, func(context.Context) ([]nativeTargetRoute, error) {
		t.Fatal("cancelled route wait performed a lookup")
		return nil, nil
	}); !errors.Is(err, context.Canceled) {
		t.Fatal("route wait hid cancellation")
	}
	failure := errors.New("private route read failure")
	if _, err := awaitNativeTargetRoute(t.Context(), ip, func(context.Context) ([]nativeTargetRoute, error) { return nil, failure }); !errors.Is(err, failure) {
		t.Fatal("route wait retried or hid an actual lookup failure")
	}
	duringRead, stopRead := context.WithCancel(t.Context())
	if _, err := awaitNativeTargetRoute(duringRead, ip, func(context.Context) ([]nativeTargetRoute, error) {
		stopRead()
		return []nativeTargetRoute{{Type: "local", Dev: "lo", Dst: ip}}, nil
	}); !errors.Is(err, context.Canceled) {
		t.Fatal("route observed after cancellation was accepted")
	}
}
