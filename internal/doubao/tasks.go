package doubao

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"
)

var ErrTaskNotReady = errors.New("任务尚未达成，暂不能领取奖励")

type taskEnum int

func (v *taskEnum) UnmarshalJSON(raw []byte) error {
	// The web API returns numeric enums; accept their string representation too.
	var number json.Number
	if err := json.Unmarshal(raw, &number); err != nil {
		return err
	}
	n, err := number.Int64()
	*v = taskEnum(n)
	return err
}

type taskReward struct {
	Amount json.RawMessage `json:"reward_amount"`
	Type   string          `json:"reward_type"`
}

type taskRecord struct {
	Code      string       `json:"campaign_code"`
	Group     string       `json:"campaign_group_code"`
	ID        string       `json:"task_id"`
	Kind      string       `json:"task_kind"`
	Title     string       `json:"title"`
	Subtitle  string       `json:"subtitle"`
	Desc      string       `json:"description"`
	Status    taskEnum     `json:"task_status"`
	Mode      taskEnum     `json:"reward_mode"`
	Rewards   []taskReward `json:"reward_summaries"`
	Threshold float64      `json:"milestone_threshold"`
	Progress  *struct {
		Current float64 `json:"current"`
		Target  float64 `json:"target"`
	} `json:"progress"`
	Daily *struct {
		CheckedIn  bool       `json:"checked_in_today"`
		Date       string     `json:"last_checkin_date"`
		Continuous int        `json:"continuous_days"`
		Total      int        `json:"total_days"`
		Reward     taskReward `json:"reward"`
	} `json:"daily_checkin"`
	Prompts []string `json:"recommend_prompts"`
}

// Task exposes only task data, never account credentials or upstream URLs.
type Task struct {
	Code        string       `json:"task_code"`
	Title       string       `json:"title"`
	Description string       `json:"description"`
	Kind        string       `json:"kind"`
	Credit      float64      `json:"credit"`
	Current     float64      `json:"current"`
	Target      float64      `json:"target"`
	Claimed     bool         `json:"claimed"`
	Claimable   bool         `json:"claimable"`
	Locked      bool         `json:"locked"`
	Status      string       `json:"status"`
	Prompts     []TaskPrompt `json:"prompts"`
	record      taskRecord
}

type TaskPrompt struct {
	Title  string `json:"show_prompt"`
	Prompt string `json:"prompt"`
}

type TaskList struct {
	Tasks      []Task  `json:"tasks"`
	Points     float64 `json:"total_amount"`
	Group      string  `json:"campaign_group_code"`
	Continuous int     `json:"continuous_days"`
	TotalDays  int     `json:"total_days"`
}

func taskStatus(value taskEnum) string {
	return map[taskEnum]string{0: "unknown", 1: "pending", 2: "claimable", 3: "claimed", 4: "in_progress"}[value]
}

func (c *Client) Tasks(ctx context.Context, a *Account) (TaskList, error) {
	raw, err := c.ugPost(ctx, a, "/alice/ug/campaign/v3/detail/", `{"campaign_group_code":"`+CheckinGroupCode+`"}`)
	if err != nil {
		return TaskList{}, err
	}
	var root struct {
		Code *int `json:"code"`
		Data *struct {
			Campaign string `json:"campaign_code"`
			Progress struct {
				Total float64 `json:"total_amount"`
			} `json:"progress"`
			Tasks      []taskRecord `json:"tasks"`
			Milestones []taskRecord `json:"milestones"`
		} `json:"data"`
	}
	if json.Unmarshal(raw, &root) != nil || root.Code == nil {
		return TaskList{}, fmt.Errorf("豆包任务响应格式无效")
	}
	if *root.Code != 0 {
		return TaskList{}, fmt.Errorf("豆包任务查询被平台拒绝（代码 %d），请稍后刷新或重新授权", *root.Code)
	}
	if root.Data == nil || root.Data.Tasks == nil || root.Data.Campaign != "" && root.Data.Campaign != CheckinGroupCode {
		return TaskList{}, fmt.Errorf("豆包任务响应缺少当前活动数据")
	}
	list := TaskList{Tasks: []Task{}, Points: root.Data.Progress.Total, Group: CheckinGroupCode}
	seen := map[string]bool{}
	for _, record := range append(root.Data.Tasks, root.Data.Milestones...) {
		record.Code = strings.TrimSpace(record.Code)
		if record.Code == "" || seen[record.Code] || record.Group != "" && record.Group != CheckinGroupCode {
			continue
		}
		seen[record.Code] = true
		task := Task{Code: record.Code, Title: record.Title, Description: record.Desc, Kind: record.Kind, Status: taskStatus(record.Status), Claimed: record.Status == 3, Claimable: record.Status == 2, Prompts: []TaskPrompt{}, record: record}
		if task.Title == "" {
			task.Title = record.Code
		}
		if task.Description == "" {
			task.Description = record.Subtitle
		}
		for _, reward := range record.Rewards {
			if reward.Type == "quota_grant" {
				if amount, err := parseQuotaAmount(reward.Amount); err == nil {
					task.Credit += amount
				}
			}
		}
		if record.Progress != nil {
			task.Current, task.Target = record.Progress.Current, record.Progress.Target
		}
		if record.Kind == "points_milestone" {
			task.Current, task.Target = list.Points, record.Threshold
			task.Locked = !task.Claimed && (record.Threshold <= 0 || list.Points < record.Threshold)
			task.Claimable = !task.Claimed && !task.Locked
		} else if record.Kind == "daily_signin" {
			// Daily check-in state is authoritative, even if task_status is stale.
			task.Claimed, task.Claimable = false, false
			if record.Daily != nil {
				list.Continuous, list.TotalDays = record.Daily.Continuous, record.Daily.Total
				task.Claimed = record.Daily.CheckedIn && record.Daily.Date == CheckinDay(time.Now())
				task.Credit, _ = parseQuotaAmount(record.Daily.Reward.Amount)
			} else {
				task.Locked = true
			}
		} else if record.Mode == 2 && record.Status != 0 && !task.Claimed {
			task.Claimable = true
		}
		if record.Status == 0 && record.Kind != "daily_signin" {
			task.Locked, task.Claimable = true, false
		}
		if task.Claimed {
			task.Status = "claimed"
		} else if task.Locked {
			task.Status = "locked"
		} else if task.Claimable {
			task.Status = "claimable"
		} else if task.Status != "in_progress" {
			task.Status = "pending"
		}
		for _, rawPrompt := range record.Prompts {
			var prompt TaskPrompt
			if json.Unmarshal([]byte(rawPrompt), &prompt) == nil && prompt.Prompt != "" && prompt.Title != "" {
				task.Prompts = append(task.Prompts, prompt)
			}
		}
		list.Tasks = append(list.Tasks, task)
	}
	return list, nil
}

