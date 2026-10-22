package httpapi

import (
	"testing"
)

func TestAnimeOrganizationHTTPReadOnlyPreviewAndStrictAuth(t *testing.T) {
	f := newAnimeHTTPFixture(t)
	endpoint := "/jobs/missing/organization-preview"
	f.call(t, "GET", endpoint, "", "", nil, 401)
	f.call(t, "GET", endpoint, f.user, "", nil, 403)
	f.call(t, "GET", endpoint, f.admin, "", nil, 404)
	f.call(t, "GET", "/subscriptions/missing/organization-preview", f.admin, "", nil, 404)
	for _, token := range []string{"", f.user} {
		status := 401
		if token != "" {
			status = 403
		}
		f.call(t, "PATCH", "/settings/organize-directory", token, "organization_auth", `broken`, status)
	}
	f.call(t, "PATCH", "/settings/organize-directory", f.admin, "organization_strict", `{"expectedRevision":1,"cid":"900","enabled":false}`, 400)
	cap := f.call(t, "GET", "/capabilities", f.admin, "", nil, 200)
	if cap["capabilities"].(map[string]any)["organize"] != false {
		t.Fatal("cloud writes prematurely enabled")
	}
	// Read-only preview does not require any configured 115 gateway.
	if f.s.Directories != nil || f.s.Accounts != nil {
		t.Fatal("unexpected fixture dependency")
	}
}
