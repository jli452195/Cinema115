package p115

import "testing"

func TestParseShareURL(t *testing.T) {
	got, err := ParseShareURL("https://115cdn.com/s/swse0qu36j9?password=fwgl")
	if err != nil {
		t.Fatal(err)
	}
	if got.Code != "swse0qu36j9" || got.Password != "fwgl" || got.URL != "https://115cdn.com/s/swse0qu36j9?password=fwgl" {
		t.Fatalf("unexpected share info: %+v", got)
	}
}

func TestParseShareURLCanonicalizesAnxiaAlias(t *testing.T) {
	for _, raw := range []string{
		"https://anxia.com/s/AbC123?password=p0a2",
		"http://www.anxia.com/s/AbC123/?password=p0a2&ignored=value",
	} {
		got, err := ParseShareURL(raw)
		if err != nil {
			t.Fatal(err)
		}
		if got.Code != "AbC123" || got.Password != "p0a2" || got.URL != "https://115cdn.com/s/AbC123?password=p0a2" {
			t.Fatalf("anxia alias was not canonicalized: %+v", got)
		}
	}
}

func TestParseShareURLRejectsUnsafeAliases(t *testing.T) {
	for _, raw := range []string{
		"https://anxia.com.evil.example/s/code",
		"https://anxia.com:8443/s/code",
		"file://anxia.com/s/code",
		"https://anxia.com/s/code/extra",
	} {
		if _, err := ParseShareURL(raw); err == nil {
			t.Fatalf("unsafe alias accepted: %s", raw)
		}
	}
}
