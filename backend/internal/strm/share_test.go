package strm

import (
	"strings"
	"testing"
)

func TestSharePathRoundTripAndValidation(t *testing.T) {
	hash := strings.Repeat("ab", 20)
	for _, password := range []string{"fwgl"} {
		input := Share{Code: "sws8jb636j9", SHA1: hash, Password: password, FileID: "3532903049309717944"}
		path, err := SharePath(input)
		if err != nil {
			t.Fatal(err)
		}
		got, err := ParseSharePath(path)
		input.SHA1 = strings.ToUpper(hash)
		if err != nil || got != input {
			t.Fatal(got, err)
		}
	}
	for _, id := range []string{"0", "01", "-1", "1abc", "18446744073709551616"} {
		if _, err := ParseSharePath(SharePrefix + "abc/" + hash + "/pass/" + id); err == nil {
			t.Error("accepted ID", id)
		}
	}
	for _, hash := range []string{"", "abc", strings.Repeat("a", 39), strings.Repeat("a", 41), strings.Repeat("g", 40)} {
		if _, err := SharePath(Share{Code: "abc", SHA1: hash, Password: "pass", FileID: "1"}); err == nil {
			t.Error("accepted SHA1", hash)
		}
	}
	for _, password := range []string{"", " ", "-", "a/b", "a+b", strings.Repeat("a", 33)} {
		if _, err := SharePath(Share{Code: "abc", SHA1: hash, Password: password, FileID: "1"}); err == nil {
			t.Error("accepted password", password)
		}
	}
}

func TestLegacyShareQuery(t *testing.T) {
	for _, raw := range []string{
		"share_code=abc&id=3532903049309717944&receive_code=fwgl",
		"id=3532903049309717944&receive_code=fwgl&share_code=abc&filename=movie.mkv&extra=1&extra=2&sha1=untrusted",
	} {
		got, err := ParseShareQuery(raw)
		if err != nil || got != (Share{Code: "abc", Password: "fwgl", FileID: "3532903049309717944"}) {
			t.Fatal(got, err)
		}
	}
	const valid = "share_code=abc&id=123&receive_code=fwgl"
	for _, raw := range []string{
		"", "id=123&receive_code=fwgl", "share_code=abc&id=123", "share_code=abc&receive_code=fwgl",
		valid + "&id=123", valid + "&share_code=abc", valid + "&receive_code=fwgl",
		valid + "&bad=%ZZ", valid + ";ignored=x", valid + "&%69d=456",
		strings.Replace(valid, "fwgl", "", 1), strings.Replace(valid, "fwgl", "%20", 1),
		strings.Replace(valid, "fwgl", "-", 1), strings.Replace(valid, "fwgl", "a%0Ab", 1),
		strings.Replace(valid, "abc", "a%2Fb", 1), strings.Replace(valid, "123", "0", 1),
		strings.Replace(valid, "123", "0123", 1), strings.Replace(valid, "123", "-1", 1),
		strings.Replace(valid, "123", "18446744073709551616", 1),
	} {
		if _, err := ParseShareQuery(raw); err == nil {
			t.Error("accepted invalid legacy parameters", raw)
		}
	}
}
