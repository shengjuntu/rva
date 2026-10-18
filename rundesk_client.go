package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// RunDeskError keeps uncertainty separate from a definitive rejection. Neither
// HTTP retries nor a browser refresh may invent a new logical operation key.
type RunDeskError struct {
	Status    int    `json:"-"`
	Message   string `json:"error"`
	Code      string `json:"code"`
	RequestID string `json:"requestId,omitempty"`
	Retryable bool   `json:"retryable"`
	Uncertain bool   `json:"uncertain"`
}

func (e *RunDeskError) Error() string { return e.Message }
func uncertain(message string) error {
	return &RunDeskError{Status: 409, Message: message, Code: "request_unconfirmed", Uncertain: true}
}
func (a *App) rdRequest(ctx context.Context, method, path string, body any, key string, out any) error {
	a.mu.Lock()
	bound := a.cfg.RunDeskURL
	a.mu.Unlock()
	if bound != "" && bound != a.rd {
		return &RunDeskError{Status: 409, Code: "backend_changed", Message: "应用已绑定原 RunDesk 地址，请恢复原连接"}
	}
	var b io.Reader
	if body != nil {
		raw, e := json.Marshal(body)
		if e != nil {
			return e
		}
		b = bytes.NewReader(raw)
	}
	req, e := http.NewRequestWithContext(ctx, method, a.rd+"/api/v1"+path, b)
	if e != nil {
		return e
	}
	// Recovery is explicit through /requests; avoid transport-level POST retries.
	req.GetBody = nil
	req.Header.Set("Content-Type", "application/json")
	if a.rdToken != "" {
		req.Header.Set("Authorization", "Bearer "+a.rdToken)
	}
	if key != "" {
		req.Header.Set("Idempotency-Key", key)
	}
	client := *a.client
	if strings.HasSuffix(path, "/runtime/prepare") {
		client.Timeout = 3 * time.Minute
	} else if strings.HasSuffix(path, "/runtime/diagnostics") {
		client.Timeout = 35 * time.Second
	}
	resp, e := client.Do(req)
	if e != nil {
		return &RunDeskError{Status: 502, Message: "RunDesk 连接失败；保留原请求，等待确认", Code: "transport_error", Uncertain: method != "GET"}
	}
	defer resp.Body.Close()
	raw, e := io.ReadAll(io.LimitReader(resp.Body, (16<<20)+1))
	if e != nil || len(raw) > 16<<20 {
		return &RunDeskError{Status: 502, Message: "RunDesk 响应未完整读取", Code: "invalid_response", Uncertain: method != "GET"}
	}
	if resp.StatusCode >= 300 {
		var v RunDeskError
		_ = json.Unmarshal(raw, &v)
		v.Status = resp.StatusCode
		if v.Message == "" {
			v.Message = "RunDesk " + resp.Status
		}
		if v.Code == "" {
			v.Code = "rundesk_error"
		}
		v.Uncertain = method != "GET" && resp.StatusCode >= 500 || v.Code == "request_in_progress" || v.Code == "request_unconfirmed"
		return &v
	}
	if out != nil {
		if e = json.Unmarshal(raw, out); e != nil {
			return &RunDeskError{Status: 502, Message: "RunDesk 返回格式无效", Code: "invalid_response", Uncertain: method != "GET"}
		}
	}
	return nil
}
func (a *App) rdCall(ctx context.Context, method, path string, body, out any) error {
	return a.rdRequest(ctx, method, path, body, "", out)
}
func (a *App) requireV1(ctx context.Context) error {
	var meta struct {
		APIVersions  []string `json:"apiVersions"`
		Capabilities []string `json:"capabilities"`
	}
	if e := a.rdCall(ctx, "GET", "/meta", nil, &meta); e != nil {
		return e
	}
	has := func(items []string, value string) bool {
		for _, v := range items {
			if v == value {
				return true
			}
		}
		return false
	}
	if !has(meta.APIVersions, "v1") || !has(meta.Capabilities, "idempotency") || !has(meta.Capabilities, "configuration-summary") || !has(meta.Capabilities, "application-connect") || !has(meta.Capabilities, "application-runtime") {
		return errors.New("需要支持应用注册、Docker 准备和 API v1 回执的 RunDesk；请使用 0.48.0 或更新版本")
	}
	return nil
}

