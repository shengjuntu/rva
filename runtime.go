package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
)

var videoTools = []string{"video_narration_update", "video_speech_materialize", "video_edit_get", "video_edit_validate", "video_edit_update", "video_edit_activate", "video_project_create", "video_production_text", "video_production_stage", "video_capabilities_get", "video_asset_list", "video_asset_import", "video_asset_inspect", "video_job_start", "video_job_get", "video_job_list", "video_job_cancel", "video_project_get", "video_news_get", "video_project_update", "video_news_audio_adopt", "video_subtitles_update", "video_speech_start", "video_speech_stop", "video_speech_adopt", "video_render_start", "video_render_stop"}

type runtimeView struct {
	AppID         string `json:"appId"`
	InstanceID    string `json:"instanceId"`
	WorkspaceID   string `json:"workspaceId"`
	ExecutionMode string `json:"executionMode"`
	Image         string `json:"image"`
	Environment   *struct {
		ID            string `json:"id"`
		State         string `json:"state"`
		PendingUpdate bool   `json:"pendingUpdate"`
	} `json:"environment,omitempty"`
	Probed       bool                `json:"probed"`
	AccountReady bool                `json:"accountReady"`
	Servers      map[string][]string `json:"servers,omitempty"`
	Errors       map[string]string   `json:"errors,omitempty"`
}

func (a *App) runtime(ctx context.Context) (runtimeView, error) {
	var v runtimeView
	a.mu.Lock()
	cfg := a.cfg
	a.mu.Unlock()
	if cfg.InstanceID == "" {
		return v, errors.New("请先打开项目并连接 RunDesk，再在 RunDesk 为视频应用选择运行环境")
	}
	if e := a.rdCall(ctx, "GET", "/applications/rundesk-video-app/runtime", nil, &v); e != nil {
		return v, e
	}
	if v.AppID != "rundesk-video-app" || v.InstanceID != cfg.InstanceID || v.WorkspaceID == "" || cfg.WorkspaceID != "" && cfg.WorkspaceID != v.WorkspaceID {
		return v, errors.New("RunDesk 应用绑定已改变，请先核对并重新连接项目")
	}
	return v, nil
}

func (a *App) prepareRuntime(ctx context.Context, p *Project) (runtimeView, error) {
	a.mu.Lock()
	managed := a.cfg.ManagedByRunDesk
	a.mu.Unlock()
	// Preserve the old unmanaged adapter until the user migrates its connection.
	if !managed {
		return runtimeView{ExecutionMode: "local"}, nil
	}
	v, e := a.runtime(ctx)
	if e != nil {
		return v, e
	}
	if p != nil && p.WorkspaceID != v.WorkspaceID {
		return v, errors.New("项目工作区与应用绑定不一致，请先在 RunDesk 核对原会话")
	}
	if p != nil && p.SessionID != "" {
		var session struct {
			InstanceID    string `json:"instanceId"`
			WorkspaceID   string `json:"workspaceId"`
			ExecutionMode string `json:"executionMode"`
		}
		if e = a.rdCall(ctx, "GET", "/sessions/"+p.SessionID, nil, &session); e != nil {
			return v, e
		}
		if session.InstanceID != v.InstanceID || session.WorkspaceID != v.WorkspaceID {
			return v, errors.New("原会话与当前应用实例不一致，未向其他实例提交任务")
		}
		if session.ExecutionMode != v.ExecutionMode {
			return v, errors.New("运行方式已改变：请重新连接以更换空会话；已有对话需点击“新建当前环境对话”，原历史保留在 RunDesk")
		}
	}
	if v.ExecutionMode == "local" {
		return v, nil
	}
	if v.ExecutionMode != "docker" {
		return v, errors.New("不支持的运行环境")
	}
	iid, wid := v.InstanceID, v.WorkspaceID
	e = a.rdCall(ctx, "POST", "/applications/rundesk-video-app/runtime/prepare", map[string]any{"instanceId": iid, "workspaceId": wid, "probe": true}, &v)
	if e != nil {
		return v, e
	}
	if v.AppID != "rundesk-video-app" || v.InstanceID != iid || v.WorkspaceID != wid || v.ExecutionMode != "docker" || v.Environment == nil || v.Environment.State != "running" || v.Environment.PendingUpdate {
		return v, errors.New("Docker 环境尚未就绪或配置已改变，请在 RunDesk 检查")
	}
	if !v.Probed || !v.AccountReady {
		return v, errors.New("请在 RunDesk 完成视频应用 Codex 容器的模型认证")
	}
	if len(v.Errors) != 0 {
		return v, errors.New("Docker 认证或 MCP 探测失败，请在 RunDesk 查看环境诊断")
	}
	available := map[string]bool{}
	for _, name := range v.Servers["video_project"] {
		available[name] = true
	}
	for _, name := range videoTools {
		if !available[name] {
			return v, fmt.Errorf("容器缺少 video_project/%s，请检查 MCP 回调地址与 Token", name)
		}
	}
	return v, nil
}

func (a *App) runtimeStatus(w http.ResponseWriter, r *http.Request) {
	v, e := a.runtime(r.Context())
	if e != nil {
		fail(w, 409, e)
		return
	}
	writeJSON(w, 200, v)
}
func (a *App) runtimePrepare(w http.ResponseWriter, r *http.Request) {
	a.bridgeMu.Lock()
	defer a.bridgeMu.Unlock()
	v, e := a.prepareRuntime(r.Context(), nil)
	if e != nil {
		fail(w, 409, e)
		return
	}
	writeJSON(w, 200, v)
}
