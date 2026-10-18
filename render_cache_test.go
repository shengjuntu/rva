package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestCacheReuseAndTamperDetection(t *testing.T) {
	a := testApp(t)
	a.font = "/usr/share/fonts/truetype/dejavu/DejaVuSans.ttf"
	p, _ := a.create("cache")
	p = nativeWorkflowFixture(t, a, p)
	d := simpleEdit()
	d.Clips[0].Text = "Cache"
	p.EditDSL = &d
	p.EditActive = true
	a.save(&p)
	a.projects[p.ID] = &p
	url := "/api/projects/" + p.ID
	start := func() Render {
		q, _ := a.get(p.ID)
		w := call(a, "POST", url+"/jobs", JobStartRequest{Type: "render", Revision: q.Revision, RequestID: uid(), Options: JobOptions{Kind: "draft"}})
		if w.Code != 202 {
			t.Fatal(w.Body.String())
		}
		var job ProductionJob
		json.Unmarshal(w.Body.Bytes(), &job)
		j := waitEditRender(t, a, p.ID, job.NativeID)
		if j.State != "completed" {
			t.Fatal(j.Error)
		}
		return j
	}
	first := start()
	second := start()
	if first.CacheKey == "" || second.ReusedFrom != first.ID || second.CacheKey != first.CacheKey {
		t.Fatal(first, second)
	}
	q, _ := a.get(p.ID)
	w := call(a, "POST", url+"/final", map[string]any{"revision": q.Revision, "renderId": second.ID, "reviewed": true})
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	w = call(a, "GET", url+"/final/verify", nil)
	var check struct {
		Verified      bool
		OutputIntact  bool
		InputsCurrent bool
	}
	json.Unmarshal(w.Body.Bytes(), &check)
	if !check.Verified {
		t.Fatal(w.Body.String())
	}
	os.WriteFile(filepath.Join(a.dir(p.ID), "renders", first.ID, "final.mp4"), []byte("damaged"), 0600)
	os.WriteFile(filepath.Join(a.dir(p.ID), "renders", second.ID, "final.mp4"), []byte("damaged"), 0600)
	w = call(a, "GET", url+"/final/verify", nil)
	json.Unmarshal(w.Body.Bytes(), &check)
	if check.Verified || check.OutputIntact {
		t.Fatal("corruption missed")
	}
	third := start()
	if third.ReusedFrom != "" {
		t.Fatal("corrupt cache reused")
	}
	q, _ = a.get(p.ID)
	q.EditDSL.Clips[0].Text = "Changed"
	q.Revision++
	a.save(&q)
	a.projects[q.ID] = &q
	fourth := start()
	if fourth.ReusedFrom != "" || fourth.CacheKey == third.CacheKey {
		t.Fatal("changed DSL reused")
	}
}
func TestCacheKeyTracksBytesAndRange(t *testing.T) {
	a := testApp(t)
	dir := t.TempDir()
	asset := Asset{ID: "asset", File: "same.png", MIME: "image/png"}
	os.WriteFile(filepath.Join(dir, asset.File), []byte("original bytes"), 0600)
	d := EditDocument{Version: 1, Canvas: EditCanvas{Width: 320, Height: 180, FPS: 30}, Tracks: []EditTrack{{ID: "visual", Kind: "video"}}, Clips: []EditClip{{ID: "picture", Track: "visual", Asset: "asset", Start: editTime(0), Duration: 2}}}
	ir, e := compileEdit(d, []Asset{asset})
	if e != nil {
		t.Fatal(e)
	}
	key, e := renderInputKey(context.Background(), dir, ir, []Asset{asset}, a.font, nil)
	if e != nil {
		t.Fatal(e)
	}
	os.WriteFile(filepath.Join(dir, asset.File), []byte("replacement bytes"), 0600)
	changed, e := renderInputKey(context.Background(), dir, ir, []Asset{asset}, a.font, nil)
	if e != nil || changed == key {
		t.Fatal("same name replacement missed")
	}
	rangeKey, e := renderInputKey(context.Background(), dir, ir, []Asset{asset}, a.font, &RenderRange{Start: 0, End: 1})
	if e != nil || rangeKey == changed {
		t.Fatal("range ignored")
	}
}
