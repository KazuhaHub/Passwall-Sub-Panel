package noderelease

import (
	"bytes"
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	versionpkg "github.com/KazuhaHub/passwall-sub-panel/internal/version"
)

// THE COPY THIS BINARY SHIPS IS THE PUBLISHED DOCUMENT, BYTE FOR BYTE.
//
// The copy exists so a failed fetch cannot look like "no release was ever
// reviewed", and it is useful only while it says the same thing as the document
// it stands in for. Two things can break that, and both are silent:
//
//   - THE CONTENT DRIFTS. Editing docs/compat/<name>.json and not the copy leaves
//     a panel offering a reviewed set that was already replaced — and the panel is
//     the only place the older answer is visible, so nobody sees the disagreement.
//   - THE MAJOR MOVES. The copy's name is written in a go:embed directive, which
//     takes a LITERAL, so it cannot follow RemoteNodeCatalogDocument. A build for
//     a new panel major would embed the previous major's document and offer its
//     releases as its own.
//
// So the guard reads the file the derived name points at, and compares bytes.
func TestTheEmbeddedCatalogMatchesThePublishedOne(t *testing.T) {
	name := versionpkg.RemoteNodeCatalogDocument(compiledMajor)

	published, err := os.ReadFile(filepath.Join("..", "..", "..", "docs", "compat", name))
	if err != nil {
		t.Fatalf("the catalog derives the document name %q and the repository publishes no such document: %v", name, err)
	}
	if !bytes.Equal(published, embeddedCatalog) {
		t.Fatalf("the copy at %s/ has drifted from docs/compat/%s; the panel would fall back to a reviewed set that was already replaced", name, name)
	}

	// The embed directive names its file with a literal, so this is the assertion
	// that catches a major bump which forgot it: if compiledMajor moved, the file
	// the derived name points at is not the one that was embedded.
	copied, err := os.ReadFile(name)
	if err != nil {
		t.Fatalf("the embedded copy is not at %q, which is the name this build derives for major %d: %v", name, compiledMajor, err)
	}
	if !bytes.Equal(copied, embeddedCatalog) {
		t.Fatalf("%s is not the file that was embedded", name)
	}
}

// A FAILED DOCUMENT FETCH OFFERS THE REVIEWED SET ALREADY IN FORCE, not an empty
// catalog.
//
// This is the degradation the shipped copy exists for, and it is worth asserting
// because the two failure modes look identical to an operator: a panel that offers
// nothing says the same thing whether the document could not be fetched or whether
// every release was withdrawn from review. The released list is what distinguishes
// them, and it is what this checks.
func TestAFailedDocumentFetchOffersTheReviewedSetInForce(t *testing.T) {
	for _, tc := range []struct {
		name     string
		document func(*http.Request) (*http.Response, error)
	}{
		{
			name: "the document cannot be fetched",
			document: func(req *http.Request) (*http.Response, error) {
				return fixtureResponse(req, http.StatusServiceUnavailable, ""), nil
			},
		},
		{
			name: "the document cannot be read",
			document: func(req *http.Request) (*http.Response, error) {
				return fixtureResponse(req, http.StatusOK, "{ not json"), nil
			},
		},
		{
			name: "the document is for another panel major",
			document: func(req *http.Request) (*http.Response, error) {
				return fixtureResponse(req, http.StatusOK, `{"schema_version":2,"panel_major":5,"released_nodes":[]}`), nil
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			transport := roundTripFunc(func(req *http.Request) (*http.Response, error) {
				if strings.HasPrefix(req.URL.String(), versionpkg.RemoteCompatURLBase) {
					return tc.document(req)
				}
				return fixtureResponse(req, http.StatusOK, fixtureBody(t, fixtureRelease(fixtureVersion))), nil
			})
			catalog, err := New(Options{
				HTTPClient: &http.Client{Transport: transport},
				Now:        func() time.Time { return fixtureNow },
				PSPMajor:   4,
			})
			if err != nil {
				t.Fatal(err)
			}
			list, err := catalog.List(context.Background())
			if err != nil || len(list.Releases) != 1 || list.Releases[0].Version != fixtureVersion {
				t.Fatalf("the reviewed set in force did not answer: list=%+v err=%v", list, err)
			}
		})
	}
}

// A DOCUMENT THAT DROPS EVERY RELEASE IS REFUSED, not published as an empty
// catalog: "nothing is reviewed" is not a state a document can put a panel in,
// and treating it as one would empty the upgrade dialog with no failure anywhere.
func TestADocumentWithNoReleasesIsRefused(t *testing.T) {
	transport := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if strings.HasPrefix(req.URL.String(), versionpkg.RemoteCompatURLBase) {
			return fixtureResponse(req, http.StatusOK, `{"schema_version":2,"panel_major":4,"released_nodes":[]}`), nil
		}
		return fixtureResponse(req, http.StatusOK, fixtureBody(t, fixtureRelease(fixtureVersion))), nil
	})
	catalog, err := New(Options{
		HTTPClient: &http.Client{Transport: transport},
		Now:        func() time.Time { return fixtureNow },
		PSPMajor:   4,
	})
	if err != nil {
		t.Fatal(err)
	}
	list, err := catalog.List(context.Background())
	if err != nil || len(list.Releases) != 1 {
		t.Fatalf("an empty document replaced the reviewed set in force: list=%+v err=%v", list, err)
	}
}
