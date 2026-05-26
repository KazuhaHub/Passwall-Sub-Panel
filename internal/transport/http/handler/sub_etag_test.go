package handler

import "testing"

func TestComputeWeakETag_StableForSameBody(t *testing.T) {
	body := []byte(`{"proxies":[{"name":"US-1"}]}`)
	a := computeWeakETag(body)
	b := computeWeakETag(body)
	if a != b {
		t.Fatalf("same body should produce same etag: %s vs %s", a, b)
	}
}

func TestComputeWeakETag_DiffersWhenBodyChanges(t *testing.T) {
	a := computeWeakETag([]byte("v1"))
	b := computeWeakETag([]byte("v2"))
	if a == b {
		t.Fatalf("different bodies must produce different etags, got %s", a)
	}
}

func TestComputeWeakETag_ShapeIsWeakQuotedHex(t *testing.T) {
	// W/" + 16 hex chars + " == 20 chars total
	got := computeWeakETag([]byte("anything"))
	if len(got) != 20 {
		t.Fatalf("unexpected etag shape: %q (len %d)", got, len(got))
	}
	if got[:3] != `W/"` || got[len(got)-1] != '"' {
		t.Fatalf("etag should be weak quoted: %q", got)
	}
}

func TestETagMatches_ExactSingle(t *testing.T) {
	etag := `W/"deadbeef00000000"`
	if !etagMatches(etag, etag) {
		t.Fatalf("identical etag should match")
	}
}

func TestETagMatches_WildcardMatchesAny(t *testing.T) {
	if !etagMatches("*", `W/"deadbeef00000000"`) {
		t.Fatalf("wildcard If-None-Match should match any current etag")
	}
}

func TestETagMatches_CommaSeparatedListWithMatch(t *testing.T) {
	// Some clients send multiple cached ETags; one of them matching is a hit.
	header := `W/"aaa", W/"deadbeef00000000", W/"bbb"`
	if !etagMatches(header, `W/"deadbeef00000000"`) {
		t.Fatalf("etag in middle of list should match")
	}
}

func TestETagMatches_WeakStrongComparisonIsLenient(t *testing.T) {
	// We only emit weak ETags ourselves, but a client/proxy that strips the
	// W/ prefix shouldn't suddenly miss revalidation.
	if !etagMatches(`"deadbeef00000000"`, `W/"deadbeef00000000"`) {
		t.Fatalf("strong-form revalidator against weak current should still match")
	}
}

func TestETagMatches_NoMatch(t *testing.T) {
	if etagMatches(`W/"aaa"`, `W/"bbb"`) {
		t.Fatalf("different etags must not match")
	}
}

func TestETagMatches_EmptyHeaderNeverMatches(t *testing.T) {
	if etagMatches("", `W/"deadbeef00000000"`) {
		t.Fatalf("absent If-None-Match must never match")
	}
	if etagMatches("   ", `W/"deadbeef00000000"`) {
		t.Fatalf("whitespace-only If-None-Match must never match")
	}
}
