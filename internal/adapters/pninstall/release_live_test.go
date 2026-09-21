package pninstall

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
	"github.com/KazuhaHub/passwall-sub-panel/internal/version"
)

// A PUBLISHED RELEASE IS THE ONLY THING THAT PROVES PUBLISHING WORKS.
//
// The fixture cases prove the reader behaves — that it verifies, substitutes and
// refuses. They cannot prove that a release carries what the reader looks for,
// under the name it looks for, signed by the key it holds. That is a property of
// the RELEASE, and it is the one an operator depends on, because this is the script
// they hand to a host.
//
// SET PSP_LIVE_NODE_RELEASE TO A PUBLISHED TAG, for example `release/4.0.1.2` or
// `v4.0.1.3`. The production renderer is used — the compiled verification key and
// the real origin — so what runs here is the path an installation takes, not a
// rehearsal of it.
func TestLivePublishedReleasePublishesAnInstallationScript(t *testing.T) {
	tag := strings.TrimSpace(os.Getenv("PSP_LIVE_NODE_RELEASE"))
	if tag == "" {
		t.Skip("set PSP_LIVE_NODE_RELEASE to a published release tag to check the real release")
	}
	releaseVersion, ok := version.VersionOfReleaseTag(tag)
	if !ok {
		t.Fatalf("%q is not a release tag this build can read", tag)
	}
	renderer, err := New(RendererOptions{})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	script, err := renderer.Render(ctx, Options{
		Endpoint:   "https://panel.example/v1/node/sync",
		AgentID:    "agt_live_release_check",
		Credential: "pspn_" + strings.Repeat("a", 40),
		Version:    releaseVersion,
		// THE TAG IS HANDED OVER, NOT RE-DERIVED, and this is the case that shows
		// why: the env var may name a release published before the address changed,
		// whose derived address is a tag nobody published. A caller that has the
		// address passes it; the assertion below is what checks that the script then
		// carries the one this release is really at.
		Tag: tag,
	})
	if err != nil {
		t.Fatalf("the published release does not serve an installation script: %v", err)
	}
	// THE TWO IDENTITIES, which is why the template was worth publishing at all: the
	// download path carries the TAG and the archive name carries the VERSION, and a
	// release addressed by only one of them is a release that cannot be installed.
	//
	// THE ASSERTION IS TWO HALVES, and the split is the point. The VALUES are what
	// this render produced and must be exactly this release's; the EXPRESSIONS are
	// the template's own addressing, which builds the URL at run time — so a check
	// for the expanded URL would fail on a correct script, which is how this case was
	// first written.
	for _, want := range []string{
		"version='" + releaseVersion + "'",
		"tag='" + tag + "'",
		"releases/download/${tag}",
		"passwall-node_${version}_linux_${arch}",
	} {
		if !strings.Contains(script, want) {
			t.Errorf("the rendered script does not contain %q, so it cannot install this release", want)
		}
	}
	// AND NOTHING IS LEFT UNFILLED — with one exception that is the template's own: a
	// line where it refuses to run when a marker survived, which necessarily mentions
	// the markers. Reading every `@@` as a missed substitution would fail on a correct
	// script.
	for _, line := range strings.Split(script, "\n") {
		if strings.Contains(line, "@@") && !strings.Contains(line, "*@@*") {
			t.Errorf("the rendered script still carries a placeholder: %s", strings.TrimSpace(line))
		}
	}
	if !strings.Contains(script, "PSP_NODE_AGENT_ID=") || !strings.Contains(script, "PSP_NODE_ENDPOINT=") {
		t.Error("the rendered script does not carry the connection environment file")
	}

	// AND THE SAME RELEASE CAN BE ASKED TO REPLACE A RELEASE IN PLACE.
	//
	// This is the half a release can be missing without anything else noticing: the
	// mode is a marker in the template, and a release published before it exists
	// substitutes nothing and installs where an upgrade was asked for. Checking it
	// here means the release that has to carry it is the thing under test.
	//
	// THE ADDRESS IS STATED, exactly as the other render and as the panel does: a
	// version no longer determines one, and deriving it here would fail this case
	// against every release published before the namespace changed — which are
	// precisely the releases this pair of checks exists to keep working.
	upgrade, err := renderer.Render(ctx, Options{
		Endpoint:   "https://panel.example/v1/node/sync",
		AgentID:    "agt_live_release_check",
		Credential: "pspn_" + strings.Repeat("a", 40),
		Version:    releaseVersion,
		Tag:        tag,
		Mode:       ports.ModeUpgrade,
	})
	// A RELEASE PUBLISHED BEFORE IN-PLACE UPGRADES EXISTED IS REFUSED, AND THAT IS THE
	// CORRECT ANSWER: the panel installs nothing when an upgrade was asked for, and the
	// operator is told to pick another release. What must never happen is the third
	// possibility — a script rendered for the install mode that says nothing about it
	// — and the adapter's own cases pin that refusal against a template without the
	// marker. So the live check accepts either answer and rejects a silent one.
	if errors.Is(err, ports.ErrInstallTemplateModeUnsupported) {
		t.Logf("the published release predates in-place upgrades; it is refused rather than installed over")
		return
	}
	if err != nil {
		t.Fatalf("the published release cannot be asked to replace a release in place: %v", err)
	}
	if !strings.Contains(upgrade, "mode='upgrade'") {
		t.Fatal("the published template rendered an in-place upgrade without the mode")
	}
	for _, line := range strings.Split(upgrade, "\n") {
		if strings.Contains(line, "@@") && !strings.Contains(line, "*@@*") {
			t.Errorf("the rendered upgrade script still carries a placeholder: %s", strings.TrimSpace(line))
		}
	}
}
