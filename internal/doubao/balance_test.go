package doubao

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const quotaFixture = `{"code":0,"data":{"current_subscription":{"display":{"short_name":"免费体验"}},"incentive_quota_section":{"window_limit_groups":[{"feature_group":"doubao_activity_reward_quota","feature_group_name":"活动赠送额度","window_limits":[{"total_amount":"200","used_amount":"0","window_type":3,"end_time":4070908800000}]}]},"window_limit_section":{"window_limit_groups":[{"feature_group":"general","window_limits":[{"total_amount":"500","used_amount":"0","window_type":2,"start_time":0,"end_time":0}]}]}}}`

const standardQuotaFixture = `{"code":0,"data":{"current_subscription":{"display":{"short_name":"标准套餐"}},"incentive_quota_section":{"window_limit_groups":[{"feature_group":"doubao_activity_reward_quota","feature_group_name":"活动赠送额度","window_limits":[{"total_amount":"4,100","used_amount":"0","window_type":3,"end_time":4070908800000}]}]},"window_limit_section":{"window_limit_groups":[{"feature_group":"monthly_quota","feature_group_name":"","window_limits":[{"total_amount":"2,100","used_amount":"0","window_type":4,"end_time":4070941200000}]}]}}}`

func TestStandardPlanAvailableBalance(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		want       float64
	}{
		{"standard plan plus gifts", standardQuotaFixture, 6200},
		{"usage deducted", strings.Replace(standardQuotaFixture, `"used_amount":"0"`, `"used_amount":"75.5"`, 1), 6124.5},
		{"expired plan", strings.Replace(standardQuotaFixture, `4070941200000`, `1000`, 1), 4100},
		{"rolling limit is separate", quotaFixture, 200},
	} {
		t.Run(tc.name, func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, tc.body) }))
			defer upstream.Close()
			client := &Client{HTTP: upstream.Client(), Base: upstream.URL}
			balance, err := client.Balance(context.Background(), &Account{Cookie: "sessionid=private-session"})
			if err != nil {
				t.Fatal(err)
			}
			got := status(Account{Credits: balance.Credits, Total: balance.Total, Plan: balance.Plan, Quotas: balance.Quotas, BalanceKnown: balance.Known})
			if got.AvailableCredits != tc.want || !got.BalanceKnown {
				t.Fatalf("incorrect available allowance: %+v", got)
			}
			if got.Plan == "标准套餐" && (got.AvailableTotal != 6200 || got.Quotas[1].Name != "套餐额度" || got.Quotas[1].WindowType != 4 || got.Quotas[1].Feature != "monthly_quota") {
				t.Fatalf("lost plan allowance details: %+v", got)
			}
		})
	}
}

func TestAvailableBalanceDoesNotDoubleCountWindows(t *testing.T) {
	credits, total := availableBalance(4100, 4100, []Quota{
		{Kind: "subscription", Feature: "monthly_quota", WindowType: 4, Remaining: 2100, Total: 2100},
		{Kind: "subscription", Feature: "monthly_quota", WindowType: 4, Remaining: 2000, Total: 2100},
		{Kind: "subscription", Feature: "monthly_quota", WindowType: 2, Remaining: 500, Total: 500},
	})
	if credits != 6100 || total != 6200 {
		t.Fatalf("duplicate windows were added: remaining=%v total=%v", credits, total)
	}
}

