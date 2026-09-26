package seed

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestUpgradeManagedDefaultsUpdatesOnlyTheOfficialBundle(t *testing.T) {
	oldTemplate := []byte("old template\n")
	oldRules := []byte("old rules\n")
	olderRules := []byte("older rules\n")
	newTemplate := []byte("new template\n")
	newRules := []byte("new rules\n")
	updates := []managedDefaultUpdate{
		{relPath: "templates/default.yaml", oldSHA256s: []string{testSHA256(oldTemplate)}, newBody: newTemplate},
		{relPath: "rulesets/default.yaml", oldSHA256s: []string{testSHA256(olderRules), testSHA256(oldRules)}, newBody: newRules},
	}

	t.Run("both official defaults", func(t *testing.T) {
		dir := writeManagedTestFiles(t, oldTemplate, oldRules)
		if err := upgradeManagedDefaults(dir, updates); err != nil {
			t.Fatal(err)
		}
		assertManagedTestFile(t, dir, "templates/default.yaml", newTemplate)
		assertManagedTestFile(t, dir, "rulesets/default.yaml", newRules)
	})

	t.Run("earlier official defaults remain supported", func(t *testing.T) {
		dir := writeManagedTestFiles(t, oldTemplate, olderRules)
		if err := upgradeManagedDefaults(dir, updates); err != nil {
			t.Fatal(err)
		}
		assertManagedTestFile(t, dir, "templates/default.yaml", newTemplate)
		assertManagedTestFile(t, dir, "rulesets/default.yaml", newRules)
	})

	t.Run("one customized file preserves the whole bundle", func(t *testing.T) {
		dir := writeManagedTestFiles(t, []byte("administrator edit\n"), oldRules)
		if err := upgradeManagedDefaults(dir, updates); err != nil {
			t.Fatal(err)
		}
		assertManagedTestFile(t, dir, "templates/default.yaml", []byte("administrator edit\n"))
		assertManagedTestFile(t, dir, "rulesets/default.yaml", oldRules)
	})

	t.Run("interrupted update completes", func(t *testing.T) {
		dir := writeManagedTestFiles(t, newTemplate, oldRules)
		if err := upgradeManagedDefaults(dir, updates); err != nil {
			t.Fatal(err)
		}
		assertManagedTestFile(t, dir, "templates/default.yaml", newTemplate)
		assertManagedTestFile(t, dir, "rulesets/default.yaml", newRules)
	})
}

func TestRoutingDefaultMigrationSourcesExistAndChanged(t *testing.T) {
	checks := map[string][]string{
		"templates/default-mihomo.yaml": {
			"13cd9b7b8d29447f86fd46503536e15359e07116c302d3b5364a66e879a84c3c",
		},
		"rulesets/default-rules.yaml": {
			"01c4be93d1bb183336940faa8ed8ebf0f08110adee12327405ab659be282adbc",
			"81ca6e2e15c700478b8a15b59ef006f4f2b46043b587484b6b6238b7dee039c3",
			"caf6d32b70f2ae4a66b5879e409280caa3552a1121a8bc137164e1c32efa112d",
			"1eab50bef8b214380cceaabacf89d1a4ec5b223193b853a53466274fac73fbe2",
			"e97534ef1ca29916168f330ce70e422ceeecdd2c43b0be5aa76766d8608cd830",
		},
	}
	for relPath, oldHashes := range checks {
		body, err := defaultsFS.ReadFile("files/" + relPath)
		if err != nil {
			t.Fatalf("read %s: %v", relPath, err)
		}
		for _, oldHash := range oldHashes {
			if testSHA256(body) == oldHash {
				t.Fatalf("managed default %s still equals its legacy hash", relPath)
			}
		}
	}
}

