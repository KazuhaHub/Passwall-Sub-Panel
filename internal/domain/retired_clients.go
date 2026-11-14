package domain

// RetiredClients are the emails of the live upstream projections a client sync
// retired, by panel, in the two kinds that must be handled differently.
//
// PanelRemoved: the user has no node left on that panel at all, so the client
// there serves nothing; removing it can only tighten access, whatever else is
// failing for the user.
//
// Regrouped: the client was replaced by another on the same panel (a merge or a
// re-key); removing it before its replacement is live would cut the user off.
type RetiredClients struct {
	PanelRemoved map[int64][]string
	Regrouped    map[int64][]string
}

// All merges both kinds into one per-panel list.
func (r RetiredClients) All() map[int64][]string {
	out := map[int64][]string{}
	for _, m := range []map[int64][]string{r.PanelRemoved, r.Regrouped} {
		for panel, emails := range m {
			out[panel] = append(out[panel], emails...)
		}
	}
	return out
}
