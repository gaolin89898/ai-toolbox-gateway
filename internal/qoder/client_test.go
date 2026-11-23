package qoder

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func testClient(t *testing.T, handler http.HandlerFunc) *Client {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	c := NewClient()
	c.base = srv.URL
	return c
}
func reply(w http.ResponseWriter, v any) { _ = json.NewEncoder(w).Encode(v) }
func TestLoginPKCEAndPollIdentity(t *testing.T) {
	l, err := NewLogin()
	if err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(l.URL)
	callback, _ := url.Parse(u.Query().Get("oauth_callback"))
	digest := sha256.Sum256([]byte(l.verifier))
	if u.Host != "qoder.cn" || u.Path != "/sso/login/aliyun" || callback.Query().Get("challenge") != base64.RawURLEncoding.EncodeToString(digest[:]) || strings.Contains(l.URL, l.verifier) {
		t.Fatal("invalid PKCE authorization URL")
	}
	mode := "pending"
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("verifier") != l.verifier || r.Header.Get("Authorization") != "" {
			t.Error("invalid polling request")
		}
		if mode == "pending" {
			w.WriteHeader(404)
			return
		}
		nonce := l.nonce
		if mode == "wrong" {
			nonce = "another-flow"
		}
		reply(w, map[string]any{"nonce": nonce, "user_id": "user-a", "token": "dt-secret", "refresh_token": "drt-secret", "expires_in": 2591999999})
	})
	if a, err := c.Poll(context.Background(), l); err != nil || a != nil {
		t.Fatalf("pending: %v", err)
	}
	mode = "wrong"
	if _, err := c.Poll(context.Background(), l); err == nil {
		t.Fatal("accepted wrong nonce")
	}
	mode = "success"
	a, err := c.Poll(context.Background(), l)
	if err != nil || a.AccountUID != "user-a" || time.Until(a.ExpiresAt) < 29*24*time.Hour {
		t.Fatalf("poll: %v", err)
	}
}

func TestBalanceRejectsUnknownAndAddsQuotas(t *testing.T) {
	for _, tc := range []struct {
		body  string
		want  float64
		valid bool
	}{
		{`{"data":{"qoderUsage":{"userId":"a","userQuota":{"remaining":"180","total":300},"addOnQuota":{"total":100,"used":40}}}}`, 240, true},
		{`{"qoderUsage":{"userQuota":{"remaining":null}}}`, 0, false},
		{`{"qoderUsage":{"userQuota":{"total":100}}}`, 0, false},
		{`{"qoderUsage":{"userId":"other","userQuota":{"remaining":100}}}`, 0, false},
		{`{"qoderUsage":{"userQuota":{"remaining":0,"total":100}}}`, 0, true},
	} {
		c := testClient(t, func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(tc.body)) })
		a := Account{AccountUID: "a"}
		err := c.balance(context.Background(), &a)
		if (err == nil) != tc.valid || tc.valid && (!a.BalanceKnown || a.Credits != tc.want) {
			t.Fatalf("%s: %+v, %v", tc.body, a, err)
		}
	}
}

func TestIdentifyUsesAuthenticatedUsageWhenUserinfoRemoved(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/userinfo" {
			w.WriteHeader(404)
			return
		}
		reply(w, map[string]any{"qoderUsage": map[string]any{"userId": "account-a"}})
	})
	a := Account{AccountUID: "account-a", Token: "dt-secret"}
	if err := c.identify(context.Background(), &a); err != nil {
		t.Fatal(err)
	}
	a.AccountUID = "account-b"
	if err := c.identify(context.Background(), &a); err == nil {
		t.Fatal("accepted mismatched usage identity")
	}
}

func TestDesktopCredentialRefreshesAndChecksIdentity(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/userinfo":
			if r.Method != "GET" {
				t.Error("userinfo must use GET")
			}
			reply(w, map[string]any{"id": "user-a", "name": "Real name"})
		case "/api/v1/deviceToken/refresh":
			reply(w, map[string]any{"token": "dt-renewed", "refresh_token": "drt-renewed", "expires_in": 2591999999, "refresh_token_expires_in": 31103999999})
		default:
			w.WriteHeader(404)
		}
	})
	s, _ := Open(filepath.Join(t.TempDir(), "accounts.json"), c)
	_ = s.commit([]Account{{ID: "qoder-a", AccountUID: "user-a", Token: "dt-old", RefreshToken: "drt-old", ExpiresAt: time.Now().Add(time.Hour)}})
	a, err := s.DesktopCredential(context.Background(), "qoder-a")
	if err != nil || a.UID != "user-a" || a.Token != "dt-renewed" || a.Name != "Real name" || time.Until(a.RefreshExpiresAt) < 300*24*time.Hour {
		t.Fatalf("credential not prepared: %v", err)
	}
	if s.snapshot()[0].RefreshToken != "drt-renewed" {
		t.Fatal("rotated native refresh token not persisted")
	}
}

