package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// Deterministic tool-level workflow: no live Codex or actual TTS model is claimed.
func TestTextDrivenProductionArtifactsEndToEnd(t *testing.T) {
	a := testApp(t)
	a.font = "/usr/share/fonts/truetype/dejavu/DejaVuSans.ttf"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "audio/wav")
		w.Write(indexTestWAV())
	}))
	defer server.Close()
	a.tts = indexConfig(server.URL)
	p, _ := a.create("Text drives the cut")
	url := "/api/projects/" + p.ID
	w := call(a, "PUT", url+"/narration", map[string]any{"revision": p.Revision, "text": "Original fact.\nSupporting detail."})
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	json.Unmarshal(w.Body.Bytes(), &p)
	checkpoint := func(stage, doc, rid string) {
		q, _ := a.get(p.ID)
		w := call(a, "POST", url+"/production-checkpoints", CheckpointRequest{Revision: q.Revision, SourceHash: narrationSourceHash(q), Stage: stage, Status: "completed", Summary: stage + " recorded", Document: doc, RenderID: rid})
		if w.Code != 200 {
			t.Fatal(stage, w.Body.String())
		}
	}
	checkpoint("plan", "Keep both sentences intact. Use a readable text card when no image supports the words.", "")
	q, _ := a.get(p.ID)
	w = call(a, "POST", url+"/jobs", JobStartRequest{Type: "tts", Revision: q.Revision, RequestID: uid(), Options: JobOptions{Source: "narration"}})
	if w.Code != 202 {
		t.Fatal(w.Body.String())
	}
	var job ProductionJob
	json.Unmarshal(w.Body.Bytes(), &job)
	speech := waitSpeech(t, a, p.ID, job.NativeID)
	if speech.State != "completed" {
		t.Fatal(speech.Error)
	}
	q, _ = a.get(p.ID)
	w = call(a, "POST", url+"/speech/"+speech.ID+"/materialize", map[string]any{"revision": q.Revision})
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	var audio struct {
		Revision int
		Asset    Asset
		Duration float64
	}
	json.Unmarshal(w.Body.Bytes(), &audio)
	checkpoint("media", "Original fact / Supporting detail → explicit text card. No claimed event footage or fabricated image.", "")
	d := simpleEdit()
	d.Canvas.Width = 640
	d.Canvas.Height = 360
	d.Tracks = append(d.Tracks, EditTrack{ID: "voice", Kind: "audio"})
	d.Clips[0].Text = "Original fact.\nSupporting detail."
	d.Clips[0].Duration = audio.Duration
	d.Clips[0].FontSize = 30
	d.Clips = append(d.Clips, EditClip{ID: "narration", Track: "voice", Asset: audio.Asset.ID, Start: editTime(0), Duration: audio.Duration})
	q, _ = a.get(p.ID)
	w = call(a, "PUT", url+"/edit", EditRequest{Revision: q.Revision, DSL: &d})
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	q, _ = a.get(p.ID)
	w = call(a, "POST", url+"/jobs", JobStartRequest{Type: "render", Revision: q.Revision, RequestID: uid(), Options: JobOptions{Kind: "draft"}})
	if w.Code != 202 {
		t.Fatal(w.Body.String())
	}
	json.Unmarshal(w.Body.Bytes(), &job)
	render := waitEditRender(t, a, p.ID, job.NativeID)
	if render.State != "completed" {
		t.Fatal(render.Error)
	}
	w = call(a, "POST", url+"/renders/"+render.ID+"/inspect", map[string]any{"time": 0.2})
	if w.Code != 200 || w.Header().Get("Content-Type") != "image/png" {
		t.Fatal("missing actual output evidence", w.Code)
	}
	checkpoint("review", "Inspected frame at 0.2s; captions visible. Audio stream exists, but no human listening is claimed.", render.ID)
	cards, _ := readBoard(t, a, p.ID)
	for _, stage := range []string{"script", "plan", "speech", "media", "edit", "render", "review"} {
		if cards[stage].State != "ready" {
			t.Fatal(stage, cards[stage])
		}
	}
	if cards["final"].State != "empty" {
		t.Fatal("draft autoaccepted")
	}
	q, _ = a.get(p.ID)
	w = call(a, "POST", url+"/edit/patch/apply", EditPatchRequest{Revision: q.Revision, Changes: []EditChange{{ID: "title", Op: "update", Fields: json.RawMessage(`{"color":"#ffeeaa"}`)}}})
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	cards, _ = readBoard(t, a, p.ID)
	if cards["speech"].State != "ready" || cards["review"].State != "stale" || cards["render"].State != "stale" {
		t.Fatal("dependencies not reflected", cards)
	}
}
