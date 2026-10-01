package httpapi

import (
	"context"
	"errors"
	"strings"
	"testing"
)

type deviceChecker struct {
	calls     int
	client    string
	lookupErr error
	change    func()
	valid     bool
}

func (d *deviceChecker) CheckCookie(context.Context, string) (bool, error) { return d.valid, nil }
func (d *deviceChecker) CurrentLoginClient(context.Context, string) (string, error) {
	d.calls++
	if d.change != nil {
		d.change()
	}
	return d.client, d.lookupErr
}

func TestAccountDeviceDetectionAndDraftIsolation(t *testing.T) {
	d := &deviceChecker{client: "115ipad", valid: true}
	r, accounts, _ := setupAccountsAPI(t, d, nil)
	accountRequest(t, r, "POST", "/accounts/default/cookie/test", `{"cookie":"UID=111_A; SEID=draft"}`, 200)
	if d.calls != 0 {
		t.Fatal("draft test performed metadata write")
	}
	snapshot, _ := accounts.Get("default")
	w := accountRequest(t, r, "GET", "/accounts/default/status", "", 200)
	if !strings.Contains(w.Body.String(), `"loginClient":"115ipad"`) {
		t.Fatal(w.Body.String())
	}
	got, _ := accounts.Get("default")
	if got.LoginClient != "115ipad" || got.Revision != snapshot.Revision {
		t.Fatal("detection did not preserve credential revision")
	}
	accountRequest(t, r, "GET", "/accounts/default/status", "", 200)
	if d.calls != 1 {
		t.Fatal("known device was unnecessarily queried")
	}
}

func TestAccountDeviceLookupFailureDoesNotInvalidateCookie(t *testing.T) {
	d := &deviceChecker{valid: true, lookupErr: errors.New("device service unavailable")}
	r, accounts, _ := setupAccountsAPI(t, d, nil)
	w := accountRequest(t, r, "GET", "/accounts/default/status", "", 200)
	if !strings.Contains(w.Body.String(), `"connected":true`) {
		t.Fatal("device lookup invalidated Cookie")
	}
	a, _ := accounts.Get("default")
	if a.LoginClient != "" {
		t.Fatal("fabricated device")
	}
}

func TestAccountDeviceLookupRejectsConcurrentReplacement(t *testing.T) {
	d := &deviceChecker{valid: true, client: "115ipad"}
	r, accounts, _ := setupAccountsAPI(t, d, nil)
	d.change = func() {
		cookie := "UID=111_A; SEID=replaced"
		if err := accounts.Update("default", 2, "小号", &cookie); err != nil {
			t.Fatal(err)
		}
	}
	accountRequest(t, r, "GET", "/accounts/default/status", "", 409)
	a, _ := accounts.Get("default")
	if a.LoginClient != "" {
		t.Fatal("stale result overwrote replacement")
	}
}

func TestQRLoginClientPersistsWithPrimaryUIDReplacement(t *testing.T) {
	qr := &accountQRFake{cookie: "UID=999_A; SEID=replacement"}
	r, accounts, id := setupAccountsAPI(t, nil, qr)
	accountRequest(t, r, "POST", "/accounts/"+id+"/qrcode", `{"clientType":"115-ipad"}`, 200)
	accountRequest(t, r, "GET", "/accounts/"+id+"/qrcode/status?qrId=115ipad", "", 200)
	next := accounts.PrimaryID()
	if next == id {
		t.Fatal("primary identity not rotated")
	}
	a, err := accounts.Get(next)
	if err != nil || a.LoginClient != "115ipad" {
		t.Fatal("QR client not saved with replacement", err)
	}
}
