package render

import (
	"context"
	"testing"

	"github.com/KazuhaHub/passwall-sub-panel/internal/domain"
)

// seedRenderFixture renders the SHIPPED default template with the SHIPPED
// default rule set (about 474 KB, 9.5k rules) for one user with two lines.
// BenchmarkSubscriptionLinePipeline has a single `MATCH,G` rule, so it cannot
// see what dominates a real cold render: how many times the rule text is
// parsed, walked and re-serialised. This fixture exists to measure that.
func seedRenderFixture(tb testing.TB, ct domain.ClientType) (*Service, *domain.User) {
	tb.Helper()
	s, _, u, _ := referenceRenderFixture(tb)
	file := map[domain.ClientType]string{
		domain.ClientMihomo:  "../../seed/files/templates/default-mihomo.yaml",
		domain.ClientSingBox: "../../seed/files/templates/default-sing-box.yaml",
	}[ct]
	rules := &domain.RuleSet{Slug: "default_rules", Enabled: true, Content: readTemplateContent(tb, "../../seed/files/rulesets/default-rules.yaml")}
	s.repos.Template = referenceTemplateRepo{content: map[domain.ClientType]string{ct: readTemplateContent(tb, file)}}
	s.repos.RuleSet = referenceRuleRepo{rule: rules}
	return s, u
}

func BenchmarkSeedRender(b *testing.B) {
	for _, ct := range []domain.ClientType{domain.ClientMihomo, domain.ClientSingBox} {
		b.Run(string(ct), func(b *testing.B) {
			s, u := seedRenderFixture(b, ct)
			ctx := context.Background()
			out, err := s.RenderForUser(ctx, u, ct)
			if err != nil {
				b.Fatal(err)
			}
			b.SetBytes(int64(len(out.Body)))
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				if _, err := s.RenderForUser(ctx, u, ct); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// The seeded render must stay renderable and keep its rules: a guard for the
// optimisation work this benchmark measures.
func TestSeedRenderKeepsEveryRule(t *testing.T) {
	s, u := seedRenderFixture(t, domain.ClientMihomo)
	out, err := s.RenderForUser(context.Background(), u, domain.ClientMihomo)
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Body) < 400_000 {
		t.Fatalf("rendered body is %d bytes; the default rule set alone is over 400 KB", len(out.Body))
	}
}
