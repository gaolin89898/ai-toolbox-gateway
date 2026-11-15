package panel

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/linguo2625469/workbuddy2api-panel/internal/doubao"
)

func TestDoubaoTaskRoutesAuthValidationAndReadback(t *testing.T) {
	var claimed atomic.Bool
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		switch r.URL.Path {
		case "/passport/account/info/v2/":
			w.Write([]byte(`{"message":"success","data":{"user_id":"123","screen_name":"测试账号"}}`))
		case "/alice/ug/campaign/v3/detail/":
			state := 2
			if claimed.Load() {
				state = 3
			}
			json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": map[string]any{"progress": map[string]any{"total_amount": 4100}, "tasks": []any{map[string]any{"campaign_code": "select_work_fields", "task_id": "private-instance", "task_status": state}, map[string]any{"campaign_code": "pending", "task_status": 1}}}})
		case "/alice/ug/campaign/item/reward/claim/":
			claimed.Store(true)
			w.Write([]byte(`{"code":0,"data":{"task_status":3}}`))
		case "/alice/commerce/sale/subscription/quota/summary/":
			w.Write([]byte(`{"code":0,"data":{"incentive_quota_section":{"window_limit_groups":[]}}}`))
		default:
			t.Errorf("unexpected request: %s", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer upstream.Close()
	s, err := doubao.Open(filepath.Join(t.TempDir(), "accounts.json"), &doubao.Client{HTTP: upstream.Client(), Base: upstream.URL})
	if err != nil {
		t.Fatal(err)
	}
	a, err := s.Add(context.Background(), doubao.Account{Cookie: "private-session"}, "")
	if err != nil {
		t.Fatal(err)
	}
	p := New(Config{AccountsOnly: true, APIKey: "key", Doubao: s})
	defer p.Close()
	base := "/panel/api/doubao/accounts/" + a.UID + "/tasks"
	call := func(method, path, body, key string, want int) string {
		t.Helper()
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		if key != "" {
			r.Header.Set("Authorization", "Bearer "+key)
		}
		w := httptest.NewRecorder()
		p.ServeHTTP(w, r)
		if w.Code != want {
			t.Fatalf("%s %s: %d want %d: %s", method, path, w.Code, want, w.Body.String())
		}
		for _, secret := range []string{"private-session", "private-instance", "cookie"} {
			if strings.Contains(w.Body.String(), secret) {
				t.Fatalf("response leaked %s", secret)
			}
		}
		return w.Body.String()
	}
	before := calls.Load()
	call("GET", base, "", "", 401)
	call("POST", base+"/claim", `{"task_code":"select_work_fields"}`, "", 401)
	for _, body := range []string{`{}`, `{} {}`, `{"task_code":""}`, `{"task_code":"select_work_fields","task_id":"injected"}`} {
		call("POST", base+"/claim", body, "key", 400)
	}
	if calls.Load() != before {
		t.Fatal("unauthorized/invalid input reached upstream")
	}
	body := call("GET", base, "", "key", 200)
	if !strings.Contains(body, `"total_amount":4100`) || !strings.Contains(body, `"claimable":true`) {
		t.Fatalf("missing task data: %s", body)
	}
	call("GET", "/panel/api/doubao/accounts/missing/tasks", "", "key", 404)
	call("POST", base+"/claim", `{"task_code":"pending"}`, "key", 409)
	if !p.tryLockAccount(a.UID) {
		t.Fatal("could not acquire account task lock")
	}
	call("POST", base+"/claim", `{"task_code":"select_work_fields"}`, "key", 409)
	p.unlockAccount(a.UID)
	call("POST", base+"/claim", `{"task_code":"select_work_fields"}`, "key", 200)
	body = call("GET", base, "", "key", 200)
	if !strings.Contains(body, `"claimed":true`) {
		t.Fatalf("claim not read back: %s", body)
	}
}
