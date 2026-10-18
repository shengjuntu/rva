package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func hyperFramesFixture(t *testing.T) (*App, Project, Asset, HyperFramesDocument) {
	t.Helper()
	a := testApp(t)
	p, err := a.create("自主动画新闻")
	if err != nil {
		t.Fatal(err)
	}
	asset := Asset{ID: uid(), Name: "新闻图片", File: "photo.png", MIME: "image/png", Size: 7}
	if err := os.WriteFile(filepath.Join(a.dir(p.ID), "workspace", "assets", asset.File), []byte("picture"), 0600); err != nil {
		t.Fatal(err)
	}
	p.NarrationText = "发布了一款新芯片。计划明年量产。"
	p.Assets = append(p.Assets, asset)
	if err := a.save(&p); err != nil {
		t.Fatal(err)
	}
	a.projects[p.ID] = &p
	d := HyperFramesDocument{SchemaVersion: 1, EntryPoint: "index.html", TextSourceHash: narrationSourceHash(p), Canvas: HyperFramesCanvas{Width: 1080, Height: 1920, FPS: 30, Duration: 5}, Files: []HyperFramesSourceFile{{Path: "index.html", Content: "<!doctype html><script src='animation.js'></script><img src='media/news.png'>"}, {Path: "animation.js", Content: "window.__timelines = {main: gsap.timeline({paused:true})};"}}, Assets: []HyperFramesAssetBinding{{AssetID: asset.ID, Path: "media/news.png"}}, Timeline: []HyperFramesReviewClip{{ID: "opening", Track: "main", Kind: "image", Start: 0, Duration: 5, AssetID: asset.ID, TextRefs: []TextReference{{Quote: "发布了一款新芯片。"}}}}}
	return a, p, asset, d
}
func hyperFramesDirect(a *App, method string, p Project, raw []byte) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, "/api/projects/"+p.ID+"/hyperframes", bytes.NewReader(raw))
	r.SetPathValue("id", p.ID)
	w := httptest.NewRecorder()
	if method == http.MethodGet {
		a.hyperFramesGet(w, r)
	} else {
		a.hyperFramesUpdate(w, r)
	}
	return w
}
func hyperFramesSave(a *App, p Project, d HyperFramesDocument) *httptest.ResponseRecorder {
	raw, _ := json.Marshal(HyperFramesRequest{Revision: p.Revision, Document: &d})
	return hyperFramesDirect(a, http.MethodPut, p, raw)
}
func TestHyperFramesSourceSnapshotPersistsAndConflicts(t *testing.T) {
	a, p, asset, d := hyperFramesFixture(t)
	w := hyperFramesSave(a, p, d)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var v HyperFramesView
	if err := json.Unmarshal(w.Body.Bytes(), &v); err != nil {
		t.Fatal(err)
	}
	if !v.Current || v.State != "ready" || v.Revision != p.Revision+1 || len(v.Assets) != 1 || v.Assets[0].SHA256 != hashBytes([]byte("picture")) || v.Document.Assets[0].SHA256 != v.Assets[0].SHA256 || v.Document.Assets[0].Size != 7 {
		t.Fatal(v)
	}
	if strings.Contains(w.Body.String(), a.dir(p.ID)) {
		t.Fatal("private asset path leaked")
	}
	// Saving source is purely persistence: it does not materialize or execute it.
	if _, err := os.Stat(filepath.Join(a.dir(p.ID), "workspace", "index.html")); !os.IsNotExist(err) {
		t.Fatal("source was materialized during save", err)
	}
	if stale := hyperFramesSave(a, p, d); stale.Code != 409 {
		t.Fatal(stale.Code, stale.Body.String())
	}
	q, _ := a.get(p.ID)
	if q.HyperFrames == nil || q.HyperFrames.Files[1].Content != d.Files[1].Content || q.Assets[0].ID != asset.ID {
		t.Fatal(q)
	}
	reloaded, err := newApp(a.data)
	if err != nil {
		t.Fatal(err)
	}
	restored, _ := reloaded.get(p.ID)
	if restored.HyperFrames == nil || hyperFramesDocumentHash(*restored.HyperFrames) != v.DocumentHash {
		t.Fatal(restored)
	}
	got := hyperFramesDirect(reloaded, http.MethodGet, restored, nil)
	if !strings.Contains(got.Body.String(), `"current":true`) {
		t.Fatal(got.Body.String())
	}
}
func TestHyperFramesNarrationAndAssetChangesRemainStale(t *testing.T) {
	a, p, asset, d := hyperFramesFixture(t)
	if w := hyperFramesSave(a, p, d); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	q, _ := a.get(p.ID)
	savedHash := hyperFramesDocumentHash(*q.HyperFrames)
	q.NarrationText += "补充消息。"
	q.Revision++
	if err := a.save(&q); err != nil {
		t.Fatal(err)
	}
	a.projects[q.ID] = &q
	v := a.hyperFramesView(q)
	if v.Current || v.State != "stale" || len(v.StaleReasons) == 0 || v.DocumentHash != savedHash {
		t.Fatal(v)
	}
	if w := hyperFramesSave(a, q, d); w.Code != 409 {
		t.Fatal(w.Code, w.Body.String())
	}
	// Even a structurally valid document with no review clips remains source-bound.
	d.Timeline = nil
	if err := validateHyperFramesProject(q, d); err == nil {
		t.Fatal("old narration hash accepted without references")
	}
	q.NarrationText = p.NarrationText
	if err := os.WriteFile(filepath.Join(a.dir(p.ID), "workspace", "assets", asset.File), []byte("changed"), 0600); err != nil {
		t.Fatal(err)
	}
	if v := a.hyperFramesView(q); v.Current || len(v.StaleReasons) == 0 {
		t.Fatal(v)
	}
	if w := hyperFramesSave(a, q, *q.HyperFrames); w.Code != 422 {
		t.Fatal(w.Code, w.Body.String())
	}
}
func TestHyperFramesRejectsSourceAndReferenceBoundaries(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*HyperFramesDocument)
	}{
		{"escape", func(d *HyperFramesDocument) { d.Files[1].Path = "../animation.js" }},
		{"absolute", func(d *HyperFramesDocument) { d.Files[1].Path = "/animation.js" }},
		{"backslash", func(d *HyperFramesDocument) { d.Files[1].Path = `other\animation.js` }},
		{"scheme", func(d *HyperFramesDocument) { d.Assets[0].Path = "https://example/x" }},
		{"duplicate", func(d *HyperFramesDocument) { d.Files[1].Path = "INDEX.HTML" }},
		{"parent-file", func(d *HyperFramesDocument) { d.Files[1].Path = "media" }},
		{"asset-collision", func(d *HyperFramesDocument) { d.Assets[0].Path = "index.html" }},
		{"runtime", func(d *HyperFramesDocument) { d.Files[1].Path = "runtime/evil.js" }},
		{"output", func(d *HyperFramesDocument) { d.Files[1].Path = "snapshot.json/child.js" }},
		{"entry-not-checkable", func(d *HyperFramesDocument) { d.EntryPoint = "custom.html"; d.Files[0].Path = "custom.html" }},
		{"missing-asset", func(d *HyperFramesDocument) { d.Assets[0].AssetID = uid() }},
		{"invented-timeline-asset", func(d *HyperFramesDocument) { d.Timeline[0].AssetID = uid() }},
		{"unmapped-timeline-asset", func(d *HyperFramesDocument) { d.Assets = nil }},
		{"invented-text", func(d *HyperFramesDocument) { d.Timeline[0].TextRefs[0].Quote = "不存在的消息" }},
		{"invalid-utf8", func(d *HyperFramesDocument) { d.Files[1].Content = string([]byte{0xff}) }},
		{"null-byte", func(d *HyperFramesDocument) { d.Files[1].Content = "code\x00" }},
		{"large-file", func(d *HyperFramesDocument) { d.Files[1].Content = strings.Repeat("x", (512<<10)+1) }},
		{"large-total", func(d *HyperFramesDocument) {
			d.Files[0].Content = strings.Repeat("x", 512<<10)
			d.Files[1].Content = strings.Repeat("x", 512<<10)
			d.Files = append(d.Files, HyperFramesSourceFile{Path: "extra.js", Content: "x"})
		}},
		{"bad-time", func(d *HyperFramesDocument) { d.Timeline[0].Duration = 6 }},
		{"duplicate-clip", func(d *HyperFramesDocument) { d.Timeline = append(d.Timeline, d.Timeline[0]) }},
		{"too-many-files", func(d *HyperFramesDocument) {
			for len(d.Files) < 129 {
				d.Files = append(d.Files, HyperFramesSourceFile{})
			}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a, p, _, d := hyperFramesFixture(t)
			tc.mutate(&d)
			if err := validateHyperFramesProject(p, d); err == nil {
				t.Fatal("invalid document accepted")
			}
			q, _ := a.get(p.ID)
			if q.Revision != p.Revision || q.HyperFrames != nil {
				t.Fatal("validation changed project")
			}
		})
	}
}
func TestHyperFramesRejectsStrictJSONAndOversize(t *testing.T) {
	a, p, _, d := hyperFramesFixture(t)
	raw, _ := json.Marshal(HyperFramesRequest{Revision: p.Revision, Document: &d})
	tests := [][]byte{
		[]byte(strings.Replace(string(raw), `"document":{`, `"document":{"invented":true,`, 1)),
		[]byte(strings.Replace(string(raw), `"canvas":{`, `"canvas":{"invented":true,`, 1)),
		[]byte(strings.Replace(string(raw), `"schemaVersion":1`, `"schemaVersion":1,"schemaVersion":1`, 1)),
		append(append([]byte{}, raw...), []byte(` {}`)...),
		[]byte(strings.Repeat("[", 1000) + "0" + strings.Repeat("]", 1000)),
		[]byte(`{"revision":1,"document":null}`),
		[]byte(`{"revision":1,"document":{"files":[{"path":"index.html","content":"` + string([]byte{0xff}) + `"}]}}`),
	}
	for i, input := range tests {
		w := hyperFramesDirect(a, http.MethodPut, p, input)
		if w.Code < 400 {
			t.Fatal(i, w.Code, w.Body.String())
		}
	}
	w := hyperFramesDirect(a, http.MethodPut, p, []byte(strings.Repeat(" ", (2<<20)+1)))
	if w.Code != 413 {
		t.Fatal(w.Code, w.Body.String())
	}
}
func TestHyperFramesRejectsSymlinkAndAssetStorageEscape(t *testing.T) {
	for _, mode := range []string{"symlink", "storage-path", "directory"} {
		t.Run(mode, func(t *testing.T) {
			a, p, asset, d := hyperFramesFixture(t)
			file := filepath.Join(a.dir(p.ID), "workspace", "assets", asset.File)
			switch mode {
			case "symlink":
				outside := filepath.Join(a.data, "outside.png")
				if err := os.WriteFile(outside, []byte("private"), 0600); err != nil {
					t.Fatal(err)
				}
				if err := os.Remove(file); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(outside, file); err != nil {
					t.Skip(err)
				}
			case "storage-path":
				p.Assets[0].File = "../secret.png"
				a.projects[p.ID] = &p
			case "directory":
				if err := os.Remove(file); err != nil {
					t.Fatal(err)
				}
				if err := os.Mkdir(file, 0700); err != nil {
					t.Fatal(err)
				}
			}
			w := hyperFramesSave(a, p, d)
			if w.Code != 422 {
				t.Fatal(w.Code, w.Body.String())
			}
			q, _ := a.get(p.ID)
			if q.Revision != p.Revision || q.HyperFrames != nil {
				t.Fatal(q)
			}
		})
	}
}
