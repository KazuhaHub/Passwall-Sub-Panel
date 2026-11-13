package destlist

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

// dlcData is one parsed dlc.dat_plain.yml: the v2fly domain-list-community
// release with every include: already expanded. Each category maps to its raw
// rules, spelled "domain:x", "full:x", "regexp:x" or "keyword:x" with
// attributes as a ":@a,@b" suffix.
type dlcData struct {
	cats map[string][]string
}

// parseDLC reads the release's plain YAML. A file with no categories is an
// error: a truncated or wrong download must never read as "every category is
// gone".
func parseDLC(raw []byte) (*dlcData, error) {
	var doc struct {
		Lists []struct {
			Name  string   `yaml:"name"`
			Rules []string `yaml:"rules"`
		} `yaml:"lists"`
	}
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("%w: geosite data does not parse: %v", domain.ErrValidation, err)
	}
	if len(doc.Lists) == 0 {
		return nil, fmt.Errorf("%w: geosite data has no categories", domain.ErrValidation)
	}
	d := &dlcData{cats: make(map[string][]string, len(doc.Lists))}
	for _, l := range doc.Lists {
		name := strings.ToLower(strings.TrimSpace(l.Name))
		if name == "" {
			continue
		}
		d.cats[name] = l.Rules
	}
	return d, nil
}

var dlcAttrRE = regexp.MustCompile(`^!?[a-z0-9_-]+$`)

// splitDLCRule separates a rule from its attribute suffix. The suffix is the
// part after the LAST ":@" and holds one or more attribute names, each
// prefixed @ and joined by commas ("domain:x:@ads,@telemetry"); the format
// also permits repeated ":@" suffixes, so it peels until what is left no
// longer ends in a valid one. Attribute names are kept LITERALLY: "cn" and
// "!cn" are two different attributes (F49), and the negation is not applied.
func splitDLCRule(rule string) (entry string, attrs []string) {
	entry = rule
	for {
		i := strings.LastIndex(entry, ":@")
		if i < 0 {
			break
		}
		var got []string
		valid := true
		for _, a := range strings.Split(entry[i+2:], ",") {
			a = strings.TrimPrefix(a, "@")
			if !dlcAttrRE.MatchString(a) {
				valid = false
				break
			}
			got = append(got, a)
		}
		if !valid {
			break
		}
		attrs = append(attrs, got...)
		entry = entry[:i]
	}
	return entry, attrs
}

// Category is one geosite category as the admin picks it from.
type Category struct {
	Name    string `json:"name"`
	Entries int    `json:"entries"`
	// Attrs are the literal attribute names that occur in the category, with
	// how many entries carry each: the only attribute filters the UI offers.
	Attrs []AttrCount `json:"attrs"`
}

// AttrCount is one attribute of a category.
type AttrCount struct {
	Name  string `json:"name"`
	Count int    `json:"count"`
}

// categories is the index of the data, sorted by name.
func (d *dlcData) categories() []Category {
	out := make([]Category, 0, len(d.cats))
	for name, rules := range d.cats {
		counts := map[string]int{}
		for _, r := range rules {
			_, attrs := splitDLCRule(r)
			for _, a := range slices.Compact(slices.Sorted(slices.Values(attrs))) {
				counts[a]++
			}
		}
		c := Category{Name: name, Entries: len(rules), Attrs: []AttrCount{}}
		for a, n := range counts {
			c.Attrs = append(c.Attrs, AttrCount{Name: a, Count: n})
		}
		slices.SortFunc(c.Attrs, func(x, y AttrCount) int { return strings.Compare(x.Name, y.Name) })
		out = append(out, c)
	}
	slices.SortFunc(out, func(x, y Category) int { return strings.Compare(x.Name, y.Name) })
	return out
}

// Derived is the canonical entries of one (category, attrs) selection.
type Derived struct {
	Entries     []byte
	EntryCount  int
	RegexpCount int
	SHA256      string
	// Skipped is how many rules were left out because they were not a form
	// this panel can send (an unknown prefix, a hostname the wire refuses, a
	// regexp Go does not compile).
	Skipped int
	// Broad is the first broad entry the probes found, "".
	Broad string
	set   map[string]struct{}
}

