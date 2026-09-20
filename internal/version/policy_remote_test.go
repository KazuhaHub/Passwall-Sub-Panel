package version

import (
	"context"
	"crypto/ed25519"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// installTestTrustRoot gives the process a root that trusts a fresh key, so a
// policy signed by it verifies.
func installTestTrustRoot(t *testing.T) (string, ed25519.PublicKey, ed25519.PrivateKey) {
	t.Helper()
	previous := ActivePolicyTrustRoot()
	t.Cleanup(func() {
		SetPolicyTrustRoot(previous)
		SetActiveReleasesPolicy(nil)
	})
	id, pub, priv := signingKey(t)
	SetPolicyTrustRoot(trustRoot(t, id, pub))
	return id, pub, priv
}

func policyDocument(expiresAt time.Time) []byte {
	return []byte(`{
	  "schema_version": 1, "revision": 7,
	  "issued_at": "2026-09-19T00:00:00Z", "expires_at": "` + expiresAt.UTC().Format(time.RFC3339) + `",
	  "applies_to_psp": {"min": "4.0.0", "max": "4.99.99"},
	  "releases": [{"version": "v0.0.1-beta11", "release_tag": "v0.0.1-beta11", "scheme": "legacy", "evidence": ["node-wire-v1"]}],
	  "upgrade_edges": [], "refusals": []
	}`)
}

// servePolicy serves a document and its detached signature over the two asset
// names, which is what a publication is.
func servePolicy(document []byte, priv ed25519.PrivateKey) (http.HandlerFunc, error) {
	signature, err := SignReleasesPolicy(document, "key-1", priv, nil)
	if err != nil {
		return nil, err
	}
	return func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/" + PolicyDocumentAsset:
			_, _ = w.Write(document)
		case "/" + PolicySignatureAsset:
			_, _ = w.Write(signature)
		default:
			http.NotFound(w, r)
		}
	}, nil
}

func TestARefreshedPolicyIsVerifiedThenInstalled(t *testing.T) {
	document := policyDocument(time.Now().Add(24 * time.Hour))
	// The handler needs the key, and the key needs no URL — so build the handler
	// first, with a key generated here rather than by the server helper.
	_, _, priv := installTestTrustRoot(t)
	handler, err := servePolicy(document, priv)
	if err != nil {
		t.Fatal(err)
	}
	baseURL := withTestServer(t, handler)

	if err := RefreshReleasesPolicy(context.Background(), baseURL, time.Now()); err != nil {
		t.Fatalf("a correctly signed policy must install: %v", err)
	}
	if policy := ActiveReleasesPolicy(); policy == nil || policy.Revision != 7 {
		t.Fatalf("policy in force = %v", policy)
	}
}

func withTestServer(t *testing.T, handler http.HandlerFunc) string {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	previous := policyHTTPClient
	t.Cleanup(func() { policyHTTPClient = previous })
	policyHTTPClient = server.Client()
	return server.URL
}

// A publication that cannot be verified must leave the running policy alone. This
// is the property the fetch most needs: "clear and reload" would let a bad
// publication — or a 404 — withdraw the policy the panel is running on.
func TestAFailedRefreshLeavesTheInstalledPolicyAlone(t *testing.T) {
	document := policyDocument(time.Now().Add(24 * time.Hour))
	_, _, priv := installTestTrustRoot(t)
	good, err := servePolicy(document, priv)
	if err != nil {
		t.Fatal(err)
	}
	baseURL := withTestServer(t, good)
	if err := RefreshReleasesPolicy(context.Background(), baseURL, time.Now()); err != nil {
		t.Fatal(err)
	}
	_ = baseURL
	installed := ActiveReleasesPolicy()
	if installed == nil {
		t.Fatal("the harness is wrong: nothing installed")
	}

	staleSignature, err := SignReleasesPolicy(document, "key-1", priv, nil)
	if err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		name    string
		handler http.HandlerFunc
		wants   string
	}{
		{
			name: "the signature 404s",
			handler: func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/"+PolicySignatureAsset {
					http.NotFound(w, r)
					return
				}
				_, _ = w.Write(document)
			},
			wants: "returned 404",
		},
		{
			name: "the document and the signature do not match",
			handler: func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/"+PolicyDocumentAsset {
					_, _ = w.Write(policyDocument(time.Now().Add(48 * time.Hour)))
					return
				}
				_, _ = w.Write(staleSignature)
			},
			wants: "verify",
		},
		{
			name: "the window has closed",
			handler: func(w http.ResponseWriter, r *http.Request) {
				expired := policyDocument(time.Now().Add(-time.Hour))
				if r.URL.Path == "/"+PolicyDocumentAsset {
					_, _ = w.Write(expired)
					return
				}
				expiredSignature, signErr := SignReleasesPolicy(expired, "key-1", priv, nil)
				if signErr != nil {
					t.Error(signErr)
					return
				}
				_, _ = w.Write(expiredSignature)
			},
			wants: "expired",
		},
		{
			name:    "the whole publication is missing",
			handler: func(w http.ResponseWriter, r *http.Request) { http.NotFound(w, r) },
			wants:   "returned 404",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// The FAULTY handler must be where the fetch goes: swapping the
			// client while keeping the good URL simply fetched the good policy
			// again, and every case "passed" by installing it twice.
			faultyURL := withTestServer(t, tc.handler)
			err := RefreshReleasesPolicy(context.Background(), faultyURL, time.Now())
			if err == nil || !strings.Contains(err.Error(), tc.wants) {
				t.Fatalf("error = %v, want it to mention %q", err, tc.wants)
			}
			after := ActiveReleasesPolicy()
			if after == nil || after.Revision != installed.Revision {
				t.Fatalf("a failed refresh changed the policy in force: %v -> %v", installed.Revision, after)
			}
		})
	}
}

