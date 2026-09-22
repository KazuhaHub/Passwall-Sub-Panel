package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	nodeprotocol "github.com/KazuhaHub/passwall-protocol/protocol"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/compatadmission"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
	"github.com/KazuhaHub/passwall-sub-panel/internal/version"
)

// The three components fail for unrelated reasons, so each answer is its own
// decision. A single "upgradable" flag would let a confident answer for one
// component hide an honest refusal for another.
func TestThePanelAnswerSaysManualWhenTheTargetCannotBePinned(t *testing.T) {
	// 3X-UI's /updatePanel takes no version argument. The upgrade is possible and
	// the target is a version PSP read a moment ago — so this is a hand-off to
	// the operator, not a managed upgrade to a known version.
	option := decidePanelUpgrade(true, &ports.PanelUpdateInfo{
		CurrentVersion: "3.5.1", LatestVersion: "v3.6.0", UpdateAvailable: true,
	}, version.CompatSupported)
	if option.State != upgradeManualOnly {
		t.Fatalf("state = %q, want manual_only", option.State)
	}
	if option.TargetPinnable {
		t.Fatal("a target /updatePanel cannot be held to was reported as pinnable")
	}
	if !hasReason(option, "target_not_pinnable") {
		t.Fatalf("reasons = %v", option.ReasonCodes)
	}
}

func TestThePanelAnswerCarriesTheCompatibilityRefusal(t *testing.T) {
	option := decidePanelUpgrade(true, &ports.PanelUpdateInfo{
		CurrentVersion: "3.5.1", LatestVersion: "v3.0.0", UpdateAvailable: true,
	}, version.CompatTooOld)
	if !hasReason(option, "target_too_old") {
		t.Fatalf("reasons = %v, want the compatibility refusal named", option.ReasonCodes)
	}
}

func TestAPanelWithNoUpdaterIsUnsupportedRatherThanBlocked(t *testing.T) {
	// Unsupported and blocked are different answers: one means this backend has
	// no such operation, the other that the operation exists and is refused.
	for _, option := range []upgradeOption{
		decidePanelUpgrade(false, nil, version.CompatUnknown),
		decideCoreUpgrade(false, "26.9.9"),
	} {
		if option.State != upgradeUnsupported {
			t.Errorf("%s: state = %q, want unsupported", option.Component, option.State)
		}
		if !hasReason(option, "capability_missing") {
			t.Errorf("%s: reasons = %v", option.Component, option.ReasonCodes)
		}
	}
}

func TestAnAlreadyLatestPanelIsReadyAndPinned(t *testing.T) {
	// Nothing to do, and saying so is the useful answer — not a refusal.
	option := decidePanelUpgrade(true, &ports.PanelUpdateInfo{
		CurrentVersion: "3.6.0", LatestVersion: "v3.6.0", UpdateAvailable: false,
	}, version.CompatSupported)
	if option.State != upgradeReady || !hasReason(option, "already_latest") {
		t.Fatalf("option = %+v", option)
	}
}

// The core CAN be told an exact version, so its path is managed rather than
// handed off — the opposite of the panel, and the reason the two answers are
// computed separately.
func TestTheCoreAnswerIsPinnable(t *testing.T) {
	option := decideCoreUpgrade(true, "26.9.9")
	if option.State != upgradeReady || !option.TargetPinnable {
		t.Fatalf("option = %+v", option)
	}
}

func TestTheAgentAnswerNeedsAnIdentityAndAnOfferedTarget(t *testing.T) {
	for _, tc := range []struct {
		name    string
		current string
		state   upgradeOptionState
		reason  string
	}{
		{"no identity", "", upgradeBlocked, "identity_unknown"},
		// Identity is the ONLY thing this refuses on. There used to be two more
		// reasons here — "nobody walked this path" and "the policy does not offer
		// it" — and each was a second document's opinion about a question the
		// operator is entitled to answer.
		{"a version this panel cannot parse", "v0.0.1-beta3", upgradeReady, "compatible"},
		{"the current version", "4.0.1", upgradeReady, "compatible"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			option := decideAgentUpgrade(tc.current)
			if option.State != tc.state {
				t.Fatalf("state = %q, want %q", option.State, tc.state)
			}
			if !hasReason(option, tc.reason) {
				t.Fatalf("reasons = %v, want %q", option.ReasonCodes, tc.reason)
			}
		})
	}
}

