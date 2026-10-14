package nodesync

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/metrics"
)

func TestConfigFailuresClassifyWrappedCausesWithoutErrorContent(t *testing.T) {
	metrics.Reset()
	defer metrics.Reset()
	causes := map[string]error{"canceled": context.Canceled, "deadline": context.DeadlineExceeded, "conflict": domain.ErrConflict, "validation": domain.ErrValidation, "unavailable": domain.ErrUnavailable, "other": errors.New("private-config-failure-marker")}
	for _, phase := range []string{"catalog", "build", "compile", "repository", "capability", "encode", "mint"} {
		for cause, err := range causes {
			recordConfigFailure(phase, fmt.Errorf("private-config-failure-marker: %w", err))
			if metrics.NodeControlConfigErrorTotal.With(phase+"_"+cause).Value() != 1 {
				t.Fatal("wrapped configuration cause was misclassified")
			}
		}
	}
	recordConfigFailure("private-config-failure-marker", errors.New("opaque"))
	recordConfigFailure("mint", nil)
	body, err := json.Marshal(metrics.Take())
	if err != nil || strings.Contains(string(body), "private-config-failure-marker") {
		t.Fatal("configuration metric exported error content or an open phase label")
	}
	count := 0
	for _, counter := range metrics.Take().Counters {
		if strings.HasPrefix(counter.Name, "psp_node_control_config_error_total{") {
			count++
			if counter.Value != 1 {
				t.Fatal("unknown phase or successful call added a failure")
			}
		}
	}
	if count != 42 {
		t.Fatal("configuration failure labels escaped their closed set")
	}
}
