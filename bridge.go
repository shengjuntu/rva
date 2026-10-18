package main

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
	"path/filepath"
	"strconv"
	"strings"
)

func (a *App) rdCall(ctx context.Context, method, path string, body, out any) error {
	var b io.Reader
	if body != nil {
		x, e := json.Marshal(body)
		if e != nil {
			return e
		}
		b = bytes.NewReader(x)
	}
	req, e := http.NewRequestWithContext(ctx, method, a.rd+"/api"+path, b)
	if e != nil {
		return e
	}
	req.Header.Set("Content-Type", "application/json")
	if a.rdToken != "" {
		req.Header.Set("Authorization", "Bearer "+a.rdToken)
	}
	resp, e := a.client.Do(req)
	if e != nil {
		return e
	}
	defer resp.Body.Close()
	raw, e := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if e != nil {
		return e
	}
	if resp.StatusCode >= 300 {
		var v struct {
			Error string `json:"error"`
		}
		_ = json.Unmarshal(raw, &v)
		if v.Error == "" {
			v.Error = resp.Status
		}
		return errors.New(v.Error)
	}
	if out != nil {
		return json.Unmarshal(raw, out)
	}
	return nil
}

const projectSkill = `---
name: video-project
description: Manage this video app project through video_project MCP tools. Use when creating or editing a storyboard, inspecting assets, rendering a video, or revising an existing video project.
---
# Video project workflow
Read video_project_get before editing. The user's message supplies the projectId.
Project JSON is authoritative. Use video_project_update with the current revision and the complete name, brief, shots, and audio when changing sound. Preserve untouched shots and their IDs. A revision conflict requires re-reading and merging; never overwrite another edit.
Each shot: id (omit for a new shot), title (max 80 characters), text (max 220 characters), duration (1–60 seconds), assetId (an existing image or video asset ID, or empty). Maximum 80 shots and 600 seconds total. Use real available assets; do not invent filenames. Assets are readable within the project workspace at assets/<file>, where file comes from each asset record. Documents are reference material. Audio can be assigned to narration/music tracks.
Audio fields: audio.narration and audio.music each contain assetId (uploaded audio, or empty), volume (linear gain 0–2; 1 = 100%), start (seconds into the video), sourceStart (seconds to skip in the source audio), loop (music only). Always set volume explicitly when selecting audio. Narration plays once; music may loop the trimmed source. Start must be less than total video duration. To clear sound, send audio with both assetId empty; omitting audio preserves existing tracks. Source audio is copied into each render snapshot and mixed by FFmpeg. No TTS service is configured: do not claim speech was generated from text. Video backgrounds remain muted.
The renderer produces a vertical 1080x1920, 30fps HTML/GSAP composition from these fields. Do not claim changes to arbitrary generated HTML are reflected in the storyboard; the next render regenerates it.
Propose the script and storyboard first. On the user's instruction to produce a video, save the storyboard and use video_render_start. Check progress with video_project_get. A render uses an immutable revision snapshot. Never claim a render succeeded before state=completed.
Do not modify project.json, access-token, or app configuration directly. Do not install global packages, change permissions, or configure other MCP servers. The app provides its own runtime setup script.
Stopping the agent does not stop a render. Use video_render_stop to cancel the render task when asked.
`

