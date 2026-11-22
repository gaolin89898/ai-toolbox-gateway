// Package antigravity manages Google Antigravity OAuth accounts. It does not
// start or modify the desktop application and never returns tokens to the panel.
package antigravity

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/linguo2625469/workbuddy2api-panel/internal/googlehttp"
)

const BaseURL = "https://daily-cloudcode-pa.googleapis.com"
const UserAgent = "antigravity/hub/2.29.1 (aidev_client; os_type=windows; arch=amd64; cl=1015941558)"

type Client struct {
	OAuthClientID, OAuthClientSecret string
	HTTP                             *http.Client
	Base, TokenURL, UserInfoURL      string
}

func NewClient() *Client {
	id, secret := strings.TrimSpace(os.Getenv("WB2A_ANTIGRAVITY_CLIENT_ID")), strings.TrimSpace(os.Getenv("WB2A_ANTIGRAVITY_CLIENT_SECRET"))
	if id == "" && secret == "" {
		id, secret = defaultClientID, defaultClientSecret
	}
	return &Client{HTTP: &http.Client{Transport: googlehttp.NewTransport(), Timeout: 25 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}, OAuthClientID: id, OAuthClientSecret: secret, Base: BaseURL, TokenURL: "https://oauth2.googleapis.com/token", UserInfoURL: "https://www.googleapis.com/oauth2/v2/userinfo"}
}

func (c *Client) request(ctx context.Context, method, endpoint, token string, body []byte, contentType string, result any) error {
	req, err := http.NewRequestWithContext(ctx, method, endpoint, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("Antigravity 请求地址无效")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("User-Agent", UserAgent)
	resp, err := c.HTTP.Do(req)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return fmt.Errorf("Antigravity 请求超时，请检查后端的 Google 服务网络连接")
		}
		return fmt.Errorf("Antigravity 连接失败，请检查 Google 服务网络连接")
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("Antigravity 上游拒绝请求（HTTP %d）", resp.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, (4<<20)+1))
	if err != nil || len(raw) > 4<<20 || json.Unmarshal(raw, result) != nil {
		return fmt.Errorf("Antigravity 响应格式无效")
	}
	return nil
}

func (c *Client) Refresh(ctx context.Context, a *Account) error {
	if a.AccessToken != "" && (time.Until(a.ExpiresAt) > 2*time.Minute || a.ExpiresAt.IsZero() && a.RefreshToken == "") {
		return nil
	}
	if a.RefreshToken == "" {
		return fmt.Errorf("Antigravity 登录已过期，请更新 OAuth 凭证")
	}
	if c.OAuthClientID == "" || c.OAuthClientSecret == "" {
		return fmt.Errorf("Antigravity OAuth 刷新需要配置 WB2A_ANTIGRAVITY_CLIENT_ID 和 WB2A_ANTIGRAVITY_CLIENT_SECRET")
	}
	form := url.Values{"client_id": {c.OAuthClientID}, "client_secret": {c.OAuthClientSecret}, "grant_type": {"refresh_token"}, "refresh_token": {a.RefreshToken}}
	var reply struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		ExpiresIn    int    `json:"expires_in"`
	}
	if err := c.request(ctx, "POST", c.TokenURL, "", []byte(form.Encode()), "application/x-www-form-urlencoded", &reply); err != nil {
		return err
	}
	if !validToken(reply.AccessToken) || reply.ExpiresIn <= 0 {
		return fmt.Errorf("Antigravity OAuth 刷新未返回有效令牌")
	}
	a.AccessToken = reply.AccessToken
	if reply.RefreshToken != "" {
		a.RefreshToken = reply.RefreshToken
	}
	a.ExpiresAt = time.Now().Add(time.Duration(reply.ExpiresIn) * time.Second)
	return nil
}

