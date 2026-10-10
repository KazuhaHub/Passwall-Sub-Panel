package metrics

import "sync"

// CounterPairVec records a bounded pair of enum labels without changing the
// existing single-label recording API or diagnostic snapshot structure.
type CounterPairVec struct {
	name, help string
	labels     [2]string
	mu         sync.RWMutex
	children   map[[2]string]*Counter
}

func NewCounterPairVec(name, help, firstLabel, secondLabel string) *CounterPairVec {
	return &CounterPairVec{name: name, help: help, labels: [2]string{firstLabel, secondLabel}, children: make(map[[2]string]*Counter)}
}

// With accepts only caller-owned enum values. As with CounterVec, children are
// retained for the process lifetime; peer strings and identities are forbidden.
func (v *CounterPairVec) With(first, second string) *Counter {
	key := [2]string{first, second}
	v.mu.RLock()
	c := v.children[key]
	v.mu.RUnlock()
	if c != nil {
		return c
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	if c = v.children[key]; c != nil {
		return c
	}
	c = NewCounter(v.name+"{"+v.labels[0]+"="+first+","+v.labels[1]+"="+second+"}", v.help)
	v.children[key] = c
	return c
}
