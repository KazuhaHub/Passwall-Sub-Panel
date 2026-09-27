package domain

import (
	"testing"
	"time"
)

// A fresh install has never saved these knobs, and every one of them reads
// as 0. Read literally, 0 would be a freshness window that keeps nothing
// live, a shared-exit threshold that switches the rule off, a per-poll cap
// that applies nothing, and a zero-length ticker (which panics). Unset means
// the shipped default, the value each knob had while it was a constant.
func TestGeoRuntimeFromSettings_UnsetIsTheDefault(t *testing.T) {
	want := GeoRuntime{
		FreshWindowSeconds: 120,
		SharedExitMinUsers: 3,
		BanMaxPerPoll:      20,
		LiftMaxPerPoll:     20,
		InfraRefresh:       5 * time.Minute,
		InfraHostTTL:       10 * time.Minute,
	}
	if got := GeoRuntimeFromSettings(GeoRuntimeSettings{}); got != want {
		t.Fatalf("GeoRuntimeFromSettings(unset) = %+v\nwant %+v", got, want)
	}
}

// Each knob is clamped to its safety bound on read, and a negative value is
// unset like 0. The bounds are not policy: a freshness window under two 10 s
// scans marks a live stream stale between scans; one over half the upstream's
// 30-minute memory reads memory as "now"; a shared-exit threshold of 1 would
// exclude every source there is; the per-poll caps bound one poll's inline
// panel pushes; the infrastructure cadences bound how stale a relay's address
// may get and how hard the resolver is asked.
func TestGeoRuntimeFromSettings_ClampsEachKnob(t *testing.T) {
	for _, c := range []struct {
		name string
		in   GeoRuntimeSettings
		got  func(GeoRuntime) any
		want any
	}{
		{"fresh window below two scans", GeoRuntimeSettings{FreshWindowSeconds: 5}, func(r GeoRuntime) any { return r.FreshWindowSeconds }, 20},
		{"fresh window beyond half the memory", GeoRuntimeSettings{FreshWindowSeconds: 5000}, func(r GeoRuntime) any { return r.FreshWindowSeconds }, 900},
		{"fresh window in range", GeoRuntimeSettings{FreshWindowSeconds: 300}, func(r GeoRuntime) any { return r.FreshWindowSeconds }, 300},
		{"shared exit of one account", GeoRuntimeSettings{SharedExitMinUsers: 1}, func(r GeoRuntime) any { return r.SharedExitMinUsers }, 2},
		{"shared exit over the ceiling", GeoRuntimeSettings{SharedExitMinUsers: 99}, func(r GeoRuntime) any { return r.SharedExitMinUsers }, 20},
		{"ban cap over the ceiling", GeoRuntimeSettings{BanMaxPerPoll: 1000}, func(r GeoRuntime) any { return r.BanMaxPerPoll }, 200},
		{"lift cap over the ceiling", GeoRuntimeSettings{LiftMaxPerPoll: 1000}, func(r GeoRuntime) any { return r.LiftMaxPerPoll }, 200},
		{"lift cap of one", GeoRuntimeSettings{LiftMaxPerPoll: 1}, func(r GeoRuntime) any { return r.LiftMaxPerPoll }, 1},
		{"infra refresh over an hour", GeoRuntimeSettings{InfraRefreshMinutes: 999}, func(r GeoRuntime) any { return r.InfraRefresh }, 60 * time.Minute},
		{"infra host TTL over a day", GeoRuntimeSettings{InfraHostTTLMinutes: 99999}, func(r GeoRuntime) any { return r.InfraHostTTL }, 1440 * time.Minute},
		{"infra host TTL of one minute", GeoRuntimeSettings{InfraHostTTLMinutes: 1}, func(r GeoRuntime) any { return r.InfraHostTTL }, time.Minute},
		{"negative fresh window is unset", GeoRuntimeSettings{FreshWindowSeconds: -1}, func(r GeoRuntime) any { return r.FreshWindowSeconds }, 120},
		{"negative shared exit is unset", GeoRuntimeSettings{SharedExitMinUsers: -1}, func(r GeoRuntime) any { return r.SharedExitMinUsers }, 3},
		{"negative ban cap is unset", GeoRuntimeSettings{BanMaxPerPoll: -1}, func(r GeoRuntime) any { return r.BanMaxPerPoll }, 20},
		{"negative infra refresh is unset", GeoRuntimeSettings{InfraRefreshMinutes: -1}, func(r GeoRuntime) any { return r.InfraRefresh }, 5 * time.Minute},
		{"negative infra host TTL is unset", GeoRuntimeSettings{InfraHostTTLMinutes: -1}, func(r GeoRuntime) any { return r.InfraHostTTL }, 10 * time.Minute},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got := c.got(GeoRuntimeFromSettings(c.in)); got != c.want {
				t.Fatalf("got %v, want %v", got, c.want)
			}
		})
	}
}

// (guard) Upgrading must change nothing: the shipped defaults are exactly the
// constants these knobs replaced — a 120 s freshness window, a shared-exit
// threshold of 3 accounts, per-poll caps of 20 suspensions and 20 lifts, a
// 5-minute infrastructure refresh and a 10-minute hostname TTL. Frozen here
// as literals, so moving a default is a deliberate edit of this test and not
// a side effect of editing a constant.
//
// Mutation: a default fresh window of 121 turns this red.
func TestDefaultGeoRuntimeEqualsTheFormerConstants(t *testing.T) {
	want := GeoRuntime{
		FreshWindowSeconds: 120,
		SharedExitMinUsers: 3,
		BanMaxPerPoll:      20,
		LiftMaxPerPoll:     20,
		InfraRefresh:       5 * time.Minute,
		InfraHostTTL:       10 * time.Minute,
	}
	if got := DefaultGeoRuntime(); got != want {
		t.Fatalf("DefaultGeoRuntime() = %+v\nwant the former constants %+v", got, want)
	}
	if LiveIPFreshWindowSeconds != want.FreshWindowSeconds || SharedExitMinUsers != want.SharedExitMinUsers {
		t.Fatalf("the named defaults moved: fresh %d, shared %d", LiveIPFreshWindowSeconds, SharedExitMinUsers)
	}
}
