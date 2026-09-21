package corecatalogdoc

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
	"github.com/KazuhaHub/passwall-sub-panel/internal/version"
)

// oneRelease names the release to read, so this checks THAT release rather than
// whichever one the reviewed list happens to offer first.
type oneRelease struct{ tag, version string }

func (o oneRelease) List(context.Context) (ports.NodeReleaseList, error) {
	return ports.NodeReleaseList{Releases: []ports.NodeReleaseCatalogEntry{
		{Version: o.version, ProductVersion: o.version, ReleaseTag: o.tag, Scheme: "product"},
	}}, nil
}

// A PUBLISHED RELEASE IS THE ONLY THING THAT PROVES PUBLISHING WORKS.
//
// The fixture cases prove the reader verifies, refuses and falls back. They cannot
// prove that a release carries the document, under the name this reader asks for,
// signed by the key this build holds — and that is the property an operator depends
// on, because it is what decides which cores the panel will offer and install.
//
// SET PSP_LIVE_NODE_RELEASE TO A PUBLISHED TAG. The production reader is used: the
// compiled verification key, the real origin, and the real download path.
func TestLivePublishedReleaseCarriesTheCoreCatalog(t *testing.T) {
	tag := strings.TrimSpace(os.Getenv("PSP_LIVE_NODE_RELEASE"))
	if tag == "" {
		t.Skip("set PSP_LIVE_NODE_RELEASE to a published release tag to check the real release")
	}
	releaseVersion, ok := version.VersionOfReleaseTag(tag)
	if !ok {
		t.Fatalf("%q is not a release tag this build can read", tag)
	}
	catalog, err := New(Options{Releases: oneRelease{tag: tag, version: releaseVersion}})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	document, err := catalog.Document(ctx)
	if err != nil {
		t.Fatalf("the published release does not serve a usable core catalog: %v", err)
	}
	if len(document.Releases) == 0 {
		t.Fatal("the published catalog carries no releases")
	}
	// THE FIELD THE PANEL'S OWN DECISIONS READ. A document that decodes and carries
	// nothing the panel offers is not a catalog, and the reader would have thrown it
	// out — so this asserts the release serves something usable, not merely present.
	recommended, err := document.Recommended(string(domain.NodeCoreXray))
	if err != nil {
		t.Fatalf("the published catalog has no recommended release the panel can offer: %v", err)
	}
	if !strings.HasPrefix(recommended.SourceURL, "https://github.com/") {
		t.Errorf("the recommended release does not name its source: %q", recommended.SourceURL)
	}
	if !recommended.Evidence.SourceAudited || !recommended.Evidence.ConfigTested || !recommended.Evidence.HandshakeTested {
		t.Errorf("the recommended release is published without the evidence the panel's gates require: %+v", recommended.Evidence)
	}
	if status := catalog.Status(); status.FallingBack || status.LastSuccess == nil {
		t.Fatalf("a document was read from the release and the reader does not say so: %+v", status)
	}
}
