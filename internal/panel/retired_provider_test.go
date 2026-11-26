package panel

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/linguo2625469/workbuddy2api-panel/internal/pool"
	"github.com/linguo2625469/workbuddy2api-panel/internal/qoder"
)

func TestRetiredDoubaoTaskAndCheckinEndpoints(t *testing.T) {
	accounts := pool.New("")
	defer accounts.Close()
	p := New(Config{AccountsOnly: true, Pool: accounts, APIKey: "test-key"})
	for _, tc := range []struct {
		method string
		path   string
		status int
	}{
		{http.MethodPost, "/panel/api/doubao/checkin_all", http.StatusNotImplemented},
		{http.MethodPost, "/panel/api/doubao/accounts/d1/tasks/claim", http.StatusNotFound},
	} {
		r := httptest.NewRequest(tc.method, tc.path, strings.NewReader("{}"))
		r.Header.Set("Authorization", "Bearer test-key")
		w := httptest.NewRecorder()
		p.ServeHTTP(w, r)
		if w.Code != tc.status {
			t.Errorf("endpoint %s %s returned %d, want %d", tc.method, tc.path, w.Code, tc.status)
		}
	}
}

func TestQoderCheckinEndpointsAreRestored(t *testing.T) {
	store, err := qoder.Open(filepath.Join(t.TempDir(), "accounts.json"), nil)
	if err != nil {
		t.Fatal(err)
	}
	p := New(Config{AccountsOnly: true, APIKey: "test-key", Qoder: store})
	defer p.Close()
	r := httptest.NewRequest(http.MethodPost, "/panel/api/qoder/checkin_all", nil)
	r.Header.Set("Authorization", "Bearer test-key")
	w := httptest.NewRecorder()
	p.ServeHTTP(w, r)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"started":true`) {
		t.Fatalf("Qoder bulk check-in endpoint returned %d: %s", w.Code, w.Body)
	}

	r = httptest.NewRequest(http.MethodPost, "/panel/api/qoder/accounts/qoder-1/checkin", nil)
	r.Header.Set("Authorization", "Bearer test-key")
	w = httptest.NewRecorder()
	p.ServeHTTP(w, r)
	if w.Code != http.StatusNotFound || !strings.Contains(w.Body.String(), "Qoder 账号不存在") {
		t.Fatalf("Qoder single-account check-in route not registered: %d %s", w.Code, w.Body)
	}
}

func TestRetiredLoomyIntegration(t *testing.T) {
	accounts := pool.New("")
	defer accounts.Close()
	p := New(Config{AccountsOnly: true, Pool: accounts, APIKey: "test-key"})
	for _, path := range []string{
		"accounts", "local_import", "balance_all", "accounts/l1/remove",
	} {
		r := httptest.NewRequest(http.MethodPost, "/panel/api/loomy/"+path, strings.NewReader("{}"))
		r.Header.Set("Authorization", "Bearer test-key")
		w := httptest.NewRecorder()
		p.ServeHTTP(w, r)
		if w.Code != http.StatusNotFound {
			t.Errorf("retired endpoint %s returned %d", path, w.Code)
		}
	}
	r := httptest.NewRequest(http.MethodGet, "/panel/api/overview", nil)
	r.Header.Set("Authorization", "Bearer test-key")
	w := httptest.NewRecorder()
	p.ServeHTTP(w, r)
	var overview map[string]json.RawMessage
	if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &overview) != nil {
		t.Fatalf("overview unavailable: %d", w.Code)
	}
	if _, exists := overview["loomy_accounts"]; exists {
		t.Fatal("retired accounts still in overview")
	}
}
