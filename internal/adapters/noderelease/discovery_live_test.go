package noderelease

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/version"
)

// THE PANEL OFFERS WHAT THE PROJECT PUBLISHES, and a release it cannot find is a
// release nobody can install. This is the discovery half of the same claim the
// adapters prove separately: they read a release's assets, this one answers whether
// an operator is ever offered it.
//
// SET PSP_LIVE_NODE_RELEASE TO A PUBLISHED TAG. The production catalog is used, so
// the listing, the identity split and the platform extraction all run as they do in
// the panel.
func TestLiveThePanelDiscoversThePublishedRelease(t *testing.T) {
	tag := strings.TrimSpace(os.Getenv("PSP_LIVE_NODE_RELEASE"))
	if tag == "" {
		t.Skip("set PSP_LIVE_NODE_RELEASE to a published release tag to check what the panel offers")
	}
	releaseVersion, ok := version.VersionOfReleaseTag(tag)
	if !ok {
		t.Fatalf("%q is not a release tag this build can read", tag)
	}
	catalog, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	list, err := catalog.List(ctx)
	if err != nil {
		t.Fatalf("the panel cannot list published releases: %v", err)
	}
	offered := make([]string, 0, len(list.Releases))
	for _, release := range list.Releases {
		offered = append(offered, release.ReleaseTag)
	}
	var found *struct{ methods, platforms []string }
	for _, release := range list.Releases {
		if release.ReleaseTag != tag {
			continue
		}
		// THE TWO IDENTITIES ARE SPLIT HERE, and an operator selecting this release
		// depends on both: the version is what the node reports and what the install
		// request carries, the tag is what the assets live under.
		if release.Version != releaseVersion || release.ProductVersion != releaseVersion {
			t.Errorf("the panel offers %s as version %q / product %q, want %q", tag, release.Version, release.ProductVersion, releaseVersion)
		}
		if release.Scheme != "product" {
			t.Errorf("the panel reads %s as scheme %q", tag, release.Scheme)
		}
		methods, platforms := release.Methods, make([]string, 0, len(release.Platforms))
		for _, platform := range release.Platforms {
			platforms = append(platforms, platform.OS+"/"+platform.Arch)
		}
		found = &struct{ methods, platforms []string }{methods, platforms}
		break
	}
	if found == nil {
		t.Fatalf("the panel does not offer %s; it offers %v", tag, offered)
	}
	// AND IT OFFERS IT AS INSTALLABLE. A release listed without the installer method
	// and the two Linux targets is one the selector would show and the install path
	// would refuse, which is worse than not listing it.
	if !liveContains(found.methods, "linux") {
		t.Errorf("the panel does not offer %s as a linux installation: %v", tag, found.methods)
	}
	for _, target := range []string{"linux/amd64", "linux/arm64"} {
		if !liveContains(found.platforms, target) {
			t.Errorf("the panel does not offer %s for %s: %v", tag, target, found.platforms)
		}
	}
}

func liveContains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
