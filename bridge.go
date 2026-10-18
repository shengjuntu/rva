package main

import (
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

//go:embed integrations/video-project
var projectSkillFiles embed.FS

var projectSkill = func() string {
	b, err := projectSkillFiles.ReadFile("integrations/video-project/SKILL.md")
	if err != nil {
		panic(err)
	}
	return string(b)
}()

func projectSkillBundle(name string) map[string]string {
	out := map[string]string{}
	err := fs.WalkDir(projectSkillFiles, "integrations/video-project", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		data, err := projectSkillFiles.ReadFile(path)
		if err != nil {
			return err
		}
		key := strings.TrimPrefix(path, "integrations/video-project/")
		text := string(data)
		if key == "SKILL.md" {
			text = strings.Replace(text, "name: video-project\n", "name: "+name+"\n", 1)
		}
		if key == "agents/openai.yaml" {
			text = strings.ReplaceAll(text, "$video-project", "$"+name)
		}
		out[key] = text
		return nil
	})
	if err != nil {
		panic(err)
	}
	return out
}

func (a *App) connect(w http.ResponseWriter, r *http.Request) {
	a.bridgeMu.Lock()
	defer a.bridgeMu.Unlock()
	id := r.PathValue("id")
	p, e := a.get(id)
	if e != nil {
		fail(w, 404, e)
		return
	}
	if r.URL.Query().Get("new") == "1" {
		expected := r.URL.Query().Get("expectedSessionId")
		if expected == "" {
			fail(w, 400, errors.New("新建对话需要当前 expectedSessionId"))
			return
		}
		if p.SessionID != expected {
			for _, previous := range p.PreviousSessionIDs {
				if previous == expected {
					writeJSON(w, 200, p)
					return
				}
			}
			fail(w, 409, errors.New("对话已改变，请刷新后重试"))
			return
		}
	}
	a.mu.Lock()
	cfg := a.cfg
	a.mu.Unlock()
	legacyCheckedID := ""
	if p.RuntimeBinding == nil && p.SessionID != "" && !cfg.ManagedByRunDesk && cfg.InstanceID != "" {
		if e = a.requireV1(r.Context()); e != nil {
			fail(w, 409, e)
			return
		}
		if e = a.checkLegacyMigrationBusy(r.Context(), cfg.InstanceID); e != nil {
			fail(w, 409, e)
			return
		}
		legacyCheckedID = cfg.InstanceID
	}
	selected, e := a.connectionBinding(r.Context(), p, cfg)
	if e != nil {
		fail(w, 409, e)
		return
	}
	if e = a.requireV1ForPreset(r.Context(), selected.PresetID); e != nil {
		fail(w, 409, e)
		return
	}
	a.mu.Lock()
	if a.cfg.RunDeskURL == "" {
		next := a.cfg
		next.RunDeskURL = a.rd
		e = atomicJSON(filepath.Join(a.data, "config.json"), next)
		if e == nil {
			a.cfg = next
		}
	}
	a.mu.Unlock()
	if e != nil {
		fail(w, 500, e)
		return
	}
	a.mu.Lock()
	if a.cfg.InstallationID == "" {
		a.cfg.InstallationID = uid()
		e = atomicJSON(filepath.Join(a.data, "config.json"), a.cfg)
	}
	installationID, iid := a.cfg.InstallationID, selected.InstanceID
	a.mu.Unlock()
	if e != nil {
		fail(w, 500, e)
		return
	}
	// Migrating an old app must not change capabilities during an active turn.
	if iid != "" && !selected.ManagedByRunDesk && iid != legacyCheckedID {
		if e = a.checkLegacyMigrationBusy(r.Context(), iid); e != nil {
			fail(w, 409, e)
			return
		}
	}
	base := a.public
	if base == "" {
		scheme := "http"
		if r.TLS != nil {
			scheme = "https"
		}
		base = scheme + "://" + r.Host
	}
	callback := a.mcpURL
	if callback == "" {
		callback = base + "/api/mcp"
	}
	var binding struct {
		Application struct {
			InstanceID  string `json:"instanceId"`
			WorkspaceID string `json:"workspaceId"`
			PresetID    string `json:"presetId"`
		} `json:"application"`
		PresetID string `json:"presetId"`
		Status   string `json:"status"`
		Message  string `json:"message"`
	}
	connectBody := map[string]any{
		"name": "视频制作", "description": "读取视频项目与素材，编写脚本、分镜并保存项目；按用户要求制作视频。任务须提供 projectId。",
		"installationId": installationID, "instanceId": iid, "workspaceId": selected.WorkspaceID, "entryUrl": base,
		"defaults": map[string]any{"skills": installedProductionSkills(), "mcp": map[string]any{"video_project": map[string]any{"url": callback, "http_headers": map[string]string{"Authorization": "Bearer " + a.token}, "enabled": true}}},
	}
	if selected.PresetID != "" {
		connectBody["presetId"] = selected.PresetID
	}
	e = a.rdCall(r.Context(), "POST", "/applications/rundesk-video-app/connect", connectBody, &binding)
	if e != nil {
		fail(w, 502, e)
		return
	}
	if binding.Application.InstanceID == "" || binding.Application.WorkspaceID == "" || binding.PresetID != selected.PresetID || binding.Application.PresetID != selected.PresetID {
		fail(w, 409, errors.New("RunDesk 返回的制作环境预设或绑定无效，未切换到其他环境"))
		return
	}
	if p.RuntimeBinding != nil || p.SessionID != "" && selected.ManagedByRunDesk {
		if selected.InstanceID != binding.Application.InstanceID || selected.WorkspaceID != binding.Application.WorkspaceID {
			fail(w, 409, errors.New("RunDesk 项目原实例或工作区已改变，未迁移原会话"))
			return
		}
	}
	iid = binding.Application.InstanceID
	// Initialize only the original legacy default on a fresh installation. A
	// preset or later project must never overwrite an existing global pair.
	if selected.PresetID == "" {
		a.mu.Lock()
		if a.cfg.InstanceID == "" {
			next := a.cfg
			next.InstanceID, next.WorkspaceID = iid, binding.Application.WorkspaceID
			next.ManagedByRunDesk, next.SkillPath = true, ""
			e = atomicJSON(filepath.Join(a.data, "config.json"), next)
			if e == nil {
				a.cfg = next
			}
		}
		a.mu.Unlock()
		if e != nil {
			fail(w, 500, e)
			return
		}
	}
	// Freeze the project pair before any session side effect. Never replace the
	// App's global legacy instance when a different project selects a preset.
	if p.RuntimeBinding == nil {
		a.mu.Lock()
		q := clone(a.projects[id])
		q.WorkspaceID = binding.Application.WorkspaceID
		q.RuntimeBinding = &ProjectRuntimeBinding{PresetID: selected.PresetID, InstanceID: iid, WorkspaceID: q.WorkspaceID, ManagedByRunDesk: true}
		e = a.save(&q)
		if e == nil {
			a.projects[id] = &q
		}
		a.mu.Unlock()
		if e != nil {
			fail(w, 500, e)
			return
		}
		p = q
	}
	if binding.Status != "connected" {
		fail(w, 409, errors.New(binding.Message))
		return
	}
	sessionScope := "session:" + p.ID
	previousSession := ""
	if p.SessionID != "" {
		var old struct {
			InstanceID    string `json:"instanceId"`
			WorkspaceID   string `json:"workspaceId"`
			ExecutionMode string `json:"executionMode"`
			Status        string `json:"status"`
			RunID         string `json:"runId"`
			ThreadID      string `json:"threadId"`
		}
		if e = a.rdCall(r.Context(), "GET", "/sessions/"+p.SessionID, nil, &old); e != nil {
			fail(w, 502, e)
			return
		}
		runtime, err := a.runtimeForProject(r.Context(), &p)
		if err != nil {
			fail(w, 409, err)
			return
		}
		changed := old.InstanceID != iid || old.WorkspaceID != p.WorkspaceID || old.ExecutionMode != runtime.ExecutionMode
		if changed || r.URL.Query().Get("new") == "1" {
			if old.Status == "running" || old.Status == "waiting" || old.Status == "starting" || old.Status == "stopping" || p.AgentSubmission != nil && p.AgentSubmission.State == "pending" {
				fail(w, 409, errors.New("请先结束当前 Agent 任务并确认原提交，再新建对话"))
				return
			}
			if changed && (old.RunID != "" || old.ThreadID != "") && r.URL.Query().Get("new") != "1" {
				fail(w, 409, errors.New("运行环境已改变；请点击“新建当前环境对话”，原历史保留在 RunDesk"))
				return
			}
			previousSession = p.SessionID
			sessionScope += ":after:" + previousSession
			p.SessionID = ""
		}
	}
	if p.SessionID == "" {
		var ss struct {
			ID string `json:"id"`
		}
		source := map[string]string{"kind": "application", "appId": "rundesk-video-app", "taskId": p.ID}
		if selected.PresetID != "" {
			source["presetId"] = selected.PresetID
		}
		sessionBody := map[string]any{"workspaceId": p.WorkspaceID, "instanceId": iid, "title": "视频 · " + p.Name, "source": source}
		if e = a.validateSessionOperationBinding(sessionScope, *p.RuntimeBinding); e != nil {
			fail(w, 409, e)
			return
		}
		e = a.rdOperation(r.Context(), sessionScope, "/sessions", sessionBody, &ss, false, true)
		if e != nil {
			fail(w, 502, e)
			return
		}
		if ss.ID == "" {
			fail(w, 409, uncertain("RunDesk 会话创建回执缺少会话编号，保留原请求等待核对"))
			return
		}
		a.mu.Lock()
		q := clone(a.projects[id])
		q.SessionID = ss.ID
		if previousSession != "" {
			q.PreviousSessionIDs = append(q.PreviousSessionIDs, previousSession)
			q.AgentSubmission = nil
			q.AgentRunID = ""
		}
		e = a.save(&q)
		if e == nil {
			a.projects[id] = &q
		}
		a.mu.Unlock()
		if e != nil {
			fail(w, 500, e)
			return
		}
		p = q
	}
	a.mu.Lock()
	q := clone(a.projects[id])
	q.ToolsVersion = version
	e = a.save(&q)
	if e == nil {
		a.projects[id] = &q
	}
	a.mu.Unlock()
	if e != nil {
		fail(w, 500, e)
		return
	}
	writeJSON(w, 200, q)
}
func (a *App) message(w http.ResponseWriter, r *http.Request) {
	a.bridgeMu.Lock()
	defer a.bridgeMu.Unlock()
	var in struct {
		Skills         []string `json:"skills,omitempty"`
		Text           string   `json:"text"`
		RequestID      string   `json:"requestId"`
		ExpectedTurnID string   `json:"expectedTurnId"`
	}
	if !decode(w, r, &in) {
		return
	}
	if strings.TrimSpace(in.Text) == "" || len(in.Text) > 128<<10 || !requestIdentifier.MatchString(in.RequestID) {
		fail(w, 400, errors.New("需要消息内容和 8–128 位 requestId"))
		return
	}
	if len(in.Skills) > 0 {
		if err := validateWorkflow(ProductionWorkflow{Skills: in.Skills}); err != nil {
			fail(w, 400, err)
			return
		}
	}
	p, e := a.session(r)
	if e != nil {
		fail(w, 409, e)
		return
	}
	if p.ToolsVersion != version {
		fail(w, 409, errors.New("请先升级项目工具配置"))
		return
	}
	if pending := p.AgentSubmission; pending != nil && pending.ID == in.RequestID {
		if pending.Text != in.Text || pending.TurnID != in.ExpectedTurnID || strings.Join(pending.Skills, "\n") != strings.Join(in.Skills, "\n") {
			fail(w, 409, &RunDeskError{Status: 409, Code: "idempotency_conflict", Message: "同一 requestId 不能用于不同消息"})
			return
		}
		if pending.State == "failed" || pending.State == "canceled" {
			fail(w, 409, &RunDeskError{Status: 409, Code: "submission_rejected", Message: pending.Error})
			return
		}
	} else {
		if p.AgentSubmission != nil && p.AgentSubmission.State == "pending" {
			fail(w, 409, uncertain("上一条消息仍待确认，请先恢复原请求"))
			return
		}
		// Preflight only NEW logical submissions; recovery keeps its original receipt.
		if in.ExpectedTurnID == "" {
			if _, err := a.prepareRuntime(r.Context(), &p); err != nil {
				fail(w, 409, err)
				return
			}
		}
		if _, err := os.Stat(a.operationFile("message:" + p.ID + ":" + in.RequestID)); err == nil {
			fail(w, 409, &RunDeskError{Status: 409, Code: "request_obsolete", Message: "此消息已有历史提交记录，请先核对已有对话"})
			return
		} else if !errors.Is(err, os.ErrNotExist) {
			fail(w, 500, err)
			return
		}
		if in.ExpectedTurnID != "" {
			var current struct {
				RunID       string `json:"runId"`
				TurnID      string `json:"turnId"`
				InstanceID  string `json:"instanceId"`
				WorkspaceID string `json:"workspaceId"`
				Source      struct {
					PresetID string `json:"presetId"`
				} `json:"source"`
			}
			if e = a.rdCall(r.Context(), "GET", "/sessions/"+p.SessionID, nil, &current); e != nil {
				fail(w, 502, e)
				return
			}
			if p.AgentRunID == "" || current.RunID != p.AgentRunID || current.TurnID != in.ExpectedTurnID {
				fail(w, 409, &RunDeskError{Status: 409, Code: "run_conflict", Message: "运行已改变，请刷新并核对当前任务"})
				return
			}
			if p.RuntimeBinding != nil && (current.InstanceID != p.RuntimeBinding.InstanceID || current.WorkspaceID != p.RuntimeBinding.WorkspaceID || current.Source.PresetID != p.RuntimeBinding.PresetID) {
				fail(w, 409, errors.New("当前会话不属于项目保存的制作环境，未发送补充指令"))
				return
			}
		}
		if e = a.saveAgent(p.ID, func(q *Project) {
			q.AgentSubmission = &AgentSubmission{SessionID: q.SessionID, RuntimeBinding: cloneRuntimeBinding(q.RuntimeBinding), Skills: in.Skills, ID: in.RequestID, Text: in.Text, TurnID: in.ExpectedTurnID, RunID: q.AgentRunID, State: "pending"}
		}); e != nil {
			fail(w, 500, e)
			return
		}
		p, _ = a.get(p.ID)
	}
	out, e := a.recoverAgent(r.Context(), p, true)
	if e != nil {
		fail(w, 409, e)
		return
	}
	writeJSON(w, 200, out)
}
func (a *App) session(r *http.Request) (Project, error) {
	p, e := a.get(r.PathValue("id"))
	if e == nil && p.SessionID == "" {
		e = errors.New("项目尚未连接 RunDesk")
	}
	return p, e
}
func (a *App) agentState(w http.ResponseWriter, r *http.Request) {
	a.bridgeMu.Lock()
	defer a.bridgeMu.Unlock()
	p, e := a.session(r)
	if e != nil {
		fail(w, 409, e)
		return
	}
	if p.AgentSubmission != nil && p.AgentSubmission.State == "pending" {
		_, _ = a.recoverAgent(r.Context(), p, true)
		p, _ = a.get(p.ID)
	}
	if pending := p.AgentSubmission; pending != nil && pending.CancelRequested && (pending.State == "accepted" || pending.State == "pending" && pending.TurnID != "") && p.AgentRunID != "" {
		err := a.rdCall(r.Context(), "POST", "/sessions/"+p.SessionID+"/stop", map[string]string{"expectedRunId": p.AgentRunID}, nil)
		if e = a.finishAgentCancel(p.ID, err); e != nil {
			fail(w, 409, e)
			return
		}
		p, _ = a.get(p.ID)
	}
	var out map[string]any
	if e = a.rdCall(r.Context(), "GET", "/sessions/"+p.SessionID, nil, &out); e != nil {
		fail(w, 502, e)
		return
	}
	out["appSubmission"] = p.AgentSubmission
	out["appRunId"] = p.AgentRunID
	writeJSON(w, 200, out)
}
func (a *App) agentStop(w http.ResponseWriter, r *http.Request) {
	a.bridgeMu.Lock()
	defer a.bridgeMu.Unlock()
	p, e := a.session(r)
	if e != nil {
		fail(w, 409, e)
		return
	}
	var in struct {
		ExpectedRunID string `json:"expectedRunId"`
	}
	if !decode(w, r, &in) {
		return
	}
	if p.AgentSubmission != nil && p.AgentSubmission.State == "pending" {
		if e = a.saveAgent(p.ID, func(q *Project) { q.AgentSubmission.CancelRequested = true }); e != nil {
			fail(w, 500, e)
			return
		}
		p, _ = a.get(p.ID)
		// For a pending steer, the enclosing run is already known; no re-send needed.
		if p.AgentSubmission.TurnID == "" {
			if _, e = a.recoverAgent(r.Context(), p, false); e != nil {
				fail(w, 409, e)
				return
			}
			p, _ = a.get(p.ID)
		}
	}
	target := in.ExpectedRunID
	if target == "" {
		target = p.AgentRunID
	}
	if p.AgentRunID == "" && target != "" {
		// Upgrade path: only adopt an existing run proven to belong to this project.
		var events []struct {
			Method string `json:"method"`
			Data   struct {
				RunID string `json:"runId"`
				Input struct {
					Text string `json:"text"`
				} `json:"input"`
			} `json:"data"`
		}
		if e = a.rdCall(r.Context(), "GET", "/sessions/"+p.SessionID+"/events?method=run%2Finput&limit=1000", nil, &events); e != nil {
			fail(w, 502, e)
			return
		}
		for _, ev := range events {
			if ev.Method == "run/input" && ev.Data.RunID == target && strings.Contains(ev.Data.Input.Text, "[Video App projectId="+p.ID+"]") {
				if e = a.saveAgent(p.ID, func(q *Project) { q.AgentRunID = target }); e != nil {
					fail(w, 500, e)
					return
				}
				p.AgentRunID = target
				break
			}
		}
	}
	if target == "" || target != p.AgentRunID {
		fail(w, 409, errors.New("目标运行尚未确认属于此应用，请同步或在 RunDesk 中核对"))
		return
	}
	e = a.rdCall(r.Context(), "POST", "/sessions/"+p.SessionID+"/stop", map[string]string{"expectedRunId": target}, nil)
	if p.AgentSubmission != nil && p.AgentSubmission.CancelRequested {
		e = a.finishAgentCancel(p.ID, e)
	}
	if e != nil {
		fail(w, 409, e)
		return
	}

	writeJSON(w, 200, map[string]bool{"ok": true})
}
func (a *App) finishAgentCancel(id string, remoteErr error) error {
	var remote *RunDeskError
	if remoteErr != nil && !(errors.As(remoteErr, &remote) && remote.Code == "run_conflict") {
		return remoteErr
	}
	message := "已请求停止原运行，原指令不再重发"
	if remoteErr != nil {
		message = "会话已进入其他运行；未停止新的运行"
	}
	if e := a.saveAgent(id, func(q *Project) { q.AgentSubmission.State = "canceled"; q.AgentSubmission.Error = message }); e != nil {
		return e
	}
	return remoteErr
}

