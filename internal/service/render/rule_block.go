package render

import (
	"crypto/sha256"
	"fmt"
	"strings"
	"sync"

	"gopkg.in/yaml.v3"
)

// A rule set is nearly all of a rendered Mihomo document (the shipped one is
// 474 KB, about 9.5k rules, 99% of the body) and none of it depends on the
// subscriber: the same lines go to everyone. The full pipeline nevertheless
// parses that text five times per render, rewrites every line three times and
// serialises it twice.
//
// A ruleBlock is what the shared rule text reduces to once, for every render:
//
//   - targets: the distinct outbound targets its rules name. Every pass over a
//     rule (line-name binding, pruning, validation, final naming, group
//     discovery) depends on the rule only through its target, so one rule per
//     distinct target says everything the other 9.5k do.
//   - standIn: those representative rules between two marker rules, as YAML
//     list items. The document is built, checked and serialised with them in
//     place of the whole block.
//   - emitted: the exact lines the serialiser writes for the whole block. They
//     replace the stand-in in the finished document.
//
// Only a block of a plain shape qualifies (see analyseRuleBlock); anything else
// keeps the full pipeline, which stays the reference implementation.
type ruleBlock struct {
	targets []string
	// standIn has no trailing newline, like every block handed to a template.
	standIn string
	// emittedStandIn is what the serialiser writes for standIn; emitted is what
	// it writes for the whole block. Both end in a newline.
	emittedStandIn string
	emitted        string
	// standInRules is the number of rules in standIn, markers included.
	standInRules int

	compared comparedContexts
}

// comparedContexts records, per render context, whether one render of that
// context was assembled both ways and came out identical. A context is what
// surrounds the rule block in the document apart from the subscriber's lines:
// the template text and the personal rules. See (*Service).mihomoBody.
type comparedContexts struct {
	mu     sync.Mutex
	proven map[[sha256.Size]byte]bool
}

// comparedContextsMax bounds the contexts one block remembers. When it is
// reached the record starts over, and each context is compared once more on
// its next render.
const comparedContextsMax = 4096

func renderContext(parts ...string) [sha256.Size]byte {
	hash := sha256.New()
	for _, part := range parts {
		fmt.Fprintf(hash, "%d:%s", len(part), part)
	}
	var context [sha256.Size]byte
	hash.Sum(context[:0])
	return context
}

func (c *comparedContexts) lookup(context [sha256.Size]byte) (proven, compared bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	proven, compared = c.proven[context]
	return proven, compared
}

func (c *comparedContexts) record(context [sha256.Size]byte, same bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.proven == nil || len(c.proven) >= comparedContextsMax {
		c.proven = map[[sha256.Size]byte]bool{}
	}
	c.proven[context] = same
}

// The marker rules bracket the stand-in so it can be found in the serialised
// document. They are ordinary rules with the built-in DIRECT target: every pass
// accepts them and none of them changes anything for it. ".invalid" is reserved
// and never resolves, though the markers never reach a client anyway.
const (
	ruleBlockBegin = "DOMAIN,rules-common-begin.psp.invalid,DIRECT"
	ruleBlockEnd   = "DOMAIN,rules-common-end.psp.invalid,DIRECT"
	// ruleBlockGroupMarker stands for the block in the text group discovery
	// reads. It is matched as a whole rule, never parsed for a target.
	ruleBlockGroupMarker = "MATCH,rules-common.psp.invalid"
	// yaml.Marshal indents a block sequence under a top-level key by four.
	ruleBlockEmitIndent = "    "
)

