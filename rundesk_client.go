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
	return a.requireV1ForPreset(ctx, "")
}
func (a *App) requireV1ForPreset(ctx context.Context, presetID string) error {
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
	if presetID != "" && !has(meta.Capabilities, "application-runtime-presets") {
		return &RunDeskError{Status: 409, Code: "production_profile_unsupported", Message: "当前 RunDesk 不支持制作环境预设；请升级至支持 application-runtime-presets 的版本，未回退到默认环境"}
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
	Purpose             string `json:"purpose,omitempty"`
	Created             string `json:"created,omitempty"`
	ExecutionState      string `json:"executionState,omitempty"`
	ExecutionObservedAt string `json:"executionObservedAt,omitempty"`
	SessionID       string                 `json:"sessionId,omitempty"`
	RuntimeBinding  *ProjectRuntimeBinding `json:"runtimeBinding,omitempty"`
	Skills          []string               `json:"skills,omitempty"`
	ID              string                 `json:"id"`
	Text            string                 `json:"text"`
	TurnID          string                 `json:"turnId,omitempty"`
	RunID           string                 `json:"runId,omitempty"`
	State           string                 `json:"state"`
	Error           string                 `json:"error,omitempty"`
	CancelRequested bool                   `json:"cancelRequested,omitempty"`
	Body            json.RawMessage        `json:"body,omitempty"`
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
	binding := a.projectRuntimeBinding(&p)
	if pending.SessionID != "" && pending.SessionID != p.SessionID || pending.RuntimeBinding != nil && *pending.RuntimeBinding != binding {
		return nil, uncertain("原消息属于保存的制作环境或会话，未在其他环境恢复提交")
	}
	suffix := "/turns"
	productionRoute := "当前后端 native：素材通过 assetId 引用；读取当前原生工程，按实际支持的 DSL/模板能力修改，通过 video_job_start(type=render) 或 video_render_start 渲染，不假设支持自由动画。"
	switch workflowFor(p).EditingBackend {
	case "hyperframes":
		productionRoute = "当前后端 hyperframes：使用 video_hyperframes_get/update/check 自由编写、保存并检查 HTML/CSS/JavaScript/GSAP 工程，绑定真实素材；通过 video_job_start(type=render) 或 video_render_start 渲染该 HyperFrames 工程。这不是原生 DSL/模板流程，不需要 video_use 或共享目录。"
	case "video_use":
		productionRoute = "当前后端 video_use：需要冻结已准备的配音和文字素材关联时调用 video_production_run_prepare，按返回的相对路径读取共享目录快照，在 work/edl.json 编排，运行适配器并调用 video_production_run_ingest 归档。以该 EDL 为剪辑依据；不要另写原生 DSL 或调用 video_render_start。"
	}
	attribution := ""
	if pending.Purpose != "" {
		attribution = fmt.Sprintf("本次制作归属 productionRequestId=%s。新建 video_job_start 的 options、video_render_start、video_speech_start、video_production_run_prepare 和 video_production_checkpoint 均传 productionRequestId=%q（此编号是用户制作请求，不是工具调用的 requestId；每个工具调用仍用自己的新 requestId）。旧工具调用或旧请求不得改挂当前编号；缺少归属的产物只会保留在历史中，不会显示为本次新片。\n", pending.ID, pending.ID)
	}
	body := map[string]any{"text": attribution + fmt.Sprintf("[Video App projectId=%s requestId=%s] 先读取项目当前 revision、已有工程和产物；自主选择必要工具与执行顺序。素材准备和对齐、剪辑、精修是产物记录和审阅视图，可以交错、重复或局部返回，不是固定步骤或审批。执行后检查实际结果；发现可修复问题就修订并重做，不把工具成功当作质量合格，真实阻塞和未执行的检查须说明。局部修改只处理受影响部分，保留无关且已满意的内容，不要求重走全部流程。通过业务工具保存真实产物与检查记录。%s\n用户要求：\n%s", p.ID, pending.ID, productionRoute, pending.Text), "files": []string{}, "skills": []any{}}
	if !binding.ManagedByRunDesk && binding.SkillPath != "" {
		body["skills"] = []map[string]string{{"name": "video-project", "path": binding.SkillPath}}
	}
	if pending.TurnID != "" {
		suffix = "/steer"
		body["expectedTurnId"] = pending.TurnID
		body["requestId"] = pending.ID
	}
	if len(pending.Body) == 0 {
		if len(pending.Skills) > 0 && binding.ManagedByRunDesk {
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