func (a *App) agentConfiguration(w http.ResponseWriter, r *http.Request) {
	p, e := a.session(r)
	if e != nil {
		fail(w, 409, e)
		return
	}
	var out any
	e = a.rdCall(r.Context(), "GET", "/sessions/"+p.SessionID+"/configuration?probe=1", nil, &out)
	if e != nil {
		fail(w, 502, e)
		return
	}
	writeJSON(w, 200, out)
}
func (a *App) events(w http.ResponseWriter, r *http.Request) {
	p, e := a.session(r)
	if e != nil {
		fail(w, 409, e)
		return
	}
	after, _ := strconv.ParseInt(r.URL.Query().Get("after"), 10, 64)
	if after < 0 {
		after = 0
	}
	var out any
	e = a.rdCall(r.Context(), "GET", fmt.Sprintf("/sessions/%s/events?after=%d", p.SessionID, after), nil, &out)
	if e != nil {
		fail(w, 502, e)
		return
	}
	writeJSON(w, 200, out)
}
func (a *App) approvals(w http.ResponseWriter, r *http.Request) {
	p, e := a.session(r)
	if e != nil {
		fail(w, 409, e)
		return
	}
	var out any
	e = a.rdCall(r.Context(), "GET", "/sessions/"+p.SessionID+"/approvals", nil, &out)
	if e != nil {
		fail(w, 502, e)
		return
	}
	writeJSON(w, 200, out)
}
func (a *App) approve(w http.ResponseWriter, r *http.Request) {
	p, e := a.session(r)
	if e != nil {
		fail(w, 409, e)
		return
	}
	aid := r.PathValue("aid")
	if !identifier.MatchString(aid) {
		fail(w, 400, errors.New("invalid approval ID"))
		return
	}
	var in map[string]any
	if !decode(w, r, &in) {
		return
	}
	var out any
	e = a.rdCall(r.Context(), "POST", "/sessions/"+p.SessionID+"/approvals/"+aid, in, &out)
	if e != nil {
		fail(w, 409, e)
		return
	}
	writeJSON(w, 200, out)
}
