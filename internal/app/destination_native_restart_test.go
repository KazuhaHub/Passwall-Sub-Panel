package app

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/KazuhaHub/passwall-protocol/protocol"
)

// Observe only the first real request after arm. The production handler still
// owns validation and its original bytes; private report payloads are discarded.
type nativeRestartProbe struct {
	mu                    sync.Mutex
	armed, seen, accepted bool
	status                *protocol.PolicyStatus
}

func (p *nativeRestartProbe) arm() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.armed, p.seen, p.accepted, p.status = true, false, false, nil
}
func (p *nativeRestartProbe) first() (*protocol.PolicyStatus, bool, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.status == nil {
		return nil, p.seen, p.accepted
	}
	status := *p.status
	return &status, p.seen, p.accepted
}
func (p *nativeRestartProbe) wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p.mu.Lock()
		capture := p.armed && r.Method == "POST" && r.URL.Path == "/v1/node/sync"
		if capture {
			p.armed = false
		}
		p.mu.Unlock()
		if !capture {
			next.ServeHTTP(w, r)
			return
		}
		body := &nativeBoundedReport{}
		r.Body = &nativeTeeBody{Reader: io.TeeReader(r.Body, body), Closer: r.Body}
		response := &nativeProbeResponse{ResponseWriter: w}
		next.ServeHTTP(response, r)
		var report struct {
			PolicyStatus *protocol.PolicyStatus `json:"policy_status"`
		}
		valid := !body.truncated && json.Unmarshal(body.buf.Bytes(), &report) == nil && report.PolicyStatus != nil
		p.mu.Lock()
		defer p.mu.Unlock()
		p.seen, p.accepted = true, valid && (response.code == 0 || response.code == 200)
		if valid {
			p.status = report.PolicyStatus
		}
	})
}

type nativeTeeBody struct {
	io.Reader
	io.Closer
}
type nativeBoundedReport struct {
	buf       bytes.Buffer
	truncated bool
}

func (b *nativeBoundedReport) Write(data []byte) (int, error) {
	n := len(data)
	if remaining := (1 << 20) - b.buf.Len(); n > remaining {
		data = data[:remaining]
		b.truncated = true
	}
	_, _ = b.buf.Write(data)
	return n, nil
}

type nativeProbeResponse struct {
	http.ResponseWriter
	code int
}

func (w *nativeProbeResponse) WriteHeader(code int) {
	if w.code == 0 {
		w.code = code
	}
	w.ResponseWriter.WriteHeader(code)
}

func TestNativeRestartProbePreservesHandlerAndFirstReport(t *testing.T) {
	probe := &nativeRestartProbe{}
	const first = `{"policy_status":{"state":"applied","digest":"first"},"private":"not retained"}`
	const second = `{"policy_status":{"state":"rejected","digest":"second"}}`
	want := first
	handler := probe.wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, err := io.ReadAll(r.Body)
		if err != nil || string(data) != want {
			t.Fatal("probe changed the production request body")
		}
		w.WriteHeader(200)
		_, _ = io.WriteString(w, "unchanged response")
	}))
	probe.arm()
	for _, body := range []string{first, second} {
		want = body
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest("POST", "/v1/node/sync", strings.NewReader(body)))
		if response.Code != 200 || response.Body.String() != "unchanged response" {
			t.Fatal("probe changed the production response")
		}
	}
	got, seen, accepted := probe.first()
	if !seen || !accepted || got == nil || got.State != "applied" || got.Digest != "first" {
		t.Fatal("probe did not retain only the first real report")
	}
	probe.arm()
	if _, seen, _ := probe.first(); seen {
		t.Fatal("probe reused a report before the restart")
	}
}

func TestNativeRestartProbeRejectsUnacceptedAndTruncatedReport(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		status     int
	}{
		{"refused", `{"policy_status":{"state":"applied"}}`, 401},
		{"malformed", "private not JSON", 200},
		{"oversized", `{"private":"` + strings.Repeat("x", 2<<20) + `","policy_status":{"state":"applied"}}`, 200},
	} {
		t.Run(tc.name, func(t *testing.T) {
			probe := &nativeRestartProbe{}
			probe.arm()
			handler := probe.wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = io.Copy(io.Discard, r.Body)
				w.WriteHeader(tc.status)
			}))
			handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("POST", "/v1/node/sync", strings.NewReader(tc.body)))
			_, seen, accepted := probe.first()
			if !seen || accepted {
				t.Fatal("probe treated an invalid or refused report as accepted")
			}
		})
	}
}
