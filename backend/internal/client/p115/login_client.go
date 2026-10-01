package p115

func KnownLoginApp(app string) bool {
	switch app {
	case "web", "android", "ios", "115android", "115ios", "115ipad", "tv", "alipaymini", "wechatmini":
		return true
	}
	return false
}

// LoginApp is shared by the managed and personal login entry points.
func LoginApp(clientType string) (string, bool) {
	app, ok := map[string]string{
		"": "alipaymini", "alipay-miniapp": "alipaymini", "wechat-miniapp": "wechatmini",
		"115-android": "115android", "115-ios": "115ios", "115-ipad": "115ipad",
		"115-tv": "tv", "115-life-android": "android", "115-life-ios": "ios",
	}[clientType]
	return app, ok
}
