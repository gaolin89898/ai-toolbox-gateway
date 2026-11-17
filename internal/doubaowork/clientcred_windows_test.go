//go:build windows

package doubaowork

import (
	"encoding/json"
	"testing"
)

func TestActiveClientProfileUsesSelectedAccount(t *testing.T) {
	for _, selected := range []string{"Default", "Profile 2", "Profile 4"} {
		t.Run(selected, func(t *testing.T) {
			identities := map[string]string{"Default": "1408909182512996", "Profile 2": "1202244761948297", "Profile 4": "2416105676342075"}
			cache := map[string]any{}
			for name, uid := range identities {
				cache[name] = map[string]any{"saman": map[string]any{"user_id": uid}}
			}
			raw, _ := json.Marshal(map[string]any{"profile": map[string]any{"last_used": selected, "info_cache": cache}})
			profile, uid, err := activeClientProfile(raw)
			if err != nil || profile != selected || uid != identities[selected] {
				t.Fatalf("selected profile or identity changed: %s %s %v", profile, uid, err)
			}
		})
	}
}

func TestActiveClientProfileNeverFallsBackToAnotherAccount(t *testing.T) {
	for _, raw := range []string{
		`{`,
		`{"profile":{"info_cache":{"Default":{"saman":{"user_id":"123"}}}}}`,
		`{"profile":{"last_used":"Profile 2","info_cache":{"Default":{"saman":{"user_id":"123"}}}}}`,
		`{"profile":{"last_used":"Default","info_cache":{"Default":{},"Profile 4":{"saman":{"user_id":"456"}}}}}`,
		`{"profile":{"last_used":"../Default","info_cache":{"../Default":{"saman":{"user_id":"123"}}}}}`,
		`{"profile":{"last_used":"Profile 2/../Default","info_cache":{"Profile 2/../Default":{"saman":{"user_id":"123"}}}}}`,
		`{"profile":{"last_used":"Default","info_cache":{"Default":{"saman":{"user_id":"0"}}}}}`,
		`{"profile":{"last_used":"Default","info_cache":{"Default":{"saman":{"user_id":"invalid"}}}}}`,
	} {
		if profile, uid, err := activeClientProfile([]byte(raw)); err == nil || profile != "" || uid != "" {
			t.Fatal("unconfirmed account accepted")
		}
	}
}
