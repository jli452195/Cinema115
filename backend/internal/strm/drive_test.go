package strm

import (
	"strings"
	"testing"
)

func TestDriveURLRoundTrip(t *testing.T) {
	for _, name := range []string{"电影 [组].S01E01.mkv", "a+b%20#?字幕.mkv"} {
		value := Drive{PickCode: "abc123", FileID: "3432417343848548293", SHA1: strings.Repeat("ab", 20), Size: 589916580, Name: name}
		raw, err := DriveURL(" https://nas:443/ ", value)
		if err != nil {
			t.Fatal(err)
		}
		got, err := ParseDrivePath(strings.TrimPrefix(raw, "https://nas:443"))
		value.SHA1 = strings.ToUpper(value.SHA1)
		if err != nil || got != value || strings.ContainsAny(raw, "?#") {
			t.Fatalf("declaration changed: %+v %q %v", got, raw, err)
		}
	}
}

func TestDrivePathRejectsMalformedIdentity(t *testing.T) {
	valid, err := DrivePath(Drive{PickCode: "abc123", FileID: "3432417343848548293", SHA1: strings.Repeat("A", 40), Size: 589916580, Name: "movie.mkv"})
	if err != nil {
		t.Fatal(err)
	}
	for _, malformed := range []string{
		strings.Replace(valid, "abc123", "abc", 1),
		strings.Replace(valid, "abc123", "%61bc123", 1),
		strings.Replace(valid, "3432417343848548293", "0", 1),
		strings.Replace(valid, "3432417343848548293", "01", 1),
		strings.Replace(valid, "3432417343848548293", "18446744073709551616", 1),
		strings.Replace(valid, strings.Repeat("A", 40), strings.Repeat("G", 40), 1),
		strings.Replace(valid, "589916580", "0", 1),
		strings.Replace(valid, "589916580", "01", 1),
		strings.Replace(valid, "589916580", "9223372036854775808", 1),
		valid + "/extra", valid + "?token=x", valid + "#fragment",
		strings.Replace(valid, DrivePrefix, "/play115/", 1),
	} {
		if _, err := ParseDrivePath(malformed); err == nil {
			t.Errorf("malformed declaration accepted: %q", malformed)
		}
	}
	prefix := strings.TrimSuffix(valid, "movie.mkv")
	for _, name := range []string{"", "%20", ".", "%2e%2e", "a%2fb", "a%5cb", "a%00b", "a%0ab", "%ff", "%zz"} {
		if _, err := ParseDrivePath(prefix + name); err == nil {
			t.Errorf("malformed name accepted: %q", name)
		}
	}
}

func TestDriveURLRejectsCredentialsAndAmbiguousOrigins(t *testing.T) {
	value := Drive{PickCode: "abc123", FileID: "123", SHA1: strings.Repeat("A", 40), Size: 42, Name: "movie.mkv"}
	for _, base := range []string{"", "file://nas", "https:///", "http://user:password@nas", "https://nas?token=x", "https://nas?", "https://nas#", "https://nas#fragment", "https://nas/prefix"} {
		if _, err := DriveURL(base, value); err == nil {
			t.Errorf("ambiguous origin accepted: %q", base)
		}
	}
	for _, name := range []string{"", "..", "a/b.mkv", "a\\b.mkv", "bad\n.mkv"} {
		value.Name = name
		if _, err := DrivePath(value); err == nil {
			t.Errorf("invalid source name accepted: %q", name)
		}
	}
}
