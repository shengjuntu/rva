package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

var errProjectDeleted = errors.New("原创建请求对应的项目已删除，不会重新创建；请明确发起新项目")

type creationTombstone struct {
	RequestID string `json:"requestId"`
	Hash      string `json:"hash"`
	ProjectID string `json:"projectId"`
}

func (a *App) checkDeletedCreation(requestID, hash string) error {
	if requestID == "" {
		return nil
	}
	raw, err := os.ReadFile(filepath.Join(a.data, "project-trash", "requests", requestID+".json"))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var record creationTombstone
	if json.Unmarshal(raw, &record) != nil || record.RequestID != requestID || record.Hash == "" {
		return errors.New("已删除项目的创建请求记录损坏，未创建新项目")
	}
	if record.Hash != hash {
		return errCreateRequestConflict
	}
	return errProjectDeleted
}

func projectManagementBusy(p Project) bool {
	if sub := p.AgentSubmission; sub != nil {
		if sub.State == "pending" || sub.State == "accepted" && !terminalProductionExecution(sub.ExecutionState) {
			return true
		}
	}
	for _, render := range p.Renders {
		if render.State == "running" || render.State == "stopping" {
			return true
		}
	}
	for _, job := range p.Speech {
		if job.State == "running" || job.State == "stopping" {
			return true
		}
	}
	for _, job := range p.Generation {
		if job.State == "running" || job.State == "stopping" {
			return true
		}
	}
	return false
}

func projectListStatus(p Project) (string, string) {
	if projectManagementBusy(p) {
		if p.AgentSubmission != nil && p.AgentSubmission.State == "accepted" && p.AgentSubmission.ExecutionState == "" {
			return "unknown", "制作状态待确认"
		}
		return "running", "制作中"
	}
	if p.AgentSubmission != nil && p.AgentSubmission.Purpose != "" {
		switch p.AgentSubmission.ExecutionState {
		case "failed", "error":
			return "failed", "制作失败"
		case "interrupted":
			return "interrupted", "制作中断"
		case "stopped", "canceled":
			return "canceled", "已停止"
		}
	}
	if p.Final != nil || p.Production != nil && p.Production.Stage == "final" {
		return "final", "已确认终稿"
	}
	for _, render := range p.Renders {
		if render.State == "completed" && render.Range == nil {
			return "completed", "已出片"
		}
	}
	return "idle", "待制作"
}

func (a *App) projectsList(w http.ResponseWriter, r *http.Request) {
	a.mu.Lock()
	list := make([]struct {
		Project
		Status      string `json:"status"`
		StatusLabel string `json:"statusLabel"`
	}, 0, len(a.projects))
	for _, p := range a.projects {
		state, label := projectListStatus(*p)
		list = append(list, struct {
			Project
			Status      string `json:"status"`
			StatusLabel string `json:"statusLabel"`
		}{clone(p), state, label})
	}
	a.mu.Unlock()
	sort.Slice(list, func(i, j int) bool {
		a, b := list[i].LastOpened, list[j].LastOpened
		if a == "" {
			a = list[i].Updated
		}
		if b == "" {
			b = list[j].Updated
		}
		if a == b {
			return list[i].ID < list[j].ID
		}
		return a > b
	})
	writeJSON(w, 200, list)
}

func (a *App) projectOpened(w http.ResponseWriter, r *http.Request) {
	a.mu.Lock()
	defer a.mu.Unlock()
	p := a.projects[r.PathValue("id")]
	if p == nil {
		fail(w, 404, errors.New("项目不存在"))
		return
	}
	q := clone(p)
	q.LastOpened = time.Now().UTC().Format(time.RFC3339Nano)
	if err := atomicJSON(filepath.Join(a.dir(q.ID), "project.json"), q); err != nil {
		fail(w, 500, err)
		return
	}
	a.projects[q.ID] = &q
	writeJSON(w, 200, map[string]string{"lastOpened": q.LastOpened})
}

func (a *App) projectManagement(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Revision int     `json:"revision"`
		Name     *string `json:"name,omitempty"`
		Archived *bool   `json:"archived,omitempty"`
	}
	if !decodeStrict(w, r, &in) {
		return
	}
	if in.Name == nil && in.Archived == nil {
		fail(w, 422, errors.New("需要项目名称或归档状态"))
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	p := a.projects[r.PathValue("id")]
	if p == nil {
		fail(w, 404, errors.New("项目不存在"))
		return
	}
	if in.Revision != p.Revision {
		fail(w, 409, errors.New("项目已变化，请刷新后重试"))
		return
	}
	q := clone(p)
	if in.Name != nil {
		name := strings.TrimSpace(*in.Name)
		if name == "" || len([]rune(name)) > 80 {
			fail(w, 422, errors.New("项目名称需为 1—80 个字符"))
			return
		}
		q.Name = name
	}
	if in.Archived != nil {
		if *in.Archived && projectManagementBusy(*p) {
			fail(w, 409, errors.New("制作仍在运行或状态待确认，暂不能归档"))
			return
		}
		q.Archived = *in.Archived
	}
	q.Revision++
	if err := a.save(&q); err != nil {
		fail(w, 500, err)
		return
	}
	a.projects[q.ID] = &q
	writeJSON(w, 200, q)
}

func (a *App) projectDelete(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Revision    int    `json:"revision"`
		ConfirmName string `json:"confirmName"`
	}
	if !decodeStrict(w, r, &in) {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	p := a.projects[r.PathValue("id")]
	if p == nil {
		fail(w, 404, errors.New("项目不存在"))
		return
	}
	if in.Revision != p.Revision || in.ConfirmName != p.Name || !p.Archived || projectManagementBusy(*p) {
		fail(w, 409, errors.New("项目版本、名称或运行状态不匹配；请先归档并重新核对"))
		return
	}
	trash := filepath.Join(a.data, "project-trash")
	if err := os.MkdirAll(filepath.Join(trash, "requests"), 0700); err != nil {
		fail(w, 500, err)
		return
	}
	dest := filepath.Join(trash, p.ID+"-"+uid())
	if err := os.Rename(a.dir(p.ID), dest); err != nil {
		fail(w, 500, err)
		return
	}
	if p.CreationRequestID != "" {
		record := creationTombstone{RequestID: p.CreationRequestID, Hash: p.CreationRequestHash, ProjectID: p.ID}
		if err := atomicJSON(filepath.Join(trash, "requests", p.CreationRequestID+".json"), record); err != nil {
			if rollbackErr := os.Rename(dest, a.dir(p.ID)); rollbackErr != nil {
				fail(w, 500, fmt.Errorf("删除未完成且回滚失败：%v；%v", err, rollbackErr))
				return
			}
			fail(w, 500, err)
			return
		}
	}
	delete(a.projects, p.ID)
	writeJSON(w, 200, map[string]any{"ok": true, "id": p.ID})
}