// Without keys nothing can be verified, and that is a state rather than a broken
// publication — the message says which so an operator does not go looking at the
// server.
func TestARefreshWithoutATrustRootSaysSo(t *testing.T) {
	previous := ActivePolicyTrustRoot()
	t.Cleanup(func() { SetPolicyTrustRoot(previous) })
	SetPolicyTrustRoot(nil)

	err := RefreshReleasesPolicy(context.Background(), "https://example.invalid", time.Now())
	if err == nil || !strings.Contains(err.Error(), "no trust root") {
		t.Fatalf("error = %v, want it to name the trust root", err)
	}
}

func TestARefreshWithoutASourceSaysSo(t *testing.T) {
	installTestTrustRoot(t)
	err := RefreshReleasesPolicy(context.Background(), "", time.Now())
	if err == nil || !strings.Contains(err.Error(), "no source") {
		t.Fatalf("error = %v, want it to name the missing source", err)
	}
}

// A body larger than any policy is refused rather than read: a wrong URL serving
// something huge must not become memory the panel holds.
func TestAnOversizedPolicyIsRefused(t *testing.T) {
	installTestTrustRoot(t)
	baseURL := withTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/"+PolicyDocumentAsset {
			_, _ = w.Write(make([]byte, maxPolicyDocumentBytes+16))
			return
		}
		_, _ = w.Write([]byte("{}"))
	})

	err := RefreshReleasesPolicy(context.Background(), baseURL, time.Now())
	if err == nil || !strings.Contains(err.Error(), "larger than") {
		t.Fatalf("error = %v, want an oversized refusal", err)
	}
}

// The backoff is tested through the SCHEDULE the loop asks for, not by timing it:
// a jitterFn that records its arguments and returns zero makes the retry cadence
// a value rather than a race.
func TestTheRefreshLoopBacksOffOnFailureAndRecovers(t *testing.T) {
	collect := func() (*[]time.Duration, func(time.Duration) time.Duration) {
		delays := &[]time.Duration{}
		return delays, func(d time.Duration) time.Duration {
			*delays = append(*delays, d)
			return 0 // fire immediately; the schedule is what is under test
		}
	}

	waitFor := func(t *testing.T, delays *[]time.Duration, n int) {
		t.Helper()
		deadline := time.Now().Add(2 * time.Second)
		for time.Now().Before(deadline) {
			if len(*delays) >= n {
				return
			}
			time.Sleep(time.Millisecond)
		}
		t.Fatalf("only %d attempts in two seconds", len(*delays))
	}

	t.Run("a failing source is retried further apart", func(t *testing.T) {
		installTestTrustRoot(t)
		baseURL := withTestServer(t, func(w http.ResponseWriter, _ *http.Request) { http.NotFound(w, nil) })
		delays, jitter := collect()

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		interval := 10 * time.Millisecond
		go RunPolicyRefresh(ctx, baseURL, interval, jitter, nil)

		waitFor(t, delays, 4)
		cancel()
		got := append([]time.Duration(nil), (*delays)[:4]...)
		want := []time.Duration{interval, 2 * interval, 4 * interval, 8 * interval}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("delays = %v, want %v", got, want)
			}
		}
	})

	t.Run("a working source keeps the interval", func(t *testing.T) {
		document := policyDocument(time.Now().Add(24 * time.Hour))
		_, _, priv := installTestTrustRoot(t)
		handler, err := servePolicy(document, priv)
		if err != nil {
			t.Fatal(err)
		}
		baseURL := withTestServer(t, handler)
		delays, jitter := collect()

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		interval := 5 * time.Millisecond
		go RunPolicyRefresh(ctx, baseURL, interval, jitter, nil)

		waitFor(t, delays, 3)
		cancel()
		for i, delay := range (*delays)[:3] {
			if delay != interval {
				t.Fatalf("attempt %d waited %v, want %v — a success must not back off", i, delay, interval)
			}
		}
	})

	t.Run("no source means no loop", func(t *testing.T) {
		delays, jitter := collect()
		RunPolicyRefresh(context.Background(), "", time.Millisecond, jitter, nil)
		if len(*delays) != 0 {
			t.Fatalf("a loop with no source asked to wait %v", *delays)
		}
	})
}
