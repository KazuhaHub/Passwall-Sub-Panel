package version

import (
	"fmt"
	"strconv"
	"strings"
)

const candidateMax = int64(MaxVersionSegmentBounds)

func readCandidateTag(tag string) (string, bool) { return VersionOfReleaseTag(tag) }
func validCandidateVersion(v string) bool        { return IsReleaseVersion(v) }
func orderCandidate(a, b string) int             { return CompareRelease(a, b) }

// AllocateCandidate allocates on an explicitly selected release line. A new
// line starts at patch zero; a stable or historical release advances the patch;
// another beta/rc increments its own counter. A target pins a feature release.
func AllocateCandidate(line Line, stage, target string, existing []string) (string, error) {
	if line.Major < 1 || line.Minor < 0 || int64(line.Major) > candidateMax || int64(line.Minor) > candidateMax {
		return "", fmt.Errorf("invalid release line")
	}
	if stage != "beta" && stage != "rc" {
		return "", fmt.Errorf("stage must be beta or rc")
	}
	prefix := line.String() + "."
	var highest string
	for _, tag := range existing {
		v, ok := readCandidateTag(tag)
		if ok && strings.HasPrefix(v, prefix) && (highest == "" || orderCandidate(v, highest) > 0) {
			highest = v
		}
	}
	base := target
	if base != "" {
		if !validCandidateVersion(base) || strings.Contains(base, "-") || len(strings.Split(base, ".")) != 3 || !strings.HasPrefix(base, prefix) {
			return "", fmt.Errorf("target must be a three-segment version on line %s", line)
		}
	} else if highest == "" {
		base = prefix + "0"
	} else {
		numeric, pre, _ := strings.Cut(highest, "-")
		fields := strings.Split(numeric, ".")
		if pre != "" {
			base = numeric
		} else {
			patch, _ := strconv.ParseInt(fields[2], 10, 64)
			if patch >= candidateMax {
				return "", fmt.Errorf("patch exhausted")
			}
			base = prefix + strconv.FormatInt(patch+1, 10)
		}
	}
	next := int64(1)
	for _, tag := range existing {
		v, ok := readCandidateTag(tag)
		if !ok {
			continue
		}
		if v == base || strings.HasPrefix(v, base+".") {
			return "", fmt.Errorf("%s is already released; select a newer target", base)
		}
		pre, found := strings.CutPrefix(v, base+"-")
		if !found {
			continue
		}
		name, number, _ := strings.Cut(pre, ".")
		if name == "rc" && stage == "beta" {
			return "", fmt.Errorf("%s already reached rc; continue rc or select a newer target", base)
		}
		if name == stage {
			n, _ := strconv.ParseInt(number, 10, 64)
			if n >= next {
				next = n + 1
			}
		}
	}
	if next > candidateMax {
		return "", fmt.Errorf("prerelease counter exhausted")
	}
	result := base + "-" + stage + "." + strconv.FormatInt(next, 10)
	if highest != "" && orderCandidate(result, highest) <= 0 {
		return "", fmt.Errorf("candidate %s does not advance line %s past %s", result, line, highest)
	}
	return result, nil
}
