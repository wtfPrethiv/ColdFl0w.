package research

import (
	"net"
	"testing"
)

func TestPublicTargets(t *testing.T) {
	for _, ip := range []string{"127.0.0.1", "10.1.2.3", "169.254.169.254", "::1", "fc00::1", "100.64.0.1"} {
		if Public(net.ParseIP(ip)) {
			t.Fatal("private allowed", ip)
		}
	}
	if !Public(net.ParseIP("8.8.8.8")) {
		t.Fatal("public blocked")
	}
	for _, u := range []string{"file:///tmp/foo", "http://127.0.0.1", "https://user:pass@example.com", "https://example.com:22"} {
		if ValidateURL(u) == nil {
			t.Fatal("bad URL allowed", u)
		}
	}
}
func TestRobots(t *testing.T) {
	rules := "User-agent: *\nDisallow: /private\nAllow: /private/papers\nDisallow: /*?secret=\n"
	if Allowed(rules, "/private/cv") {
		t.Fatal("disallow ignored")
	}
	if !Allowed(rules, "/private/papers/a") {
		t.Fatal("longer allow ignored")
	}
	if Allowed(rules, "/page?secret=yes") {
		t.Fatal("wildcard ignored")
	}
	if !Allowed(rules, "/public") {
		t.Fatal("public blocked")
	}
}
