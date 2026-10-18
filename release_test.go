package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestRangeRenderAndFinalSnapshot(t *testing.T) {
	a := testApp(t)
	a.font = "/usr/share/fonts/truetype/dejavu/DejaVuSans.ttf"
	p, _ := a.create("release")
	p = nativeWorkflowFixture(t, a, p)
	d := simpleEdit()
	d.Clips[0].Text = "Preview"
	d.Clips[0].Duration = 3
	p.EditDSL = &d
	p.EditActive = true
	p.NarrationText = "source text"
	a.save(&p)
	a.projects[p.ID] = &p
	url := "/api/projects/" + p.ID
	start := func(span *RenderRange) Render {
		w := call(a, "POST", url+"/jobs", JobStartRequest{Type: "render", Revision: p.Revision, RequestID: uid(), Options: JobOptions{Kind: "draft", Range: span}})
		if w.Code != 202 {
			t.Fatal(w.Code, w.Body.String())
		}
		var j ProductionJob
		json.Unmarshal(w.Body.Bytes(), &j)
		job := waitEditRender(t, a, p.ID, j.NativeID)
		if job.State != "completed" {
			log, _ := os.ReadFile(filepath.Join(a.dir(p.ID), "renders", job.ID, "render.log"))
			t.Fatal(job.Error, string(log))
		}
		return job
	}
	partial := start(&RenderRange{Start: 1, End: 2})
	if partial.Duration != 1 || partial.Range == nil {
		t.Fatal(partial)
	}
	w := call(a, "POST", url+"/final", map[string]any{"revision": p.Revision, "renderId": partial.ID, "reviewed": true})
	if w.Code != 409 {
		t.Fatal("preview finalized", w.Code)
	}
	full := start(nil)
	w = call(a, "POST", url+"/final", map[string]any{"revision": p.Revision, "renderId": full.ID, "reviewed": false})
	if w.Code != 422 {
		t.Fatal("unreviewed accepted")
	}
	w = call(a, "POST", url+"/final", map[string]any{"revision": p.Revision, "renderId": full.ID, "reviewed": true})
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	q, _ := a.get(p.ID)
	if q.Final == nil || q.Final.VideoHash == "" || q.Final.SnapshotHash == "" {
		t.Fatal(q.Final)
	}
	if _, e := os.Stat(filepath.Join(a.dir(p.ID), "finals", q.Final.ID+".json")); e != nil {
		t.Fatal(e)
	}
	w = call(a, "GET", url+"/final", nil)
	var result struct{ Current bool }
	json.Unmarshal(w.Body.Bytes(), &result)
	if !result.Current {
		t.Fatal(w.Body.String())
	}
	w = call(a, "PUT", url+"/narration", map[string]any{"revision": q.Revision, "text": "changed text"})
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	w = call(a, "GET", url+"/final", nil)
	json.Unmarshal(w.Body.Bytes(), &result)
	if result.Current {
		t.Fatal("stale final considered current")
	}
}
