package service

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"strm115/backend/internal/netproxy"
	"strm115/backend/internal/storage"
)

func networkProxyFixture(t *testing.T, store accountSecretStore) (*NetworkProxy, *netproxy.Manager) {
	t.Helper()
	runtime := netproxy.New()
	t.Cleanup(runtime.CloseIdleConnections)
	s, err := NewNetworkProxy(store, runtime)
	if err != nil {
		t.Fatal(err)
	}
	return s, runtime
}

func TestNetworkProxyPersistsEncryptedCredentialsAndAppliesOnlyAfterSave(t *testing.T) {
	path := filepath.Join(t.TempDir(), "network.enc")
	store, err := NewSessionStore(path, "test-key")
	if err != nil {
		t.Fatal(err)
	}
	s, runtime := networkProxyFixture(t, store)
	password := "private-password"
	input := NetworkProxyInput{Enabled: true, URL: "socks5://proxy.example:1080", Username: "account", Password: &password, Services: []string{"github", "tmdb"}}
	view, err := s.Save(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(view)
	disk, err := os.ReadFile(path)
	if err != nil || strings.Contains(string(raw), password) || strings.Contains(string(disk), password) || !view.PasswordConfigured {
		t.Fatal("credentials were exposed or not persisted")
	}
	loaded, err := NewSessionStore(path, "test-key")
	if err != nil {
		t.Fatal(err)
	}
	restarted, _ := networkProxyFixture(t, loaded)
	if !restarted.View().Enabled || !restarted.View().PasswordConfigured || restarted.View().URL != input.URL {
		t.Fatal("restart lost proxy configuration")
	}
	input.Password = nil
	input.Services = []string{"mikan"}
	if _, err := s.Save(context.Background(), input); err != nil {
		t.Fatal(err)
	}
	req, _ := http.NewRequest(http.MethodGet, "https://mikanani.me/", nil)
	u, _ := runtime.Transport("mikan").Proxy(req)
	if p, _ := u.User.Password(); p != password {
		t.Fatal("omitted password did not retain saved authentication")
	}
	empty := ""
	input.Password = &empty
	if view, err := s.Save(context.Background(), input); err != nil || view.PasswordConfigured {
		t.Fatal("explicit password clear failed", err)
	}
	input.Enabled = false
	if _, err := s.Save(context.Background(), input); err != nil {
		t.Fatal(err)
	}
	if proxy, _ := runtime.Transport("mikan").Proxy(req); proxy != nil {
		t.Fatal("disabled saved configuration still proxies")
	}
}

func TestNetworkProxyFailedPersistenceAndInvalidScopeKeepLivePolicy(t *testing.T) {
	store := &failingAccountStore{}
	s, runtime := networkProxyFixture(t, store)
	input := NetworkProxyInput{Enabled: true, URL: "http://proxy.example:7890", Services: []string{"tmdb"}}
	if _, err := s.Save(context.Background(), input); err != nil {
		t.Fatal(err)
	}
	store.err = errors.New("disk failed")
	input.Enabled = false
	if _, err := s.Save(context.Background(), input); err == nil {
		t.Fatal("save failure was ignored")
	}
	req, _ := http.NewRequest(http.MethodGet, "https://api.themoviedb.org/3", nil)
	if proxy, _ := runtime.Transport("tmdb").Proxy(req); proxy == nil || !s.View().Enabled {
		t.Fatal("failed save changed routing")
	}
	store.err = nil
	input.Services = []string{"115"}
	if _, err := s.Save(context.Background(), input); !errors.Is(err, netproxy.ErrConfig) {
		t.Fatal("accepted protected service")
	}
}

type networkProbeRoundTripper func(*http.Request) (*http.Response, error)

func (f networkProbeRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestNetworkProxyProbeUsesDraftWithoutSavingOrLeakingSecrets(t *testing.T) {
	store := &failingAccountStore{}
	s, runtime := networkProxyFixture(t, store)
	password := "private-password"
	input := NetworkProxyInput{Enabled: true, URL: "http://proxy.example:7890", Username: "user", Password: &password, Services: []string{"tmdb"}}
	calls := 0
	s.probeClient = func(draft *netproxy.Manager, scope string) *http.Client {
		return &http.Client{Transport: networkProbeRoundTripper(func(r *http.Request) (*http.Response, error) {
			calls++
			proxy, _ := draft.Transport(scope).Proxy(r)
			if scope != "tmdb" || proxy == nil || r.URL.Host != "api.themoviedb.org" {
				t.Fatal("probe did not use selected draft policy and fixed URL")
			}
			return &http.Response{StatusCode: 401, Body: io.NopCloser(strings.NewReader("{}")), Header: make(http.Header)}, nil
		})}
	}
	result, err := s.Test(context.Background(), input, "tmdb")
	if err != nil || !result.Reachable || !result.Proxied || result.Status != 401 || calls != 1 {
		t.Fatal("unexpected probe result", result, err)
	}
	if store.Value() != "" || s.View().Enabled {
		t.Fatal("probe saved or enabled draft")
	}
	req, _ := http.NewRequest(http.MethodGet, "https://api.themoviedb.org/3", nil)
	if proxy, _ := runtime.Transport("tmdb").Proxy(req); proxy != nil {
		t.Fatal("probe mutated live routing")
	}
	s.probeClient = func(*netproxy.Manager, string) *http.Client {
		return &http.Client{Transport: networkProbeRoundTripper(func(*http.Request) (*http.Response, error) {
			return nil, errors.New("proxy authentication failed: " + password)
		})}
	}
	result, err = s.Test(context.Background(), input, "tmdb")
	if err != nil || result.Reachable || strings.Contains(result.Message, password) {
		t.Fatal("probe leaked credentials or misreported failure")
	}
	if _, err := s.Test(context.Background(), input, "http://arbitrary.invalid/"); !errors.Is(err, netproxy.ErrConfig) {
		t.Fatal("probe accepted arbitrary destination")
	}
}

func TestTelegramUserProxyProbeUsesNativeDraftTunnelAndClosesIt(t *testing.T) {
	closed := make(chan struct{})
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodConnect || r.Host != "149.154.167.41:443" || r.Header.Get("Proxy-Authorization") != "Basic dXNlcjpzZWNyZXQ=" {
			t.Errorf("probe did not use native DC/auth: %s %s", r.Method, r.Host)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		conn, _, err := w.(http.Hijacker).Hijack()
		if err != nil {
			t.Error(err)
			return
		}
		defer conn.Close()
		_ = conn.SetDeadline(time.Now().Add(2 * time.Second))
		_, _ = io.WriteString(conn, "HTTP/1.1 200 Connection Established\r\n\r\n")
		_, _ = io.Copy(io.Discard, conn)
		close(closed)
	}))
	defer proxy.Close()
	store := &failingAccountStore{}
	s, runtime := networkProxyFixture(t, store)
	password := "secret"
	input := NetworkProxyInput{Enabled: true, URL: proxy.URL, Username: "user", Password: &password, Services: []string{"telegram_user"}}
	result, err := s.Test(context.Background(), input, "telegram_user")
	if err != nil || !result.Reachable || !result.Proxied || result.Status != 0 || !strings.Contains(result.Message, "不代表用户登录") {
		t.Fatal("invalid native probe result", result, err)
	}
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("probe retained the test tunnel")
	}
	if store.Value() != "" || s.View().Enabled {
		t.Fatal("native probe saved its draft")
	}
	req, _ := http.NewRequest(http.MethodGet, "https://api.telegram.org/", nil)
	if u, _ := runtime.Transport("telegram_user").Proxy(req); u != nil {
		t.Fatal("native probe changed live routing")
	}
}

