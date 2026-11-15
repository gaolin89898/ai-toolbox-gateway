package doubaowork

import (
	"fmt"
	"strings"
	"time"
)

// 豆包工作（Doubao Work）上游协议常量。聊天端点、bot 与应用 ID 取自豆包工作
// 客户端（SamanthaDoubaoWork 2.36.11）的公开请求，参考 doubaowork2api 的协议研究。
const (
	WorkAppID          = "1044603"
	WorkBotID          = "7338286299411103781"
	WorkUA             = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/147.0.0.0 Safari/537.36 SamanthaDoubaoWork/2.36.11"
	WorkPCVersion      = "2.36.11"
	WorkRuntimeVersion = "3.45.3"
	WorkVersionCode    = "20800"
)

// WorkModel 是豆包工作模型目录的一个条目：ID 即官方 model_item_key。
type WorkModel struct {
	ID            string
	Name          string
	ContextWindow int
	MemoryProfile string
	Efforts       []string
	DefaultEffort string
}

var allEfforts = []string{"low", "medium", "high", "xhigh", "max"}

// WorkCatalog 是豆包工作电脑版模型菜单的内置快照。实际可用模型以账号当前官方
// 配置为准；未知模型不会静默映射到其他模型。
var WorkCatalog = []WorkModel{
	{ID: "person-deepseek-v4-1-flash-261017", Name: "DeepSeek V4.1 Flash", ContextWindow: 300000, MemoryProfile: "300k_230", Efforts: allEfforts, DefaultEffort: "max"},
	{ID: "seed-lite-7b", Name: "豆包 2.1 Lite", ContextWindow: 256000, MemoryProfile: "256k", Efforts: []string{"low", "medium", "high"}, DefaultEffort: "high"},
	{ID: "person-kimi-k3-1-pro-261111", Name: "Kimi K3.1 Pro", ContextWindow: 300000, MemoryProfile: "300k", Efforts: allEfforts, DefaultEffort: "high"},
	{ID: "person-kimi-k3-1-pro-highspeed-261111", Name: "Kimi K3.1 Pro 高速", ContextWindow: 300000, MemoryProfile: "300k", Efforts: allEfforts, DefaultEffort: "high"},
	{ID: "person-kimi-k3-261111", Name: "Kimi K3", ContextWindow: 300000, MemoryProfile: "300k", Efforts: allEfforts, DefaultEffort: "high"},
	{ID: "person-glm-5-3-261111", Name: "GLM 5.3", ContextWindow: 300000, MemoryProfile: "300k", Efforts: allEfforts, DefaultEffort: "high"},
	{ID: "person-glm-5-3-flash-261111", Name: "GLM 5.3 Flash", ContextWindow: 300000, MemoryProfile: "300k", Efforts: allEfforts, DefaultEffort: "high"},
	{ID: "person-deepseek-v4-pro-ga-260813", Name: "DeepSeek V4 Pro", ContextWindow: 300000, MemoryProfile: "300k", Efforts: allEfforts, DefaultEffort: "high"},
}

// WorkModelByKey 按官方 model_item_key 查找目录条目。
func WorkModelByKey(key string) (WorkModel, bool) {
	for _, m := range WorkCatalog {
		if m.ID == key {
			return m, true
		}
	}
	return WorkModel{}, false
}

// WorkEffort 把 OpenAI 的 reasoning_effort 名称映射为豆包工作的整数档位（3-7）。
// 空值取模型默认档；未知名称按默认档处理。
func WorkEffort(modelID, effort string) int {
	levels := map[string]int{"minimal": 3, "low": 3, "medium": 4, "high": 5, "xhigh": 6, "max": 7}
	if n, ok := levels[strings.ToLower(strings.TrimSpace(effort))]; ok {
		return n
	}
	def := "high"
	if m, ok := WorkModelByKey(modelID); ok && m.DefaultEffort != "" {
		def = m.DefaultEffort
	}
	if n, ok := levels[def]; ok {
		return n
	}
	return 5
}

// WorkQuery 构造豆包工作请求的查询串（与客户端一致，参数排序）。
func WorkQuery(deviceID, webID, fingerprint string) string {
	if webID == "" {
		webID = deviceID
	}
	if fingerprint == "" {
		fingerprint = "verify_" + deviceID
	}
	tab := newID()
	params := [][2]string{
		{"aid", WorkAppID}, {"real_aid", WorkAppID},
		{"device_id", deviceID}, {"tea_uuid", deviceID}, {"web_id", webID},
		{"device_platform", "web"}, {"doubao_device_platform", "desktop"},
		{"language", "zh"}, {"region", "CN"}, {"sys_region", "CN"},
		{"pkg_type", "release_version"}, {"version_code", WorkVersionCode},
		{"pc_version", WorkPCVersion}, {"doubao_pc_version", WorkPCVersion},
		{"runtime", "web"}, {"runtime_version", WorkRuntimeVersion},
		{"samantha_web", "1"}, {"web_platform", "desktop"},
		{"use-olympus-account", "1"}, {"client_platform", "pc_client"},
		{"chromium_version", "147.0.7727.149"}, {"channel", "win"},
		{"fp", fingerprint}, {"web_tab_id", tab}, {"tz_name", "Asia/Shanghai"},
	}
	sortPairs(params)
	parts := make([]string, 0, len(params))
	for _, p := range params {
		parts = append(parts, urlQueryEscape(p[0])+"="+urlQueryEscape(p[1]))
	}
	return strings.Join(parts, "&")
}

