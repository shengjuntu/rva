package main

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func locateCall(a *App, pid string, body any) *httptest.ResponseRecorder {
	b, _ := json.Marshal(body)
	r := httptest.NewRequest("POST", "/api/projects/"+pid+"/edit/locate", strings.NewReader(string(b)))
	r.SetPathValue("id", pid)
	w := httptest.NewRecorder()
	a.editLocate(w, r)
	return w
}
func textRefProject(t *testing.T) (*App, Project) {
	t.Helper()
	a := testApp(t)
	p, err := a.create("text refs")
	if err != nil {
		t.Fatal(err)
	}
	p = nativeWorkflowFixture(t, a, p)
	p.NarrationText = "甲乙甲乙🙂发布。\n计划量产。"
	d := simpleEdit()
	d.TextSourceHash = narrationSourceHash(p)
	d.Clips[0].TextRefs = []TextReference{{Quote: "甲乙", Occurrence: 1}}
	d.Clips = append(d.Clips, EditClip{ID: "second", Track: "titles", After: "title", Duration: 2, Text: "自由画面", TextRefs: []TextReference{{Quote: "甲乙", Occurrence: 2}, {Quote: "🙂发布。"}}}, EditClip{ID: "background", Track: "titles", After: "second", Duration: 1, Text: "无引用背景"})
	p.EditDSL = &d
	p.EditActive = true
	if err = a.save(&p); err != nil {
		t.Fatal(err)
	}
	a.projects[p.ID] = &p
	return a, p
}
func TestTextReferencesExactRangesOccurrencesAndUnicode(t *testing.T) {
	for _, tc := range []struct {
		text              string
		ref               TextReference
		start, end, count int
		bad               bool
	}{
		{"甲乙甲乙🙂发布", TextReference{"甲乙", 2}, 2, 4, 2, false},
		{"甲乙甲乙🙂发布", TextReference{"🙂发布", 0}, 4, 7, 1, false},
		{"哈哈哈", TextReference{"哈哈", 2}, 1, 3, 2, false},
		{"哈哈哈", TextReference{"哈哈", 0}, -1, -1, 2, true},
		{"甲", TextReference{"甲", 2}, -1, -1, 1, true},
		{"甲", TextReference{"乙", 0}, -1, -1, 0, true},
		{"甲", TextReference{"甲\x00", 0}, -1, -1, 0, true},
		{"甲", TextReference{"甲\x01", 0}, -1, -1, 0, true},
		{"甲\n乙", TextReference{"甲\n乙", 0}, 0, 3, 1, false},
	} {
		start, end, count, err := resolveTextReference(tc.text, tc.ref)
		if (err != nil) != tc.bad || start != tc.start || end != tc.end || count != tc.count {
			t.Fatalf("%+v => %d %d %d %v", tc, start, end, count, err)
		}
	}
	quote := strings.Repeat("长", 1500)
	start, end, _, err := resolveTextReference("🙂"+quote, TextReference{Quote: quote})
	if err != nil || start != 1 || end != 1501 {
		t.Fatal(start, end, err)
	}
}
func TestNativeTextReferencesSourcePinValidationAndLegacyCompatibility(t *testing.T) {
	_, p := textRefProject(t)
	if _, err := compileProjectEdit(p, *p.EditDSL); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name   string
		mutate func(*Project, *EditDocument)
	}{
		{"missing-hash", func(p *Project, d *EditDocument) { d.TextSourceHash = "" }},
		{"stale-source", func(p *Project, d *EditDocument) { p.NarrationText += "改" }},
		{"wrong-quote", func(p *Project, d *EditDocument) { d.Clips[0].TextRefs[0].Quote = "不存在" }},
		{"ambiguous", func(p *Project, d *EditDocument) { d.Clips[0].TextRefs[0].Occurrence = 0 }},
		{"too-many", func(p *Project, d *EditDocument) {
			for len(d.Clips[0].TextRefs) < 33 {
				d.Clips[0].TextRefs = append(d.Clips[0].TextRefs, TextReference{Quote: "发布"})
			}
		}},
		{"too-large", func(p *Project, d *EditDocument) { d.Clips[0].TextRefs[0].Quote = strings.Repeat("a", 16385) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			q := clone(&p)
			d := *q.EditDSL
			tc.mutate(&q, &d)
			if _, err := compileProjectEdit(q, d); err == nil {
				t.Fatal("accepted invalid text references")
			}
		})
	}
	legacy := simpleEdit()
	if _, err := compileProjectEdit(Project{}, legacy); err != nil {
		t.Fatal("legacy required narration", err)
	}
}
func TestTextReferenceJSONRejectsIgnoredFieldsAndNulls(t *testing.T) {
	for _, raw := range []string{`{"quote":"甲","extra":true}`, `{"quote":"甲","occurrence":null}`, `{"quote":"甲","occurrence":0}`, `{"quote":"甲","occurrence":true}`, `null`, `{"quote":"甲","occurrence":-1}`, `{"quote":"甲\u0001"}`} {
		var ref TextReference
		if err := json.Unmarshal([]byte(raw), &ref); err == nil {
			t.Fatal("accepted", raw)
		}
	}
	for _, raw := range []string{`{"id":"a","track":"b","textRefs":null}`, `{"id":"a","track":"b","textRefs":[{"quote":"甲","extra":true}]}`, `{"id":"a","track":"b","other":true}`} {
		var clip EditClip
		if err := json.Unmarshal([]byte(raw), &clip); err == nil {
			t.Fatal("accepted", raw)
		}
		var external exchangeEDLClip
		if err := json.Unmarshal([]byte(raw), &external); err == nil {
			t.Fatal("accepted external", raw)
		}
	}
	for _, raw := range []string{`{"quote":"甲","occurrence":null}`, `{"quote":"甲","occurrence":0}`, `{"quote":"甲","unexpected":true}`} {
		var request EditLocateRequest
		if err := json.Unmarshal([]byte(raw), &request); err == nil {
			t.Fatal("accepted query", raw)
		}
	}
}
func TestEditLocateCurrentNativeUsesOverlapAndResolvedTimes(t *testing.T) {
	a, p := textRefProject(t)
	w := locateCall(a, p.ID, map[string]any{"quote": "乙甲"})
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var got EditLocateResult
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if !got.Current || got.Backend != "native" || got.Query.StartRune != 1 || got.Query.EndRune != 3 || len(got.Matches) != 2 || got.Matches[0].ClipID != "title" || got.Matches[1].ClipID != "second" || got.Matches[1].Start != 1 || got.Matches[1].End != 3 || !reflect.DeepEqual(got.UnmappedClipIDs, []string{"background"}) {
		t.Fatalf("%+v", got)
	}
	w = locateCall(a, p.ID, map[string]any{"quote": "甲乙"})
	if w.Code != 422 || !strings.Contains(w.Body.String(), `"occurrenceCount":2`) {
		t.Fatal(w.Code, w.Body.String())
	}
	w = locateCall(a, p.ID, map[string]any{"quote": "甲乙", "occurrence": 2})
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	json.Unmarshal(w.Body.Bytes(), &got)
	if len(got.Matches) != 1 || got.Matches[0].ClipID != "second" {
		t.Fatal(w.Body.String())
	}
	w = locateCall(a, p.ID, map[string]any{"quote": "计划量产"})
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	json.Unmarshal(w.Body.Bytes(), &got)
	if len(got.Matches) != 0 || len(got.UnmappedClipIDs) != 1 {
		t.Fatal(w.Body.String())
	}
	w = locateCall(a, p.ID, map[string]any{"quote": "甲乙", "renderId": "../other"})
	if w.Code != 404 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w = locateCall(a, "unknown", map[string]any{"quote": "甲乙"}); w.Code != 404 {
		t.Fatal(w.Code, w.Body.String())
	}
}
func TestNativeRefSavePatchRenderAndUndoRejectChangedSource(t *testing.T) {
	a, p := textRefProject(t)
	p.NarrationText += "新稿"
	p.Revision++
	a.projects[p.ID] = &p
	d := *p.EditDSL
	for _, route := range []struct {
		method, path string
		body         any
	}{
		{"POST", "/edit/validate", EditRequest{Revision: p.Revision, DSL: &d}},
		{"PUT", "/edit", EditRequest{Revision: p.Revision, DSL: &d}},
		{"POST", "/render", map[string]any{"revision": p.Revision}},
		{"POST", "/edit/patch/preview", EditPatchRequest{Revision: p.Revision, Changes: []EditChange{{ID: "title", Op: "update", Fields: json.RawMessage(`{"text":"改样式"}`)}}}},
	} {
		w := call(a, route.method, "/api/projects/"+p.ID+route.path, route.body)
		if w.Code != 422 || !strings.Contains(w.Body.String(), "TEXT_SOURCE_STALE") {
			t.Fatal(route.path, w.Code, w.Body.String())
		}
	}
	w := locateCall(a, p.ID, map[string]any{"quote": "发布"})
	if w.Code != 409 {
		t.Fatal(w.Code, w.Body.String())
	}
	newHash := narrationSourceHash(p)
	w = call(a, "POST", "/api/projects/"+p.ID+"/edit/patch/apply", EditPatchRequest{Revision: p.Revision, TextSourceHash: &newHash, Changes: []EditChange{{ID: "second", Op: "update", Fields: json.RawMessage(`{"text":"只改这一项"}`)}}})
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	q, _ := a.get(p.ID)
	if q.EditDSL.TextSourceHash != newHash || !reflect.DeepEqual(q.EditDSL.Clips[0], p.EditDSL.Clips[0]) || !reflect.DeepEqual(q.EditDSL.Clips[2], p.EditDSL.Clips[2]) {
		t.Fatal("patch changed unrelated clips")
	}
	if !strings.Contains(w.Body.String(), "textSourceHash") {
		t.Fatal("document pin missing from patch diff")
	}
	w = call(a, "POST", "/api/projects/"+p.ID+"/edit/undo", map[string]any{"revision": q.Revision})
	if w.Code != 422 || !strings.Contains(w.Body.String(), "TEXT_SOURCE_STALE") {
		t.Fatal(w.Code, w.Body.String())
	}
}

