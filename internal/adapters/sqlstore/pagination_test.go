package sqlstore

import (
	"strings"
	"testing"
)

// TestLikeColsEscapeIsPortable guards against re-introducing the beta.7
// regression: likeCols emitted `ESCAPE '\'`, which is a 1064 syntax error on
// MySQL (backslash escapes the closing quote in a MySQL string literal),
// breaking every keyword search on MySQL deployments while passing on the
// SQLite test backend. The escape char must be `!` (a plain literal in
// SQLite / MySQL / Postgres alike) and must never be a backslash.
func TestLikeColsEscapeIsPortable(t *testing.T) {
	sql := likeCols("upn", "display_name", "email")

	if strings.Contains(sql, `\`) {
		t.Fatalf("likeCols must not emit a backslash escape (1064 on MySQL): %q", sql)
	}
	wantClauses := 3
	if got := strings.Count(sql, "ESCAPE '!'"); got != wantClauses {
		t.Fatalf("likeCols(3 cols): got %d ESCAPE '!' clauses, want %d: %q", got, wantClauses, sql)
	}
	if got := strings.Count(sql, " OR "); got != wantClauses-1 {
		t.Fatalf("likeCols(3 cols): got %d OR joins, want %d: %q", got, wantClauses-1, sql)
	}
	for _, want := range []string{
		"LOWER(upn) LIKE ? ESCAPE '!'",
		"LOWER(display_name) LIKE ? ESCAPE '!'",
		"LOWER(email) LIKE ? ESCAPE '!'",
	} {
		if !strings.Contains(sql, want) {
			t.Fatalf("likeCols missing clause %q in %q", want, sql)
		}
	}
}

// TestKeywordLikeEscaping locks the symmetric half: the bound pattern escapes
// `%`, `_`, and the escape char itself with `!`, lowercases, and wraps in
// substring `%`. The ESCAPE char declared by likeCols must match the prefix
// likeEscaper injects here, or wildcards leak through.
func TestKeywordLikeEscaping(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"", ""},
		{"   ", ""},
		{"User_5", "%user!_5%"},  // underscore escaped + lowercased
		{"50%", "%50!%%"},        // percent escaped
		{"a!b", "%a!!b%"},        // escape char itself escaped
		{"SFA/1.1", "%sfa/1.1%"}, // slash is not a meta-char — left as-is
		{"plain", "%plain%"},     // no meta chars
	}
	for _, c := range cases {
		if got := keywordLike(c.in); got != c.want {
			t.Fatalf("keywordLike(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
