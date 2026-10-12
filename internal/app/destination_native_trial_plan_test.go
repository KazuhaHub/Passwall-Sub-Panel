package app

import (
	"testing"

	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

func nativeTrialProvisioningReady(status *ports.ServerStatus, inbound *ports.Inbound, id, port int) bool {
	return status != nil && inbound != nil && status.CoreEngine == "xray" && status.XrayVersion == "26.6.27" && status.XrayState == "running" && inbound.ID == id && inbound.Enable && inbound.Port == port && inbound.Protocol == "vless"
}

func TestNativeTrialProvisioningRequiresObservedRuntimeAndInbound(t *testing.T) {
	status := ports.ServerStatus{CoreEngine: "xray", XrayVersion: "26.6.27", XrayState: "running"}
	inbound := ports.Inbound{ID: 1, Enable: true, Port: 4444, Protocol: "vless"}
	if !nativeTrialProvisioningReady(&status, &inbound, 1, 4444) {
		t.Fatal("actual trial provisioning baseline rejected")
	}
	if nativeTrialProvisioningReady(nil, &inbound, 1, 4444) || nativeTrialProvisioningReady(&status, nil, 1, 4444) {
		t.Fatal("missing observed runtime allowed provisioning")
	}
	for _, change := range []func(*ports.ServerStatus){
		func(s *ports.ServerStatus) { s.CoreEngine = "" },
		func(s *ports.ServerStatus) { s.CoreEngine = "sing-box" },
		func(s *ports.ServerStatus) { s.XrayVersion = "26.7.28" },
		func(s *ports.ServerStatus) { s.XrayState = "starting" },
	} {
		s := status
		change(&s)
		if nativeTrialProvisioningReady(&s, &inbound, 1, 4444) {
			t.Fatal("unready core allowed trial provisioning")
		}
	}
	for _, change := range []func(*ports.Inbound){
		func(i *ports.Inbound) { i.ID++ },
		func(i *ports.Inbound) { i.Port++ },
		func(i *ports.Inbound) { i.Enable = false },
		func(i *ports.Inbound) { i.Protocol = "trojan" },
	} {
		i := inbound
		change(&i)
		if nativeTrialProvisioningReady(&status, &i, 1, 4444) {
			t.Fatal("unapplied or foreign inbound allowed trial provisioning")
		}
	}
}