func archiveLocateFixture(t *testing.T, a *App, p Project, backend string) (Project, Render) {
	t.Helper()
	id := uid()
	dir := filepath.Join(a.dir(p.ID), "renders", id)
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := atomicJSON(filepath.Join(dir, "snapshot.json"), p); err != nil {
		t.Fatal(err)
	}
	snapshotHash, _, _ := hashLocal(filepath.Join(dir, "snapshot.json"))
	r := Render{ID: id, State: "completed", Engine: backend, SourceHash: narrationSourceHash(p), ContentHash: productionHash(p), Duration: 4}
	proof := RenderProof{Schema: "video.render-proof.v1", Key: strings.Repeat("a", 64), VideoHash: strings.Repeat("b", 64), VideoBytes: 1, SnapshotHash: snapshotHash}
	if backend == "video_use" {
		var edl exchangeEDL
		edl.SchemaVersion = 1
		edl.Canvas = exchangeCanvas{Width: 320, Height: 180, FPS: 30}
		edl.Clips = []exchangeEDLClip{{ID: "old-mapped", AssetID: "asset-original", Path: "input/media/one.png", Type: "image", Start: editTime(0), Duration: 2, TextRefs: []TextReference{{Quote: "甲乙", Occurrence: 2}}}, {ID: "old-background", Path: "input/media/one.png", Type: "image", Start: editTime(2), Duration: 2}}
		atomicJSON(filepath.Join(dir, "edit-ir.json"), edl)
		editHash, _, _ := hashLocal(filepath.Join(dir, "edit-ir.json"))
		run := ProductionRun{ID: uid(), State: "completed", RenderID: id, SourceHash: r.SourceHash, InputHash: proof.Key, InputBasisHash: externalProductionBasis(p), OutputHash: proof.VideoHash, SnapshotHash: snapshotHash, EditHash: editHash}
		p.ProductionRuns = append(p.ProductionRuns, run)
		r.ContentHash = run.InputBasisHash
	}
	if err := atomicJSON(filepath.Join(dir, "render-proof.json"), proof); err != nil {
		t.Fatal(err)
	}
	p.Renders = append(p.Renders, r)
	a.projects[p.ID] = &p
	return p, r
}
func TestEditLocateHistoricalNativeAndExternalUseFrozenSource(t *testing.T) {
	for _, backend := range []string{"ffmpeg_edit", "video_use"} {
		t.Run(backend, func(t *testing.T) {
			a, p := textRefProject(t)
			if backend == "video_use" {
				wf := defaultWorkflow()
				wf.EditingBackend = "video_use"
				p.Workflow = &wf
			}
			p, render := archiveLocateFixture(t, a, p, backend)
			w := locateCall(a, p.ID, map[string]any{"quote": "甲乙", "occurrence": 2, "renderId": render.ID})
			if w.Code != 200 {
				t.Fatal(w.Code, w.Body.String())
			}
			var got EditLocateResult
			json.Unmarshal(w.Body.Bytes(), &got)
			if !got.Current || len(got.Matches) != 1 || got.SourceHash != render.SourceHash {
				t.Fatal(w.Body.String())
			}
			p.NarrationText = "完全不同的新稿。"
			p.Revision++
			a.projects[p.ID] = &p
			w = locateCall(a, p.ID, map[string]any{"quote": "甲乙", "occurrence": 2, "renderId": render.ID})
			if w.Code != 200 {
				t.Fatal(w.Code, w.Body.String())
			}
			json.Unmarshal(w.Body.Bytes(), &got)
			if got.Current || got.SourceHash != render.SourceHash || got.Warning == "" || len(got.Matches) != 1 {
				t.Fatal(w.Body.String())
			}
			w = locateCall(a, p.ID, map[string]any{"quote": "新稿", "renderId": render.ID})
			if w.Code != 422 {
				t.Fatal("historical matched current text", w.Code, w.Body.String())
			}
			other, _ := a.create("other")
			w = locateCall(a, other.ID, map[string]any{"quote": "甲乙", "renderId": render.ID})
			if w.Code != 404 {
				t.Fatal("cross-project render accepted", w.Code, w.Body.String())
			}
			if backend == "video_use" {
				w = locateCall(a, p.ID, map[string]any{"quote": "甲乙", "occurrence": 2})
				if w.Code != 409 {
					t.Fatal("stale default external returned", w.Code, w.Body.String())
				}
				os.WriteFile(filepath.Join(a.dir(p.ID), "renders", render.ID, "edit-ir.json"), []byte(`{"schemaVersion":1}`), 0600)
				w = locateCall(a, p.ID, map[string]any{"quote": "甲乙", "occurrence": 2, "renderId": render.ID})
				if w.Code != 409 {
					t.Fatal("tampered EDL accepted", w.Code, w.Body.String())
				}
			}
		})
	}
}

