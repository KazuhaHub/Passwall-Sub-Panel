package clientdoc

import (
	"bytes"
	"testing"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

func TestMintIsCanonicalAcrossAttachmentOrder(t *testing.T) {
	client := &domain.PSPClient{
		ID: 7, UserID: 8, PanelID: 9, Email: "u8@example.test",
		UUID: "uuid-8", Password: "password-8",
	}
	client.SetDesiredLifecycle(domain.UserLifecycle{
		Enable: true, ExpiryTime: 1893456000000, QuotaHeadroom: 123,
		IPLimit: 2, DeviceLimit: 3,
	})
	a := []domain.PSPClientInbound{
		{ClientID: 7, NodeID: 12, FlowOverride: "xtls-rprx-vision"},
		{ClientID: 7, NodeID: 11},
	}
	b := []domain.PSPClientInbound{a[1], a[0]}
	bodyA, etagA, err := Mint(client, a).Canonical()
	if err != nil {
		t.Fatal(err)
	}
	bodyB, etagB, err := Mint(client, b).Canonical()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(bodyA, bodyB) || etagA != etagB {
		t.Fatalf("logical order changed canonical identity:\nA=%s %s\nB=%s %s", bodyA, etagA, bodyB, etagB)
	}
}

func TestMintChangesETagForOwnedIntentOnly(t *testing.T) {
	client := &domain.PSPClient{ID: 1, UserID: 2, PanelID: 3, Email: "a", UUID: "u"}
	client.SetDesiredLifecycle(domain.UserLifecycle{Enable: true})
	_, before, err := Mint(client, nil).Canonical()
	if err != nil {
		t.Fatal(err)
	}
	client.LastRawTotalBytes = 999 // observed accounting is not desired content
	_, afterCounter, _ := Mint(client, nil).Canonical()
	if afterCounter != before {
		t.Fatal("observed counter entered desired document ETag")
	}
	client.PanelQuotaHeadroom = 123
	client.PanelIPLimit = 5
	client.PanelDeviceLimit = 6
	_, afterDirective, _ := Mint(client, nil).Canonical()
	if afterDirective != before {
		t.Fatal("directive/compatibility fields entered roster document ETag")
	}
	client.DesiredExpiryTime = 42
	_, afterIntent, _ := Mint(client, nil).Canonical()
	if afterIntent == before {
		t.Fatal("owned desired change did not change ETag")
	}
}
