package panel

import (
	"errors"
	"testing"

	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

// A panel the pool does not hold answers the shared sentinel, so a caller can
// tell "this panel is gone" from a transient failure.
func TestPoolGetUnknownPanelIsErrPanelNotRegistered(t *testing.T) {
	p := &Pool{clients: map[int64]ports.PanelClient{}}
	if _, err := p.Get(42); !errors.Is(err, ports.ErrPanelNotRegistered) {
		t.Fatalf("err = %v, want ports.ErrPanelNotRegistered", err)
	}
}
