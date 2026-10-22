package version

import (
	"fmt"
	"strings"
)

// ReleaseStage separates immutable candidate stages from final approval.
func ReleaseStage(v string, prerelease bool) string {
	if !validCandidateVersion(v) {
		return "unknown"
	}
	if strings.Contains(v, "-beta.") {
		return "beta"
	}
	if strings.Contains(v, "-rc.") {
		return "rc"
	}
	if prerelease {
		if len(strings.Split(v, ".")) == 3 && orderCandidate(v, "4.0.2") >= 0 {
			return "pending"
		}
		return "beta"
	}
	return "stable"
}

// ReleaseAliases lists eligible rolling pointers. Plain artifacts are staged
// without pointers until final acceptance and explicit stable approval.
func ReleaseAliases(v string, approved bool) []string {
	switch ReleaseStage(v, !approved) {
	case "beta":
		return []string{"beta", "testing"}
	case "rc":
		return []string{"rc", "beta", "testing"}
	case "stable":
		if approved {
			return []string{"stable", "latest", "rc", "beta", "testing"}
		}
	}
	return nil
}

// AdvanceRelease keeps the base version and advances a published candidate.
// Existing final tags are resolved by the caller, which must check their source.
func AdvanceRelease(source, stage string, tags []string) (string, error) {
	v, ok := readCandidateTag(source)
	if !ok || !strings.HasPrefix(source, "v") || !strings.Contains(v, "-") {
		return "", fmt.Errorf("source must be a numbered beta or rc release tag")
	}
	base, pre, _ := strings.Cut(v, "-")
	if len(strings.Split(base, ".")) != 3 {
		return "", fmt.Errorf("source must have three segments")
	}
	for _, tag := range tags {
		other, valid := readCandidateTag(tag)
		if valid && strings.HasPrefix(other, base+"-") && orderCandidate(other, v) > 0 {
			return "", fmt.Errorf("source is superseded by %s; advance the newest candidate", tag)
		}
	}
	if stage == "stable" {
		return base, nil
	}
	if stage != "rc" || !strings.HasPrefix(pre, "beta.") {
		return "", fmt.Errorf("only beta to rc, beta to stable and rc to stable are allowed")
	}
	for _, tag := range tags {
		other, valid := readCandidateTag(tag)
		if valid && (other == base || strings.HasPrefix(other, base+".")) {
			return "", fmt.Errorf("target already finalized")
		}
	}
	return base + "-rc.1", nil
}
