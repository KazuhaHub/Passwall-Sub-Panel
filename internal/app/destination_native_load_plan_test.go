package app

import (
	"encoding/json"
	"errors"
	"math"
	"strconv"
	"strings"
	"testing"
)

func TestNativeLoadMetricSchemaSeparatesOverflowBudget(t *testing.T) {
	phase := nativeLoadPhase{}
	decode := func() map[string]json.RawMessage {
		t.Helper()
		data, err := json.Marshal(phase)
		if err != nil {
			t.Fatal("cannot encode load phase")
		}
		var fields map[string]json.RawMessage
		if json.Unmarshal(data, &fields) != nil {
			t.Fatal("cannot decode load phase")
		}
		return fields
	}
	ordinary := decode()
	original := []string{"name", "rules", "duration_ms", "attempted", "delivered", "blocked", "transport_failures", "target_requests", "control_successes", "heartbeat_advances", "window_delivered", "late_delivered", "hit_rules", "hit_events", "losses", "samples"}
	if len(ordinary) != len(original) {
		t.Fatal("ordinary workload changed its closed evidence schema")
	}
	for _, field := range original {
		if _, ok := ordinary[field]; !ok {
			t.Fatal("ordinary load evidence field missing")
		}
	}
	phase.BudgetMaxRows = 20000
	overflow := decode()
	if len(overflow) != len(original)+1 || string(overflow["budget_max_rows"]) != "20000" {
		t.Fatal("overflow evidence omitted its actual budget")
	}
}

type nativeLoadWorkload uint8

const (
	nativeWebhookWorkload nativeLoadWorkload = iota
	nativeTrialWorkload
	nativeBlockOverflowWorkload
)

type nativeLoadSample struct {
	ElapsedMS      int64  `json:"elapsed_ms"`
	CoreGoroutines uint64 `json:"core_goroutines"`
	CoreFDs        uint64 `json:"core_fds"`
	AgentCPUTicks  uint64 `json:"agent_cpu_ticks"`
	CoreCPUTicks   uint64 `json:"core_cpu_ticks"`
}

type nativeLoadLoss struct {
	Reason    string `json:"reason"`
	Rows      int64  `json:"rows"`
	Events    int64  `json:"events"`
	Unmatched int64  `json:"unmatched"`
}

type nativeLoadPhase struct {
	Name              string             `json:"name"`
	Rules             int                `json:"rules"`
	DurationMS        int64              `json:"duration_ms"`
	Attempted         uint64             `json:"attempted"`
	Delivered         uint64             `json:"delivered"`
	Blocked           uint64             `json:"blocked"`
	TransportFailures uint64             `json:"transport_failures"`
	TargetRequests    int64              `json:"target_requests"`
	ControlSuccesses  uint64             `json:"control_successes"`
	HeartbeatAdvances uint64             `json:"heartbeat_advances"`
	WindowDelivered   []uint64           `json:"window_delivered"`
	LateDelivered     uint64             `json:"late_delivered"`
	HitRules          int                `json:"hit_rules"`
	HitEvents         int64              `json:"hit_events"`
	BudgetMaxRows     int64              `json:"budget_max_rows,omitempty"`
	Losses            []nativeLoadLoss   `json:"losses"`
	Samples           []nativeLoadSample `json:"samples"`
}

// Read the two CPU counters and process birth coordinate from Linux stat. The
// command name may contain spaces and parentheses; field offsets follow it.
func nativeLoadProcCounters(input string) (cpu, birth uint64, err error) {
	end := strings.LastIndexByte(input, ')')
	if end < 0 || !strings.Contains(input[:end], "(") {
		return 0, 0, errors.New("missing owned process coordinates")
	}
	fields := strings.Fields(input[end+1:])
	if len(fields) < 20 {
		return 0, 0, errors.New("incomplete process counters")
	}
	user, e1 := strconv.ParseUint(fields[11], 10, 64)
	system, e2 := strconv.ParseUint(fields[12], 10, 64)
	birth, e3 := strconv.ParseUint(fields[19], 10, 64)
	if e1 != nil || e2 != nil || e3 != nil || birth == 0 || user > math.MaxUint64-system {
		return 0, 0, errors.New("invalid owned process counters")
	}
	return user + system, birth, nil
}

func TestNativeLoadProcCountersRequireBirthAndExactCPUFields(t *testing.T) {
	fields := strings.Fields("S 1 1 1 0 -1 0 0 0 0 0 7 3 0 0 0 0 2 0 123")
	build := func(values []string) string { return "42 (owned (core) fixture) " + strings.Join(values, " ") }
	if cpu, birth, err := nativeLoadProcCounters(build(fields)); err != nil || cpu != 10 || birth != 123 {
		t.Fatal("resource sample lost the owned CPU or birth coordinates")
	}
	for _, change := range []func([]string){
		func(f []string) { f[11] = "-1" }, func(f []string) { f[12] = "private" },
		func(f []string) { f[19] = "0" }, func(f []string) { f[19] = "invalid" },
		func(f []string) { f[11], f[12] = "18446744073709551615", "1" },
	} {
		values := append([]string{}, fields...)
		change(values)
		if _, _, err := nativeLoadProcCounters(build(values)); err == nil {
			t.Fatal("resource sample accepted malformed or unbound counters")
		}
	}
	for _, input := range []string{"", "42 no process name", "42 (core) S 1", "42 (core)"} {
		if _, _, err := nativeLoadProcCounters(input); err == nil {
			t.Fatal("incomplete process sample was accepted")
		}
	}
}
