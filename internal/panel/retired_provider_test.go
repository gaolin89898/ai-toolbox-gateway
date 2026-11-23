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
	for _, tc := range []struct{ method, path string }{
		{http.MethodPost, "/panel/api/doubao/checkin_all"},
		{http.MethodPost, "/panel/api/doubao/accounts/d1/tasks/claim"},
	} {
		r := httptest.NewRequest(tc.method, tc.path, strings.NewReader("{}"))
		r.Header.Set("Authorization", "Bearer test-key")
		w := httptest.NewRecorder()
		p.ServeHTTP(w, r)
		if w.Code != http.StatusNotFound {
			t.Errorf("retired endpoint %s %s returned %d", tc.method, tc.path, w.Code)
		}
	}
}

func TestRetiredQoderCheckinEndpoints(t *testing.T) {
	store, err := qoder.Open(filepath.Join(t.TempDir(), "accounts.json"), nil)
	if err != nil {
		t.Fatal(err)
	}
	p := New(Config{AccountsOnly: true, APIKey: "test-key", Qoder: store})
	defer p.Close()
	for _, path := range []string{"checkin_all", "accounts/qoder-1/checkin"} {
		r := httptest.NewRequest(http.MethodPost, "/panel/api/qoder/"+path, nil)
		r.Header.Set("Authorization", "Bearer test-key")
		w := httptest.NewRecorder()
		p.ServeHTTP(w, r)
		if w.Code != http.StatusNotFound {
			t.Errorf("retired Qoder endpoint %s returned %d", path, w.Code)
		}
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
