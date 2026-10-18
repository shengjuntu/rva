package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func readBoard(t *testing.T, a *App, id string) (map[string]ProductionCard, string) {
	t.Helper()
	w := call(a, "GET", "/api/projects/"+id+"/production-board", nil)
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	var v struct {
		SourceHash string
		Stages     []ProductionCard
	}
	if e := json.Unmarshal(w.Body.Bytes(), &v); e != nil {
		t.Fatal(e)
	}
	out := map[string]ProductionCard{}
	for _, c := range v.Stages {
		out[c.ID] = c
	}
	return out, v.SourceHash
}
func TestProductionBoardRecordsAndStaleness(t *testing.T) {
	a := testApp(t)
	p, _ := a.create("board")
	url := "/api/projects/" + p.ID
	cards, _ := readBoard(t, a, p.ID)
	for _, id := range []string{"script", "plan", "speech", "media", "edit", "render", "review", "final"} {
		if cards[id].State != "empty" {
			t.Fatal(id, cards[id])
		}
	}
	w := call(a, "PUT", url+"/narration", map[string]any{"revision": p.Revision, "text": "权威原稿。\n"})
	json.Unmarshal(w.Body.Bytes(), &p)
	cards, hash := readBoard(t, a, p.ID)
	if hash != hashBytes([]byte(p.NarrationText)) || cards["script"].State != "ready" {
		t.Fatal(cards)
	}
	req := CheckpointRequest{Revision: p.Revision, SourceHash: hash, Stage: "plan", Status: "completed", Summary: "按原稿安排", Document: "保留事实，不按图片重写文稿。"}
	w = call(a, "POST", url+"/production-checkpoints", req)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	q, _ := a.get(p.ID)
	cards, _ = readBoard(t, a, p.ID)
	if cards["plan"].State != "ready" || len(cards["plan"].Items) != 1 {
		t.Fatal(cards["plan"])
	}
	req.Revision = q.Revision
	req.SourceHash = hashBytes([]byte(strings.TrimSpace(q.NarrationText)))
	w = call(a, "POST", url+"/production-checkpoints", req)
	if w.Code != 409 {
		t.Fatal("normalized source hash accepted")
	}
	req.SourceHash = hash
	req.Stage = "review"
	req.RenderID = uid()
	w = call(a, "POST", url+"/production-checkpoints", req)
	if w.Code != 409 {
		t.Fatal("review without real completed render accepted")
	}
	req.Stage = "media"
	req.RenderID = ""
	req.Status = "blocked"
	req.Summary = "第二条事实缺合适画面"
	req.Document = "可以使用文字卡表达，不更改事实。"
	w = call(a, "POST", url+"/production-checkpoints", req)
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	q, _ = a.get(p.ID)
	cards, _ = readBoard(t, a, p.ID)
	if cards["media"].State != "blocked" {
		t.Fatal(cards["media"])
	}
	w = call(a, "PUT", url+"/narration", map[string]any{"revision": q.Revision, "text": "修改后的原稿"})
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	cards, _ = readBoard(t, a, p.ID)
	if cards["plan"].State != "stale" || cards["media"].State != "stale" {
		t.Fatal(cards)
	}
}
func TestCheckpointMCPContracts(t *testing.T) {
	a := testApp(t)
	p, _ := a.create("mcp board")
	p.NarrationText = "Input"
	a.save(&p)
	a.projects[p.ID] = &p
	args := map[string]any{"projectId": p.ID, "revision": p.Revision, "sourceHash": narrationSourceHash(p), "stage": "plan", "status": "working", "summary": "Plan", "document": "Keep the original text."}
	raw, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": map[string]any{"name": "video_production_checkpoint", "arguments": args}})
	var out bytes.Buffer
	runMCP(bytes.NewReader(raw), &out, "http://localhost", a.token, &http.Client{Transport: localMCPTransport{handler: a.routes()}})
	if strings.Contains(out.String(), `"isError":true`) {
		t.Fatal(out.String())
	}
	q, _ := a.get(p.ID)
	if len(q.Checkpoints) != 1 {
		t.Fatal("checkpoint missing")
	}
	raw = []byte(`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`)
	out.Reset()
	runMCP(bytes.NewReader(raw), &out, "http://localhost", a.token, &http.Client{Transport: localMCPTransport{handler: a.routes()}})
	var listing struct {
		Result struct{ Tools []struct{ Name string } }
	}
	json.Unmarshal(out.Bytes(), &listing)
	if len(listing.Result.Tools) != len(videoTools) {
		t.Fatal(len(listing.Result.Tools), len(videoTools))
	}
	seen := map[string]bool{}
	for _, tool := range listing.Result.Tools {
		if seen[tool.Name] {
			t.Fatal("duplicate tool")
		}
		seen[tool.Name] = true
	}
	for _, id := range []string{"video_production_board_get", "video_production_checkpoint", "video_render_inspect"} {
		if !seen[id] {
			t.Fatal(id)
		}
	}
}
func TestRenderedFrameMCPReturnsImageEvidence(t *testing.T) {
	a := testApp(t)
	p, _ := a.create("frame tool")
	id := uid()
	data, e := os.ReadFile("examples/edit-preview.mp4")
	if e != nil {
		t.Fatal(e)
	}
	dir := filepath.Join(a.dir(p.ID), "renders", id)
	if e = os.MkdirAll(dir, 0700); e != nil {
		t.Fatal(e)
	}
	os.WriteFile(filepath.Join(dir, "final.mp4"), data, 0600)
	p.Renders = []Render{{ID: id, State: "completed", Duration: 2}}
	a.save(&p)
	a.projects[p.ID] = &p
	raw, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": map[string]any{"name": "video_render_inspect", "arguments": map[string]any{"projectId": p.ID, "renderId": id, "time": 0.25}}})
	var out bytes.Buffer
	runMCP(bytes.NewReader(raw), &out, "http://localhost", a.token, &http.Client{Transport: localMCPTransport{handler: a.routes()}})
	var response struct {
		Result struct {
			IsError bool
			Content []struct {
				Type     string
				MimeType string
				Data     string
			}
		}
	}
	json.Unmarshal(out.Bytes(), &response)
	if response.Result.IsError {
		t.Fatal(out.String())
	}
	found := false
	for _, item := range response.Result.Content {
		if item.Type == "image" && item.MimeType == "image/png" && len(item.Data) > 20 {
			found = true
		}
	}
	if !found {
		t.Fatal("no rendered image block", out.String())
	}
}
func TestReviewStaysBoundToReviewedRender(t *testing.T) {
	a := testApp(t)
	p, _ := a.create("review provenance")
	d := simpleEdit()
	p.NarrationText = "original"
	p.EditDSL = &d
	p.EditActive = true
	first := Render{ID: uid(), State: "completed", Engine: "ffmpeg_edit", ContentHash: productionHash(p), SourceHash: narrationSourceHash(p)}
	p.Renders = []Render{first}
	a.save(&p)
	a.projects[p.ID] = &p
	w := call(a, "POST", "/api/projects/"+p.ID+"/production-checkpoints", CheckpointRequest{Revision: p.Revision, SourceHash: narrationSourceHash(p), Stage: "review", Status: "completed", Summary: "Checked sampleframes", Document: "Sampled only, no listening.", RenderID: first.ID})
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	q, _ := a.get(p.ID)
	cards, _ := readBoard(t, a, p.ID)
	if cards["review"].State != "ready" {
		t.Fatal(cards["review"])
	}
	next := first
	next.ID = uid()
	q.Renders = append(q.Renders, next)
	a.save(&q)
	a.projects[q.ID] = &q
	cards, _ = readBoard(t, a, p.ID)
	if cards["review"].State != "stale" || len(cards["review"].Items) != 2 {
		t.Fatal(cards["review"])
	}
}
func TestRenderedFrameMCPRejectsNullTime(t *testing.T) {
	a := testApp(t)
	p, _ := a.create("null time")
	raw, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": map[string]any{"name": "video_render_inspect", "arguments": map[string]any{"projectId": p.ID, "renderId": uid(), "time": nil}}})
	var out bytes.Buffer
	runMCP(bytes.NewReader(raw), &out, "http://localhost", a.token, &http.Client{Transport: localMCPTransport{handler: a.routes()}})
	if !strings.Contains(out.String(), `"isError":true`) || !strings.Contains(out.String(), "numeric time") {
		t.Fatal(out.String())
	}
}