func (a *App) connect(w http.ResponseWriter, r *http.Request) {
	a.bridgeMu.Lock()
	defer a.bridgeMu.Unlock()
	id := r.PathValue("id")
	p, e := a.get(id)
	if e != nil {
		fail(w, 404, e)
		return
	}
	if p.SessionID != "" && p.ToolsVersion == version && r.URL.Query().Get("refresh") != "1" {
		writeJSON(w, 200, p)
		return
	}
	a.mu.Lock()
	iid := a.cfg.InstanceID
	a.mu.Unlock()
	if iid == "" {
		var inst struct {
			ID string `json:"id"`
		}
		e = a.rdCall(r.Context(), "POST", "/instances", map[string]any{"name": "视频制作 App", "description": "RunDesk Video App 专用配置"}, &inst)
		if e != nil {
			fail(w, 502, e)
			return
		}
		iid = inst.ID
		a.mu.Lock()
		a.cfg.InstanceID = iid
		e = atomicJSON(filepath.Join(a.data, "config.json"), a.cfg)
		a.mu.Unlock()
		if e != nil {
			fail(w, 500, e)
			return
		}
	}
	// Configuration belongs to the shared video instance. Do not reload it
	// while another project is running through this App.
	var sessions []struct {
		InstanceID string `json:"instanceId"`
		Status     string `json:"status"`
	}
	if e = a.rdCall(r.Context(), "GET", "/sessions", nil, &sessions); e != nil {
		fail(w, 502, e)
		return
	}
	for _, ss := range sessions {
		if ss.InstanceID == iid && (ss.Status == "running" || ss.Status == "waiting" || ss.Status == "starting" || ss.Status == "stopping") {
			fail(w, 409, errors.New("视频实例还有运行中的任务，请结束后再更新工具配置"))
			return
		}
	}
	if p.WorkspaceID == "" {
		var ws struct {
			ID string `json:"id"`
		}
		e = a.rdCall(r.Context(), "POST", "/workspaces", map[string]string{"name": "视频 · " + p.Name, "path": filepath.Join(a.dir(id), "workspace")}, &ws)
		if e != nil {
			fail(w, 502, e)
			return
		}
		a.mu.Lock()
		q := clone(a.projects[id])
		q.WorkspaceID = ws.ID
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
	suffix := "?instanceId=" + url.QueryEscape(iid)
	e = a.rdCall(r.Context(), "PUT", "/workspaces/"+p.WorkspaceID+"/skills/video-project"+suffix, map[string]string{"content": projectSkill}, nil)
	if e != nil {
		fail(w, 502, e)
		return
	}
	var mcp struct {
		Version string `json:"version"`
	}
	e = a.rdCall(r.Context(), "GET", "/workspaces/"+p.WorkspaceID+"/mcp"+suffix, nil, &mcp)
	if e != nil {
		fail(w, 502, e)
		return
	}
	binary, e := os.Executable()
	if e != nil {
		fail(w, 500, e)
		return
	}
	e = a.rdCall(r.Context(), "PUT", "/workspaces/"+p.WorkspaceID+"/mcp/video_project"+suffix, map[string]any{"version": mcp.Version, "config": map[string]any{"command": binary, "args": []string{"mcp", "--url", a.public}, "env": map[string]string{"VIDEO_APP_TOKEN": a.token}, "enabled": true}}, nil)
	if e != nil {
		fail(w, 502, e)
		return
	}
	if p.SessionID == "" {
		var ss struct {
			ID string `json:"id"`
		}
		e = a.rdCall(r.Context(), "POST", "/sessions", map[string]string{"workspaceId": p.WorkspaceID, "instanceId": iid, "title": "视频 · " + p.Name}, &ss)
		if e != nil {
			fail(w, 502, e)
			return
		}
		a.mu.Lock()
		q := clone(a.projects[id])
		q.SessionID = ss.ID
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
		Text           string `json:"text"`
		RequestID      string `json:"requestId"`
		ExpectedTurnID string `json:"expectedTurnId"`
	}
	if !decode(w, r, &in) {
		return
	}
	if strings.TrimSpace(in.Text) == "" || len(in.Text) > 128<<10 {
		fail(w, 400, errors.New("消息为空或过长"))
		return
	}
	p, e := a.get(r.PathValue("id"))
	if e != nil || p.SessionID == "" {
		fail(w, 409, errors.New("请先连接 RunDesk"))
		return
	}
	if p.ToolsVersion != version {
		fail(w, 409, errors.New("请先更新项目工具配置，再开始对话"))
		return
	}
	text := fmt.Sprintf("[Video App projectId=%s] 使用 video-project Skill 和 video_project MCP。先读取项目当前 revision。\n用户要求：\n%s", p.ID, in.Text)
	path := "/sessions/" + p.SessionID + "/turns"
	body := map[string]any{"text": text, "files": []string{}, "skills": []any{}}
	if in.ExpectedTurnID != "" {
		path = "/sessions/" + p.SessionID + "/steer"
		body["expectedTurnId"] = in.ExpectedTurnID
		body["requestId"] = in.RequestID
	}
	var out any
	e = a.rdCall(r.Context(), "POST", path, body, &out)
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
	p, e := a.session(r)
	if e != nil {
		fail(w, 409, e)
		return
	}
	var out any
	e = a.rdCall(r.Context(), "GET", "/sessions/"+p.SessionID, nil, &out)
	if e != nil {
		fail(w, 502, e)
		return
	}
	writeJSON(w, 200, out)
}
func (a *App) agentStop(w http.ResponseWriter, r *http.Request) {
	p, e := a.session(r)
	if e != nil {
		fail(w, 409, e)
		return
	}
	e = a.rdCall(r.Context(), "POST", "/sessions/"+p.SessionID+"/stop", map[string]any{}, nil)
	if e != nil {
		fail(w, 409, e)
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
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