func TestEnsureUpgradesPreviousIndependentRoutingDefaults(t *testing.T) {
	template, err := defaultsFS.ReadFile("files/templates/default-mihomo.yaml")
	if err != nil {
		t.Fatal(err)
	}
	rules, err := defaultsFS.ReadFile("files/rulesets/default-rules.yaml")
	if err != nil {
		t.Fatal(err)
	}
	currentRules := rules
	rules = []byte(strings.ReplaceAll(string(rules), "\r\n", "\n"))
	// The REJECT-QUIC / PASS-UDP defaults were replaced by DIRECT UDP with QUIC
	// following it. Only the explanatory comments differ in the file; the
	// selector members themselves come from the renderer.
	previousRejectQUICRules := []byte(strings.NewReplacer(
		"  # HTTP/3's usual transport (UDP/443) has its own runtime selector. It defaults\n"+
			"  # to the general 🎮 UDP控制 selector, so one switch governs all UDP; a\n"+
			"  # subscriber can still select the proxy, DIRECT, or REJECT (which lets\n"+
			"  # browsers fall back to TCP) for QUIC alone.\n",
		"  # HTTP/3's usual transport (UDP/443) has its own runtime selector. It defaults\n"+
			"  # to REJECT so browsers immediately fall back to TCP instead of stalling on\n"+
			"  # a slow UDP path; a subscriber can independently select the proxy or DIRECT.\n",
		"  # All remaining non-local UDP -> the 🎮 UDP控制 selector, default DIRECT.\n"+
			"  # DIRECT allows UDP without relying on the proxy node's UDP support; it\n"+
			"  # leaves from the local exit IP, which can differ from the TCP proxy exit.\n"+
			"  # PASS (Mihomo only) hands UDP to the service rules below instead.\n",
		"  # All remaining non-local UDP -> the 🎮 UDP控制 selector, default PASS.\n"+
			"  # PASS continues through the later domain/region rules, so each service keeps\n"+
			"  # its normal routing decision instead of all UDP being forced direct or proxy.\n",
	).Replace(string(rules)))
	if got := testSHA256(previousRejectQUICRules); got != "e97534ef1ca29916168f330ce70e422ceeecdd2c43b0be5aa76766d8608cd830" {
		t.Fatalf("previous reject-QUIC rules hash = %s", got)
	}
	previousBeta10Rules := []byte(strings.NewReplacer(
		"  # HTTP/3's usual transport (UDP/443) has its own runtime selector. It defaults\n"+
			"  # to REJECT so browsers immediately fall back to TCP instead of stalling on\n"+
			"  # a slow UDP path; a subscriber can independently select the proxy or DIRECT.\n",
		"  # HTTP/3's usual transport (UDP/443) has its own runtime selector. It defaults\n"+
			"  # to DIRECT so a slow UDP-capable proxy does not stall normal web browsing;\n"+
			"  # a subscriber can independently select the proxy or REJECT (which lets\n"+
			"  # browsers fall back to TCP).\n",
	).Replace(string(previousRejectQUICRules)))
	if got := testSHA256(previousBeta10Rules); got != "1eab50bef8b214380cceaabacf89d1a4ec5b223193b853a53466274fac73fbe2" {
		t.Fatalf("previous beta.10 rules hash = %s", got)
	}
	// Reconstruct prior official prefixes without copying the unchanged
	// 9,000-line tail. Their pinned hashes prove they are published defaults.
	previousDirectRules := []byte(strings.NewReplacer(
		"  # HTTP/3's usual transport (UDP/443) has its own runtime selector. It defaults\n"+
			"  # to DIRECT so a slow UDP-capable proxy does not stall normal web browsing;\n"+
			"  # a subscriber can independently select the proxy or REJECT (which lets\n"+
			"  # browsers fall back to TCP).\n",
		"  # HTTP/3's usual transport (UDP/443) has its own runtime selector. It defaults\n"+
			"  # to the general UDP selector (DIRECT), but a subscriber can independently force the\n"+
			"  # selected proxy, DIRECT, or REJECT (which lets browsers fall back to TCP).\n",
		"  # All remaining non-local UDP -> the 🎮 UDP控制 selector, default PASS.\n"+
			"  # PASS continues through the later domain/region rules, so each service keeps\n"+
			"  # its normal routing decision instead of all UDP being forced direct or proxy.\n",
		"  # All remaining non-local UDP -> the 🎮 UDP控制 selector, default DIRECT.\n"+
			"  # UDP stays enabled and uses the local exit IP even when the main node is a proxy.\n",
	).Replace(string(previousBeta10Rules)))
	if got := testSHA256(previousDirectRules); got != "caf6d32b70f2ae4a66b5879e409280caa3552a1121a8bc137164e1c32efa112d" {
		t.Fatalf("previous direct-default rules hash = %s", got)
	}
	previousBeta7Rules := []byte(strings.NewReplacer(
		"  - '🎮 UDP控制'\n  - '⚡ QUIC控制'\n", "  - '⚡ QUIC控制'\n  - '🎮 UDP控制'\n",
		"to the general UDP selector (DIRECT), but", "to the general UDP selector, but",
		"  # All remaining non-local UDP -> the 🎮 UDP控制 selector, default DIRECT.\n"+
			"  # UDP stays enabled and uses the local exit IP even when the main node is a proxy.\n"+
			"  # Group display order is UDP then QUIC; matching must remain QUIC then UDP.\n"+
			"  # Both rules\n",
		"  # All remaining non-local UDP -> the 🎮 UDP控制 selector. Both rules\n",
	).Replace(string(previousDirectRules)))
	if got := testSHA256(previousBeta7Rules); got != "81ca6e2e15c700478b8a15b59ef006f4f2b46043b587484b6b6238b7dee039c3" {
		t.Fatalf("previous beta.7 rules hash = %s", got)
	}
	for _, tc := range []struct {
		name              string
		sourceRules       []byte
		customizeTemplate bool
		customizeRules    bool
	}{
		{name: "untouched reject-QUIC defaults upgrade", sourceRules: previousRejectQUICRules},
		{name: "untouched beta.10 defaults upgrade", sourceRules: previousBeta10Rules},
		{name: "untouched direct defaults upgrade", sourceRules: previousDirectRules},
		{name: "untouched beta.7 defaults upgrade", sourceRules: previousBeta7Rules},
		{name: "customized template preserves whole bundle", sourceRules: previousRejectQUICRules, customizeTemplate: true},
		{name: "customized rules preserve whole bundle", sourceRules: previousRejectQUICRules, customizeRules: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			oldTemplate := append([]byte(nil), template...)
			oldRules := append([]byte(nil), tc.sourceRules...)
			if tc.customizeTemplate {
				oldTemplate = append(oldTemplate, []byte("# administrator customization\n")...)
			}
			if tc.customizeRules {
				oldRules = append(oldRules, []byte("# administrator customization\n")...)
			}
			for relPath, body := range map[string][]byte{
				"templates/default-mihomo.yaml": oldTemplate,
				"rulesets/default-rules.yaml":   oldRules,
			} {
				path := filepath.Join(dir, relPath)
				if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, body, 0o644); err != nil {
					t.Fatal(err)
				}
			}
			wantTemplate := template
			wantRules := currentRules
			if tc.customizeTemplate || tc.customizeRules {
				wantTemplate = oldTemplate
				wantRules = oldRules
			}
			// Ensure must be idempotent for both upgraded and customized bundles.
			for attempt := 0; attempt < 2; attempt++ {
				if err := Ensure(dir); err != nil {
					t.Fatal(err)
				}
				assertManagedTestFile(t, dir, "templates/default-mihomo.yaml", wantTemplate)
				assertManagedTestFile(t, dir, "rulesets/default-rules.yaml", wantRules)
			}
		})
	}
}

