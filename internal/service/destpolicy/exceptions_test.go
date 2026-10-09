package destpolicy

import "testing"

func TestExceptionTargetNormalizationRetainsExactAddressesAndPrivateSuffixSites(t *testing.T) {
	for _, test := range []struct{ target, match, want string }{
		{"https://Login.Example.co.uk:443/path?q=x", "site", "domain:example.co.uk"},
		{"a.foo.github.io", "site", "domain:foo.github.io"},
		{"LOGIN.Example.test.", "host", "full:login.example.test"},
		{"例子.测试", "host", "full:xn--fsqu00a.xn--0zwm56d"},
		{"203.0.113.4", "site", "203.0.113.4/32"},
		{"[2001:db8::1]:443", "host", "2001:db8::1/128"},
		{"::ffff:203.0.113.4", "host", "203.0.113.4/32"},
	} {
		got, err := exceptionEntry(test.target, test.match)
		if err != nil || got != test.want {
			t.Fatalf("target %s/%s = %q, %v; want %s", test.target, test.match, got, err, test.want)
		}
	}
	for _, target := range []string{"com", "github.io", "regexp:.*", "https://user:secret@example.test/", "foo.example.test:70000", "fe80::1%eth0", "example.test\nfull:other.test"} {
		if _, err := exceptionEntry(target, "site"); err == nil {
			t.Fatalf("unsafe/invalid target accepted: %q", target)
		}
	}
}