func hasReason(option upgradeOption, want string) bool {
	for _, reason := range option.ReasonCodes {
		if reason == want {
			return true
		}
	}
	return false
}

func upgradeOptionsRequest(t *testing.T, client ports.XUIClient, query string) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	h := &AdminServersHandler{
		repo: upgradeModeRepo{panel: &domain.XUIPanel{ID: 7, Kind: domain.PanelKind3XUI, PanelVersion: "3.5.1"}},
		pool: fakeWebCertPool{client: client},
	}
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Params = gin.Params{{Key: "id", Value: "7"}}
	c.Request = httptest.NewRequest(http.MethodGet, "/admin/servers/7/upgrade-options?"+query, nil)
	h.UpgradeOptions(c)
	return recorder
}

func decodeOption(t *testing.T, recorder *httptest.ResponseRecorder) upgradeOption {
	t.Helper()
	var option upgradeOption
	if err := json.Unmarshal(recorder.Body.Bytes(), &option); err != nil {
		t.Fatalf("decode %s: %v", recorder.Body.String(), err)
	}
	return option
}

// The component is required, not defaulted. A default would answer one question
// while the caller asked another, and the three answers are not interchangeable.
func TestUpgradeOptionsRequiresAComponent(t *testing.T) {
	recorder := upgradeOptionsRequest(t, &unpinnableUpgradeClient{}, "")
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("response = %d %s", recorder.Code, recorder.Body.String())
	}
	if !strings.Contains(recorder.Body.String(), "component_required") {
		t.Fatalf("body = %s", recorder.Body.String())
	}
}

func TestUpgradeOptionsReportsManualOnlyForAThreeXUIPanel(t *testing.T) {
	client := &unpinnableUpgradeClient{info: &ports.PanelUpdateInfo{
		CurrentVersion: "3.5.1", LatestVersion: "v3.6.0", UpdateAvailable: true,
	}}
	recorder := upgradeOptionsRequest(t, client, "component=panel")
	if recorder.Code != http.StatusOK {
		t.Fatalf("response = %d %s", recorder.Code, recorder.Body.String())
	}
	option := decodeOption(t, recorder)
	if option.State != upgradeManualOnly || option.TargetVersion != "v3.6.0" {
		t.Fatalf("option = %+v", option)
	}
}

// A backend with no upgrade capability answers unsupported, and the answer comes
// from the endpoint rather than from a hidden button.
func TestUpgradeOptionsReportsUnsupportedForABackendWithoutTheCapability(t *testing.T) {
	for _, component := range []string{"panel", "core"} {
		recorder := upgradeOptionsRequest(t, &noUpgradeClient{}, "component="+component)
		option := decodeOption(t, recorder)
		if option.State != upgradeUnsupported {
			t.Fatalf("%s: option = %+v", component, option)
		}
	}
}

// THE LIST IS THE RELEASES THE PANEL CAN SEE, AND NOTHING ELSE IS FILTERED.
//
// It was built from the verified edges, then from a signed policy's offered set,
// and each filter could empty the dialog for a node the operator was looking at.
// What is left is the panel's own answer about what is published.
func TestAgentTargetsComeFromTheReleaseList(t *testing.T) {
	releases := []string{"4.0.0", "4.0.1", "4.0.2", "4.0.2"}

	targets := agentTargets("4.0.0", releases)
	// The node's own version is not a target, and the catalog may name one release
	// twice through two platforms.
	if len(targets) != 2 {
		t.Fatalf("targets = %+v, want the two releases that are not the node's own", targets)
	}
	for _, target := range targets {
		if target.Version == "4.0.0" {
			t.Fatalf("the node's own version was offered as a target: %+v", target)
		}
	}

	// A VERSION NO EDGE EVER STARTED FROM IS NO LONGER A DEAD END. A node on a
	// stamp from the replaced scheme is the case this exists for: it had no edge,
	// and it must still get the whole list.
	fromLegacy := agentTargets("v0.0.1-beta9", releases)
	if len(fromLegacy) != 3 {
		t.Fatalf("targets = %+v, want all three releases", fromLegacy)
	}

	// An unknown identity cannot be the start of anything.
	if got := agentTargets("", releases); got != nil {
		t.Fatalf("targets = %+v, want nil", got)
	}

	// A REPORTED IDENTITY CARRYING A COMMIT IS STILL THAT VERSION. A node built
	// from a known commit reports "4.0.0 (abc1234)", and comparing the whole
	// string offered it the exact release it is already running — which the write
	// path then refuses as a no-op, after the operator had picked it from a list
	// that showed it.
	withCommit := agentTargets("4.0.0 (abc1234)", releases)
	if len(withCommit) != 2 {
		t.Fatalf("targets = %+v, want the two releases that are not the node's own", withCommit)
	}
	for _, target := range withCommit {
		if target.Version == "4.0.0" {
			t.Fatalf("a node reporting a commit was offered its own version: %+v", target)
		}
	}
}

