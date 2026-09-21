package pninstall

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/KazuhaHub/passwall-sub-panel/internal/pkg/releaseasset"
	"github.com/KazuhaHub/passwall-sub-panel/internal/ports"
)

// THE TEMPLATE IS SIGNED, AND THE SIGNATURE IS THE TRUST.
//
// A checksum that agrees with itself at a mutable origin establishes nothing: the
// manifest and the template arrive over the same channel from the same host, so
// anything that could replace one could replace the other. The detached signature
// over the manifest is what makes the digest worth checking.
//
// SIGNED WITH A TEST KEY HERE, because the production private key is a release
// secret — the same seam the Node side's own verifier exposes.

const templateName = "passwall-node-install-template.sh"

type fixture struct {
	server *httptest.Server
	key    ed25519.PublicKey
	calls  int
	// served is what the origin answers for each asset, by name.
	served map[string][]byte
}

func newFixture(t *testing.T, template string) *fixture {
	t.Helper()
	f := &fixture{served: map[string][]byte{}}
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	f.key = public
	digest := sha256.Sum256([]byte(template))
	manifest := fmt.Sprintf("%s  %s\n", hex.EncodeToString(digest[:]), templateName)
	signature := ed25519.Sign(private, []byte(manifest))
	f.served[releaseasset.ChecksumAsset] = []byte(manifest)
	f.served[releaseasset.SignatureAsset] = []byte(base64.StdEncoding.EncodeToString(signature) + "\n")
	f.served[templateName] = []byte(template)
	f.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.calls++
		name := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
		body, ok := f.served[name]
		if !ok {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write(body)
	}))
	t.Cleanup(f.server.Close)
	return f
}

func (f *fixture) renderer(t *testing.T) *Renderer {
	t.Helper()
	r, err := New(RendererOptions{
		HTTPClient: f.server.Client(),
		BaseURL:    f.server.URL + "/download/",
		PublicKey:  f.key,
	})
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func validOptions() Options {
	return Options{
		Endpoint:   "https://panel.example/psp/v1/node/sync",
		AgentID:    "agt_node-1",
		Credential: "pspn_" + strings.Repeat("a", 40),
		Version:    "4.0.1",
	}
}

const templateFixture = `#!/bin/sh
version=@@VERSION@@
tag=@@TAG@@
agent=@@AGENT_ID@@
endpoint=@@ENDPOINT@@
credential=@@CREDENTIAL@@
environment=@@ENVIRONMENT@@
case "$version$tag" in *@@*) echo "unsubstituted" >&2; exit 1;; esac
`

func TestItRendersThePublishedTemplateForThisIdentity(t *testing.T) {
	f := newFixture(t, templateFixture)
	rendered, err := f.renderer(t).Render(context.Background(), validOptions())
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"version='4.0.1'",
		"tag='release/4.0.1'",
		"agent='agt_node-1'",
		"endpoint='https://panel.example/psp/v1/node/sync'",
		"credential='pspn_" + strings.Repeat("a", 40) + "'",
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("the rendered script does not carry %s:\n%s", want, rendered)
		}
	}
	// THE ENVIRONMENT FILE IS WRITTEN THE WAY THE DAEMON READS IT: the same two
	// keys, each value quoted, which is the format the Node side's parser accepts.
	if !strings.Contains(rendered, `environment='PSP_NODE_AGENT_ID="agt_node-1"`) {
		t.Fatalf("the environment block is not the format the daemon parses:\n%s", rendered)
	}
	// AND NOTHING IS LEFT UNSUBSTITUTED. The template's own sentinel catches this
	// at run time, on a host the operator has already trusted it on; catching it
	// here costs a refusal instead of an install.
	if strings.Contains(strings.ReplaceAll(rendered, "*@@*", ""), "@@") {
		t.Fatalf("a placeholder survived rendering:\n%s", rendered)
	}
}

// A SIGNATURE BY ANY OTHER KEY IS REFUSED, and nothing is rendered from the
// template it covers.
func TestItRefusesAManifestSignedByAnotherKey(t *testing.T) {
	f := newFixture(t, templateFixture)
	other, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	r, err := New(RendererOptions{HTTPClient: f.server.Client(), BaseURL: f.server.URL + "/download/", PublicKey: other})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.Render(context.Background(), validOptions()); err == nil {
		t.Fatal("a manifest signed by another key was accepted; the signature is the whole trust")
	}
}