func (s *Store) Tasks(ctx context.Context, id string) (TaskList, error) {
	for _, a := range s.snapshot() {
		if a.ID == id {
			if a.Disabled {
				return TaskList{}, fmt.Errorf("该豆包账号已停用")
			}
			return s.client.Tasks(ctx, &a)
		}
	}
	return TaskList{}, os.ErrNotExist
}

func (c *Client) claimTask(ctx context.Context, a *Account, task Task) error {
	body := map[string]string{}
	if task.Kind == "points_milestone" || task.record.Mode == 2 || task.Code == "pc_task_complete_work_task_daily" {
		body["campaign_group_code"], body["campaign_item_code"] = CheckinGroupCode, task.Code
	}
	if task.Kind != "points_milestone" && task.record.Mode != 2 {
		if strings.TrimSpace(task.record.ID) == "" {
			return fmt.Errorf("豆包任务缺少平台实例，请刷新后重试")
		}
		body["task_id"] = task.record.ID
	}
	rawBody, _ := json.Marshal(body)
	raw, err := c.ugPost(ctx, a, "/alice/ug/campaign/item/reward/claim/", string(rawBody))
	if err != nil {
		return err
	}
	var root struct {
		Code *int `json:"code"`
		Data *struct {
			BizError *struct {
				Code taskEnum `json:"code"`
			} `json:"biz_error"`
		} `json:"data"`
	}
	if json.Unmarshal(raw, &root) != nil || root.Code == nil {
		return fmt.Errorf("豆包领奖响应格式无效，请刷新任务确认领取状态")
	}
	if *root.Code != 0 {
		return fmt.Errorf("豆包领奖被平台拒绝（代码 %d）", *root.Code)
	}
	if root.Data == nil {
		return fmt.Errorf("豆包领奖响应缺少结果，请刷新任务确认领取状态")
	}
	if biz := root.Data.BizError; biz != nil && biz.Code != 0 && biz.Code != 4 && biz.Code != 6 {
		return fmt.Errorf("豆包奖励未领取（代码 %d），请确认任务已达成", biz.Code)
	}
	return nil
}

// ClaimTask always obtains the current server instance, then reads back the
// result. It never trusts a task ID or completed state submitted by the browser.
func (s *Store) ClaimTask(ctx context.Context, id, code string) (TaskList, error) {
	if err := s.lock(ctx); err != nil {
		return TaskList{}, err
	}
	defer s.op.Unlock()
	accounts := s.snapshot()
	for i := range accounts {
		a := &accounts[i]
		if a.ID != id {
			continue
		}
		if a.Disabled {
			return TaskList{}, fmt.Errorf("该豆包账号已停用")
		}
		list, err := s.client.Tasks(ctx, a)
		if err != nil {
			return TaskList{}, err
		}
		for _, task := range list.Tasks {
			if task.Code != code {
				continue
			}
			if task.Claimed {
				return list, nil
			}
			if !task.Claimable || task.Locked || task.Kind == "daily_signin" {
				return TaskList{}, ErrTaskNotReady
			}
			if err := s.client.claimTask(ctx, a, task); err != nil {
				return TaskList{}, err
			}
			list, err = s.client.Tasks(ctx, a)
			if err != nil {
				return TaskList{}, fmt.Errorf("领奖请求已提交，状态回读失败，请刷新确认")
			}
			for _, updated := range list.Tasks {
				if updated.Code == code && updated.Claimed {
					a.CheckinPoints = list.Points
					_ = s.refreshBalance(ctx, a)
					if err := s.commit(accounts); err != nil {
						return TaskList{}, err
					}
					return list, nil
				}
			}
			return TaskList{}, fmt.Errorf("平台尚未确认奖励已领取，请稍后刷新任务")
		}
		return TaskList{}, fmt.Errorf("豆包活动中不存在该任务")
	}
	return TaskList{}, os.ErrNotExist
}
