package app

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

type nativePageSample struct {
	ElapsedMS  int64 `json:"elapsed_ms"`
	DurationUS int64 `json:"duration_us"`
	Items      int   `json:"items"`
	Total      int64 `json:"total"`
}

type nativePageQuery struct {
	Name    string             `json:"name"`
	Samples []nativePageSample `json:"samples"`
}

type nativePageReport struct {
	Schema   int               `json:"schema_version"`
	WindowMS int64             `json:"window_ms"`
	Queries  []nativePageQuery `json:"queries"`
}

type nativePageIdentity struct{ panel, user, policy, group, generation int64 }

// Decode private response bodies in memory, returning only row counts. A 200
// SPA or another fixture's published policy cannot become a successful sample.
func nativeValidatePage(body []byte, policies bool, own nativePageIdentity) (items int, total int64, valid bool) {
	if policies {
		var view struct {
			Generation int64                `json:"published_generation"`
			Published  bool                 `json:"published_has_access_control"`
			Allow      []struct{ ID int64 } `json:"allow"`
			Block      []struct{ ID int64 } `json:"block"`
			Observe    []struct{ ID int64 } `json:"observe"`
			Groups     []struct {
				ID    int64  `json:"group_id"`
				Stage string `json:"stage"`
			} `json:"allowlist_groups"`
		}
		if json.Unmarshal(body, &view) != nil || !view.Published || view.Generation != own.generation || view.Allow == nil || len(view.Allow) != 0 || view.Observe == nil || len(view.Observe) != 0 || len(view.Block) != 1 || view.Block[0].ID != own.policy || len(view.Groups) != 1 || view.Groups[0].ID != own.group || view.Groups[0].Stage != "trial" {
			return 0, 0, false
		}
		return 2, 2, true
	}
	var view struct {
		Items []domain.DestHitRecord  `json:"items"`
		Group string                  `json:"group_by"`
		Total int64                   `json:"total"`
		Page  int                     `json:"page"`
		Size  int                     `json:"page_size"`
		Loss  *domain.DestAuditLosses `json:"losses"`
	}
	if json.Unmarshal(body, &view) != nil || view.Items == nil || view.Group != "none" || view.Page != 1 || view.Size != 50 || len(view.Items) > 50 || view.Total < int64(len(view.Items)) || view.Loss == nil || view.Loss.Scope != "panel" || view.Loss.Complete || view.Loss.Rows < 0 || view.Loss.Events < 0 || view.Loss.Unmatched < 0 {
		return 0, 0, false
	}
	for _, row := range view.Items {
		if row.PanelID != own.panel || row.Count <= 0 || row.Dest == "" || row.FirstAt > row.LastAt {
			return 0, 0, false
		}
		switch row.Source {
		case fmt.Sprintf("g%d", own.group):
			if row.UserID != 0 || row.Port != 0 || row.Action != "observe" {
				return 0, 0, false
			}
		case fmt.Sprintf("p%d", own.policy):
			if row.UserID != own.user || row.Action != "block" {
				return 0, 0, false
			}
		default:
			return 0, 0, false
		}
	}
	return len(view.Items), view.Total, true
}

func TestNativePageSamplesRejectSPAStalePolicyAndPrivateTrialIdentity(t *testing.T) {
	own := nativePageIdentity{panel: 2, user: 3, policy: 4, group: 5, generation: 6}
	policy := `{"published_generation":6,"published_has_access_control":true,"allow":[],"block":[{"id":4}],"observe":[],"allowlist_groups":[{"group_id":5,"stage":"trial"}]}`
	if _, _, ok := nativeValidatePage([]byte(policy), true, own); !ok {
		t.Fatal("current owned policy rejected")
	}
	stale := own
	stale.generation++
	if _, _, ok := nativeValidatePage([]byte(policy), true, stale); ok {
		t.Fatal("otherwise valid stale snapshot accepted")
	}
	for _, body := range []string{`<html>successful SPA</html>`, `{}`, `{"published_generation":5}`, `{"published_generation":6,"published_has_access_control":true,"allow":[],"block":[{"id":99}],"observe":[],"allowlist_groups":[{"group_id":5,"stage":"trial"}]}`} {
		if _, _, ok := nativeValidatePage([]byte(body), true, own); ok {
			t.Fatal("SPA or stale/foreign policy accepted")
		}
	}
	page := map[string]any{"group_by": "none", "page": 1, "page_size": 50, "total": 1, "losses": domain.DestAuditLosses{Scope: "panel"}, "items": []domain.DestHitRecord{{PanelID: 2, Source: "g5", Action: "observe", Dest: "owned.test", Count: 1}}}
	encode := func() []byte {
		data, err := json.Marshal(page)
		if err != nil {
			t.Fatal("encode owned page")
		}
		return data
	}
	if _, _, ok := nativeValidatePage(encode(), false, own); !ok {
		t.Fatal("owned anonymous trial page rejected")
	}
	for _, row := range []domain.DestHitRecord{
		{PanelID: 2, Source: "g5", Action: "observe", Dest: "owned.test", Count: 1, UserID: 3},
		{PanelID: 2, Source: "g5", Action: "observe", Dest: "owned.test", Count: 1, Port: 443},
		{PanelID: 9, Source: "g5", Action: "observe", Dest: "owned.test", Count: 1},
		{PanelID: 2, Source: "p99", Action: "block", Dest: "owned.test", Count: 1, UserID: 3},
	} {
		page["items"] = []domain.DestHitRecord{row}
		if _, _, ok := nativeValidatePage(encode(), false, own); ok {
			t.Fatal("foreign or identified trial page accepted")
		}
	}
	page["items"] = nil
	if _, _, ok := nativeValidatePage(encode(), false, own); ok {
		t.Fatal("missing page accepted")
	}
}
