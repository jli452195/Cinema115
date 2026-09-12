package p115

import "testing"

func TestParseShareURL(t *testing.T) {
	got, err := ParseShareURL("https://115cdn.com/s/swse0qu36j9?password=fwgl")
	if err != nil {
		t.Fatal(err)
	}
	if got.Code != "swse0qu36j9" || got.Password != "fwgl" {
		t.Fatalf("unexpected share info: %+v", got)
	}
}
