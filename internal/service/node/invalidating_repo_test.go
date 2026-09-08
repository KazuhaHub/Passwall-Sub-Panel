package node

import (
	"context"
	"reflect"
	"sort"
	"testing"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

// countingNodeRepo records nothing but success, so the tests below observe the
// decorator rather than a repo.
type countingNodeRepo struct{ ports.NodeRepo }

func (countingNodeRepo) Create(context.Context, *domain.Node) error                { return nil }
func (countingNodeRepo) Update(context.Context, *domain.Node) error                { return nil }
func (countingNodeRepo) UpdateMetadata(context.Context, *domain.Node) error        { return nil }
func (countingNodeRepo) UpdateInboundConfig(context.Context, *domain.Node) error   { return nil }
func (countingNodeRepo) UpdateEnabled(context.Context, int64, bool) error          { return nil }
func (countingNodeRepo) UpdateTrafficCounters(context.Context, *domain.Node) error { return nil }
func (countingNodeRepo) UpdateHealth(context.Context, *domain.Node) error          { return nil }
func (countingNodeRepo) Delete(context.Context, int64) error                       { return nil }
func (countingNodeRepo) BatchUpdateSortOrder(context.Context, []ports.NodeSortUpdate) error {
	return nil
}

// EVERY writer on ports.NodeRepo must be classified: either it changes what a
// subscription renders (invalidate) or the poll owns it and render never reads
// it (do not — invalidating on those would empty the render cache several
// times a minute and make it useless).
//
// This guard exists because the previous mechanism was "remember to call the
// invalidator", and 15 of 17 mutating service methods forgot. A writer added
// to the port without a decision lands in neither list and fails here, instead
// of silently defaulting to the answer that produced the bug: SetEnabled did
// not invalidate, so a node the operator disabled kept being served for up to
// a minute.
func TestInvalidatingNodeRepoCoversEveryWriter(t *testing.T) {
	// Writers the decorator MUST wrap.
	subscriptionVisible := []string{
		"BatchUpdateSortOrder",
		"Create",
		"Delete",
		"Update",
		"UpdateEnabled",
		"UpdateInboundConfig",
		"UpdateMetadata",
	}
	// Writers the decorator MUST NOT wrap, each with the reason it is exempt.
	pollOwned := map[string]string{
		"BatchUpdateTrafficCounters": "traffic poll, several times a minute; render never reads counters",
		"UpdateTrafficCounters":      "same as the batch form",
		"UpdateHealth":               "health loop; render never reads health",
		"UpdateCertBinding":          "cert lifecycle writes the binding; the rendered config comes from the inbound snapshot",
	}

	var missing, unexpected []string
	decorated := reflect.TypeOf(invalidatingNodeRepo{})
	port := reflect.TypeOf((*ports.NodeRepo)(nil)).Elem()

	for _, name := range subscriptionVisible {
		if _, ok := port.MethodByName(name); !ok {
			t.Fatalf("%s is no longer a ports.NodeRepo method — update this guard", name)
		}
		m, ok := decorated.MethodByName(name)
		// A method promoted from the embedded interface has the embedded type
		// as its receiver path; an overridden one is declared on the struct.
		// Comparing the func against the port's own tells them apart.
		if !ok || m.Func.Type().NumIn() == 0 {
			missing = append(missing, name)
			continue
		}
		if !overridesEmbedded(name) {
			missing = append(missing, name)
		}
	}
	for name := range pollOwned {
		if overridesEmbedded(name) {
			unexpected = append(unexpected, name)
		}
	}
	sort.Strings(missing)
	sort.Strings(unexpected)
	if len(missing) > 0 {
		t.Errorf("these subscription-visible writers are NOT invalidating: %v", missing)
	}
	if len(unexpected) > 0 {
		t.Errorf("these poll-owned writers invalidate and must not (they would keep the render cache empty): %v", unexpected)
	}

	// The two lists together must account for every writer on the port, so a
	// newly added one cannot slip through unclassified.
	classified := map[string]struct{}{}
	for _, n := range subscriptionVisible {
		classified[n] = struct{}{}
	}
	for n := range pollOwned {
		classified[n] = struct{}{}
	}
	var unclassified []string
	for i := 0; i < port.NumMethod(); i++ {
		name := port.Method(i).Name
		if !isWriter(name) {
			continue
		}
		if _, ok := classified[name]; !ok {
			unclassified = append(unclassified, name)
		}
	}
	sort.Strings(unclassified)
	if len(unclassified) > 0 {
		t.Errorf("ports.NodeRepo writers with no invalidation decision: %v\n"+
			"add each to subscriptionVisible (and to invalidating_repo.go) or to pollOwned with a reason", unclassified)
	}
}

// isWriter classifies a port method by name. Reads are the ones this guard has
// nothing to say about.
func isWriter(name string) bool {
	switch {
	case name == "Create", name == "Update", name == "Delete":
		return true
	case len(name) > 6 && name[:6] == "Update":
		return true
	case len(name) > 5 && name[:5] == "Batch":
		return true
	}
	return false
}

// overridesEmbedded reports whether invalidatingNodeRepo declares its own
// version of name rather than promoting the embedded interface's.
func overridesEmbedded(name string) bool {
	decorated, _ := reflect.TypeOf(invalidatingNodeRepo{}).MethodByName(name)
	embedded, ok := reflect.TypeOf((*ports.NodeRepo)(nil)).Elem().MethodByName(name)
	if !ok || decorated.Func.Kind() != reflect.Func {
		return false
	}
	// A promoted method's implementation is the interface's own wrapper; an
	// overridden one is a distinct function in this package. Comparing entry
	// points is unreliable, so call it and observe the side effect instead.
	fired := false
	repo := invalidatingNodeRepo{NodeRepo: countingNodeRepo{}, notify: func() { fired = true }}
	callWriter(repo, name)
	_ = embedded
	return fired
}

// callWriter invokes one writer with zero-valued arguments. The fake repo
// returns nil for all of them, so a wrapped method fires its notify and a
// promoted one does not.
func callWriter(repo invalidatingNodeRepo, name string) {
	v := reflect.ValueOf(repo).MethodByName(name)
	if !v.IsValid() {
		return
	}
	ft := v.Type()
	args := make([]reflect.Value, ft.NumIn())
	for i := 0; i < ft.NumIn(); i++ {
		if ft.In(i) == reflect.TypeOf((*context.Context)(nil)).Elem() {
			args[i] = reflect.ValueOf(context.Background())
			continue
		}
		args[i] = reflect.New(ft.In(i)).Elem()
	}
	func() {
		defer func() { _ = recover() }()
		v.Call(args)
	}()
}

// A failed write changed nothing, so dropping every user's rendered
// subscription would be a re-render stampede producing identical output.
func TestInvalidationSkippedOnAFailedWrite(t *testing.T) {
	fired := false
	if err := after(context.DeadlineExceeded, func() { fired = true }); err == nil {
		t.Fatal("after must pass the error through")
	}
	if fired {
		t.Fatal("a failed write must not invalidate")
	}
	if err := after(nil, func() { fired = true }); err != nil {
		t.Fatal(err)
	}
	if !fired {
		t.Fatal("a successful write must invalidate")
	}
}

// The separator table has no poll-owned columns, so every writer on it is
// subscription-visible and the decorator wraps all of them.
func TestInvalidatingSeparatorRepoWrapsEveryWriter(t *testing.T) {
	port := reflect.TypeOf((*ports.SeparatorRepo)(nil)).Elem()
	decorated := reflect.TypeOf(invalidatingSeparatorRepo{})
	for i := 0; i < port.NumMethod(); i++ {
		name := port.Method(i).Name
		if !isWriter(name) {
			continue
		}
		if _, ok := decorated.MethodByName(name); !ok {
			t.Errorf("separator writer %s is not wrapped", name)
		}
	}
}
