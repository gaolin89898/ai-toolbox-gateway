package doubaowork

import (
	"encoding/json"
	"testing"
)

func TestWorkEffortLevels(t *testing.T) {
	cases := map[string]int{"low": 3, "medium": 4, "high": 5, "xhigh": 6, "max": 7, "": 5}
	for in, want := range cases {
		if got := WorkEffort("person-kimi-k3-261111", in); got != want {
			t.Fatalf("WorkEffort(%q) = %d, want %d", in, got, want)
		}
	}
	if got := WorkEffort("person-deepseek-v4-1-flash-261017", ""); got != 7 {
		t.Fatalf("default effort for deepseek flash = %d, want 7", got)
	}
}

func TestWorkPayloadShape(t *testing.T) {
	payload := WorkPayload("你好", "person-deepseek-v4-1-flash-261017", 3, "635978201961726")
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	var back map[string]any
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatal(err)
	}
	option := back["option"].(map[string]any)
	initExt := option["conversation_init_ext"].(map[string]any)
	if initExt["model_item_key"] != "person-deepseek-v4-1-flash-261017" {
		t.Fatalf("model key missing in conversation_init_ext")
	}
	modelConfig := option["model_config"].(map[string]any)
	if modelConfig["model_item_key"] != "person-deepseek-v4-1-flash-261017" {
		t.Fatalf("model key missing in model_config")
	}
	if modelConfig["model_extra_params"] == nil {
		t.Fatalf("model_extra_params missing for catalog model")
	}
	if _, ok := option["general_task_param"].(map[string]any); !ok {
		t.Fatalf("general_task_param must be an object")
	}
	clientMeta := back["client_meta"].(map[string]any)
	if clientMeta["bot_id"] != WorkBotID {
		t.Fatalf("bot id mismatch")
	}
}
