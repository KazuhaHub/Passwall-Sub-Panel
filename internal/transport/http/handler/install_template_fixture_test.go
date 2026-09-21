package handler

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/KazuhaHub/passwall-sub-panel/internal/adapters/pninstall"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

// THE INSTALLATION ENDPOINTS' TEMPLATE SOURCE, FOR TESTS.
//
// IT IS THE REAL RENDERER OVER A FIXTURE ORIGIN, not a stub. A stub would let
// these tests pass while the substitution, the signature check or the tag
// derivation were broken — and the properties they are about (the script carries
// the credential, and it addresses the release by TAG while naming the archive by
// VERSION) live in that composition rather than in the handler. The key is a test
// key because the production private key is a release secret; the renderer takes
// the verification key as a seam for exactly this reason.
//
// THE TEMPLATE IS NOT A COPY OF THE NODE PROJECT'S. It carries the two things the
// handler tests assert the script must address, so the assertion is about a script
// that really was fetched and substituted rather than about a fixture's own text
// echoing itself back.
const fixtureInstallTemplateBody = `#!/bin/sh
version=@@VERSION@@
tag=@@TAG@@
mode=@@MODE@@
agent=@@AGENT_ID@@
endpoint=@@ENDPOINT@@
credential=@@CREDENTIAL@@
environment=@@ENVIRONMENT@@
curl -fsSL "https://github.com/KazuhaHub/Passwall-Node/releases/download/${tag}/passwall-node_${version}_linux_${arch}" \
  -o /tmp/passwall-node
case "$version$tag$mode" in *@@*) echo "unsubstituted" >&2; exit 1;; esac
`

// fixtureInstallTemplate serves a signed release for ANY tag, so a caller can ask
// for whatever version it is about without the fixture knowing the test's list.
func fixtureInstallTemplate(t *testing.T) ports.NodeInstallTemplate {
	t.Helper()
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256([]byte(fixtureInstallTemplateBody))
	manifest := fmt.Sprintf("%s  passwall-node-install-template.sh\n", hex.EncodeToString(digest[:]))
	assets := map[string][]byte{
		"passwall-node-install-template.sh": []byte(fixtureInstallTemplateBody),
		"SHA256SUMS.txt":                    []byte(manifest),
		"SHA256SUMS.txt.sig":                []byte(base64.StdEncoding.EncodeToString(ed25519.Sign(private, []byte(manifest))) + "\n"),
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, ok := assets[r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]]
		if !ok {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write(body)
	}))
	t.Cleanup(server.Close)
	template, err := pninstall.New(pninstall.RendererOptions{
		HTTPClient: server.Client(),
		BaseURL:    server.URL + "/download/",
		PublicKey:  public,
	})
	if err != nil {
		t.Fatal(err)
	}
	return template
}