// A TEMPLATE THAT DOES NOT MATCH THE MANIFEST IS REFUSED, which is the digest
// check's only job: the signature says the manifest is ours, the digest says this
// is the file the manifest names.
func TestItRefusesATemplateTheManifestDoesNotName(t *testing.T) {
	f := newFixture(t, templateFixture)
	f.served[templateName] = []byte(templateFixture + "# appended after the manifest was signed\n")
	if _, err := f.renderer(t).Render(context.Background(), validOptions()); err == nil {
		t.Fatal("a template whose digest is not in the manifest was rendered")
	}
}

// A PLACEHOLDER THE RENDERER DOES NOT KNOW IS REFUSED rather than shipped. The
// template is published by another repository now, so it can gain a placeholder
// this build has never heard of — and a script carrying `@@NEW@@` is a script that
// fails on the host, after the operator has already run it.
func TestItRefusesATemplateWithAPlaceholderItCannotFill(t *testing.T) {
	f := newFixture(t, templateFixture+"new=@@SOMETHING_NEW@@\n")
	// The manifest is re-signed over the changed template, so the digest is right
	// and the refusal has to come from the placeholder check itself.
	f = resign(t, f, templateFixture+"new=@@SOMETHING_NEW@@\n")
	if _, err := f.renderer(t).Render(context.Background(), validOptions()); err == nil {
		t.Fatal("a template carrying a placeholder this build cannot fill was rendered")
	}
}

// WHAT CANNOT BE RENDERED IS REFUSED BEFORE ANYTHING IS FETCHED: a version that is
// not a release version, and a connection this panel would not accept either.
func TestItRefusesWhatItCannotRenderWithoutFetching(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*Options)
	}{
		{"not a release version", func(o *Options) { o.Version = "latest" }},
		{"a version with no identity", func(o *Options) { o.Version = "dev" }},
		{"an insecure endpoint", func(o *Options) { o.Endpoint = "http://panel.example/v1/node/sync" }},
		{"an endpoint that is not the sync path", func(o *Options) { o.Endpoint = "https://panel.example/psp" }},
		{"a short credential", func(o *Options) { o.Credential = "pspn_short" }},
		{"a credential with whitespace", func(o *Options) { o.Credential = "pspn_" + strings.Repeat(" ", 40) }},
		{"no agent", func(o *Options) { o.AgentID = "" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t, templateFixture)
			options := validOptions()
			tc.mutate(&options)
			if _, err := f.renderer(t).Render(context.Background(), options); err == nil {
				t.Fatal("accepted")
			}
			if f.calls != 0 {
				t.Fatalf("a refusal fetched %d assets; it must decide before the network", f.calls)
			}
		})
	}
}

// A PUBLICATION THAT IS NOT THERE IS A REFUSAL, not an empty script.
func TestItRefusesWhenTheReleaseIsNotPublished(t *testing.T) {
	f := newFixture(t, templateFixture)
	delete(f.served, templateName)
	if _, err := f.renderer(t).Render(context.Background(), validOptions()); err == nil {
		t.Fatal("an unpublished release rendered a script")
	}
}

// resign replaces the served template and re-signs the manifest over it, so a case
// can vary the template while keeping the signature valid.
func resign(t *testing.T, f *fixture, template string) *fixture {
	t.Helper()
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256([]byte(template))
	manifest := fmt.Sprintf("%s  %s\n", hex.EncodeToString(digest[:]), templateName)
	f.served[releaseasset.ChecksumAsset] = []byte(manifest)
	f.served[releaseasset.SignatureAsset] = []byte(base64.StdEncoding.EncodeToString(ed25519.Sign(private, []byte(manifest))) + "\n")
	f.served[templateName] = []byte(template)
	f.key = public
	return f
}

// A RELEASE THAT DOES NOT PUBLISH THE TEMPLATE IS NOT AN UNREACHABLE ORIGIN.
//
// The two are the same error value to a reader that only asks whether the read
// worked, and they need different answers: an unreachable origin is worth another
// attempt, while a release that never carried the file will answer the same way
// forever. The distinction is made where the read is classified, so a caller can
// tell an operator to pick another release rather than to try again.
func TestAMissingTemplateIsReportedAsAPublisherDecision(t *testing.T) {
	f := newFixture(t, templateFixture)
	delete(f.served, TemplateAsset)

	_, err := f.renderer(t).Render(context.Background(), validOptions())
	if !errors.Is(err, ErrTemplateMissing) {
		t.Fatalf("a release without the template reported %v", err)
	}
	// AND IT IS STILL A SOURCE FAILURE, so a caller that only knows the port's two
	// kinds still answers "the publication is what is wrong" rather than 400.
	if !errors.Is(err, ports.ErrInstallTemplateSource) {
		t.Fatalf("it stopped being a source failure: %v", err)
	}
	if errors.Is(err, ErrNotRenderable) {
		t.Fatalf("a publisher's omission was reported as the caller's mistake: %v", err)
	}
}
