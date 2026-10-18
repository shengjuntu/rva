package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"
)

type ProductionCheckpoint struct {
	ID         string `json:"id"`
	Stage      string `json:"stage"`
	Status     string `json:"status"`
	SourceHash string `json:"sourceHash"`
	BasisHash  string `json:"basisHash"`
	Summary    string `json:"summary"`
	Document   string `json:"document"`
	RenderID   string `json:"renderId,omitempty"`
	Created    string `json:"created"`
	Revision   int    `json:"revision"`
}
type CheckpointRequest struct {
	Revision   int    `json:"revision"`
	SourceHash string `json:"sourceHash"`
	Stage      string `json:"stage"`
	Status     string `json:"status"`
	Summary    string `json:"summary"`
	Document   string `json:"document"`
	RenderID   string `json:"renderId,omitempty"`
}
type ProductionItem struct {
	ID      string `json:"id"`
	Title   string `json:"title"`
	Kind    string `json:"kind"`
	URL     string `json:"url,omitempty"`
	Text    string `json:"text,omitempty"`
	Summary string `json:"summary,omitempty"`
}
type ProductionCard struct {
	ID      string           `json:"id"`
	Title   string           `json:"title"`
	State   string           `json:"state"`
	Summary string           `json:"summary"`
	Text    string           `json:"text,omitempty"`
	Items   []ProductionItem `json:"items"`
}