func (c *Client) Inspect(ctx context.Context, a *Account) error {
	if err := c.Refresh(ctx, a); err != nil {
		return err
	}
	var identity struct {
		Email string `json:"email"`
	}
	if err := c.request(ctx, "GET", c.UserInfoURL, a.AccessToken, nil, "application/json", &identity); err != nil {
		return err
	}
	if identity.Email == "" {
		return fmt.Errorf("Antigravity 授权未返回账号邮箱")
	}
	if a.Email != "" && !strings.EqualFold(a.Email, identity.Email) {
		return fmt.Errorf("Antigravity 凭证与填写的邮箱不一致")
	}
	a.Email = identity.Email
	var reply struct {
		Project any `json:"cloudaicompanionProject"`
		Error   any `json:"error"`
		Tier    struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"paidTier"`
	}
	body := []byte(`{"metadata":{"ideType":"ANTIGRAVITY","ideName":"antigravity","ideVersion":"2.29.1","platform":"WINDOWS_AMD64"}}`)
	if err := c.request(ctx, "POST", c.Base+"/v1internal:loadCodeAssist", a.AccessToken, body, "application/json", &reply); err != nil {
		return err
	}
	if reply.Error != nil {
		return fmt.Errorf("Antigravity 账号尚未获得服务授权，请先在官方客户端完成登录")
	}
	project, _ := reply.Project.(string)
	if m, ok := reply.Project.(map[string]any); ok {
		project, _ = m["id"].(string)
	}
	if project != "" {
		a.ProjectID = project
	}
	if a.ProjectID == "" {
		return fmt.Errorf("Antigravity 未返回项目 ID，请先在官方客户端完成初始化，或填写授权文件中的 project_id")
	}
	a.Tier = reply.Tier.Name
	if a.Tier == "" {
		a.Tier = reply.Tier.ID
	}
	return nil
}

// QuotaBucket is one allowance window (weekly or five-hour) inside a model group.
type QuotaBucket struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Window    string    `json:"window"`
	Remaining float64   `json:"remaining"`
	ResetAt   time.Time `json:"reset_at,omitempty"`
}

// QuotaGroup is a model family that shares weekly and five-hour limits.
type QuotaGroup struct {
	Name        string        `json:"name"`
	Description string        `json:"description,omitempty"`
	Buckets     []QuotaBucket `json:"buckets"`
}

// Quotas reads the weekly and five-hour allowance summary from Code Assist.
func (c *Client) Quotas(ctx context.Context, a *Account) ([]QuotaGroup, error) {
	if a.ProjectID == "" {
		return nil, fmt.Errorf("Antigravity 账号缺少项目 ID，无法查询额度")
	}
	if err := c.Refresh(ctx, a); err != nil {
		return nil, err
	}
	body := []byte("{\"project\":\"" + a.ProjectID + "\"}")
	var reply struct {
		Groups []struct {
			DisplayName string `json:"displayName"`
			Description string `json:"description"`
			Buckets     []struct {
				BucketID          string   `json:"bucketId"`
				DisplayName       string   `json:"displayName"`
				Window            string   `json:"window"`
				ResetTime         string   `json:"resetTime"`
				RemainingFraction *float64 `json:"remainingFraction"`
			} `json:"buckets"`
		} `json:"groups"`
	}
	if err := c.request(ctx, "POST", c.Base+"/v1internal:retrieveUserQuotaSummary", a.AccessToken, body, "application/json", &reply); err != nil {
		return nil, err
	}
	out := []QuotaGroup{}
	for _, g := range reply.Groups {
		group := QuotaGroup{Name: g.DisplayName, Description: g.Description}
		for _, b := range g.Buckets {
			if b.RemainingFraction == nil {
				continue
			}
			reset, _ := time.Parse(time.RFC3339, b.ResetTime)
			group.Buckets = append(group.Buckets, QuotaBucket{ID: b.BucketID, Name: b.DisplayName, Window: b.Window, Remaining: *b.RemainingFraction, ResetAt: reset})
		}
		if len(group.Buckets) > 0 {
			out = append(out, group)
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("Antigravity 额度响应未包含可识别的模型配额")
	}
	return out, nil
}

func validToken(v string) bool {
	return v != "" && len(v) <= 32768 && !strings.ContainsAny(v, "\r\n\x00") && strings.TrimSpace(v) == v
}
