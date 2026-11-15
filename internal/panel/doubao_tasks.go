package panel

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/linguo2625469/workbuddy2api-panel/internal/doubao"
)

func (p *Panel) doubaoTasks(w http.ResponseWriter, r *http.Request) {
	if !p.doubaoReady(w) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
	defer cancel()
	list, err := p.cfg.Doubao.Tasks(ctx, r.PathValue("uid"))
	if err != nil {
		p.doubaoTaskError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (p *Panel) doubaoTaskClaim(w http.ResponseWriter, r *http.Request) {
	if !p.doubaoReady(w) {
		return
	}
	var input struct {
		Code string `json:"task_code"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4<<10)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if dec.Decode(&input) != nil || dec.Decode(new(any)) != io.EOF || strings.TrimSpace(input.Code) == "" || len(input.Code) > 200 {
		writeErr(w, http.StatusBadRequest, "豆包任务参数无效")
		return
	}
	uid := r.PathValue("uid")
	if !p.tryLockAccount(uid) {
		writeErr(w, http.StatusConflict, "该账号的任务操作仍在执行")
		return
	}
	defer p.unlockAccount(uid)
	ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
	defer cancel()
	list, err := p.cfg.Doubao.ClaimTask(ctx, uid, input.Code)
	if err != nil {
		p.doubaoTaskError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "message": "奖励已领取", "tasks": list.Tasks, "total_amount": list.Points})
}

func (p *Panel) doubaoTaskError(w http.ResponseWriter, err error) {
	code := http.StatusBadGateway
	if errors.Is(err, os.ErrNotExist) {
		code = http.StatusNotFound
	} else if errors.Is(err, doubao.ErrTaskNotReady) {
		code = http.StatusConflict
	}
	writeErr(w, code, err.Error())
}