// derive selects the rules of category that carry EVERY attribute in attrs
// (all of them when attrs is empty), strips the attribute suffixes and returns
// the canonical, sorted, de-duplicated entries. ok is false when the category
// does not exist.
func (d *dlcData) derive(category string, attrs []string) (res *Derived, ok bool) {
	rules, ok := d.cats[strings.ToLower(category)]
	if !ok {
		return nil, false
	}
	want := slices.Compact(slices.Sorted(slices.Values(attrs)))
	res = &Derived{set: map[string]struct{}{}}
	for _, r := range rules {
		entry, have := splitDLCRule(r)
		if !hasAll(have, want) {
			continue
		}
		canon, kind := canonicalDLCEntry(entry)
		if canon == "" {
			res.Skipped++
			continue
		}
		if res.Broad == "" && broadReasonFor(canon, true) != "" {
			res.Broad = canon
		}
		if _, seen := res.set[canon]; seen {
			continue
		}
		res.set[canon] = struct{}{}
		if kind == "regexp" {
			res.RegexpCount++
		}
	}
	lines := make([]string, 0, len(res.set))
	for e := range res.set {
		lines = append(lines, e)
	}
	slices.Sort(lines)
	res.Entries = []byte(strings.Join(lines, "\n"))
	res.EntryCount = len(lines)
	sum := sha256.Sum256(res.Entries)
	res.SHA256 = hex.EncodeToString(sum[:])
	return res, true
}

func hasAll(have, want []string) bool {
	for _, w := range want {
		if !slices.Contains(have, w) {
			return false
		}
	}
	return true
}

// canonicalDLCEntry turns one stripped rule into a canonical entry and says
// its kind, or returns "" when it is not usable.
func canonicalDLCEntry(rule string) (canon, kind string) {
	i := strings.IndexByte(rule, ':')
	if i <= 0 {
		return "", ""
	}
	kind, value := rule[:i], rule[i+1:]
	switch kind {
	case "domain", "full":
		host, ok := NormalizeDomain(value)
		if !ok {
			return "", ""
		}
		return kind + ":" + host, kind
	case "keyword":
		v := strings.ToLower(strings.TrimSpace(value))
		if !validKeyword(v) {
			return "", ""
		}
		return "keyword:" + v, kind
	case "regexp":
		if value == "" || len(value) > MaxRegexpBytes {
			return "", ""
		}
		if _, err := regexp.Compile(value); err != nil {
			return "", ""
		}
		return "regexp:" + value, kind
	}
	return "", ""
}

// diffCategories compares two data sets category by category. Only categories
// that differ are returned, sorted by name.
type categoryDiff struct {
	Name    string `json:"name"`
	Old     int    `json:"old"`
	New     int    `json:"new"`
	Added   int    `json:"added"`
	Removed int    `json:"removed"`
	// State is "added" (a category the old data lacks), "removed" (one the new
	// data lacks) or "changed".
	State string `json:"state"`
}

func diffCategories(old, cur *dlcData) []categoryDiff {
	var out []categoryDiff
	names := map[string]struct{}{}
	if old != nil {
		for n := range old.cats {
			names[n] = struct{}{}
		}
	}
	for n := range cur.cats {
		names[n] = struct{}{}
	}
	for name := range names {
		var oldRules, newRules []string
		var inOld, inNew bool
		if old != nil {
			oldRules, inOld = old.cats[name]
		}
		newRules, inNew = cur.cats[name]
		oldSet, newSet := toSet(oldRules), toSet(newRules)
		added, removed := 0, 0
		for r := range newSet {
			if _, ok := oldSet[r]; !ok {
				added++
			}
		}
		for r := range oldSet {
			if _, ok := newSet[r]; !ok {
				removed++
			}
		}
		if added == 0 && removed == 0 && inOld == inNew {
			continue
		}
		state := "changed"
		switch {
		case !inOld:
			state = "added"
		case !inNew:
			state = "removed"
		}
		out = append(out, categoryDiff{Name: name, Old: len(oldSet), New: len(newSet), Added: added, Removed: removed, State: state})
	}
	slices.SortFunc(out, func(a, b categoryDiff) int { return strings.Compare(a.Name, b.Name) })
	return out
}

func toSet(in []string) map[string]struct{} {
	m := make(map[string]struct{}, len(in))
	for _, s := range in {
		m[s] = struct{}{}
	}
	return m
}
