package destpolicy

import "context"

// Info is what the published snapshot says about how it compiled: for the
// status page, and for tests.
type Info struct {
	Generation int64 `json:"generation"`
	// Geosite is the rule-data version (the geosite release tag) the published
	// lists were derived from; GeoIP the GeoIP database digest.
	Geosite string `json:"geosite_release,omitempty"`
	GeoIP   string `json:"geoip_sha256,omitempty"`
	// GeoIPUnavailable: a country in force could not be expanded for want of
	// a GeoIP database, so the rule that names it is not in the policy.
	GeoIPUnavailable bool `json:"geoip_unavailable"`
	// Policies is the note of each policy in force; SourceNotes says why a
	// source rule compiled to nothing ("geoip_unavailable" or "empty").
	Policies    map[int64]*PolicyNote `json:"policies"`
	SourceNotes map[int64]string      `json:"source_notes"`
}

// Info reads the published snapshot's compile notes. With nothing published it
// is empty.
func (s *Service) Info(ctx context.Context) (*Info, error) {
	st, err := s.state.Get(ctx)
	if err != nil {
		return nil, err
	}
	info := &Info{Generation: st.PublishedGeneration, Policies: map[int64]*PolicyNote{}, SourceNotes: map[int64]string{}}
	if st.PublishedGeneration == 0 {
		return info, nil
	}
	cb, err := s.baseFor(ctx, st.PublishedGeneration)
	if err != nil {
		return nil, err
	}
	info.Geosite, info.GeoIP, info.GeoIPUnavailable = cb.base.geosite, cb.base.geoip, cb.base.geoipUnavailable
	for id, n := range cb.base.policies {
		c := *n
		info.Policies[id] = &c
	}
	for id, n := range cb.base.sourceNotes {
		info.SourceNotes[id] = n
	}
	return info, nil
}
