package destlist

import (
	"bytes"
	"crypto/sha256"
	"encoding/csv"
	"encoding/hex"
	"io"
	"net/netip"
	"regexp"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"golang.org/x/net/idna"
	"golang.org/x/net/publicsuffix"
	"gopkg.in/yaml.v3"
)

const (
	MaxCustomBytes = 4 << 20
	MaxRemoteBytes = 16 << 20
	MaxEntries     = 50000
	MaxSamples     = 20
)

type Sample = domain.DestParseSample
type Report = domain.DestParseReport

type Parsed struct {
	Entries                 []byte
	EntryCount, RegexpCount int
	ContentSHA256           string
	Report                  Report
}

type Error struct {
	Code  string
	Line  int
	Entry string
}

func (e *Error) Error() string {
	if e.Entry != "" {
		return e.Code + ": " + e.Entry
	}
	return e.Code
}
func (e *Error) Unwrap() error { return domain.ErrValidation }

func ParseCustom(raw []byte) (Parsed, error) { return parse(raw, false) }
func ParseRemote(raw []byte) (Parsed, error) { return parse(raw, true) }

type inputLine struct {
	number int
	text   string
}

func parse(raw []byte, remote bool) (Parsed, error) {
	limit := MaxCustomBytes
	if remote {
		limit = MaxRemoteBytes
	}
	if len(raw) > limit {
		return Parsed{}, &Error{Code: "dest_list_too_large"}
	}
	if !utf8.Valid(raw) {
		return Parsed{}, &Error{Code: "dest_list_parse_failed"}
	}
	lines, err := sourceLines(raw, remote)
	if err != nil {
		return Parsed{}, err
	}
	p := Parsed{Report: Report{Samples: []Sample{}}}
	set := make(map[string]struct{})
	for _, line := range lines {
		text := strings.TrimSpace(line.text)
		if text == "" {
			continue
		}
		if strings.HasPrefix(text, "#") {
			p.ignore(line, "comment")
			continue
		}
		values, reason := expandLine(text)
		if reason != "" {
			p.ignore(line, reason)
			continue
		}
		for _, value := range values {
			entry, reason := normalize(value)
			if reason != "" {
				p.ignore(line, reason)
				continue
			}
			if IsBroad(entry) {
				if remote {
					return Parsed{}, &Error{Code: "broad_entry", Line: line.number, Entry: boundedText(entry)}
				}
				p.ignore(line, "broad_entry")
				continue
			}
			if _, exists := set[entry]; exists {
				p.ignore(line, "duplicate")
				continue
			}
			if len(set) == MaxEntries {
				return Parsed{}, &Error{Code: "dest_list_too_large"}
			}
			set[entry] = struct{}{}
			if strings.HasPrefix(entry, "regexp:") {
				p.RegexpCount++
			}
			if entry != text {
				p.Report.Rewritten++
				p.sample(line, "normalized")
			}
		}
	}
	p.finish(set)
	return p, nil
}

func (p *Parsed) finish(set map[string]struct{}) {
	entries := make([]string, 0, len(set))
	for entry := range set {
		entries = append(entries, entry)
	}
	slices.Sort(entries)
	if len(entries) > 0 {
		p.Entries = []byte(strings.Join(entries, "\n") + "\n")
	}
	p.EntryCount, p.Report.Accepted = len(entries), len(entries)
	digest := sha256.Sum256(p.Entries)
	p.ContentSHA256 = hex.EncodeToString(digest[:])
}

func sourceLines(raw []byte, remote bool) ([]inputLine, error) {
	plain := strings.Split(strings.TrimPrefix(string(raw), "\ufeff"), "\n")
	if remote {
		for _, text := range plain {
			text = strings.TrimSpace(text)
			if text == "" || strings.HasPrefix(text, "#") {
				continue
			}
			if strings.HasPrefix(text, "payload:") || strings.HasPrefix(text, "---") {
				return payloadLines(raw)
			}
		}
	}
	lines := make([]inputLine, 0, len(plain))
	for i, text := range plain {
		lines = append(lines, inputLine{i + 1, text})
	}
	return lines, nil
}

func payloadLines(raw []byte) ([]inputLine, error) {
	decoder := yaml.NewDecoder(bytes.NewReader(raw))
	var doc yaml.Node
	invalid := &Error{Code: "dest_list_parse_failed"}
	if err := decoder.Decode(&doc); err != nil {
		return nil, invalid
	}
	var extra yaml.Node
	if err := decoder.Decode(&extra); err != io.EOF {
		return nil, invalid
	}
	if len(doc.Content) != 1 || doc.Content[0].Kind != yaml.MappingNode {
		return nil, invalid
	}
	var payload *yaml.Node
	root := doc.Content[0]
	for i := 0; i < len(root.Content); i += 2 {
		if root.Content[i].Value != "payload" {
			continue
		}
		if payload != nil {
			return nil, invalid
		}
		payload = root.Content[i+1]
	}
	if payload == nil || payload.Kind != yaml.SequenceNode {
		return nil, invalid
	}
	lines := make([]inputLine, 0, len(payload.Content))
	for _, node := range payload.Content {
		if node.Kind != yaml.ScalarNode || node.Tag != "!!str" || strings.ContainsAny(node.Value, "\r\n") {
			return nil, invalid
		}
		lines = append(lines, inputLine{node.Line, node.Value})
	}
	return lines, nil
}

