package doubao

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestTasksParsesV3TasksAndMilestones(t *testing.T) {
	body := `{"code":0,"data":{"campaign_code":"pc_incentive_task_mvp_v3","progress":{"total_amount":4100},"tasks":[
{"campaign_code":"daily_signin","title":"每日签到","task_kind":"daily_signin","task_status":3,"daily_checkin":{"checked_in_today":false,"last_checkin_date":"2000-01-01","continuous_days":2,"total_days":15,"reward":{"reward_amount":"200","reward_type":"quota_grant"}}},
{"campaign_code":"select_work_fields","task_id":"server-instance","title":"选择工作领域领取奖励","task_kind":"chat_activity","task_status":"2","reward_summaries":[{"reward_amount":"100","reward_type":"quota_grant"},{"reward_amount":3,"reward_type":"pro_trial_days"}]},
{"campaign_code":"document","task_kind":"chat_activity","task_status":4,"description":"项目方案","recommend_prompts":["invalid","{\"show_prompt\":\"工作报告\",\"prompt\":\"生成工作报告\"}"],"progress":{"current":1,"target":2}},
{"campaign_code":"unknown","task_kind":"chat_activity","task_status":0,"reward_mode":2},
{"campaign_code":"select_work_fields","task_status":1}
],"milestones":[
{"campaign_code":"points_5000","task_kind":"points_milestone","task_status":1,"reward_mode":2,"milestone_threshold":5000,"reward_summaries":[{"reward_amount":1300,"reward_type":"quota_grant"}]},
{"campaign_code":"points_2000","task_kind":"points_milestone","task_status":1,"reward_mode":2,"milestone_threshold":2000},
{"campaign_code":"points_500","task_kind":"points_milestone","task_status":3,"reward_mode":2,"milestone_threshold":500}
]}}`
	c := checkinUpstream(t, "", body)
	list, err := c.Tasks(context.Background(), &Account{Cookie: "sessionid=private"})
	if err != nil {
		t.Fatal(err)
	}
	if len(list.Tasks) != 7 || list.Points != 4100 || list.Continuous != 2 || list.TotalDays != 15 {
		t.Fatalf("unexpected list: %+v", list)
	}
	if daily := list.Tasks[0]; daily.Claimed || daily.Claimable || daily.Credit != 200 {
		t.Fatalf("stale checkin must remain pending: %+v", daily)
	}
	if fields := list.Tasks[1]; !fields.Claimable || fields.Credit != 100 || fields.Title != "选择工作领域领取奖励" {
		t.Fatalf("bad claimable task: %+v", fields)
	}
	if doc := list.Tasks[2]; doc.Status != "in_progress" || len(doc.Prompts) != 1 || doc.Prompts[0].Prompt != "生成工作报告" || doc.Current != 1 || doc.Target != 2 {
		t.Fatalf("bad document task: %+v", doc)
	}
	if !list.Tasks[3].Locked || list.Tasks[3].Claimable || !list.Tasks[4].Locked || list.Tasks[4].Claimable || !list.Tasks[5].Claimable || !list.Tasks[6].Claimed {
		t.Fatalf("bad milestone/unknown gating: %+v", list.Tasks)
	}
	raw, _ := json.Marshal(list)
	for _, secret := range []string{"sessionid", "server-instance", "task_id", "cookie"} {
		if strings.Contains(string(raw), secret) {
			t.Fatalf("private field exposed: %s", secret)
		}
	}
}

func TestTasksCheckinRequiresTodaysDate(t *testing.T) {
	for _, signed := range []bool{false, true} {
		body := `{"code":0,"data":{"tasks":[{"campaign_code":"daily_signin","task_kind":"daily_signin","task_status":1,"daily_checkin":{"checked_in_today":SIGNED,"last_checkin_date":"DATE","reward":{"reward_amount":400}}}]}}`
		body = strings.ReplaceAll(body, "DATE", CheckinDay(time.Now()))
		body = strings.ReplaceAll(body, "SIGNED", map[bool]string{true: "true", false: "false"}[signed])
		list, err := checkinUpstream(t, "", body).Tasks(context.Background(), &Account{})
		if err != nil || list.Tasks[0].Claimed != signed || list.Tasks[0].Credit != 400 {
			t.Fatalf("bad daily state: %+v %v", list, err)
		}
	}
}

func TestTasksRejectInvalidResponse(t *testing.T) {
	for _, body := range []string{`not-json`, `{}`, `{"code":7,"msg":"sessionid=private"}`, `{"code":0}`, `{"code":0,"data":{}}`, `{"code":0,"data":{"campaign_code":"other","tasks":[]}}`} {
		if _, err := checkinUpstream(t, "", body).Tasks(context.Background(), &Account{}); err == nil || strings.Contains(err.Error(), "sessionid") {
			t.Fatalf("invalid response accepted or leaked: %s %v", body, err)
		}
	}
}

func taskTestStore(t *testing.T, handler http.HandlerFunc) *Store {
	t.Helper()
	upstream := httptest.NewServer(handler)
	t.Cleanup(upstream.Close)
	s, err := Open(filepath.Join(t.TempDir(), "accounts.json"), &Client{HTTP: upstream.Client(), Base: upstream.URL})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.commit([]Account{{ID: "account", Cookie: "sessionid=private"}}); err != nil {
		t.Fatal(err)
	}
	return s
}