type agentOperation struct {
	Key      string          `json:"key"`
	Backend  string          `json:"backend"`
	Path     string          `json:"path"`
	Body     json.RawMessage `json:"body"`
	Response json.RawMessage `json:"response,omitempty"`
}

// Call with bridgeMu held. One immutable file per logical operation. The key
// and body are recorded before the first possible network side effect.
func (a *App) operationFile(scope string) string {
	hash := sha256.Sum256([]byte(scope))
	return filepath.Join(a.data, "rundesk-requests", fmt.Sprintf("%x.json", hash))
}

func (a *App) rdOperation(ctx context.Context, scope, path string, body any, out any, strict, allowSend bool) error {
	file := a.operationFile(scope)
	raw, e := json.Marshal(body)
	if e != nil {
		return e
	}
	op := agentOperation{}
	saved, e := os.ReadFile(file)
	if e == nil {
		if e = json.Unmarshal(saved, &op); e != nil {
			return e
		}
		if op.Backend != a.rd || op.Path != path {
			return uncertain("请求属于原 RunDesk 地址或路径，请恢复原连接后核对")
		}
		if strict && !sameJSON(op.Body, raw) {
			return &RunDeskError{Status: 409, Message: "此请求编号已用于另一条内容", Code: "idempotency_conflict"}
		}
	} else if !errors.Is(e, os.ErrNotExist) {
		return e
	} else {
		if !allowSend {
			return uncertain("没有保存的提交记录，未启动任务")
		}
		op = agentOperation{Key: "video-" + uid(), Backend: a.rd, Path: path, Body: raw}
		if e = os.MkdirAll(filepath.Dir(file), 0700); e != nil {
			return e
		}
		if e = atomicJSON(file, op); e != nil {
			return e
		}
	}
	if len(op.Response) > 0 {
		if out == nil {
			return nil
		}
		return json.Unmarshal(op.Response, out)
	}
	var result json.RawMessage
	if strings.HasSuffix(path, "/steer") {
		// Native steer has its own durable requestId + expectedTurnId deduplication.
		if !allowSend {
			return uncertain("补充指令接收情况待确认；不会因停止而重发")
		}
		e = a.rdCall(ctx, "POST", path, op.Body, &result)
	} else {
		var receipt struct {
			State      string          `json:"state"`
			Method     string          `json:"method"`
			Path       string          `json:"path"`
			HTTPStatus int             `json:"httpStatus"`
			Response   json.RawMessage `json:"response"`
		}
		e = a.rdCall(ctx, "GET", "/requests/"+op.Key, nil, &receipt)
		var remote *RunDeskError
		if errors.As(e, &remote) && remote.Status == 404 && remote.Code == "request_not_found" {
			if !allowSend {
				return uncertain("提交尚未找到回执，继续等待确认；停止操作不会重新启动任务")
			}
			e = a.rdRequest(ctx, "POST", path, op.Body, op.Key, &result)
		} else if e == nil {
			if receipt.Method != "POST" || receipt.Path != path {
				return uncertain("回执与保存的请求不一致，请核对后台数据目录")
			}
			if receipt.State != "completed" {
				return uncertain("原请求" + receipt.State + "，等待 RunDesk 确认；不会更换请求编号")
			}
			if receipt.HTTPStatus < 200 || receipt.HTTPStatus >= 300 {
				var failure RunDeskError
				_ = json.Unmarshal(receipt.Response, &failure)
				failure.Status = receipt.HTTPStatus
				if failure.Message == "" {
					failure.Message = "原请求被 RunDesk 拒绝"
				}
				failure.Uncertain = receipt.HTTPStatus >= 500
				return &failure
			}
			result = receipt.Response
		} else if errors.As(e, &remote) {
			// Authentication/read failures cannot prove the original write failed.
			remote.Uncertain = true
		}
	}
	if e != nil {
		return e
	}
	if out != nil {
		if e = json.Unmarshal(result, out); e != nil {
			return uncertain("接收回执无效，保留请求等待核对")
		}
	}
	op.Response = result
	if e = atomicJSON(file, op); e != nil {
		return uncertain("RunDesk 已响应，但本地回执尚未保存；请重试同一请求")
	}
	return nil
}

