package destlist

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

func TestValidateRemoteURL(t *testing.T) {
	for _, ok := range []string{"https://example.com/list.txt", "https://example.com:8443/a/b?x=1", "HTTPS://Example.com/x"} {
		if _, err := ValidateRemoteURL(ok); err != nil {
			t.Errorf("%q refused: %v", ok, err)
		}
	}
	for _, c := range []struct {
		in   string
		want error
	}{
		{"http://example.com/list.txt", ErrInsecureURL},
		{"ftp://example.com/list.txt", ErrInsecureURL},
		{"file:///etc/hosts", ErrInsecureURL},
		{"//example.com/x", ErrInsecureURL},
		{"example.com/list.txt", ErrInsecureURL},
		{"https:///nohost", domain.ErrValidation},
		{"https://user:pw@example.com/x", domain.ErrValidation},
		{"", domain.ErrValidation},
		{"https://example.com/" + strings.Repeat("a", 1100), domain.ErrValidation},
		{"https://exa mple.com/", domain.ErrValidation},
	} {
		if _, err := ValidateRemoteURL(c.in); !errors.Is(err, c.want) {
			t.Errorf("%q: err = %v, want %v", c.in, err, c.want)
		}
	}
	// An insecure URL is its own, distinguishable refusal.
	_, err := ValidateRemoteURL("http://example.com/x")
	if !errors.Is(err, domain.ErrValidation) {
		t.Errorf("insecure url must still be a validation error: %v", err)
	}
}

func tlsServer(t *testing.T, h http.HandlerFunc) (*httptest.Server, Doer) {
	t.Helper()
	srv := httptest.NewTLSServer(h)
	t.Cleanup(srv.Close)
	return srv, srv.Client()
}

func TestFetchBody_ReadsAndRefusesToTruncate(t *testing.T) {
	body := strings.Repeat("a", 1000)
	srv, doer := tlsServer(t, func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(body)) })

	got, err := fetchBody(context.Background(), doer, srv.URL, 1000)
	if err != nil || string(got) != body {
		t.Fatalf("a body exactly at the limit: %d bytes, %v", len(got), err)
	}
	// One byte over is an ERROR, never a shorter body: a truncated list would
	// be a different list that happens to parse.
	got, err = fetchBody(context.Background(), doer, srv.URL, 999)
	var big *TooLargeError
	if !errors.As(err, &big) || big.Kind != "remote_bytes" || big.Limit != 999 || got != nil {
		t.Fatalf("one byte over: body %d bytes, err %v", len(got), err)
	}
	if !errors.Is(err, ErrListTooLarge) {
		t.Fatalf("an oversize fetch must classify as dest_list_too_large: %v", err)
	}
}

func TestFetchBody_RefusesOnAdvertisedLengthWithoutReading(t *testing.T) {
	srv, doer := tlsServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "5000")
		_, _ = w.Write([]byte(strings.Repeat("a", 5000)))
	})
	_, err := fetchBody(context.Background(), doer, srv.URL, 100)
	if !errors.Is(err, ErrListTooLarge) {
		t.Fatalf("err = %v", err)
	}
}

func TestFetchBody_Non2xxIsAnError(t *testing.T) {
	srv, doer := tlsServer(t, func(w http.ResponseWriter, r *http.Request) { http.Error(w, "nope", http.StatusNotFound) })
	_, err := fetchBody(context.Background(), doer, srv.URL, 1000)
	if err == nil || !strings.Contains(err.Error(), "404") {
		t.Fatalf("err = %v, want it to name the 404", err)
	}
	if errors.Is(err, ErrListTooLarge) {
		t.Fatal("a 404 is not an oversize list")
	}
}

func TestFetchBody_ContextCancels(t *testing.T) {
	srv, doer := tlsServer(t, func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() })
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := fetchBody(ctx, doer, srv.URL, 10); err == nil {
		t.Fatal("a cancelled fetch succeeded")
	}
}

// The default client is the SSRF-refusing one: a list URL that resolves to a
// loopback address is refused at dial time, which is how a "remote list" would
// otherwise reach the panel's own admin API or a cloud metadata endpoint.
func TestDefaultClient_RefusesLoopback(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("x")) }))
	defer srv.Close()
	_, err := fetchBody(context.Background(), newSafeClient(), srv.URL, 100)
	if err == nil || !strings.Contains(err.Error(), "non-public") {
		t.Fatalf("a loopback list URL was fetched or failed for another reason: %v", err)
	}
}

func TestRefuseInsecureRedirect(t *testing.T) {
	https, _ := http.NewRequest(http.MethodGet, "https://example.com/next", nil)
	plain, _ := http.NewRequest(http.MethodGet, "http://example.com/next", nil)
	if err := refuseInsecureRedirect(https, nil); err != nil {
		t.Errorf("an https redirect refused: %v", err)
	}
	if err := refuseInsecureRedirect(plain, nil); err == nil {
		t.Error("a downgrade to http was followed")
	}
	many := make([]*http.Request, 6)
	if err := refuseInsecureRedirect(https, many); err == nil {
		t.Error("an endless redirect chain was followed")
	}
	if newSafeClient().CheckRedirect == nil {
		t.Error("the default client has no redirect policy")
	}
}
