package service

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestObsoleteThirdPartyConfigIgnored(t *testing.T) {
	var c EmbyProxyConfig
	if err := json.Unmarshal([]byte(`{"port":8098,"directLinkCacheMinutes":9,"thirdPartyUrl":"file:///invalid-old-setting"}`), &c); err != nil {
		t.Fatal(err)
	}
	if _, err := normalizeEmbyProxy(c); err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(c)
	if err != nil || strings.Contains(string(raw), "thirdPartyUrl") {
		t.Fatal("obsolete setting persisted")
	}
}
