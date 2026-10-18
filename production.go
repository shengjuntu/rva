package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
)

// Text is the authoring source; shots are an ordered, editable projection.
type TextSegment struct {
	ID   string `json:"id"`
	Text string `json:"text"`
}
type Production struct {
	Text          string        `json:"text"`
	Segments      []TextSegment `json:"segments"`
	Stage         string        `json:"stage"`
	FinalRenderID string        `json:"finalRenderId,omitempty"`
	FinalHash     string        `json:"finalHash,omitempty"`
}
type ProductionRequest struct {
	Revision int    `json:"revision"`
	Text     string `json:"text,omitempty"`
	Stage    string `json:"stage,omitempty"`
	RenderID string `json:"renderId,omitempty"`
}

func productionNarrationCurrent(p Project) bool {
	if p.Audio == nil || p.Audio.Narration.AssetID == "" {
		return true
	}
	if p.Subtitles == nil {
		return false
	}
	n, b := p.Audio.Narration, p.Subtitles.Binding
	return b.AssetID == n.AssetID && b.TextHash == hashBytes([]byte(storyboardText(p))) && b.ScriptHash == speechScriptHash(p) && b.Start == n.Start && b.SourceStart == n.SourceStart && b.SHA256 != ""
}
func productionHash(p Project) string {
	if usesEditDSL(p) {
		b, _ := json.Marshal(p.EditDSL)
		return hashBytes(b)
	}
	b, _ := json.Marshal(struct {
		Shots     []Shot
		Audio     *AudioMix
		Subtitles *SubtitleTrack
	}{p.Shots, p.Audio, p.Subtitles})
	return hashBytes(b)
}
func refreshProduction(p *Project) {
	if p.Production != nil && p.Production.Stage == "final" && p.Production.FinalHash != productionHash(*p) {
		p.Production.Stage = "adjustment"
		p.Production.FinalHash = ""
		p.Production.FinalRenderID = ""
	}
}
func splitProductionText(text string) []string {
	parts := []string{}
	chunk := []rune{}
	for _, c := range text {
		chunk = append(chunk, c)
		if len(chunk) >= 220 || strings.ContainsRune("。！？!?\n", c) {
			parts = append(parts, string(chunk))
			chunk = nil
		}
	}
	if len(chunk) > 0 {
		parts = append(parts, string(chunk))
	}
	return parts
}
func validateTextProjection(prod *Production, shots []Shot) error {
	if prod == nil {
		return nil
	}
	if len(shots) != len(prod.Segments) {
		return errors.New("正文段落数量已固定，请通过正文入口调整文本")
	}
	for i, s := range shots {
		if s.SegmentID != prod.Segments[i].ID || s.Text != prod.Segments[i].Text {
			return errors.New("镜头必须保持正文顺序与原文；请通过正文入口修改文本")
		}
	}
	return nil
}
func (a *App) production(w http.ResponseWriter, r *http.Request) {
	var in ProductionRequest
	if !decode(w, r, &in) {
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
		fail(w, 409, errors.New("项目已修改，请重新读取后合并"))
		return
	}
	q := clone(p)
	if r.Method == "PUT" {
		if strings.TrimSpace(in.Text) == "" || len(in.Text) > 128*1024 {
			fail(w, 400, errors.New("正文不能为空且不能超过128KiB"))
			return
		}
		parts := splitProductionText(in.Text)
		if len(parts) > 80 {
			fail(w, 400, errors.New("正文最多80段，请缩短或拆分项目"))
			return
		}
		if q.Production != nil && q.Production.Text == in.Text {
			writeJSON(w, 200, q)
			return
		}
		old := q.Shots
		used := map[string]bool{}
		total := 0.0
		prod := &Production{Text: in.Text, Segments: []TextSegment{}, Stage: "draft"}
		if len(old) > 0 {
			prod.Stage = "adjustment"
		}
		q.Shots = []Shot{}
		for i, text := range parts {
			shot := Shot{ID: uid(), SegmentID: uid(), Text: text, Title: fmt.Sprintf("第 %d 段", i+1), Duration: float64(len([]rune(text))) / 4.5}
			if shot.Duration < 1 {
				shot.Duration = 1
			}
			if shot.Duration > 60 {
				shot.Duration = 60
			}
			// Keep exact unchanged paragraphs and their selected media, even after insertion.
			for _, s := range old {
				if !used[s.ID] && s.Text == text {
					shot = s
					used[s.ID] = true
					if shot.SegmentID == "" {
						shot.SegmentID = uid()
					}
					break
				}
			}
			total += shot.Duration
			prod.Segments = append(prod.Segments, TextSegment{ID: shot.SegmentID, Text: text})
			q.Shots = append(q.Shots, shot)
		}
		if total > 600 {
			fail(w, 400, errors.New("估计时长超过10分钟，请缩短正文"))
			return
		}
		// Keep old sound visible but block it until regenerated/rebound, rather than silently clearing it.
		if e := validateAudio(q.Audio, q.Assets, total); e != nil {
			fail(w, 409, fmt.Errorf("正文时长变化与音轨冲突，请先调整音轨：%w", e))
			return
		}
		q.Production = prod
	} else {
		if q.Production == nil {
			fail(w, 409, errors.New("请先保存制作正文"))
			return
		}
		switch in.Stage {
		case "draft", "adjustment":
			q.Production.Stage = in.Stage
			q.Production.FinalHash = ""
			q.Production.FinalRenderID = ""
		case "final":
			if usesEditDSL(q) {
				fail(w, 409, errors.New("当前终稿确认仅用于旧正文模式，剪辑模式请先检查渲染结果"))
				return
			}
			if e := validateTextProjection(q.Production, q.Shots); e != nil {
				fail(w, 409, e)
				return
			}
			refreshSubtitleState(&q)
			if !productionNarrationCurrent(q) {
				fail(w, 409, errors.New("当前配音未绑定这版正文，请重新采用配音或核对字幕后再确认终稿"))
				return
			}
			if q.Subtitles != nil && q.Subtitles.Enabled && q.Subtitles.Status != "ready" {
				fail(w, 409, errors.New("终稿前请试听并核对当前字幕"))
				return
			}
			hash := productionHash(q)
			found := false
			for _, job := range q.Renders {
				if job.ID == in.RenderID && job.State == "completed" && job.ContentHash == hash {
					found = true
					break
				}
			}
			if !found {
				fail(w, 409, errors.New("只能选用与当前正文、素材、音轨和字幕一致的已完成视频作为终稿"))
				return
			}
			q.Production.Stage = "final"
			q.Production.FinalHash = hash
			q.Production.FinalRenderID = in.RenderID
		default:
			fail(w, 400, errors.New("阶段必须是draft、adjustment或final"))
			return
		}
	}
	q.Revision++
	if e := a.save(&q); e != nil {
		fail(w, 500, e)
		return
	}
	a.projects[q.ID] = &q
	writeJSON(w, 200, q)
}
