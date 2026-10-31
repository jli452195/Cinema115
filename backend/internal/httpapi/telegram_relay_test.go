package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/gotd/td/tg"
	"strm115/backend/internal/client/tguser"
	"strm115/backend/internal/model"
	"strm115/backend/internal/service"
	"strm115/backend/internal/storage"
)

type telegramRelayAPIFake struct {
	sends                   int
	peerError               error
	avatarError             error
	avatarCalls             int
	avatarPeer, avatarPhoto string
}

func (*telegramRelayAPIFake) View() tguser.View { return tguser.View{HasSession: true} }
func (*telegramRelayAPIFake) SavedRelayPeers() []tguser.RelayPeer {
	return []tguser.RelayPeer{{ID: "channel:100", AvatarID: "999", Kind: "channel", Source: true}}
}
func (f *telegramRelayAPIFake) ListRelayPeers(context.Context, string) (tguser.RelayPeerPage, error) {
	return tguser.RelayPeerPage{Peers: []tguser.RelayPeer{{ID: "channel:100", Title: "来源", Source: true}}, SelectionToken: "selected"}, f.peerError
}
func (f *telegramRelayAPIFake) RelayAvatar(ctx context.Context, peerID, photo string) ([]byte, string, error) {
	f.avatarCalls++
	f.avatarPeer, f.avatarPhoto = peerID, photo
	if _, ok := ctx.Deadline(); !ok {
		return nil, "", context.DeadlineExceeded
	}
	return []byte("avatar-image"), "image/jpeg", f.avatarError
}
func (*telegramRelayAPIFake) PrepareRelay(_ context.Context, source, target, _, _ string) (tguser.RelayIdentity, error) {
	return tguser.RelayIdentity{OwnerID: 42, SourceID: source, SourceTitle: "来源", TargetID: target, TargetTitle: "目标"}, nil
}
func (*telegramRelayAPIFake) RunRelay(context.Context, tguser.RelayStateStore, func(context.Context, int64, *tg.Message) error) error {
	return nil
}
func (f *telegramRelayAPIFake) RelaySend(context.Context, int64, string, tguser.RelayPayload, int64) error {
	f.sends++
	return nil
}
func (*telegramRelayAPIFake) RelayConnected() bool { return false }

func TestTelegramRelayAdminAPIAndNoSendingFromConfiguration(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx := context.Background()
	db, err := storage.Open(ctx, filepath.Join(t.TempDir(), "relay.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	transport := &telegramRelayAPIFake{}
	relay, err := service.NewTelegramRelay(ctx, db, transport, nil)
	if err != nil {
		t.Fatal(err)
	}
	router := gin.New()
	admin := router.Group("/api/admin", func(c *gin.Context) {
		if c.GetHeader("Authorization") != "test-admin" {
			c.AbortWithStatus(http.StatusUnauthorized)
		}
	})
	RegisterTelegramRelay(admin, relay)
	request := func(method, path, body string, auth bool) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(method, "/api/admin/telegram-relay"+path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		if auth {
			req.Header.Set("Authorization", "test-admin")
		}
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		return w
	}
	for _, operation := range []struct{ method, path, body string }{{"GET", "", ""}, {"POST", "/peers", "{}"}, {"POST", "/rules", "{}"}, {"DELETE", "/rules/rule", ""}, {"GET", "/avatars/self?photo=100", ""}} {
		if w := request(operation.method, operation.path, operation.body, false); w.Code != 401 {
			t.Fatal("endpoint bypassed admin", operation, w.Code)
		}
	}
	if transport.avatarCalls != 0 {
		t.Fatal("unauthenticated avatar request reached Telegram")
	}
	for _, path := range []string{"/avatars/self", "/avatars/self?photo=" + strings.Repeat("1", 33), "/avatars/" + strings.Repeat("a", 81) + "?photo=100"} {
		if w := request("GET", path, "", true); w.Code != 400 {
			t.Fatal("invalid avatar parameters accepted", path, w.Code)
		}
	}
	if transport.avatarCalls != 0 {
		t.Fatal("invalid avatar request reached Telegram")
	}
	for _, peer := range []string{"self", "channel:100"} {
		w := request("GET", "/avatars/"+peer+"?photo=100", "", true)
		if w.Code != 200 || w.Body.String() != "avatar-image" || w.Header().Get("Content-Type") != "image/jpeg" || w.Header().Get("X-Content-Type-Options") != "nosniff" || w.Header().Get("Cross-Origin-Resource-Policy") != "same-origin" || w.Header().Get("Cache-Control") != "no-store" || transport.avatarPeer != peer || transport.avatarPhoto != "100" {
			t.Fatal("avatar response or identity changed", w.Code, w.Header())
		}
	}
	for _, failure := range []struct {
		err    error
		status int
	}{{tguser.ErrAvatarUnavailable, 404}, {tguser.ErrLogin, 409}, {tguser.ErrInput, 400}, {context.DeadlineExceeded, 504}} {
		transport.avatarError = failure.err
		if w := request("GET", "/avatars/self?photo=100", "", true); w.Code != failure.status || strings.Contains(w.Body.String(), "avatar-image") {
			t.Fatal("avatar failure response incorrect", failure.err, w.Code, w.Body.String())
		}
	}
	transport.avatarError = nil
	for _, operation := range []struct {
		method, path, body string
		status             int
	}{
		{"GET", "", "", 200}, {"POST", "/peers", "{}", 200}, {"POST", "/peers", "broken", 400}, {"POST", "/peers", `{"cursor":"` + strings.Repeat("a", 65) + `"}`, 400},
		{"POST", "/rules", `{"sourceId":"channel:100","targetId":"user:200","linkTypes":["115"],"includePattern":"["}`, 400},
		{"POST", "/rules", "{}", 400}, {"DELETE", "/rules/missing", "", 404},
	} {
		w := request(operation.method, operation.path, operation.body, true)
		if w.Code != operation.status || w.Header().Get("Cache-Control") != "no-store" {
			t.Fatal(operation, w.Code, w.Body.String())
		}
	}
	created := request("POST", "/rules", `{"sourceId":"channel:100","targetId":"user:200","enabled":true,"linkTypes":["115","ed2k"]}`, true)
	var response struct {
		Rule model.TelegramRelayRule `json:"rule"`
	}
	if created.Code != 200 || json.Unmarshal(created.Body.Bytes(), &response) != nil || response.Rule.ID == "" {
		t.Fatal("create failed", created.Code, created.Body.String())
	}
	id := response.Rule.ID
	for _, operation := range []struct {
		method, path string
		status       int
	}{{"GET", "/rules/" + id + "/jobs", 404}, {"POST", "/rules/" + id + "/retry", 404}, {"DELETE", "/rules/" + id, 204}} {
		w := request(operation.method, operation.path, "", true)
		if w.Code != operation.status {
			t.Fatal(operation, w.Code, w.Body.String())
		}
	}
	transport.peerError = tguser.ErrLogin
	if w := request("POST", "/peers", "{}", true); w.Code != 409 {
		t.Fatal("login error not actionable", w.Code)
	}
	if transport.sends != 0 {
		t.Fatal("configuration or read endpoint sent Telegram messages")
	}
}