func narrationSourceHash(p Project) string { return hashBytes([]byte(p.NarrationText)) }
func checkpointBasis(p Project, stage, renderID string) string {
	v := map[string]any{"text": narrationSourceHash(p), "workflow": workflowFor(p)}
	if stage == "media" || stage == "review" {
		media := []any{}
		for _, asset := range p.Assets {
			if strings.HasPrefix(asset.MIME, "image/") || strings.HasPrefix(asset.MIME, "video/") {
				media = append(media, map[string]any{"id": asset.ID, "name": asset.Name, "mime": asset.MIME, "description": asset.Description})
			}
		}
		v["media"] = media
	}
	if stage == "review" {
		v["dsl"] = p.EditDSL
		v["active"] = p.EditActive
		v["renderId"] = renderID
	}
	raw, _ := json.Marshal(v)
	return hashBytes(raw)
}
func (a *App) productionCheckpoint(w http.ResponseWriter, r *http.Request) {
	var in CheckpointRequest
	if !decodeStrict(w, r, &in) {
		return
	}
	if (in.Stage != "plan" && in.Stage != "media" && in.Stage != "review") || (in.Status != "working" && in.Status != "completed" && in.Status != "blocked") || strings.TrimSpace(in.Summary) == "" || len(in.Summary) > 2000 || strings.TrimSpace(in.Document) == "" || len(in.Document) > 32000 || !utf8.ValidString(in.Summary+in.Document) || strings.ContainsRune(in.Summary+in.Document, 0) {
		fail(w, 422, fmt.Errorf("需要plan/media/review阶段、有效状态、简述及最多32000字节的自由制作记录"))
		return
	}
	if in.RenderID != "" && !identifier.MatchString(in.RenderID) {
		fail(w, 422, fmt.Errorf("renderId格式无效"))
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	p := a.projects[r.PathValue("id")]
	if p == nil {
		fail(w, 404, fmt.Errorf("项目不存在"))
		return
	}
	if p.Revision != in.Revision || strings.TrimSpace(p.NarrationText) == "" || in.SourceHash != narrationSourceHash(*p) {
		fail(w, 409, fmt.Errorf("项目修订或权威正文指纹已变化，请重新读取"))
		return
	}
	if in.Stage != "review" && in.RenderID != "" {
		fail(w, 422, fmt.Errorf("只有检查记录接受renderId"))
		return
	}
	if in.Stage == "review" && in.Status == "completed" {
		valid := false
		for _, j := range p.Renders {
			if j.ID == in.RenderID && j.State == "completed" && j.Engine == "ffmpeg_edit" && j.Range == nil && j.ContentHash == productionHash(*p) && j.SourceHash == narrationSourceHash(*p) {
				valid = true
			}
		}
		if !valid {
			fail(w, 409, fmt.Errorf("完成检查需引用当前正文及完整DSL的已完成渲染"))
			return
		}
	}
	q := clone(p)
	record := ProductionCheckpoint{ID: uid(), Stage: in.Stage, Status: in.Status, SourceHash: in.SourceHash, BasisHash: checkpointBasis(q, in.Stage, in.RenderID), Summary: in.Summary, Document: in.Document, RenderID: in.RenderID, Created: time.Now().UTC().Format(time.RFC3339Nano), Revision: p.Revision}
	q.Checkpoints = append(q.Checkpoints, record)
	if len(q.Checkpoints) > 30 {
		q.Checkpoints = q.Checkpoints[len(q.Checkpoints)-30:]
	}
	q.Revision++
	if e := a.save(&q); e != nil {
		fail(w, 500, e)
		return
	}
	a.projects[q.ID] = &q
	writeJSON(w, 200, map[string]any{"revision": q.Revision, "checkpoint": record})
}
func checkpointCard(p Project, id, title string) ProductionCard {
	card := ProductionCard{ID: id, Title: title, State: "empty", Summary: "尚无制作记录", Items: []ProductionItem{}}
	for i := len(p.Checkpoints) - 1; i >= 0; i-- {
		c := p.Checkpoints[i]
		if c.Stage != id {
			continue
		}
		card.State = map[string]string{"working": "running", "completed": "ready", "blocked": "blocked"}[c.Status]
		card.Summary = c.Summary
		if id == "review" && c.RenderID != "" {
			card.Items = append(card.Items, ProductionItem{ID: c.RenderID, Title: "此报告检查的成片", Kind: "video", URL: "/api/projects/" + p.ID + "/renders/" + c.RenderID + "/final.mp4"})
			for i := len(p.Renders) - 1; i >= 0; i-- {
				j := p.Renders[i]
				if j.State == "completed" && j.Range == nil && j.Engine == "ffmpeg_edit" && j.ContentHash == productionHash(p) && j.SourceHash == narrationSourceHash(p) {
					if j.ID != c.RenderID {
						card.State = "stale"
						card.Summary = "已有较新的完整成片，此报告仅对应所附旧成片。" + c.Summary
					}
					break
				}
			}
		}
		card.Items = append(card.Items, ProductionItem{ID: c.ID, Title: "制作记录", Kind: "markdown", Text: c.Document, Summary: "Agent保存的说明，不代表人工验收"})
		if c.BasisHash != checkpointBasis(p, id, c.RenderID) || c.SourceHash != narrationSourceHash(p) {
			card.State = "stale"
			card.Summary = "输入已变化，需重新核对。" + c.Summary
		}
		break
	}
	return card
}
func nativeCardState(state string) string {
	switch state {
	case "running", "stopping":
		return "running"
	case "completed":
		return "ready"
	case "failed", "interrupted":
		return "failed"
	}
	return "empty"
}
func (a *App) productionBoard(w http.ResponseWriter, r *http.Request) {
	p, e := a.get(r.PathValue("id"))
	if e != nil {
		fail(w, 404, e)
		return
	}
	script := ProductionCard{ID: "script", Title: "权威正文", State: "empty", Summary: "提供文字后，由RunDesk完成制作", Text: p.NarrationText, Items: []ProductionItem{}}
	if strings.TrimSpace(p.NarrationText) != "" {
		script.State = "ready"
		script.Summary = "已保存原稿；素材与剪辑以这份文字为依据"
	}
	speech := ProductionCard{ID: "speech", Title: "配音与字幕", State: "empty", Summary: "尚无独立口播配音", Items: []ProductionItem{}}
	projection := clone(&p)
	projection.Shots = narrationShots(p.NarrationText)
	for i := len(p.Speech) - 1; i >= 0; i-- {
		j := p.Speech[i]
		if j.Source != "narration" {
			continue
		}
		speech.State = nativeCardState(j.State)
		speech.Summary = fmt.Sprintf("%s · %.2f秒 · 合成片段时序，非逐字对齐", speechBackend(j.Backend), j.Duration)
		if j.Error != "" {
			speech.Summary = j.Error
		}
		if j.ScriptHash != speechScriptHash(projection) {
			speech.State = "stale"
			speech.Summary = "正文已变化，此配音属于旧稿"
		}
		if j.State == "completed" {
			base := "/api/projects/" + p.ID + "/speech/" + j.ID + "/files/"
			for _, item := range []ProductionItem{{ID: j.ID + "-audio", Title: "试听配音", Kind: "audio", URL: base + "narration.wav"}, {ID: j.ID + "-subtitles", Title: "字幕 SRT", Kind: "link", URL: base + "subtitles.srt"}, {ID: j.ID + "-timing", Title: "实测时间清单", Kind: "json", URL: base + "manifest.json"}} {
				speech.Items = append(speech.Items, item)
			}
		}
		break
	}
	media := checkpointCard(p, "media", "文字与画面安排")
	visualCount := 0
	for _, asset := range p.Assets {
		kind := ""
		if strings.HasPrefix(asset.MIME, "image/") {
			kind = "image"
		}
		if strings.HasPrefix(asset.MIME, "video/") {
			kind = "video"
		}
		if kind == "" {
			continue
		}
		visualCount++
		if visualCount <= 12 {
			media.Items = append(media.Items, ProductionItem{ID: asset.ID, Title: asset.Name, Kind: kind, URL: "/api/projects/" + p.ID + "/assets/" + asset.ID})
		}
	}
	if media.State == "empty" && visualCount > 0 {
		media.Summary = fmt.Sprintf("已有%d份视觉素材，尚无文字覆盖与选用理由记录", visualCount)
	}
	edit := ProductionCard{ID: "edit", Title: "剪辑方案", State: "empty", Summary: "尚未保存剪辑方案", Items: []ProductionItem{}}
	if p.EditDSL != nil {
		ir, err := compileEdit(*p.EditDSL, p.Assets)
		if err != nil {
			edit.State = "failed"
			edit.Summary = "当前剪辑无法编译"
		} else {
			edit.State = "ready"
			edit.Summary = fmt.Sprintf("%d条轨道 · %d个片段 · %.2f秒；%dx%d", len(p.EditDSL.Tracks), len(ir.Clips), ir.Duration, ir.Canvas.Width, ir.Canvas.Height)
			if !p.EditActive {
				edit.State = "stale"
				edit.Summary += "；尚未启用"
			}
			edit.Items = append(edit.Items, ProductionItem{ID: "dsl", Title: "剪辑详情", Kind: "json", URL: "/api/projects/" + p.ID + "/edit"})
		}
	}
	render := ProductionCard{ID: "render", Title: "初稿与调整", State: "empty", Summary: "尚无渲染任务", Items: []ProductionItem{}}
	for i := len(p.Renders) - 1; i >= 0; i-- {
		j := p.Renders[i]
		render.State = nativeCardState(j.State)
		render.Summary = fmt.Sprintf("修订%d · %.2f秒", j.Revision, j.Duration)
		if j.Range != nil {
			render.Summary += " · 局部预览"
		}
		if j.Error != "" {
			render.Summary = j.Error
		}
		if j.ContentHash != productionHash(p) || j.SourceHash != narrationSourceHash(p) {
			render.State = "stale"
			render.Summary += " · 对应旧内容"
		}
		if j.State == "completed" {
			render.Items = append(render.Items, ProductionItem{ID: j.ID, Title: "观看制作结果", Kind: "video", URL: "/api/projects/" + p.ID + "/renders/" + j.ID + "/final.mp4"})
		}
		break
	}
	final := ProductionCard{ID: "final", Title: "终稿记录", State: "empty", Summary: "初稿完成后，通过对话提出修改或确认终稿", Items: []ProductionItem{}}
	if p.Final != nil {
		final.State = "ready"
		final.Summary = "已记录人工确认的完整成片；字节完整性可在高级工具复核"
		if !usesEditDSL(p) || p.Final.ContentHash != productionHash(p) || p.Final.NarrationHash != narrationSourceHash(p) {
			final.State = "stale"
			final.Summary = "终稿对应旧内容"
		}
		final.Items = append(final.Items, ProductionItem{ID: p.Final.ID, Title: "查看已确认成片", Kind: "video", URL: "/api/projects/" + p.ID + "/renders/" + p.Final.RenderID + "/final.mp4"})
	}
	writeJSON(w, 200, map[string]any{"projectId": p.ID, "revision": p.Revision, "sourceHash": narrationSourceHash(p), "stages": []ProductionCard{script, checkpointCard(p, "plan", "制作简报"), speech, media, edit, render, checkpointCard(p, "review", "结果检查"), final}})
}
