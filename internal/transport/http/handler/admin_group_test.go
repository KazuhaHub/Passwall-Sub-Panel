package handler

import (
	"testing"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

func TestGroupDTOUsesEmptyTagsArray(t *testing.T) {
	dto := toGroupDTO(&domain.Group{
		ID:        1,
		Slug:      "default",
		Name:      "Default",
		TagFilter: domain.TagFilter{All: true},
	})

	if dto.TagFilter.Tags == nil {
		t.Fatal("TagFilter.Tags is nil, want empty slice")
	}
	if len(dto.TagFilter.Tags) != 0 {
		t.Fatalf("len(TagFilter.Tags) = %d, want 0", len(dto.TagFilter.Tags))
	}
}

// A group policy must be held to the same floor as a per-user limit. Create
// skipped this entirely at first: a negative cap there is inherited by every
// member, pushed verbatim as the panel's LimitIP, and does not even raise a
// capability-gap warning, because that only fires for a positive limit.
func TestValidateGroupLimits(t *testing.T) {
	neg, negI := int64(-1), -1
	ok, okI := int64(0), 3
	for _, tc := range []struct {
		name    string
		limits  domain.GroupLimits
		wantErr bool
	}{
		{"states nothing", domain.GroupLimits{}, false},
		{"explicit zeroes", domain.GroupLimits{TrafficLimitBytes: &ok, IPLimit: &okI}, false},
		{"negative traffic", domain.GroupLimits{TrafficLimitBytes: &neg}, true},
		{"negative ip", domain.GroupLimits{IPLimit: &negI}, true},
		{"negative device", domain.GroupLimits{DeviceLimit: &negI}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := validateGroupLimits(tc.limits); (err != nil) != tc.wantErr {
				t.Fatalf("validateGroupLimits(%+v) error = %v, wantErr %v", tc.limits, err, tc.wantErr)
			}
		})
	}
}

// Clearing a policy changes what members enforce just as much as setting one,
// so it has to register as a change and trigger the re-push. Comparing values
// while ignoring nil-ness would miss exactly that case.
func TestSameGroupLimits(t *testing.T) {
	three, seven := 3, 7
	base := domain.GroupLimits{IPLimit: &three}
	if !sameGroupLimits(base, domain.GroupLimits{IPLimit: &three}) {
		t.Error("equal values must compare equal")
	}
	if sameGroupLimits(base, domain.GroupLimits{IPLimit: &seven}) {
		t.Error("a changed value must compare different")
	}
	if sameGroupLimits(base, domain.GroupLimits{}) {
		t.Error("clearing a policy must compare different — members stop enforcing it")
	}
	if sameGroupLimits(domain.GroupLimits{}, base) {
		t.Error("setting a policy on a group that stated nothing must compare different")
	}
	if !sameGroupLimits(domain.GroupLimits{}, domain.GroupLimits{}) {
		t.Error("two empty policies are the same")
	}
}