func sortPairs(p [][2]string) {
	for i := 1; i < len(p); i++ {
		for j := i; j > 0 && p[j][0] < p[j-1][0]; j-- {
			p[j], p[j-1] = p[j-1], p[j]
		}
	}
}

// WorkPayload 构造豆包工作 /chat/completion 请求体（字段与参考实现一致）。
func WorkPayload(prompt, modelKey string, effort int, deviceID string) map[string]any {
	localMessageID := newID()
	workspace := ""
	taskParam := map[string]any{
		"runtime_type": 2, "sandbox_auth_type": 1, "device_name": "wb2api",
		"folder_name": "", "local_app_id": WorkAppID, "local_device_id": deviceID, "workspace": workspace,
	}
	generalTask := map[string]any{
		"action": 0, "thread_local_message_id": []any{localMessageID},
		"client_option": map[string]any{
			"enable_sandbox": true, "os": "Linux", "shared_folder_path": []any{workspace},
			"workspace": workspace, "sandbox_auth_type": 1,
		},
		"runtime_type": 2, "agent_task_param": taskParam, "server_do_compare": true,
	}
	taskJSON := mustJSON(taskParam)
	generalJSON := mustJSON(generalTask)
	initOption := mustJSON(map[string]any{"project_id": "", "need_ack_conversation": true})
	modelConfig := map[string]any{"model_item_key": modelKey, "reasoning_effort": effort}
	if m, ok := WorkModelByKey(modelKey); ok && m.MemoryProfile != "" && m.ContextWindow > 0 {
		modelConfig["model_extra_params"] = map[string]any{
			"memory_profile": m.MemoryProfile, "total_window_size": fmt.Sprintf("%d", m.ContextWindow),
		}
	}
	return map[string]any{
		"client_meta": map[string]any{
			"local_conversation_id": "local_" + idNoDash()[:16],
			"conversation_id":       "", "bot_id": WorkBotID,
			"last_section_id": "", "last_message_index": nil,
		},
		"messages": []any{map[string]any{
			"local_message_id": localMessageID,
			"content_block": []any{map[string]any{
				"block_type": 10000,
				"content": map[string]any{
					"text_block":    map[string]any{"text": prompt, "icon_url": "", "icon_url_dark": "", "summary": ""},
					"pc_event_block": "",
				},
				"block_id": newID(), "parent_id": "", "meta_info": []any{}, "extra_info": map[string]any{}, "append_fields": []any{},
			}},
			"message_status": 0,
		}},
		"option": map[string]any{
			"send_message_scene": "", "create_time_ms": time.Now().UnixMilli(), "collect_id": "",
			"is_audio": false, "answer_with_suggest": false, "agent_mode": 1, "tts_switch": false,
			"need_deep_think": 10001, "click_clear_context": false, "from_suggest": false,
			"is_regen": false, "is_replace": false, "is_from_click_option": false, "is_from_click_softlink": false,
			"disable_sse_cache": false, "select_text_action": "", "is_select_text": false, "resend_for_regen": false,
			"scene_type": 0, "unique_key": newID(), "start_seq": 0, "need_create_conversation": true,
			"conversation_init_option": map[string]any{"project_id": "", "need_ack_conversation": true},
			"conversation_init_ext": map[string]any{
				"agent_task_param": taskJSON, "model_item_key": modelKey,
				"reasoning_effort": fmt.Sprintf("%d", effort), "mode_id": "3",
			},
			"regen_query_id": []any{}, "edit_query_id": []any{}, "regen_instruction": "",
			"no_replace_for_regen": false, "message_from": 0, "shared_app_name": "", "shared_app_id": "",
			"sse_recv_event_options": map[string]any{"support_chunk_delta": true},
			"support_lazy_fetch_stream": true, "is_ai_playground": false, "is_old_user": true,
			"general_task_param": generalTask,
			"recovery_option": map[string]any{
				"is_recovery": false, "req_create_time_sec": time.Now().Unix(), "append_sse_event_scene": 0,
			},
			"message_storage_type": 0, "related_deleted_message_ids": map[string]any{}, "connector_info_list": []any{},
			"model_config": modelConfig,
			"aggregate_params": map[string]any{
				"mention_skill_list": "[]", "mention_plugin_list": "[]", "mention_ext": "[{}]",
				"conversation_mode": "", "mode_id": "3", "model_item_key": modelKey,
				"agent_mode": "1", "reasoning_effort": fmt.Sprintf("%d", effort), "provider_id": "",
			},
		},
		"user_context": []any{},
		"ext": map[string]any{
			"general_task_param": generalJSON, "use_deep_think": "10001", "agent_mode": "1",
			"conversation_init_option": initOption, "sub_conv_firstmet_type": "1",
			"collection_id": "", "is_finish": "1", "commerce_credit_config_enable": "0",
		},
	}
}
