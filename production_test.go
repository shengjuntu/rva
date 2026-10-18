package main

import (
	"encoding/json"
	"strings"
	"testing"
)

func productionCall(t *testing.T, a *App, p Project, text string) Project {
	t.Helper()
	w := call(a, "PUT", "/api/projects/"+p.ID+"/production/text", ProductionRequest{Revision: p.Revision, Text: text})
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var q Project
	if e := json.Unmarshal(w.Body.Bytes(), &q); e != nil {
		t.Fatal(e)
	}
	return q
}
func TestProductionTextPreservesExactTextAndMapping(t *testing.T) {
	a := testApp(t)
	p, _ := a.create("新闻")
	text := "第一条事实。\n第二条事实！"
	p = productionCall(t, a, p, text)
	var b strings.Builder
	for _, s := range p.Shots {
		b.WriteString(s.Text)
	}
	if b.String() != text {
		t.Fatal("text lost")
	}
	first := p.Shots[0]
	p.Shots[0].Title = "已调整标题"
	p.Shots[0].Duration = 7
	p, e := a.edit(p.ID, p)
	if e != nil {
		t.Fatal(e)
	}
	p = productionCall(t, a, p, "新导语。"+text)
	if p.Shots[1].ID != first.ID || p.Shots[1].SegmentID != first.SegmentID || p.Shots[1].Duration != 7 || p.Shots[1].Title != "已调整标题" {
		t.Fatal("unchanged mapping lost", p.Shots)
	}
	if p.Production.Stage != "adjustment" {
		t.Fatal(p.Production)
	}
	p.Shots[1].Text = "无授权的改写"
	if _, e = a.edit(p.ID, p); e == nil {
		t.Fatal("accepted projection divergence")
	}
}
func TestProductionFinalRequiresCurrentCompletedRenderAndInvalidates(t *testing.T) {
	a := testApp(t)
	p, _ := a.create("新闻")
	p = productionCall(t, a, p, "测试正文。")
	rid := uid()
	a.mu.Lock()
	a.projects[p.ID].Renders = append(a.projects[p.ID].Renders, Render{ID: rid, State: "completed", ContentHash: productionHash(p)})
	a.mu.Unlock()
	w := call(a, "POST", "/api/projects/"+p.ID+"/production/stage", ProductionRequest{Revision: p.Revision, Stage: "final", RenderID: rid})
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	p, _ = a.get(p.ID)
	if p.Production.Stage != "final" {
		t.Fatal(p)
	}
	p.Shots[0].Title = "画面发生变化"
	p, e := a.edit(p.ID, p)
	if e != nil {
		t.Fatal(e)
	}
	if p.Production.Stage != "adjustment" || p.Production.FinalRenderID != "" {
		t.Fatal("final stale")
	}
	w = call(a, "POST", "/api/projects/"+p.ID+"/production/stage", ProductionRequest{Revision: p.Revision, Stage: "final", RenderID: rid})
	if w.Code != 409 {
		t.Fatal("old render accepted", w.Code)
	}
	w = call(a, "PUT", "/api/projects/"+p.ID+"/production/text", ProductionRequest{Revision: p.Revision - 1, Text: "覆盖。"})
	if w.Code != 409 {
		t.Fatal("stale update accepted")
	}
}
func TestProductionLimitsAndRoundTrip(t *testing.T) {
	a := testApp(t)
	p, _ := a.create("新闻")
	for _, text := range []string{"", strings.Repeat("句。", 81)} {
		if w := call(a, "PUT", "/api/projects/"+p.ID+"/production/text", ProductionRequest{Revision: p.Revision, Text: text}); w.Code != 400 {
			t.Fatal(w.Code)
		}
	}
	p = productionCall(t, a, p, strings.Repeat("长", 500))
	if len(p.Shots) != 3 {
		t.Fatal(len(p.Shots))
	}
	loaded, e := newApp(a.data)
	if e != nil {
		t.Fatal(e)
	}
	q, e := loaded.get(p.ID)
	if e != nil || q.Production.Text != p.Production.Text {
		t.Fatal("persistence failed", e)
	}
}

func TestDraftCaptionPreviewKeepsReviewAndRejectsStaleBinding(t *testing.T) {
	_, p := adoptedProject(t)
	p.Subtitles.Draft = true
	refreshSubtitleState(&p)
	if p.Subtitles.Status != "unreviewed" || !draftSubtitlePreviewAllowed(p) {
		t.Fatal("valid bound draft blocked")
	}
	if !p.Subtitles.Draft {
		t.Fatal("preview forged review")
	}
	p.Shots[0].Text = "改变正文"
	if draftSubtitlePreviewAllowed(p) {
		t.Fatal("stale binding accepted")
	}
}

func TestFinalCannotUseStaleNarrationWhenCaptionsDisabled(t *testing.T) {
	a, p := adoptedProject(t)
	p = productionCall(t, a, p, storyboardText(p))
	p.Subtitles.Enabled = false
	a.mu.Lock()
	a.projects[p.ID].Subtitles.Enabled = false
	a.mu.Unlock()
	p = productionCall(t, a, p, "正文已经改写。")
	rid := uid()
	a.mu.Lock()
	a.projects[p.ID].Renders = append(a.projects[p.ID].Renders, Render{ID: rid, State: "completed", ContentHash: productionHash(p)})
	a.mu.Unlock()
	w := call(a, "POST", "/api/projects/"+p.ID+"/production/stage", ProductionRequest{Revision: p.Revision, Stage: "final", RenderID: rid})
	if w.Code != 409 {
		t.Fatal("stale narration finalized", w.Body.String())
	}
}
