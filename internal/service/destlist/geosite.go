package destlist

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"regexp"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"
)

type Category struct {
	Name              string   `json:"name"`
	Count             int      `json:"count"`
	RegexpCount       int      `json:"regexp_count"`
	SourceCount       int      `json:"source_count"`
	IgnoredBroadCount int      `json:"ignored_broad_count"`
	Attrs             []string `json:"attrs"`
}

type geoEntry struct {
	entry        string
	attrs        []string
	line         int
	source, base string
	broad        bool
}
type Catalog struct {
	entries    map[string][]geoEntry
	categories []Category
}

func ParseGeosite(raw, checksum []byte) (*Catalog, error) {
	if len(raw) > MaxRemoteBytes {
		return nil, &Error{Code: "dest_list_too_large"}
	}
	fields := strings.Fields(string(checksum))
	if len(fields) != 2 || strings.TrimPrefix(fields[1], "*") != "dlc.dat_plain.yml" {
		return nil, &Error{Code: "dest_geosite_checksum_failed"}
	}
	want, err := hex.DecodeString(fields[0])
	digest := sha256.Sum256(raw)
	if err != nil || len(want) != len(digest) || !bytes.Equal(want, digest[:]) {
		return nil, &Error{Code: "dest_geosite_checksum_failed"}
	}
	return parseCatalog(raw)
}

// parseCatalog is also used for the local cache, whose contents were verified
// before an atomic file replacement. A network response must use ParseGeosite.
func parseCatalog(raw []byte) (*Catalog, error) {
	if len(raw) > MaxRemoteBytes {
		return nil, &Error{Code: "dest_list_too_large"}
	}
	var source struct {
		Lists []struct {
			Name   string   `yaml:"name"`
			Length int      `yaml:"length"`
			Rules  []string `yaml:"rules"`
		} `yaml:"lists"`
	}
	decoder := yaml.NewDecoder(bytes.NewReader(raw))
	decoder.KnownFields(true)
	invalid := &Error{Code: "dest_geosite_parse_failed"}
	if err := decoder.Decode(&source); err != nil {
		return nil, invalid
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF || len(source.Lists) == 0 {
		return nil, invalid
	}
	catalog := &Catalog{entries: make(map[string][]geoEntry, len(source.Lists))}
	for _, list := range source.Lists {
		name := strings.ToLower(list.Name)
		if !categoryName.MatchString(name) || list.Length != len(list.Rules) || list.Length < 0 {
			return nil, invalid
		}
		if _, duplicate := catalog.entries[name]; duplicate {
			return nil, invalid
		}
		info := Category{Name: name, SourceCount: len(list.Rules), Attrs: []string{}}
		unique := map[string]struct{}{}
		attrs := map[string]struct{}{}
		entries := make([]geoEntry, 0, len(list.Rules))
		for index, rule := range list.Rules {
			base, attrText, found := strings.Cut(rule, ":@")
			var entryAttrs []string
			if found {
				for _, attr := range strings.Split(strings.ReplaceAll(attrText, ":@", ",@"), ",@") {
					if !attributeName.MatchString(attr) {
						return nil, invalid
					}
					entryAttrs = append(entryAttrs, attr)
					attrs[attr] = struct{}{}
				}
			}
			entry, reason := normalize(base)
			if reason != "" {
				return nil, &Error{Code: "dest_geosite_parse_failed", Entry: boundedText(name + ": " + rule + " (" + reason + ")")}
			}
			broad := IsBroad(entry)
			if broad {
				info.IgnoredBroadCount++
			} else if _, exists := unique[entry]; !exists {
				unique[entry] = struct{}{}
				info.Count++
				if strings.HasPrefix(entry, "regexp:") {
					info.RegexpCount++
				}
			}
			entries = append(entries, geoEntry{entry: entry, attrs: entryAttrs, line: index + 1, source: rule, base: base, broad: broad})
		}
		for attr := range attrs {
			info.Attrs = append(info.Attrs, attr)
		}
		slices.Sort(info.Attrs)
		catalog.entries[name] = entries
		catalog.categories = append(catalog.categories, info)
	}
	slices.SortFunc(catalog.categories, func(a, b Category) int { return strings.Compare(a.Name, b.Name) })
	return catalog, nil
}

var categoryName = regexp.MustCompile(`^[a-z0-9][a-z0-9._!-]*$`)
var attributeName = regexp.MustCompile(`^!?[A-Za-z0-9_-]+$`)

func (c *Catalog) Categories() []Category {
	result := make([]Category, len(c.categories))
	for i, category := range c.categories {
		result[i] = category
		result[i].Attrs = append([]string{}, category.Attrs...)
	}
	return result
}

func (c *Catalog) Select(category string, attrs []string) (Parsed, error) {
	entries, exists := c.entries[category]
	if !exists {
		return Parsed{}, &Error{Code: "dest_geosite_category_unknown"}
	}
	var available []string
	for _, info := range c.categories {
		if info.Name == category {
			available = info.Attrs
			break
		}
	}
	for _, attr := range attrs {
		if !slices.Contains(available, attr) {
			return Parsed{}, &Error{Code: "dest_geosite_attribute_unknown"}
		}
	}
	p := Parsed{Report: Report{Samples: []Sample{}}}
	set := map[string]struct{}{}
	for _, entry := range entries {
		matches := true
		for _, attr := range attrs {
			if !slices.Contains(entry.attrs, attr) {
				matches = false
				break
			}
		}
		if matches {
			line := inputLine{number: entry.line, text: entry.source}
			if entry.broad {
				p.ignore(line, "broad_entry")
				continue
			}
			if _, exists := set[entry.entry]; exists {
				p.ignore(line, "duplicate")
				continue
			}
			if len(set) == MaxEntries {
				return Parsed{}, &Error{Code: "dest_list_too_large"}
			}
			set[entry.entry] = struct{}{}
			if strings.HasPrefix(entry.entry, "regexp:") {
				p.RegexpCount++
			}
			if entry.entry != entry.base {
				p.Report.Rewritten++
				p.normalizedSample(line, entry.entry)
			}
		}
	}
	p.finish(set)
	if p.EntryCount == 0 {
		return p, &Error{Code: "dest_list_empty_after_filter"}
	}
	return p, nil
}