func expandLine(text string) ([]string, string) {
	if strings.HasPrefix(text, "@@") {
		return nil, "unsupported_format"
	}
	if strings.HasPrefix(text, "||") {
		if strings.Contains(text, "$") {
			return nil, "unsupported_modifier"
		}
		if !strings.HasSuffix(text, "^") {
			return nil, "unsupported_format"
		}
		return []string{"domain:" + strings.TrimSuffix(strings.TrimPrefix(text, "||"), "^")}, ""
	}
	fields := strings.Fields(text)
	if len(fields) > 1 && (fields[0] == "0.0.0.0" || fields[0] == "127.0.0.1") {
		var values []string
		for _, host := range fields[1:] {
			if strings.HasPrefix(host, "#") {
				break
			}
			values = append(values, host)
		}
		return values, ""
	}
	// Split Clash syntax before the value; a raw regexp may itself contain a
	// comma, so it must never be treated as a rule-provider record.
	kind, _, comma := strings.Cut(text, ",")
	prefix := map[string]string{"DOMAIN-SUFFIX": "domain:", "DOMAIN": "full:", "DOMAIN-KEYWORD": "keyword:", "DOMAIN-REGEX": "regexp:", "IP-CIDR": "", "IP-CIDR6": ""}
	if comma {
		if mapped, known := prefix[strings.TrimSpace(kind)]; known {
			reader := csv.NewReader(strings.NewReader(text))
			reader.FieldsPerRecord = -1
			reader.TrimLeadingSpace = true
			fields, err := reader.Read()
			if err != nil || len(fields) < 2 || len(fields) > 3 {
				return nil, "unsupported_format"
			}
			return []string{mapped + strings.TrimSpace(fields[1])}, ""
		}
	}
	return []string{text}, ""
}

func normalize(value string) (string, string) {
	value = strings.TrimSpace(value)
	if prefix, err := netip.ParsePrefix(value); err == nil {
		return prefix.Masked().String(), ""
	}
	if addr, err := netip.ParseAddr(value); err == nil && addr.Zone() == "" {
		return netip.PrefixFrom(addr, addr.BitLen()).String(), ""
	}
	prefix, text, tagged := strings.Cut(value, ":")
	if !tagged {
		prefix, text = "domain", value
	}
	switch prefix {
	case "regexp":
		if text == "" || strings.ContainsAny(text, "\r\n") {
			return "", "invalid_entry"
		}
		if _, err := regexp.Compile(text); err != nil {
			return "", "invalid_regexp"
		}
		return "regexp:" + text, ""
	case "keyword":
		if text == "" || strings.ContainsAny(text, " \t\r\n") {
			return "", "invalid_entry"
		}
		return "keyword:" + strings.ToLower(text), ""
	case "domain", "full":
		if prefix == "domain" {
			text = strings.TrimPrefix(strings.TrimPrefix(strings.TrimPrefix(text, "*."), "+."), ".")
		}
		text = strings.TrimSuffix(strings.ToLower(text), ".")
		if !tagged && (text == "localhost" || text == "localhost.localdomain" || text == "broadcasthost") {
			return "", "local_host"
		}
		// Already-ASCII hostnames use the Protocol grammar unchanged. Running
		// an existing punycode label back through today's IDNA tables can reject
		// legacy labels that the released catalog and both cores still accept.
		ascii := text
		if !hostname.MatchString(ascii) {
			var err error
			ascii, err = idna.Lookup.ToASCII(text)
			if err != nil {
				return "", "invalid_entry"
			}
		}
		if ascii == "" || len(ascii) > 253 || !hostname.MatchString(ascii) {
			return "", "invalid_entry"
		}
		for _, label := range strings.Split(ascii, ".") {
			if label == "" || len(label) > 63 {
				return "", "invalid_entry"
			}
		}
		return prefix + ":" + ascii, ""
	default:
		return "", "unsupported_format"
	}
}

var hostname = regexp.MustCompile(`^[a-z0-9._-]+$`)
var broadProbes = [...]string{"q7m2.example.com", "v4h9.example.org", "p8r3.example.net", "k2w6.example.cn", "n5t1.example.co.uk", "z3d8.example.jp", "a9s4.example.de", "f6b7.example.io"}

// IsBroad is also used when attaching an existing list to an allow rule.
// It is a conservative probe check, not a proof that a regexp is narrow.
func IsBroad(entry string) bool {
	if prefix, err := netip.ParsePrefix(entry); err == nil {
		return (prefix.Addr().Is4() && prefix.Bits() < 8) || (prefix.Addr().Is6() && prefix.Bits() < 16)
	}
	prefix, value, _ := strings.Cut(entry, ":")
	switch prefix {
	case "domain", "full":
		suffix, _ := publicsuffix.PublicSuffix(value)
		return suffix == value
	case "keyword":
		return utf8.RuneCountInString(value) < 4
	case "regexp":
		expression, err := regexp.Compile(value)
		if err != nil {
			return false
		}
		for _, probe := range broadProbes {
			if !expression.MatchString(probe) {
				return false
			}
		}
		return true
	}
	return false
}

func (p *Parsed) ignore(line inputLine, reason string) {
	p.Report.Ignored++
	if reason == "broad_entry" {
		p.Report.IgnoredBroad++
	}
	p.sample(line, reason)
}
func (p *Parsed) sample(line inputLine, reason string) {
	if len(p.Report.Samples) < MaxSamples {
		p.Report.Samples = append(p.Report.Samples, Sample{Line: line.number, Text: boundedText(line.text), Reason: reason})
	}
}
func boundedText(text string) string {
	if len(text) <= 512 {
		return text
	}
	end := 512
	for !utf8.RuneStart(text[end]) {
		end--
	}
	return text[:end]
}