func TestClaimTaskUsesFreshServerInstanceAndIsIdempotent(t *testing.T) {
	var claimed atomic.Bool
	var calls atomic.Int32
	s := taskTestStore(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Cookie") != "sessionid=private" || r.Method != "POST" || r.URL.Query().Get("aid") != "497858" {
			t.Error("missing authenticated web request context")
		}
		switch r.URL.Path {
		case "/alice/ug/campaign/v3/detail/":
			state := 2
			if claimed.Load() {
				state = 3
			}
			json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": map[string]any{"tasks": []any{map[string]any{"campaign_code": "select_work_fields", "task_id": "current-instance", "task_kind": "chat_activity", "task_status": state}}}})
		case "/alice/ug/campaign/item/reward/claim/":
			calls.Add(1)
			var body map[string]string
			json.NewDecoder(r.Body).Decode(&body)
			if len(body) != 1 || body["task_id"] != "current-instance" {
				t.Errorf("incorrect instance claim: %+v", body)
			}
			claimed.Store(true)
			w.Write([]byte(`{"code":0,"data":{"task_status":3}}`))
		case "/alice/commerce/sale/subscription/quota/summary/":
			w.WriteHeader(503) // A balance service outage must not undo a verified claim.
		default:
			t.Errorf("unexpected request: %s", r.URL.Path)
			w.WriteHeader(404)
		}
	})
	var wg sync.WaitGroup
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			list, err := s.ClaimTask(context.Background(), "account", "select_work_fields")
			if err != nil || !list.Tasks[0].Claimed {
				t.Errorf("claim failed: %+v %v", list, err)
			}
		}()
	}
	wg.Wait()
	if calls.Load() != 1 {
		t.Fatalf("reward claimed %d times", calls.Load())
	}
}

func TestClaimTaskBlocksPendingAndLockedTasks(t *testing.T) {
	var calls atomic.Int32
	s := taskTestStore(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/alice/ug/campaign/v3/detail/" {
			calls.Add(1)
		}
		w.Write([]byte(`{"code":0,"data":{"progress":{"total_amount":200},"tasks":[{"campaign_code":"pending","task_status":1},{"campaign_code":"daily","task_kind":"daily_signin","task_status":2}],"milestones":[{"campaign_code":"locked","task_kind":"points_milestone","task_status":1,"milestone_threshold":500,"reward_mode":2}]}}`))
	})
	for _, code := range []string{"pending", "daily", "locked"} {
		if _, err := s.ClaimTask(context.Background(), "account", code); !errors.Is(err, ErrTaskNotReady) {
			t.Fatalf("pending task %s accepted: %v", code, err)
		}
	}
	if calls.Load() != 0 {
		t.Fatal("blocked task reached claim endpoint")
	}
	if _, err := s.ClaimTask(context.Background(), "missing", "pending"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing account: %v", err)
	}
}

func TestTasksDisabledAccountDoesNotCallUpstream(t *testing.T) {
	s := taskTestStore(t, func(w http.ResponseWriter, r *http.Request) { t.Error("disabled account reached upstream") })
	if _, err := s.Action(context.Background(), "account", "disable"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Tasks(context.Background(), "account"); err == nil {
		t.Fatal("disabled tasks query succeeded")
	}
	if _, err := s.ClaimTask(context.Background(), "account", "task"); err == nil {
		t.Fatal("disabled task claim succeeded")
	}
}

func TestClaimTaskMilestoneAndDailyPayloads(t *testing.T) {
	for _, test := range []struct {
		kind, code string
		mode       taskEnum
		wantID     bool
	}{
		{"points_milestone", "points_500", 2, false},
		{"chat_activity", "pc_task_complete_work_task_daily", 1, true},
		{"chat_activity", "direct_claim", 2, false},
	} {
		t.Run(test.code, func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var body map[string]string
				json.NewDecoder(r.Body).Decode(&body)
				if body["campaign_group_code"] != CheckinGroupCode || body["campaign_item_code"] != test.code || (body["task_id"] == "server-instance") != test.wantID {
					t.Errorf("bad claim payload: %+v", body)
				}
				w.Write([]byte(`{"code":0,"data":{"task_status":3}}`))
			}))
			defer upstream.Close()
			c := &Client{HTTP: upstream.Client(), Base: upstream.URL}
			if err := c.claimTask(context.Background(), &Account{}, Task{Kind: test.kind, Code: test.code, record: taskRecord{ID: "server-instance", Mode: test.mode}}); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestClaimTaskRejectsBusinessErrorsAndUnconfirmedReadback(t *testing.T) {
	for _, response := range []string{`{"code":710010702}`, `{"code":0,"data":{"biz_error":{"code":9,"message":"sessionid=private"}}}`, `{"code":0}`, `invalid`, `{"code":0,"data":{"task_status":3}}`} {
		s := taskTestStore(t, func(w http.ResponseWriter, r *http.Request) {
			if strings.HasSuffix(r.URL.Path, "/claim/") {
				w.Write([]byte(response))
			} else {
				w.Write([]byte(`{"code":0,"data":{"tasks":[{"campaign_code":"task","task_id":"instance","task_status":2}]}}`))
			}
		})
		if _, err := s.ClaimTask(context.Background(), "account", "task"); err == nil || strings.Contains(err.Error(), "sessionid") {
			t.Fatalf("unconfirmed claim accepted or leaked: %s %v", response, err)
		}
	}
}
