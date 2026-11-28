package render

import (
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"reflect"
	"strings"
	"testing"
)

// The functions below are the rule-line readers as they were before they were
// made cheaper. The current ones must return exactly what these return.

func referenceRewriteRuleTarget(line string, transform func(string) (string, error), rawPolicy ...bool) (string, error) {
	fields := splitRuleFields(line)
	target := ruleOutboundTarget(fields)
	spans := ruleFieldSpans(line)
	if len(rawPolicy) > 0 && rawPolicy[0] {
		target = mihomoRuleTarget(line)
	}
	if target == "" {
		return line, nil
	}
	replacement, err := transform(target)
	if err == nil && len(rawPolicy) > 0 && rawPolicy[0] && strings.ContainsAny(replacement, ",\r\n") {
		return "", fmt.Errorf("mihomo rule target %q cannot contain a comma or newline; reference this line through a proxy group", replacement)
	}
	if err != nil || replacement == target {
		return line, err
	}
	if index := ruleOutboundTargetIndex(fields); index >= 0 && index < len(spans) {
		span := spans[index]
		part := line[span[0]:span[1]]
		trimmed := strings.TrimSpace(part)
		begin := span[0] + strings.Index(part, trimmed)
		end := begin + len(trimmed)
		if len(rawPolicy) > 0 && rawPolicy[0] {
			return line[:begin] + replacement + line[end:], nil
		}
		var encoded strings.Builder
		writer := csv.NewWriter(&encoded)
		if err := writer.Write([]string{replacement}); err != nil {
			return "", err
		}
		writer.Flush()
		return line[:begin] + strings.TrimSuffix(encoded.String(), "\n") + line[end:], writer.Error()
	}
	return "", fmt.Errorf("cannot locate subscription rule target %q", target)
}

func referenceSplitCSVLine(line string) []string {
	if strings.ContainsAny(line, "()") {
		return splitTopLevelRuleFields(line)
	}
	r := csv.NewReader(strings.NewReader(line))
	r.FieldsPerRecord = -1
	parts, err := r.Read()
	if err == nil {
		return parts
	}
	if err != nil && err != io.EOF {
		return strings.Split(line, ",")
	}
	return parts
}

// ruleLineCorpus is every rule of the shipped rule set, hand-picked awkward
// lines, and random recombinations of their pieces.
func ruleLineCorpus(t *testing.T) []string {
	t.Helper()
	lines := []string{
		"", " ", ",", ",,", "MATCH", "MATCH,", "MATCH,G", " MATCH , G ", "- MATCH,G", "# MATCH,G", "DOMAIN,{{ x }},G",
		"DOMAIN,a.example,G", "DOMAIN,a.example,\"G\"", "DOMAIN,a.example,'G'", "DOMAIN,a.example,\"one, two\"", "DOMAIN,a.example,'it''s'",
		"DOMAIN,a.example,it's", "DOMAIN,a.example,it's,no-resolve", "DOMAIN,a.example,say \"hi\"", "DOMAIN,\"a.example\",G",
		"IP-CIDR,10.0.0.0/8,G,no-resolve", "IP-CIDR,10.0.0.0/8,G,src", "IP-CIDR,10.0.0.0/8,G,NO-RESOLVE,src", "IP-CIDR,10.0.0.0/8,no-resolve",
		"IP-CIDR,10.0.0.0/8,,G", "IP-CIDR,10.0.0.0/8,G,", "AND,((NETWORK,UDP),(DST-PORT,443)),G", "AND,((NETWORK,UDP),(DST-PORT,443)),\"G, H\"",
		"OR,((DOMAIN,a),(DOMAIN,b)", "NOT,(DOMAIN,a)),G", "SUB-RULE,(NETWORK,tcp),child", "sub-rule,(NETWORK,tcp),child,no-resolve",
		"DOMAIN,a\r,G", "DOMAIN,a\nb,G", "DOMAIN,a.example,G\r\n", "DOMAIN,a.example,\"G", "DOMAIN,a.example,G\"", "\"DOMAIN,a.example,G\"",
		"DOMAIN,a\\,b,G", "DOMAIN,\"a\\\"b\",G", "DOMAIN,a.example,🚀 节点选择", "DOMAIN,a.example, 🚀 节点选择 ,no-resolve",
	}
	for _, line := range strings.Split(goldenSeedRuleSet(t).Content, "\n") {
		lines = append(lines, strings.TrimPrefix(line, "- "))
	}
	pieces := []string{"DOMAIN", "MATCH", "SUB-RULE", "AND", "IP-CIDR", ",", ",", ",", "(", ")", "\"", "'", " ", "G", "no-resolve", "src", "a.example", "one, two", "\r", "\n", "\\", "#", "- ", "{{", "🚀 节点选择"}
	r := rand.New(rand.NewSource(1))
	for i := 0; i < 20000; i++ {
		var line strings.Builder
		for n := r.Intn(9); n >= 0; n-- {
			line.WriteString(pieces[r.Intn(len(pieces))])
		}
		lines = append(lines, line.String())
	}
	return lines
}

func TestRuleLineReadersMatchTheirReference(t *testing.T) {
	failing := errors.New("no such line")
	transforms := map[string]func(string) (string, error){
		"same":    func(name string) (string, error) { return name, nil },
		"renamed": func(name string) (string, error) { return "psp-line-token-7:direct", nil },
		"quoting": func(name string) (string, error) { return "one, \"two\"", nil },
		"failing": func(name string) (string, error) { return "", failing },
	}
	for _, line := range ruleLineCorpus(t) {
		if got, want := splitCSVLine(line), referenceSplitCSVLine(line); !reflect.DeepEqual(got, want) {
			t.Fatalf("splitCSVLine(%q) = %q, reference %q", line, got, want)
		}
		for name, transform := range transforms {
			for _, raw := range []bool{false, true} {
				got, gotErr := rewriteRuleTarget(line, transform, raw)
				want, wantErr := referenceRewriteRuleTarget(line, transform, raw)
				if got != want || fmt.Sprint(gotErr) != fmt.Sprint(wantErr) {
					t.Fatalf("rewriteRuleTarget(%q, %s, raw=%v) = %q, %v; reference %q, %v", line, name, raw, got, gotErr, want, wantErr)
				}
			}
		}
	}
}