func TestBalanceUsesSubscriptionQuotas(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.URL.Path != "/alice/commerce/sale/subscription/quota/summary/" {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		q := r.URL.Query()
		if q.Get("aid") != "497858" || q.Get("device_platform") != "web" || q.Get("web_platform") != "browser" || q.Get("version_code") != "20800" {
			t.Error("missing web parameters needed for exact allowance amounts")
		}
		var body map[string]any
		if json.NewDecoder(r.Body).Decode(&body) != nil || body["product_line"] != "membership" || len(body) != 1 {
			t.Error("unexpected quota request body")
		}
		cookie, err := r.Cookie("sessionid")
		if err != nil || cookie.Value != "private-session" || r.Header.Get("Agw-Js-Conv") != "str" || r.Header.Get("Origin") != BaseURL || r.Header.Get("Referer") != BaseURL+"/chat/" {
			t.Error("missing authenticated web headers")
		}
		fmt.Fprint(w, quotaFixture)
	}))
	defer upstream.Close()
	client := &Client{HTTP: upstream.Client(), Base: upstream.URL}
	value, err := client.Balance(context.Background(), &Account{Cookie: "sessionid=private-session"})
	if err != nil || !value.Known || value.Credits != 200 || value.Total != 200 || value.Plan != "免费体验" || len(value.Quotas) != 2 {
		t.Fatalf("activity quota must remain separate from 500 weekly quota: %+v %v", value, err)
	}
	activity, weekly := value.Quotas[0], value.Quotas[1]
	if activity.Used != 0 || activity.Remaining != 200 || activity.ExpiresAt == nil || activity.ExpiresAt.UnixMilli() != 4070908800000 || weekly.Name != "近 7 天" || weekly.Remaining != 500 || weekly.StartsAt != nil || weekly.ExpiresAt != nil {
		t.Fatalf("lost allowance details: %+v", value.Quotas)
	}
}

func TestBalanceParsingAndFailures(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		http       int
		want       float64
		fail       bool
	}{
		{name: "thousands separator", body: strings.Replace(quotaFixture, `"200"`, `"3,900"`, 1), want: 3900},
		{name: "remaining subtracts usage", body: strings.Replace(quotaFixture, `"used_amount":"0"`, `"used_amount":"25.5"`, 1), want: 174.5},
		{name: "real zero", body: strings.Replace(quotaFixture, `"used_amount":"0"`, `"used_amount":"200"`, 1)},
		{name: "overage", body: strings.Replace(quotaFixture, `"used_amount":"0"`, `"used_amount":"201"`, 1)},
		{name: "expired", body: strings.Replace(quotaFixture, `4070908800000`, `1000`, 1)},
		{name: "numeric values", body: strings.Replace(quotaFixture, `"total_amount":"200"`, `"total_amount":200`, 1), want: 200},
		{name: "empty allocations", body: `{"code":0,"data":{"incentive_quota_section":{"window_limit_groups":[]}}}`},
		{name: "percentages only", body: strings.Replace(quotaFixture, `"total_amount":"200",`, `"used_percent":0,`, 1), fail: true},
		{name: "missing used amount", body: strings.Replace(quotaFixture, `"used_amount":"0",`, ``, 1), fail: true},
		{name: "null amount", body: strings.Replace(quotaFixture, `"total_amount":"200"`, `"total_amount":null`, 1), fail: true},
		{name: "invalid amount", body: strings.Replace(quotaFixture, `"200"`, `"private-session"`, 1), fail: true},
		{name: "negative amount", body: strings.Replace(quotaFixture, `"200"`, `"-1"`, 1), fail: true},
		{name: "bad grouping", body: strings.Replace(quotaFixture, `"200"`, `"2,00"`, 1), fail: true},
		{name: "missing code", body: `{"data":{}}`, fail: true},
		{name: "missing data", body: `{"code":0}`, fail: true},
		{name: "missing details", body: `{"code":0,"data":{"incentive_quota_section":{}}}`, fail: true},
		{name: "upstream rejection", body: `{"code":13,"msg":"private-session"}`, fail: true},
		{name: "malformed JSON", body: `private-session`, fail: true},
		{name: "HTTP denial", http: 403, body: `private-session`, fail: true},
		{name: "oversized", body: strings.Repeat(" ", (1<<20)+1), fail: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if tc.http != 0 {
					w.WriteHeader(tc.http)
				}
				fmt.Fprint(w, tc.body)
			}))
			defer upstream.Close()
			c := &Client{HTTP: upstream.Client(), Base: upstream.URL}
			value, err := c.Balance(context.Background(), &Account{Cookie: "sessionid=private-session"})
			if (err != nil) != tc.fail || err == nil && (!value.Known || value.Credits != tc.want) {
				t.Fatalf("unexpected quota result: %+v %v", value, err)
			}
			if err != nil && strings.Contains(err.Error(), "private-session") {
				t.Fatal("upstream response leaked credentials")
			}
		})
	}
}