func TestDesktopRenewalsAreRetainedBeforeLeavingCurrentAccount(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		reply(w, map[string]any{"id": "user-a", "name": "Native name"})
	})
	s, _ := Open(filepath.Join(t.TempDir(), "accounts.json"), c)
	_ = s.commit([]Account{{ID: "a", AccountUID: "user-a", Token: "dt-old", RefreshToken: "drt-old", ExpiresAt: time.Now().Add(time.Hour)}})
	s.SetDesktopSource(func() *Account {
		return &Account{AccountUID: "user-a", Token: "dt-native-renewed", RefreshToken: "drt-native-renewed", ExpiresAt: time.Now().Add(24 * time.Hour), RefreshExpiresAt: time.Now().Add(30 * 24 * time.Hour)}
	})
	accounts := s.snapshot()
	if err := s.syncDesktop(context.Background(), accounts); err != nil {
		t.Fatal(err)
	}
	if got := s.snapshot()[0]; got.Token != "dt-native-renewed" || got.Nickname != "Native name" {
		t.Fatal("native renewal not retained")
	}
	// A future credential claiming another UID must not replace this account.
	s.SetDesktopSource(func() *Account {
		return &Account{AccountUID: "user-a", Token: "dt-mismatched", RefreshToken: "drt-other", ExpiresAt: time.Now().Add(48 * time.Hour)}
	})
	c.HTTP.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"id":"someone-else"}`)), Header: make(http.Header)}, nil
	})
	if err := s.syncDesktop(context.Background(), s.snapshot()); err == nil {
		t.Fatal("accepted another identity from native storage")
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestStoreIdentityGuardRefreshRotationAndPublicStatus(t *testing.T) {
	uid := "a"
	failBalance := false
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/userinfo":
			reply(w, map[string]any{"uid": uid, "nickname": "Alice"})
		case "/sash/api/v2/me/usage":
			if failBalance {
				w.WriteHeader(503)
				_, _ = w.Write([]byte("private-upstream-secret"))
				return
			}
			reply(w, map[string]any{"qoderUsage": map[string]any{"userQuota": map[string]any{"remaining": 200, "total": 300}}})
		case "/api/v1/deviceToken/refresh":
			reply(w, map[string]any{"device_token": "dt-rotated-secret", "refresh_token": "drt-rotated-secret", "expires_in": 3600})
		default:
			t.Errorf("unexpected %s", r.URL.Path)
			w.WriteHeader(404)
		}
	})
	path := filepath.Join(t.TempDir(), "accounts.json")
	s, _ := Open(path, c)
	a, _, err := s.Add(context.Background(), Account{Token: "dt-secret", RefreshToken: "drt-secret", ExpiresAt: time.Now().Add(-time.Hour)}, "")
	if err != nil {
		t.Fatal(err)
	}
	uid = "b"
	if _, _, err = s.Add(context.Background(), Account{Token: "dt-other"}, a.UID); err == nil || len(s.List()) != 1 {
		t.Fatal("wrong account update accepted")
	}
	failBalance = true
	if _, _, err = s.Action(context.Background(), a.UID, "balance"); err == nil {
		t.Fatal("expected balance failure")
	}
	reopened, err := Open(path, c)
	if err != nil || reopened.snapshot()[0].RefreshToken != "drt-rotated-secret" {
		t.Fatalf("rotation lost: %v", err)
	}
	public, _ := json.Marshal(reopened.List())
	if strings.Contains(string(public), "secret") || strings.Contains(string(public), "token\"") || !strings.Contains(string(public), "HTTP 503") {
		t.Fatalf("unsafe public status: %s", public)
	}
	if _, _, err = s.Action(context.Background(), a.UID, "checkin"); err == nil {
		t.Fatal("retired check-in accepted for an enabled account")
	}
	_, _, _ = s.Action(context.Background(), a.UID, "remove")
	reopened, _ = Open(path, c)
	if len(reopened.List()) != 0 {
		t.Fatal("remove did not persist")
	}
}

func TestBalanceRecordsUserType(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"qoderUsage":{"userId":"a","userType":"personal_standard","userQuota":{"remaining":0,"total":0}}}`))
	})
	a := Account{AccountUID: "a"}
	if err := c.balance(context.Background(), &a); err != nil {
		t.Fatal(err)
	}
	if a.UserType != "personal_standard" {
		t.Fatalf("free plan not detected: %+v", a)
	}
}

func TestOpenClearsOnlyRetiredCampaignErrors(t *testing.T) {
	path := filepath.Join(t.TempDir(), "accounts.json")
	if err := os.WriteFile(path, []byte(`[
		{"id":"qoder-1","user_type":"personal_pro","last_period":"2026-11-23","claimed_until":"2026-11-24T02:00:00Z","last_error":"Qoder 当前没有可识别的每日积分活动，请在客户端查看活动资格"},
		{"id":"qoder-2","last_error":"Qoder 登录凭证失效或无权访问，请重新授权"}
	]`), 0600); err != nil {
		t.Fatal(err)
	}
	s, err := Open(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	accounts := s.List()
	if accounts[0].LastError != "" || accounts[0].CheckinSupported || accounts[0].CheckinDone {
		t.Fatalf("retired campaign status was exposed: %+v", accounts[0])
	}
	if accounts[1].LastError != "Qoder 登录凭证失效或无权访问，请重新授权" {
		t.Fatalf("credential error was cleared: %+v", accounts[1])
	}
}
