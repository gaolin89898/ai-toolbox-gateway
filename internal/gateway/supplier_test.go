package gateway

import (
	"context"
	"net/http"
	"testing"
)

type forbiddenSupplierSource struct{ t *testing.T }

func (s forbiddenSupplierSource) GatewayAccounts() []Account {
	s.t.Error("queried another application's accounts")
	return nil
}
func (s forbiddenSupplierSource) GatewayCredential(context.Context, string) (Credential, error) {
	s.t.Error("queried another application's credentials")
	return Credential{}, nil
}

func TestSelectedSupplierCatalogDoesNotQueryOtherApplications(t *testing.T) {
	r := testRouter(t, "qoder", func(w http.ResponseWriter, req *http.Request) {
		qoderCatalog(w)
	}, Account{ID: "a"})
	r.Sources["minimax"] = forbiddenSupplierSource{t}
	models := r.ModelsForSupplier(context.Background(), "qoder")
	if len(models) != 1 || str(models[0]["id"]) != "qoder/chat" {
		t.Fatalf("wrong catalog: %v", models)
	}
	if models := r.ModelsForSupplier(context.Background(), "workbuddy-cn"); len(models) != 0 {
		t.Fatal("native provider catalog leaked into WorkBuddy selection")
	}
}

func TestSupplierModelBoundariesAndBareNames(t *testing.T) {
	for _, tc := range []struct{ supplier, model, want string }{
		{"auto", "cn:m", "cn:m"}, {"auto", "qoder/chat", "qoder/chat"},
		{"workbuddy-cn", "m", "cn:m"}, {"workbuddy-cn", "cn:m", "cn:m"},
		{"workbuddy-global", "m", "global:m"}, {"qoder", "chat", "qoder/chat"},
		{"qoder", "qoder/chat", "qoder/chat"}, {"trae", "minimax/chat", ""},
		{"workbuddy-cn", "global:m", ""}, {"qoder", "cn:m", ""},
		{"workbuddy-global", "qoder/chat", ""}, {"qoder", "", ""},
	} {
		got, err := ResolveSupplierModel(tc.supplier, tc.model)
		if got != tc.want || (err != nil) != (tc.want == "") {
			t.Fatalf("%+v: got %q err=%v", tc, got, err)
		}
	}
}

func TestRetiredSupplierCompatibility(t *testing.T) {
	if got, err := NormalizeSupplier(" Loomy "); err != nil || got != "auto" {
		t.Fatalf("legacy supplier failed migration: %q %v", got, err)
	}
	if got, err := NormalizeSupplier(" Doubao "); err != nil || got != "auto" {
		t.Fatalf("legacy doubao supplier failed migration: %q %v", got, err)
	}
	if _, _, ok := Split("loomy/chat"); ok {
		t.Fatal("retired provider still routes")
	}
	if _, _, ok := Split("doubao/chat"); ok {
		t.Fatal("retired doubao provider still routes")
	}
	for _, supplier := range append([]string{"auto"}, SupplierPriority...) {
		if supplier == "loomy" || supplier == "doubao" {
			t.Fatal("retired provider still in catalog priority")
		}
		for _, retired := range []string{"loomy/chat", "doubao/chat"} {
			if model, err := ResolveSupplierModel(supplier, retired); err == nil || model != "" {
				t.Fatalf("retired model accepted by %s: %q %v", supplier, model, err)
			}
		}
	}
}