// analyseRuleBlock reduces shared rule text placed at the given indentation, or
// returns nil when the text is not of the shape the reduction is exact for.
//
// The accepted shape is what the rule editor and the shipped rule set produce:
// a block sequence at column 0, one rule per line, with whole-line comments
// between rules. Everything is decided by running the real parser and the real
// serialiser over the text, never by imitating them:
//
//   - the text must parse to one sequence of string scalars, one per "- " line;
//   - re-serialising it (what prepareRules does when no target is rewritten)
//     must give one line per rule again, with comments only between rules;
//   - parsing those lines inside a document and serialising that document (what
//     finishMihomo does) must reproduce them unchanged, four spaces in.
//
// The last step is the proof that the lines spliced into a finished document
// are the lines the full pipeline would have written there.
func analyseRuleBlock(shared, indent string) *ruleBlock {
	// A tab, a carriage return or a placeholder makes the text depend on how the
	// template is composed around it; leave those to the full pipeline.
	if strings.ContainsAny(shared, "\t\r") || strings.Contains(shared, "{{") {
		return nil
	}
	dashLines := 0
	for _, line := range strings.Split(strings.TrimRight(shared, "\n"), "\n") {
		switch {
		case line == "" || strings.HasPrefix(line, "#"):
		case strings.HasPrefix(line, "- "):
			dashLines++
		default:
			return nil // Indented, continued, flow or multi-document text.
		}
		if isNodeRefLine(indent, line) {
			return nil // expandNodeRefs would replace the line.
		}
	}
	var document yaml.Node
	if err := yaml.Unmarshal([]byte(shared), &document); err != nil {
		return nil
	}
	if len(document.Content) != 1 || hasYAMLComment(&document) {
		return nil
	}
	sequence := document.Content[0]
	if sequence.Kind != yaml.SequenceNode || sequence.Style != 0 || sequence.Anchor != "" || hasYAMLComment(sequence) || len(sequence.Content) != dashLines || dashLines == 0 {
		return nil
	}
	values := make([]string, len(sequence.Content))
	for index, rule := range sequence.Content {
		if rule.Kind != yaml.ScalarNode || rule.Tag != "!!str" || rule.Anchor != "" || rule.Style&^(yaml.DoubleQuotedStyle|yaml.SingleQuotedStyle) != 0 {
			return nil
		}
		// A comment may only sit on its own lines directly above a rule. One
		// above the first rule would be claimed by whatever precedes the block.
		if rule.LineComment != "" || rule.FootComment != "" || index == 0 && rule.HeadComment != "" {
			return nil
		}
		// Every rule must be one the reference checker accepts; a block with a
		// rule it rejects fails in the full pipeline, with its error.
		if strings.ContainsAny(rule.Value, "\n\r") || len(splitRuleFields(rule.Value)) < 2 {
			return nil
		}
		values[index] = rule.Value
	}

	// What prepareRules returns for this text when it rewrites no target.
	raw, err := yaml.Marshal(&document)
	if err != nil {
		return nil
	}
	prepared := strings.Split(strings.TrimRight(string(raw), "\n"), "\n")
	ruleLines := make([]string, 0, len(values))
	for _, line := range prepared {
		switch {
		case strings.HasPrefix(line, "- "):
			ruleLines = append(ruleLines, line)
		case strings.HasPrefix(line, "#"):
		default:
			return nil
		}
		if strings.Contains(line, "{{") || isNodeRefLine(indent, line) {
			return nil
		}
	}
	if len(ruleLines) != len(values) || !strings.HasPrefix(prepared[0], "- ") || !strings.HasPrefix(prepared[len(prepared)-1], "- ") {
		return nil
	}

	// The second half of the round trip, in a document of its own: the lines as
	// a template would carry them, between the two markers.
	framed := append(append([]string{"- " + ruleBlockBegin}, prepared...), "- "+ruleBlockEnd)
	var composed, want strings.Builder
	composed.WriteString("rules:\n")
	want.WriteString("rules:\n")
	for _, line := range framed {
		composed.WriteString(indent + line + "\n")
		want.WriteString(ruleBlockEmitIndent + line + "\n")
	}
	parsed, err := parseMihomoSubscription(composed.String())
	if err != nil {
		return nil
	}
	rules, err := referenceSequence(referenceField(parsed.Content[0], "rules"))
	if err != nil || len(rules) != len(values)+2 {
		return nil
	}
	for index, value := range values {
		if rule := rules[index+1]; rule.Kind != yaml.ScalarNode || rule.Tag != "!!str" || rule.Value != value {
			return nil
		}
	}
	serialised, err := yaml.Marshal(parsed)
	if err != nil || string(serialised) != want.String() {
		return nil
	}

	block := &ruleBlock{}
	representative := make([]bool, len(values))
	seenTargets, seenSubRules := map[string]bool{}, map[string]bool{}
	for index, value := range values {
		// The same two readings the passes take of a rule: its outbound target
		// (line names, pruning, groups) and its sub-rule reference (validation).
		if target := mihomoRuleTarget(value); target != "" && !seenTargets[target] {
			seenTargets[target] = true
			block.targets = append(block.targets, target)
			representative[index] = true
		}
		if reference := subRuleReference(splitRuleFields(value)); reference != "" && !seenSubRules[reference] {
			seenSubRules[reference] = true
			representative[index] = true
		}
	}
	standIn := []string{"- " + ruleBlockBegin}
	for index, line := range ruleLines {
		if representative[index] {
			standIn = append(standIn, line)
		}
	}
	standIn = append(standIn, "- "+ruleBlockEnd)
	block.standIn = strings.Join(standIn, "\n")
	block.standInRules = len(standIn)
	block.emittedStandIn = ruleBlockEmitIndent + strings.Join(standIn, "\n"+ruleBlockEmitIndent) + "\n"
	block.emitted = ruleBlockEmitIndent + strings.Join(prepared, "\n"+ruleBlockEmitIndent) + "\n"
	return block
}

