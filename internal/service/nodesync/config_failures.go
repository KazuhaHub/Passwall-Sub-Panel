package nodesync

import (
	"context"
	"errors"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/metrics"
)

func recordConfigFailure(phase string, err error) {
	if err == nil {
		return
	}
	switch phase {
	case "catalog", "build", "compile", "repository", "capability", "encode", "mint":
	default:
		return
	}
	cause := "other"
	switch {
	case errors.Is(err, context.Canceled):
		cause = "canceled"
	case errors.Is(err, context.DeadlineExceeded):
		cause = "deadline"
	case errors.Is(err, domain.ErrConflict):
		cause = "conflict"
	case errors.Is(err, domain.ErrValidation):
		cause = "validation"
	case errors.Is(err, domain.ErrUnavailable):
		cause = "unavailable"
	}
	metrics.NodeControlConfigErrorTotal.With(phase + "_" + cause).Inc()
}
