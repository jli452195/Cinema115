package notification

import (
	"errors"
	"strings"
	"testing"
)

func TestNotificationImageURLRequiresPublicHTTPAddress(t *testing.T) {
	for _, raw := range []string{"https://images.example.com/poster.jpg?size=large", "http://8.8.8.8/poster.png", "https://[2001:4860:4860::8888]/poster.png"} {
		if err := ValidateImageURL(raw); err != nil {
			t.Fatal(raw, err)
		}
	}
	for _, raw := range []string{
		"", "/api/image/local.jpg", "file:///poster.jpg", "data:image/png;base64,abc", "https://user:secret@example.com/p.jpg",
		"http://localhost/x.jpg", "http://NAS/x.jpg", "http://host.local/x.jpg", "http://host.internal/x.jpg",
		"http://127.0.0.1/x.jpg", "http://10.0.0.1/x.jpg", "http://192.168.1.1/x.jpg", "http://169.254.169.254/x.jpg",
		"http://127.1/x.jpg", "http://0x7f.0.0.1/x.jpg", "http://0177.0.0.1/x.jpg", "http://127.0.0.0x1/x.jpg",
		"http://100.64.0.1/x.jpg", "http://0.0.0.0/x.jpg", "http://[::1]/x.jpg", "http://[::ffff:127.0.0.1]/x.jpg",
		"http://[fd00::1]/x.jpg", "https://example.com:99999/x.jpg", "https://example.com/x.jpg#fragment",
	} {
		if err := ValidateImageURL(raw); !errors.Is(err, ErrImage) {
			t.Errorf("invalid URL accepted: %q %v", raw, err)
		}
	}
}

func TestNotificationImageModesAndFixedCoverOrigins(t *testing.T) {
	mode, imageURL, err := NormalizeImage("", "invalid local draft")
	if err != nil || mode != "cover" || imageURL != "" {
		t.Fatal(mode, imageURL, err)
	}
	if mode, _, err := NormalizeImage("none", "invalid local draft"); err != nil || mode != "none" {
		t.Fatal(mode, err)
	}
	if _, _, err := NormalizeImage("custom", ""); !errors.Is(err, ErrImage) {
		t.Fatal(err)
	}
	if _, _, err := NormalizeImage("unknown", ""); !errors.Is(err, ErrImage) {
		t.Fatal(err)
	}
	const bangumi = "/api/bangumi/image/l-13-c5/400602_ZI8Y9.jpg"
	hash := strings.Repeat("a", 64)
	for _, sample := range [][2]string{
		{bangumi, "https://lain.bgm.tv/pic/cover/l/13/c5/400602_ZI8Y9.jpg"},
		{"/api/mikan/image/201904/bb09460c.jpg", "https://mikanani.me/images/Bangumi/201904/bb09460c.jpg"},
		{"/api/garden/image/623249/poster.jpeg", "https://bgm.animes.garden/bangumi/subject/623249/poster.jpeg"},
		{"/api/anibt/image/native_123/" + hash + ".webp", "https://r2.anibt.net/anime_covers/native_123/" + hash + "/v4/w360.webp"},
	} {
		if got := PublicCoverURL(sample[0]); got != sample[1] {
			t.Fatal(sample[0], got)
		}
	}
	for _, raw := range []string{"http://127.0.0.1/api/bangumi/image/l-13-c5/x.jpg", "/api/mikan/image/202613/x.jpg", "/api/bangumi/image/l-13-c5/../x.jpg", "/api/garden/image/0/poster.jpeg", "https://evil.example.com/poster.jpg"} {
		if got := PublicCoverURL(raw); got != "" {
			t.Fatal("非官方封面被接受", raw, got)
		}
	}
	if PhotoURL("none", "https://example.com/p.jpg", bangumi) != "" || PhotoURL("cover", "", "") != "" || PhotoURL("custom", "http://localhost/p.jpg", "") != "" {
		t.Fatal("图片模式未遵守缺图回退规则")
	}
	if PhotoURL("custom", "https://example.com/p.jpg", bangumi) != "https://example.com/p.jpg" {
		t.Fatal("自定义图片未覆盖封面")
	}
}

func TestPhotoCaptionLimitCountsDecodedUTF16Text(t *testing.T) {
	if !FitsPhotoCaption("<b>"+strings.Repeat("&amp;", 1024)+"</b>", "HTML") || FitsPhotoCaption("<b>"+strings.Repeat("😀", 513)+"</b>", "HTML") || !FitsPhotoCaption(strings.Repeat("😀", 512), "") {
		t.Fatal("图片说明未按解码后的 UTF-16 可见长度限制")
	}
}
