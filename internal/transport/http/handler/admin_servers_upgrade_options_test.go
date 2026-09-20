package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
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

func TestTheAgentAnswerNeedsBothAnOfferedTargetAndAWalkedPath(t *testing.T) {
	for _, tc := range []struct {
		name    string
		current string
		offered bool
		edge    bool
		state   upgradeOptionState
		reason  string
	}{
		{"no identity", "", true, true, upgradeBlocked, "identity_unknown"},
		{"not offered", "v0.0.1-beta11", false, true, upgradeBlocked, "no_offered_target"},
		{"offered but never walked", "v0.0.1-beta3", true, false, upgradeBlocked, "upgrade_edge_unverified"},
		{"offered and walked", "v0.0.1-beta3", true, true, upgradeReady, "edge_verified"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			option := decideAgentUpgrade(tc.current, tc.offered, tc.edge)
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

// Filtering a release list by version alone offers releases that are ahead and
// that nobody has ever moved a node onto. The request is then refused by the edge
// check, and the operator learns to distrust the list instead of the request —
// so the list is built from the edges, not from the catalog.
func TestAgentTargetsComeFromWalkedPaths(t *testing.T) {
	edges := []version.UpgradeEdge{
		{ID: "a", From: "v0.0.1-beta3", To: "v0.0.1-beta11"},
		{ID: "b", From: "v0.0.1-beta9", To: "v0.0.1-beta10"},
		{ID: "c", From: "v0.0.1-beta3", To: "v0.0.1-beta11"}, // same pair again
	}

	targets := agentTargets("v0.0.1-beta3", edges, false, nil)
	if len(targets) != 1 {
		t.Fatalf("targets = %+v, want exactly the one path leaving beta3", targets)
	}
	if targets[0].Version != "v0.0.1-beta11" || !targets[0].EdgeVerified {
		t.Fatalf("target = %+v", targets[0])
	}
	// No policy in force: there is no offered-target list to consult, and the
	// edge is the whole answer.
	if !targets[0].OfferedByPolicy {
		t.Fatal("without a policy every walked path is what there is")
	}

	// A node with no edge leaving it has no targets, whatever the catalog says.
	if got := agentTargets("v0.0.1-beta11", edges, false, nil); len(got) != 0 {
		t.Fatalf("targets = %+v, want none", got)
	}
	// An unknown identity cannot be the start of anything.
	if got := agentTargets("", edges, false, nil); got != nil {
		t.Fatalf("targets = %+v, want nil", got)
	}
}

// With a policy in force the offered list is part of the answer: a walked path
// to a release the policy no longer offers is not something to put in front of
// an operator.
func TestAgentTargetsCarryThePolicyAnswerWhenOneIsInForce(t *testing.T) {
	edges := []version.UpgradeEdge{{ID: "a", From: "v0.0.1-beta3", To: "v0.0.1-beta11"}}

	listed := agentTargets("v0.0.1-beta3", edges, true, []string{"v0.0.1-beta11"})
	if len(listed) != 1 || !listed[0].OfferedByPolicy {
		t.Fatalf("targets = %+v, want it reported as offered", listed)
	}

	unlisted := agentTargets("v0.0.1-beta3", edges, true, []string{"v0.0.1-beta10"})
	if len(unlisted) != 1 || unlisted[0].OfferedByPolicy {
		t.Fatalf("targets = %+v, want the policy answer carried rather than assumed", unlisted)
	}
}