type AgentSubmission struct {
	Skills          []string        `json:"skills,omitempty"`
	ID              string          `json:"id"`
	Text            string          `json:"text"`
	TurnID          string          `json:"turnId,omitempty"`
	RunID           string          `json:"runId,omitempty"`
	State           string          `json:"state"`
	Error           string          `json:"error,omitempty"`
	CancelRequested bool            `json:"cancelRequested,omitempty"`
	Body            json.RawMessage `json:"body,omitempty"`
}

func (a *App) saveAgent(id string, update func(*Project)) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	p := a.projects[id]
	if p == nil {
		return errors.New("项目不存在")
	}
	next := clone(p)
	update(&next)
	if e := a.save(&next); e != nil {
		return e
	}
	a.projects[id] = &next
	return nil
}
func (a *App) recoverAgent(ctx context.Context, p Project, allowSend bool) (any, error) {
	pending := p.AgentSubmission
	if pending == nil || (pending.State != "pending" && pending.State != "accepted") {
		return nil, nil
	}
	suffix := "/turns"
	productionRoute := "素材通过 assetId 引用；调用 video_render_start 在 App 中渲染当前原生剪辑。"
	if workflowFor(p).EditingBackend == "video_use" {
		productionRoute = "采用 video_use：完成配音和文字素材对齐后调用 video_production_run_prepare，按返回的相对路径读取共享目录快照，在 work/edl.json 编排，运行适配器并调用 video_production_run_ingest 归档。以该 EDL 为剪辑依据；不要另写原生 DSL 或调用 video_render_start。"
	}
	body := map[string]any{"text": fmt.Sprintf("[Video App projectId=%s requestId=%s] 按素材准备和对齐、剪辑、精修三个阶段执行，通过业务工具保存产物。先读取项目当前 revision。%s\n用户要求：\n%s", p.ID, pending.ID, productionRoute, pending.Text), "files": []string{}, "skills": []any{}}
	if !a.cfg.ManagedByRunDesk && a.cfg.SkillPath != "" {
		body["skills"] = []map[string]string{{"name": "video-project", "path": a.cfg.SkillPath}}
	}
	if pending.TurnID != "" {
		suffix = "/steer"
		body["expectedTurnId"] = pending.TurnID
		body["requestId"] = pending.ID
	}
	if len(pending.Body) == 0 {
		if len(pending.Skills) > 0 && a.cfg.ManagedByRunDesk {
			refs, err := a.resolveProductionSkills(ctx, p)
			if err != nil {
				return nil, err
			}
			body["skills"] = refs
		}
		raw, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		if err = a.saveAgent(p.ID, func(q *Project) { q.AgentSubmission.Body = raw }); err != nil {
			return nil, err
		}
		pending.Body = raw
	}
	var result map[string]any
	e := a.rdOperation(ctx, "message:"+p.ID+":"+pending.ID, "/sessions/"+p.SessionID+suffix, pending.Body, &result, true, allowSend && !pending.CancelRequested)
	state := "accepted"
	message := ""
	runID := pending.RunID
	if e != nil {
		state = "pending"
		message = e.Error()
		var remote *RunDeskError
		if errors.As(e, &remote) && pending.TurnID != "" {
			remote.Uncertain = true
		}
		if errors.As(e, &remote) && remote.Status >= 400 && remote.Status < 500 && !remote.Uncertain && pending.TurnID == "" {
			state = "failed"
		}
	} else if pending.TurnID == "" {
		runID, _ = result["runId"].(string)
		if result["id"] != p.SessionID || runID == "" {
			e = uncertain("回执没有有效的运行编号")
			state = "pending"
			message = e.Error()
		}
	}
	if saveErr := a.saveAgent(p.ID, func(q *Project) {
		if q.AgentSubmission != nil && q.AgentSubmission.ID == pending.ID {
			q.AgentSubmission.State = state
			q.AgentSubmission.Error = message
			q.AgentSubmission.RunID = runID
			if state == "accepted" && runID != "" {
				q.AgentRunID = runID
			}
		}
	}); saveErr != nil {
		return nil, saveErr
	}
	return result, e
}

func sameJSON(a, b []byte) bool {
	var x, y any
	if json.Unmarshal(a, &x) != nil || json.Unmarshal(b, &y) != nil {
		return false
	}
	p, _ := json.Marshal(x)
	q, _ := json.Marshal(y)
	return bytes.Equal(p, q)
}
