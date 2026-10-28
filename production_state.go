package main

import (
	"errors"
	"net/http"
	"sort"
	"time"
)

func terminalProductionExecution(state string) bool {
	switch state {
	case "completed", "failed", "error", "interrupted", "stopped", "canceled":
		return true
	}
	return false
}

// Only a request explicitly carried by the current production submission may
// claim work as the new movie. Omitting the owner keeps the old tool behavior,
// and its output remains available exclusively through historical versions.
func validateProductionOwner(p Project, requestID string) error {
	if requestID == "" {
		return nil
	}
	if !requestIdentifier.MatchString(requestID) || p.AgentSubmission == nil || p.AgentSubmission.Purpose == "" || p.AgentSubmission.ID != requestID {
		return errors.New("productionRequestId不属于项目当前制作请求，请读取当前请求并核对")
	}
	return nil
}

func (a *App) observeProductionRemote(p Project, remote map[string]any) (Project, error) {
	sub := p.AgentSubmission
	if sub == nil || sub.Purpose == "" || sub.State != "accepted" || sub.RunID == "" {
		return p, nil
	}
	runID, _ := remote["runId"].(string)
	status, _ := remote["status"].(string)
	if runID != sub.RunID || status == "" {
		return p, errors.New("远端运行编号或状态不属于当前制作")
	}
	if status != sub.ExecutionState {
		err := a.saveAgent(p.ID, func(q *Project) {
			if q.AgentSubmission != nil && q.AgentSubmission.ID == sub.ID && q.AgentSubmission.RunID == runID {
				q.AgentSubmission.ExecutionState = status
				q.AgentSubmission.ExecutionObservedAt = time.Now().UTC().Format(time.RFC3339Nano)
			}
		})
		if err != nil {
			return p, err
		}
		return a.get(p.ID)
	}
	return p, nil
}

type productionUpdate struct {
	ID       string `json:"id"`
	Stage    string `json:"stage"`
	Status   string `json:"status"`
	Summary  string `json:"summary"`
	Created  string `json:"created"`
	RenderID string `json:"renderId,omitempty"`
}

type productionPreview struct {
	RenderID   string  `json:"renderId"`
	Time       float64 `json:"time"`
	InspectURL string  `json:"inspectURL"`
}

type productionStateView struct {
	ProjectID      string             `json:"projectId"`
	Revision       int                `json:"revision"`
	RequestID      string             `json:"requestId,omitempty"`
	Purpose        string             `json:"purpose,omitempty"`
	State          string             `json:"state"`
	ExecutionState string             `json:"executionState,omitempty"`
	StatusKnown    bool               `json:"statusKnown"`
	Summary        string             `json:"summary"`
	StartedAt      string             `json:"startedAt,omitempty"`
	RunID          string             `json:"runId,omitempty"`
	RenderID       string             `json:"renderId,omitempty"`
	Render         *Render            `json:"render,omitempty"`
	Preview        *productionPreview `json:"preview,omitempty"`
	Jobs           []ProductionJob    `json:"jobs"`
	Updates        []productionUpdate `json:"updates"`
	ProgressKnown  bool               `json:"progressKnown"`
	Error          string             `json:"error,omitempty"`
}

func (a *App) productionState(w http.ResponseWriter, r *http.Request) {
	p, err := a.get(r.PathValue("id"))
	if err != nil {
		fail(w, 404, err)
		return
	}
	v := productionStateView{ProjectID: p.ID, Revision: p.Revision, State: "idle", Summary: "准备制作", Jobs: []ProductionJob{}, Updates: []productionUpdate{}}
	sub := p.AgentSubmission
	if sub == nil || sub.Purpose == "" {
		writeJSON(w, 200, v)
		return
	}
	v.RequestID, v.Purpose, v.StartedAt, v.RunID = sub.ID, sub.Purpose, sub.Created, sub.RunID
	if sub.State == "accepted" && sub.RunID != "" && p.SessionID == sub.SessionID {
		var remote map[string]any
		if err := a.rdCall(r.Context(), "GET", "/sessions/"+p.SessionID, nil, &remote); err == nil {
			if next, observationError := a.observeProductionRemote(p, remote); observationError == nil {
				p, sub, v.Revision, v.StatusKnown = next, next.AgentSubmission, next.Revision, true
			}
		}
	}
	v.ExecutionState = sub.ExecutionState
	for _, item := range p.Checkpoints {
		if item.ProductionRequestID == sub.ID {
			v.Updates = append(v.Updates, productionUpdate{ID: item.ID, Stage: item.Stage, Status: item.Status, Summary: item.Summary, Created: item.Created, RenderID: item.RenderID})
		}
	}
	sort.Slice(v.Updates, func(i, j int) bool { return v.Updates[i].Created < v.Updates[j].Created })
	if len(v.Updates) > 8 {
		v.Updates = v.Updates[len(v.Updates)-8:]
	}
	for _, item := range p.Speech {
		if item.ProductionRequestID == sub.ID {
			v.Jobs = append(v.Jobs, speechJobView(p.ID, item))
		}
	}
	for _, item := range p.Generation {
		if item.ProductionRequestID == sub.ID {
			v.Jobs = append(v.Jobs, generationView(p.ID, item))
		}
	}
	var full *Render
	for _, item := range p.Renders {
		if item.ProductionRequestID != sub.ID || item.State != "completed" {
			continue
		}
		v.Jobs = append(v.Jobs, renderJobView(p.ID, item))
		if item.Duration > 0 {
			mid := item.Duration / 2
			if mid > 1 {
				mid = 1
			}
			v.Preview = &productionPreview{RenderID: item.ID, Time: mid, InspectURL: "/api/projects/" + p.ID + "/renders/" + item.ID + "/inspect"}
		}
		if item.Range == nil && renderCurrent(p, item) {
			j := item
			full = &j
		}
	}
	if len(v.Updates) != 0 {
		v.Summary = v.Updates[len(v.Updates)-1].Summary
	}
	switch {
	case sub.State == "pending":
		v.State, v.Summary = "pending", "正在核对制作请求"
	case sub.State == "failed":
		v.State, v.Summary, v.Error = "failed", "制作请求未获接收", sub.Error
	case sub.State == "canceled":
		v.State, v.Summary = "canceled", "制作已停止"
	case !v.StatusKnown:
		v.State, v.Summary = "unknown", "正在核对本次制作的运行状态"
	case sub.ExecutionState == "failed" || sub.ExecutionState == "error":
		v.State, v.Summary = "failed", "本次制作失败"
	case sub.ExecutionState == "interrupted":
		v.State, v.Summary = "interrupted", "本次制作中断"
	case sub.ExecutionState == "stopped" || sub.ExecutionState == "canceled":
		v.State, v.Summary = "canceled", "本次制作已停止"
	case sub.ExecutionState == "completed":
		if full != nil {
			v.State, v.Summary, v.RenderID, v.Render = "completed", "本次完整视频已完成", full.ID, full
		} else {
			v.State, v.Summary = "no_output", "运行已结束，尚未有可确认的完整新片"
		}
	default:
		v.State = "running"
		if sub.ExecutionState == "waiting" {
			v.Summary = "制作等待处理，请在 RunDesk 查看"
		} else if v.Summary == "准备制作" {
			v.Summary = "正在制作本次视频"
		}
	}
	// A completed snapshot can be shown only after the matching remote run has
	// reached its terminal success state; previews are single real frames only.
	if v.State != "running" && v.State != "pending" {
		v.Preview = nil
	}
	writeJSON(w, 200, v)
}