func TestExchangeEDLTextReferencesValidateFrozenTextAndAdapterVersion(t *testing.T) {
	a, p, asset, shared := exchangeFixture(t)
	run, p := prepareExchange(t, a, p)
	inputs, err := os.OpenRoot(filepath.Join(shared, run.RelativeRunRoot))
	if err != nil {
		t.Fatal(err)
	}
	defer inputs.Close()
	archiveDir := t.TempDir()
	archive, err := os.OpenRoot(archiveDir)
	if err != nil {
		t.Fatal(err)
	}
	defer archive.Close()
	var manifest ExchangeManifest
	if err := exchangeJSON(inputs, "input/manifest.json", 4<<20, &manifest); err != nil {
		t.Fatal(err)
	}
	completion := ExchangeCompletion{SchemaVersion: 1, Engine: "video_use", UpstreamCommit: exchangeVideoUseCommit, ProjectID: p.ID, ProductionRunID: run.ID, SourceHash: run.SourceHash, InputHash: run.InputHash}
	clip := map[string]any{"id": "picture", "assetId": asset.ID, "path": manifest.Media[0].Path, "type": "image", "start": 0, "duration": 1, "textRefs": []any{map[string]any{"quote": p.NarrationText}}}
	edl := map[string]any{"schemaVersion": 1, "canvas": map[string]any{"width": 320, "height": 180, "fps": 30}, "clips": []any{clip}}
	validate := func(version int, verified bool) error {
		atomicJSON(filepath.Join(archiveDir, "edit-ir.json"), edl)
		editHash, _, _ := hashLocal(filepath.Join(archiveDir, "edit-ir.json"))
		report := map[string]any{"schemaVersion": 1, "engine": "video_use", "upstreamCommit": exchangeVideoUseCommit, "projectId": p.ID, "productionRunId": run.ID, "sourceHash": run.SourceHash, "inputHash": run.InputHash, "edlHash": editHash, "narrationSHA256": manifest.Narration.SHA256, "passed": true, "adapterVersion": version, "textReferences": map[string]any{"sourceVerified": verified}}
		checks := []any{}
		for _, name := range []string{"dimensions", "videoCodec", "audioCodec", "duration", "fileNonempty", "frameRate"} {
			checks = append(checks, map[string]any{"name": name, "ok": true})
		}
		report["checks"] = checks
		atomicJSON(filepath.Join(archiveDir, "validation.json"), report)
		return validateProductionExchange(context.Background(), archive, inputs, run, completion, editHash)
	}
	if err := validate(2, true); err != nil {
		t.Fatal(err)
	}
	if err := validate(1, true); err == nil {
		t.Fatal("old adapter accepted references")
	}
	if err := validate(2, false); err == nil {
		t.Fatal("unverified source accepted")
	}
	clip["textRefs"] = []any{map[string]any{"quote": "不是原文"}}
	if err := validate(2, true); err == nil {
		t.Fatal("invented quote accepted")
	}
	clip["textRefs"] = nil
	if err := validate(2, true); err == nil {
		t.Fatal("null references accepted")
	}
	delete(clip, "textRefs")
	if err := validate(1, false); err != nil {
		t.Fatal("legacy no refs refused", err)
	}
	os.WriteFile(filepath.Join(shared, run.RelativeRunRoot, manifest.TextPath), []byte("changed"), 0600)
	if err := validate(2, true); err == nil {
		t.Fatal("changed frozen source accepted")
	}
}

