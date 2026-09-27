package service

import "testing"

func TestThirdPartyConfigValidation(t *testing.T) {
	for _, target := range []string{"http://nas:8115", "https://tools.example", ""} {
		c, err := normalizeEmbyProxy(EmbyProxyConfig{Port: 8098, ThirdPartyURL: target, DirectLinkCacheMinutes: 9})
		if err != nil || c.ThirdPartyURL != target {
			t.Fatalf("valid address rejected: %s %v", target, err)
		}
	}
	for _, target := range []string{"file:///etc/passwd", "http://user:pass@nas:8115", "http://nas:8115/path", "http://nas:8115?token=secret", "http://nas:8098", "http://nas:", "http://nas:99999", "http://nas:8115#x"} {
		if _, err := normalizeEmbyProxy(EmbyProxyConfig{Port: 8098, ThirdPartyURL: target, DirectLinkCacheMinutes: 9}); err == nil {
			t.Fatalf("invalid address accepted: %s", target)
		}
	}
}
