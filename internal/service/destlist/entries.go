package destlist

import (
	"context"
	"strings"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

// Entry patches preserve untouched original lines and comments. Removing one
// hostname from a multi-host hosts line retains its other normalized entries.
func patchCustomText(old []byte, add, remove []string) ([]byte, error) {
	removed := map[string]bool{}
	for _, entry := range append(append([]string(nil), add...), remove...) {
		if strings.ContainsAny(entry, "\r\n") || len(entry) > MaxCustomBytes {
			return nil, &Error{Code: "dest_list_parse_failed"}
		}
		parsed, err := ParseCustom([]byte(entry))
		if err != nil {
			return nil, err
		}
		if parsed.EntryCount == 0 && parsed.Report.IgnoredBroad == 0 {
			return nil, &Error{Code: "dest_list_parse_failed"}
		}
	}
	for _, entry := range remove {
		parsed, err := ParseCustom([]byte(entry))
		if err != nil {
			return nil, err
		}
		for _, line := range strings.Split(strings.TrimSuffix(string(parsed.Entries), "\n"), "\n") {
			if line != "" {
				removed[line] = true
			}
		}
	}
	var result strings.Builder
	for _, line := range strings.SplitAfter(string(old), "\n") {
		parsed, err := ParseCustom([]byte(line))
		if err != nil {
			return nil, err
		}
		changed := false
		var remaining []string
		for _, entry := range strings.Split(strings.TrimSuffix(string(parsed.Entries), "\n"), "\n") {
			if entry == "" {
				continue
			}
			if removed[entry] {
				changed = true
			} else {
				remaining = append(remaining, entry)
			}
		}
		if changed {
			for _, entry := range remaining {
				result.WriteString(entry + "\n")
			}
		} else {
			result.WriteString(line)
		}
	}
	if len(add) > 0 {
		if result.Len() > 0 && !strings.HasSuffix(result.String(), "\n") {
			result.WriteByte('\n')
		}
		for _, entry := range add {
			if result.Len()+len(entry)+1 > MaxCustomBytes {
				return nil, &Error{Code: "dest_list_too_large"}
			}
			result.WriteString(entry + "\n")
		}
	}
	if result.Len() > MaxCustomBytes {
		return nil, &Error{Code: "dest_list_too_large"}
	}
	return []byte(result.String()), nil
}

func prepareEntryPatch(list domain.DestList, allow bool, add, remove []string) (domain.DestList, error) {
	if list.Kind != domain.DestListCustom {
		return domain.DestList{}, domain.ErrValidation
	}
	text, err := patchCustomText(list.SourceText, add, remove)
	if err != nil {
		return domain.DestList{}, err
	}
	parsed, err := ParseCustom(text)
	if err != nil {
		return domain.DestList{}, err
	}
	if allow && parsed.Report.IgnoredBroad > 0 {
		return domain.DestList{}, &Error{Code: "dest_list_too_broad"}
	}
	list.SourceText, list.Entries, list.ContentSHA256 = text, parsed.Entries, parsed.ContentSHA256
	list.EntryCount, list.RegexpCount, list.ParseReport = parsed.EntryCount, parsed.RegexpCount, &parsed.Report
	return list, nil
}

func (s *Service) Entries(ctx context.Context, id int64, add, remove []string) (domain.DestList, error) {
	if s == nil || s.store == nil {
		return domain.DestList{}, domain.ErrUnavailable
	}
	store, ok := s.store.(interface {
		EditListEntries(context.Context, int64, time.Time, func(domain.DestList, bool) (domain.DestList, error)) (domain.DestList, error)
	})
	if !ok {
		return domain.DestList{}, domain.ErrUnavailable
	}
	ctx, release, err := s.operationGate.Read(ctx)
	if err != nil {
		return domain.DestList{}, err
	}
	defer release()
	old, err := s.store.GetList(ctx, id)
	if err != nil {
		return domain.DestList{}, err
	}
	preflight, err := prepareEntryPatch(old, false, add, remove)
	if err != nil {
		return domain.DestList{}, err
	}
	if s.validateSave != nil {
		if err := s.validateSave(ctx, preflight); err != nil {
			return domain.DestList{}, err
		}
	}
	return store.EditListEntries(ctx, id, s.now(), func(current domain.DestList, allow bool) (domain.DestList, error) {
		return prepareEntryPatch(current, allow, add, remove)
	})
}