func TestEditGetStaleReferencesRemainReadableAndRefOnlyPatchIsAffected(t *testing.T) {
	a, p := textRefProject(t)
	w := call(a, "POST", "/api/projects/"+p.ID+"/edit/patch/preview", EditPatchRequest{Revision: p.Revision, Changes: []EditChange{{ID: "second", Op: "update", Fields: json.RawMessage(`{"textRefs":[{"quote":"计划量产。"}]}`)}}})
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var preview struct {
		Affected []string     `json:"affectedClipIds"`
		After    EditDocument `json:"after"`
	}
	json.Unmarshal(w.Body.Bytes(), &preview)
	if !reflect.DeepEqual(preview.Affected, []string{"second"}) || !reflect.DeepEqual(preview.After.Clips[0], p.EditDSL.Clips[0]) || !reflect.DeepEqual(preview.After.Clips[2], p.EditDSL.Clips[2]) {
		t.Fatal(w.Body.String())
	}
	p.NarrationText = "新稿"
	p.Revision++
	a.projects[p.ID] = &p
	w = call(a, "GET", "/api/projects/"+p.ID+"/edit", nil)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"staleTextRefs":true`) || !strings.Contains(w.Body.String(), `"dsl":`) {
		t.Fatal(w.Code, w.Body.String())
	}
}

func TestTextRefsRealAdapterArchiveAndLocator(t *testing.T) {
	adapter, vendor := os.Getenv("VIDEO_USE_TEST_ADAPTER"), os.Getenv("VIDEO_USE_TEST_ROOT")
	if adapter == "" || vendor == "" {
		t.Skip("set VIDEO_USE_TEST_ADAPTER and VIDEO_USE_TEST_ROOT for offline adapter integration")
	}
	for _, binary := range []string{"python3", "ffmpeg", "ffprobe"} {
		if _, err := exec.LookPath(binary); err != nil {
			t.Skip(err)
		}
	}
	a, p, asset, shared := exchangeFixture(t)
	run, p := prepareExchange(t, a, p)
	runRoot := filepath.Join(shared, run.RelativeRunRoot)
	var manifest ExchangeManifest
	b, err := os.ReadFile(filepath.Join(shared, run.ManifestPath))
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(b, &manifest); err != nil {
		t.Fatal(err)
	}
	edl := map[string]any{"schemaVersion": 1, "canvas": map[string]any{"width": 320, "height": 180, "fps": 30}, "clips": []any{
		map[string]any{"id": "mapped-a", "assetId": asset.ID, "path": manifest.Media[0].Path, "type": "image", "start": 0, "duration": .5, "textRefs": []any{map[string]any{"quote": "芯片"}, map[string]any{"quote": "发布。"}}},
		map[string]any{"id": "background-stable", "assetId": asset.ID, "path": manifest.Media[0].Path, "type": "image", "start": .5, "duration": .5},
	}}
	if err := atomicJSON(filepath.Join(runRoot, "work/edl.json"), edl); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("python3", adapter, "render", "--manifest", filepath.Join(shared, run.ManifestPath), "--edl", filepath.Join(runRoot, "work/edl.json"), "--out", filepath.Join(runRoot, "artifacts", "refs-render"), "--video-use-root", vendor)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatal(err, string(out))
	}
	w := call(a, "POST", "/api/projects/"+p.ID+"/production-runs/"+run.ID+"/ingest", map[string]any{"revision": p.Revision})
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	p, _ = a.get(p.ID)
	if len(p.Renders) != 1 {
		t.Fatal(p.Renders)
	}
	render := p.Renders[0]
	w = locateCall(a, p.ID, map[string]any{"quote": "片发布"})
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var result EditLocateResult
	if err = json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if !result.Current || result.Backend != "video_use" || result.RenderID != render.ID || len(result.Matches) != 1 || result.Matches[0].ClipID != "mapped-a" || result.Matches[0].Start != 0 || result.Matches[0].End != .5 || len(result.Matches[0].MatchedRefs) != 2 || !reflect.DeepEqual(result.UnmappedClipIDs, []string{"background-stable"}) {
		t.Fatal(w.Body.String())
	}
	// Archive authority survives deletion of the entire mutable exchange run.
	if err := os.RemoveAll(runRoot); err != nil {
		t.Fatal(err)
	}
	w = locateCall(a, p.ID, map[string]any{"quote": "发布", "renderId": render.ID})
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	p.NarrationText = "另一个稿件。"
	p.Revision++
	a.projects[p.ID] = &p
	w = locateCall(a, p.ID, map[string]any{"quote": "芯片", "renderId": render.ID})
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	json.Unmarshal(w.Body.Bytes(), &result)
	if result.Current || len(result.Matches) != 1 || result.SourceHash != render.SourceHash {
		t.Fatal(w.Body.String())
	}
}
