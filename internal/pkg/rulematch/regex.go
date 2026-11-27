package rulematch

import (
	"regexp"
	"strconv"
	"strings"
	"sync"
)

// Mihomo compiles DOMAIN-REGEX with regexp2 (a .NET-style engine) and
// IgnoreCase (rules/common/domain_regex.go). Go's regexp is RE2, which reads
// many constructs differently, so a pattern is only evaluated when it stays in
// the subset both engines read the same way: literals, ".", "^", "$", character
// classes (no nesting, no empty class), the escapes \d \w \s \D \W \S and
// escaped punctuation, the quantifiers * + ? {n} {n,} {n,m} (greedy only),
// alternation, and plain or (?: groups. Everything else is refused, so the
// answer is Unknown instead of a wrong No. RE2 runs in linear time, so none of
// the browser matcher's backtracking limits are needed here.
const regexPunct = ".^$*+?()[]{}|\\/-"

var (
	regexMu    sync.Mutex
	regexCache = map[string]*regexp.Regexp{}
)

func regexFor(src string) *regexp.Regexp {
	regexMu.Lock()
	defer regexMu.Unlock()
	if re, ok := regexCache[src]; ok {
		return re
	}
	if len(regexCache) > 5000 {
		regexCache = map[string]*regexp.Regexp{}
	}
	re := safeRegex(src)
	regexCache[src] = re
	return re
}

func isClassEscape(c byte) bool { return strings.IndexByte("dwsDWS", c) >= 0 }

func escapeOK(src string, i int) bool {
	return i < len(src) && (isClassEscape(src[i]) || strings.IndexByte(regexPunct, src[i]) >= 0)
}

// safeRegex compiles src with (?i) when it is inside the shared subset.
func safeRegex(src string) *regexp.Regexp {
	if src == "" || len(src) > 200 {
		return nil
	}
	n := len(src)
	depth := 0
	atom := false // the previous token can take a quantifier
	for i := 0; i < n; {
		ch := src[i]
		switch {
		case ch == '\\':
			if !escapeOK(src, i+1) {
				return nil
			}
			i += 2
			atom = true
		case ch == '[':
			i++
			if i < n && src[i] == '^' {
				i++
			}
			members := 0
			prevClass := false // a range whose end is an escape class does not compile in regexp2
			for i < n && src[i] != ']' {
				if src[i] == '[' {
					return nil
				}
				if src[i] == '-' && members > 0 && i+1 < n && src[i+1] != ']' {
					if prevClass || (src[i+1] == '\\' && i+2 < n && isClassEscape(src[i+2])) {
						return nil
					}
				}
				if src[i] == '\\' {
					if !escapeOK(src, i+1) {
						return nil
					}
					prevClass = isClassEscape(src[i+1])
					i += 2
				} else {
					prevClass = false
					i++
				}
				members++
			}
			if i >= n || members == 0 {
				return nil
			}
			i++
			atom = true
		case ch == '(':
			if i+1 < n && src[i+1] == '?' {
				if i+2 >= n || src[i+2] != ':' {
					return nil
				}
				i += 3
			} else {
				i++
			}
			depth++
			atom = false
		case ch == ')':
			if depth == 0 {
				return nil
			}
			depth--
			i++
			atom = true
		case ch == '|':
			i++
			atom = false
		case ch == '*' || ch == '+' || ch == '?':
			if !atom {
				return nil
			}
			i++
			atom = false
		case ch == '{':
			j := i + 1
			lo := j
			for j < n && src[j] >= '0' && src[j] <= '9' {
				j++
			}
			if j == lo || j-lo > 4 || !atom {
				return nil
			}
			min, _ := strconv.Atoi(src[lo:j])
			if j < n && src[j] == ',' {
				j++
				hi := j
				for j < n && src[j] >= '0' && src[j] <= '9' {
					j++
				}
				if j-hi > 4 {
					return nil
				}
				if j > hi {
					if max, _ := strconv.Atoi(src[hi:j]); max < min {
						return nil
					}
				}
			}
			if j >= n || src[j] != '}' {
				return nil
			}
			i = j + 1
			atom = false
		case ch == '}':
			return nil
		case ch == '^' || ch == '$':
			i++
			atom = false
		default:
			i++
			atom = true
		}
	}
	if depth != 0 {
		return nil
	}
	re, err := regexp.Compile("(?i)" + src)
	if err != nil {
		return nil
	}
	return re
}