// THE READ PATH MUST ANSWER WHAT THE WRITE PATH WILL ANSWER.
//
// decideAgentUpgrade above refuses on identity alone, which is correct for what
// it knows; the option as a whole has to know more. A node that reports a version
// but cannot be upgraded at all was told "ready / compatible" here, offered the
// full release list, and refused by nodeagentupgrade.Request the moment the
// operator chose from it. ADR 0033 and R09 name exactly that as the failure: the
// list offering an action the service will not perform.
//
// The commonest instance is not exotic. A Passwall-Node whose own compiled
// version is not a canonical release never constructs an upgrade client
// (cmd/node/upgrade_linux.go's remoteUpgradeEnabled requires
// releaseid.ValidVersion), so it never registers the handler and never advertises
// task.agent.upgrade.v1 — and compatadmission does not allow that capability to
// be forced.
func TestTheAgentAnswerRefusesWhatTheUpgradeServiceWouldRefuse(t *testing.T) {
	gin.SetMode(gin.TestMode)
	observed := time.Now().UTC()
	panel := &domain.XUIPanel{ID: 7, Kind: domain.PanelKindPSP, PanelVersion: "4.0.1"}

	answer := func(agent *domain.NodeAgent) upgradeOption {
		t.Helper()
		h := &AdminServersHandler{
			repo:   upgradeModeRepo{panel: panel},
			agents: nodeMetricsAgentRepo{agent: agent},
		}
		recorder := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(recorder)
		c.Params = gin.Params{{Key: "id", Value: "7"}}
		c.Request = httptest.NewRequest(http.MethodGet, "/admin/servers/7/upgrade-options?component=agent", nil)
		return h.decideAgentOption(c, 7)
	}

	// A node that reports a version but advertises no upgrade capability. This is
	// the shape a legacy-scheme or dev build takes on the wire.
	withoutCapability := answer(&domain.NodeAgent{
		ObservedProtocolVersion: nodeprotocol.ProtocolVersion1,
		ObservedCapabilities:    []string{nodeprotocol.CapabilityTaskExecutionV1},
		ProtocolObservedAt:      &observed,
	})
	if withoutCapability.State != upgradeBlocked {
		t.Fatalf("state = %q, want %q: the write path refuses this node", withoutCapability.State, upgradeBlocked)
	}
	if !hasReason(withoutCapability, string(compatadmission.ReasonCapabilityMissing)) {
		t.Fatalf("reasons = %v, want the machine-readable refusal", withoutCapability.ReasonCodes)
	}
	if withoutCapability.Detail == "" {
		t.Fatal("a refusal an operator cannot read is not actionable")
	}
	if withoutCapability.TargetPinnable {
		t.Fatal("a blocked answer must not claim the target can be pinned")
	}

	// A node the write path would accept is still ready, and still gets its list.
	ready := answer(&domain.NodeAgent{
		ObservedProtocolVersion: nodeprotocol.ProtocolVersion1,
		ObservedCapabilities:    nodeprotocol.AgentUpgradeCapabilities(),
		ProtocolObservedAt:      &observed,
	})
	if ready.State != upgradeReady || !hasReason(ready, "compatible") {
		t.Fatalf("a capable node was refused: %+v", ready)
	}

	// No agent record at all leaves the identity-only answer alone rather than
	// inventing a refusal: that is a different unknown, and decideAgentUpgrade
	// already has an opinion about it.
	if none := answer(nil); none.State != upgradeReady {
		t.Fatalf("state = %q, want the identity-only answer when nothing is known", none.State)
	}
}