func TestEnsureUpgradesUnmodifiedDNSDefaults(t *testing.T) {
	const oldHost = "v5brh3pn84.cloudflare-gateway.com"
	const newHost = "l9f26nnn5d.cloudflare-gateway.com"
	const previousMihomoForeign = `      # Foreign domains -> org Cloudflare Gateway (primary; bare resolution
      # verified working — TCP ping + full DoH Status:0), public Cloudflare
      # 1.1.1.1 / 1.0.0.1 as backup. The CF IPs carry IP SANs so DoH-by-IP
      # validates cleanly; Google 8.8.8.8-by-IP is NOT used (its cert is
      # dns.google-only, no IP SAN -> would fail certificate validation).
      "geosite:geolocation-!cn":
      - https://l9f26nnn5d.cloudflare-gateway.com/dns-query
      - https://1.1.1.1/dns-query
      - https://1.0.0.1/dns-query`
	files := []struct {
		relPath     string
		oldLFHash   string
		oldCRLFHash string
	}{
		{"templates/default-mihomo.yaml", "f7e3a2784a67fcdf38ba580f51ba0ae577aac4bcfc1c72efc32c8c39ca4f3e4b", "426353853592fb9a17afe5cbe801c8c674fc9394ad052b45b3b6ea20789b093d"},
		{"templates/default-sing-box.yaml", "e73031f8844a9bc54922510ff209286f3fb24f709de448427fb32f0ebef5cf27", "6cfb82005a288b1b2a662cb91dd249337f0678b3ff0df4a10551b25f735439f8"},
	}
	for _, tc := range []struct {
		name                    string
		customized              string
		crlf                    bool
		previousGatewayHostOnly bool
	}{
		{"LF both untouched", "", false, false},
		{"CRLF both untouched", "", true, false},
		{"Gateway hostname only", "", false, true},
		{"customized Mihomo", files[0].relPath, false, false},
		{"customized Sing-box", files[1].relPath, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			wants := make(map[string][]byte)
			for _, file := range files {
				current, err := defaultsFS.ReadFile("files/" + file.relPath)
				if err != nil {
					t.Fatal(err)
				}
				previousText := strings.ReplaceAll(string(current), "\r\n", "\n")
				oldHash := file.oldLFHash
				if file.relPath == files[0].relPath {
					start := strings.Index(previousText, "      # Foreign domains ->")
					if start < 0 {
						t.Fatal("missing Mihomo foreign DNS policy")
					}
					end := strings.Index(previousText[start:], "\n\n  proxies:")
					if end < 0 {
						t.Fatal("missing Mihomo proxies block after DNS policy")
					}
					previousText = previousText[:start] + previousMihomoForeign + previousText[start+end:]
					if tc.previousGatewayHostOnly {
						oldHash = "fbe93907df14dcfe5a6d26207c7fbd5b717be1734491bbeb5aa6de66d636af47"
					}
				}
				if !tc.previousGatewayHostOnly || file.relPath != files[0].relPath {
					previousText = strings.Replace(previousText, newHost, oldHost, 1)
				}
				previous := []byte(previousText)
				if tc.crlf {
					previous = []byte(strings.ReplaceAll(string(previous), "\n", "\r\n"))
					if tc.previousGatewayHostOnly && file.relPath == files[0].relPath {
						oldHash = "802667bf6c7caf954dc431bbd926b045583333a570320172a082621a6a635038"
					} else {
						oldHash = file.oldCRLFHash
					}
				}
				if got := testSHA256(previous); got != oldHash {
					t.Fatalf("previous %s hash = %s", file.relPath, got)
				}
				want := current
				if file.relPath == tc.customized {
					previous = append(previous, []byte("# administrator customization\n")...)
					want = previous
				}
				wants[file.relPath] = want
				path := filepath.Join(dir, file.relPath)
				if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, previous, 0o644); err != nil {
					t.Fatal(err)
				}
			}
			for attempt := 0; attempt < 2; attempt++ {
				if err := Ensure(dir); err != nil {
					t.Fatal(err)
				}
				for relPath, want := range wants {
					assertManagedTestFile(t, dir, relPath, want)
				}
			}
		})
	}
}

func writeManagedTestFiles(t *testing.T, template, rules []byte) string {
	t.Helper()
	dir := t.TempDir()
	for relPath, body := range map[string][]byte{
		"templates/default.yaml": template,
		"rulesets/default.yaml":  rules,
	} {
		path := filepath.Join(dir, relPath)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, body, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func assertManagedTestFile(t *testing.T, dir, relPath string, want []byte) {
	t.Helper()
	got, err := os.ReadFile(filepath.Join(dir, relPath))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(want) {
		t.Fatalf("%s = %q, want %q", relPath, got, want)
	}
}

func testSHA256(body []byte) string {
	return fmt.Sprintf("%x", sha256.Sum256(body))
}