func hasYAMLComment(node *yaml.Node) bool {
	return node.HeadComment != "" || node.LineComment != "" || node.FootComment != ""
}

// isNodeRefLine reports whether expandNodeRefs would treat the line, placed at
// the given indentation, as a node-set reference and replace it.
func isNodeRefLine(indent, line string) bool {
	return strings.Contains(line, "@") && inlineNodeRefRE.MatchString(indent+line)
}

// standsInRules reports whether the stand-in landed where a rule block belongs:
// as consecutive items directly in the document's top-level `rules` list.
func (b *ruleBlock) standsInRules(document *yaml.Node) bool {
	root := document.Content[0]
	for index := 0; index+1 < len(root.Content); index += 2 {
		if root.Content[index].Value != "rules" {
			continue
		}
		rules := root.Content[index+1]
		if rules.Kind != yaml.SequenceNode {
			return false
		}
		for first, rule := range rules.Content {
			if rule.Kind == yaml.ScalarNode && rule.Value == ruleBlockBegin {
				last := first + b.standInRules - 1
				return last < len(rules.Content) && rules.Content[last].Kind == yaml.ScalarNode && rules.Content[last].Value == ruleBlockEnd
			}
		}
		return false
	}
	return false
}

// splice puts the whole block where the serialised document carries the
// stand-in. It refuses unless the stand-in is there exactly once and exactly as
// analysed: a rewritten representative, a copy made through a YAML alias, a
// comment attached to a marker or a different nesting depth all fail the match.
func (b *ruleBlock) splice(document string) (string, bool) {
	if strings.Count(document, ruleBlockBegin) != 1 || strings.Count(document, ruleBlockEnd) != 1 {
		return "", false
	}
	at := strings.Index(document, b.emittedStandIn)
	if at < 1 || document[at-1] != '\n' {
		return "", false
	}
	return document[:at] + b.emitted + document[at+len(b.emittedStandIn):], true
}

// ruleBlockCacheEntries bounds the analysed blocks kept. An entry pins the rule
// text it was made from and its serialised form, about 1.1 MB for the shipped
// rule set. A deployment has one block per distinct combination of bound rule
// sets and subscription domain, usually one or two.
const ruleBlockCacheEntries = 8

type ruleBlockKey struct {
	shared, indent string
}

type ruleBlockEntry[T any] struct {
	once  sync.Once
	block *T
	used  uint64
}

// ruleBlockCache keeps analysed rule blocks across renders, keyed by the rule
// text itself, so an edited rule set is simply a different key and nothing has
// to invalidate. It holds nothing about any subscriber. The zero value is ready
// to use: tests build a Service as a literal.
type ruleBlockCache[T any] struct {
	mu      sync.Mutex
	entries map[ruleBlockKey]*ruleBlockEntry[T]
	clock   uint64
}

// get returns the analysed block for the text, or nil when the text does not
// qualify. A refusal is cached as well: it costs as much to reach as a result.
// Concurrent first renders of one text analyse it once.
func (c *ruleBlockCache[T]) get(shared, indent string, analyse func(shared, indent string) *T) *T {
	key := ruleBlockKey{shared, indent}
	c.mu.Lock()
	if c.entries == nil {
		c.entries = map[ruleBlockKey]*ruleBlockEntry[T]{}
	}
	entry := c.entries[key]
	if entry == nil {
		if len(c.entries) >= ruleBlockCacheEntries {
			var oldest ruleBlockKey
			first := true
			for candidate, existing := range c.entries {
				if first || existing.used < c.entries[oldest].used {
					oldest, first = candidate, false
				}
			}
			delete(c.entries, oldest)
		}
		entry = &ruleBlockEntry[T]{}
		c.entries[key] = entry
	}
	c.clock++
	entry.used = c.clock
	c.mu.Unlock()
	entry.once.Do(func() { entry.block = analyse(shared, indent) })
	return entry.block
}