func TestTelegramUserProxyProbeSanitizesFailuresAndHonorsContext(t *testing.T) {
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusProxyAuthRequired)
		_, _ = io.WriteString(w, "private-password private-upstream-body")
	}))
	defer proxy.Close()
	s, _ := networkProxyFixture(t, &failingAccountStore{})
	password := "private-password"
	input := NetworkProxyInput{Enabled: true, URL: proxy.URL, Username: "user", Password: &password, Services: []string{"telegram_user"}}
	result, err := s.Test(context.Background(), input, "telegram_user")
	if err != nil || result.Reachable || !result.Proxied || strings.Contains(result.Message, "private-") {
		t.Fatal("probe leaked or misreported a proxy rejection", result, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := s.Test(ctx, input, "telegram_user"); !errors.Is(err, context.Canceled) {
		t.Fatal("native probe ignored cancellation", err)
	}
	ctx, cancel = context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	result, err = s.Test(ctx, input, "telegram_user")
	if err != nil || result.Reachable || !strings.Contains(result.Message, "超时") {
		t.Fatal("native probe did not report timeout", result, err)
	}
}

func TestAnimeProxyStoredCoversUseLocalImagesWithoutFetchingOrRewritingEntries(t *testing.T) {
	s, site, directories, input := animeAdminFixture(t)
	ctx := context.Background()
	entry, err := s.Store.GetAnimeEntry(ctx, input.Config.Entry.ID)
	if err != nil {
		t.Fatal(err)
	}
	original := "https://lain.bgm.tv/pic/cover/l/06/8c/278826_test.jpg"
	entry.Metadata.CoverURL = original
	if _, err := s.Store.SaveAnimeEntry(ctx, entry); err != nil {
		t.Fatal(err)
	}
	input.SettingsRevision = animeSaveReadySettings(t, s).Revision
	saved, err := s.SaveDefinition(ctx, "admin", "proxy_cover_01", "", input)
	if err != nil {
		t.Fatal(err)
	}
	reads, directoryReads := site.reads, directories.calls
	view, err := s.Subscription(ctx, saved.ID)
	if err != nil || view.Entry.Metadata.CoverURL != "/api/bangumi/image/l-06-8c/278826_test.jpg" {
		t.Fatal("detail cover did not use local cache", view.Entry.Metadata.CoverURL, err)
	}
	list, err := s.Subscriptions(ctx, storage.AnimeSubscriptionFilter{})
	if err != nil || len(list.Items) != 1 || list.Items[0].Entry.Metadata.CoverURL != view.Entry.Metadata.CoverURL {
		t.Fatal("list cover did not use local cache", err)
	}
	stored, err := s.Store.GetAnimeEntry(ctx, entry.ID)
	if err != nil || stored.Metadata.CoverURL != original || reads != site.reads || directoryReads != directories.calls {
		t.Fatal("cover display fetched remote data or rewrote the stored entry", err)
	}
}
