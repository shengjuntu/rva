package main

import (
	"bytes"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func exchangeFixture(t *testing.T) (*App, Project, Asset, string) {
	t.Helper()
	a := testApp(t)
	root := t.TempDir()
	t.Setenv("VIDEO_EXCHANGE_ROOT", root)
	p, e := a.create("exchange")
	if e != nil {
		t.Fatal(e)
	}
	p.NarrationText = "芯片发布。"
	p.SessionID = "session-test"
	wf := defaultWorkflow()
	wf.EditingBackend = "video_use"
	p.Workflow = &wf
	var b bytes.Buffer
	im := image.NewRGBA(image.Rect(0, 0, 32, 32))
	for y := 0; y < 32; y++ {
		for x := 0; x < 32; x++ {
			im.Set(x, y, color.RGBA{R: 255, A: 255})
		}
	}
	png.Encode(&b, im)
	asset := Asset{ID: uid(), Name: "red.png", File: "red.png", MIME: "image/png", Size: int64(b.Len())}
	p.Assets = []Asset{asset}
	if e = os.WriteFile(filepath.Join(a.dir(p.ID), "workspace", "assets", asset.File), b.Bytes(), 0600); e != nil {
		t.Fatal(e)
	}
	j := SpeechJob{ID: uid(), Source: "narration", State: "completed", Duration: 1, Files: map[string]NewsFile{}}
	proj := p
	proj.Shots = narrationShots(p.NarrationText)
	j.ScriptHash = speechScriptHash(proj)
	dir := a.speechDir(p.ID, j.ID)
	os.MkdirAll(dir, 0700)
	f, e := os.Create(filepath.Join(dir, "narration.wav"))
	if e != nil {
		t.Fatal(e)
	}
	if e = writeSpeechWAVHeader(f, 24000); e != nil {
		t.Fatal(e)
	}
	f.Write(make([]byte, 48000))
	f.Close()
	os.WriteFile(filepath.Join(dir, "subtitles.srt"), []byte("1\n00:00:00,000 --> 00:00:01,000\n芯片发布。\n"), 0600)
	manifest := SpeechManifest{Schema: 1, JobID: j.ID, ScriptHash: j.ScriptHash, Duration: 1, Cues: []SubtitleCue{{Start: 0, End: 1, Text: p.NarrationText}}, Shots: []SpeechShot{{ID: "n1", Duration: 1}}}
	atomicJSON(filepath.Join(dir, "manifest.json"), manifest)
	for _, n := range []string{"narration.wav", "subtitles.srt", "manifest.json"} {
		h, size, e := hashLocal(filepath.Join(dir, n))
		if e != nil {
			t.Fatal(e)
		}
		j.Files[n] = NewsFile{SHA256: h, Size: size}
	}
	p.Speech = []SpeechJob{j}
	units := []MediaAlignmentUnit{{ID: "chip", Quote: p.NarrationText, Occurrence: 1, VisualIntent: "产品示意", AssetIDs: []string{asset.ID}, Role: "illustration", Reason: "示意", Status: "selected"}}
	basis, _, e := a.mediaAlignmentBasis(p, units)
	if e != nil {
		t.Fatal(e)
	}
	p.MediaAlignment = &MediaAlignmentDocument{SourceHash: narrationSourceHash(p), BasisHash: basis, Units: units}
	if e = a.save(&p); e != nil {
		t.Fatal(e)
	}
	a.projects[p.ID] = &p
	return a, p, asset, root
}
func prepareExchange(t *testing.T, a *App, p Project) (ProductionRun, Project) {
	t.Helper()
	w := call(a, "POST", "/api/projects/"+p.ID+"/production-runs/prepare", map[string]any{"revision": p.Revision, "requestId": uid()})
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var v struct {
		Run ProductionRun `json:"run"`
	}
	if e := json.Unmarshal(w.Body.Bytes(), &v); e != nil {
		t.Fatal(e)
	}
	q, _ := a.get(p.ID)
	return v.Run, q
}
func TestProductionExchangeFreezeRetryAndPersist(t *testing.T) {
	a, p, asset, root := exchangeFixture(t)
	request := map[string]any{"revision": p.Revision, "requestId": uid()}
	url := "/api/projects/" + p.ID + "/production-runs/prepare"
	w := call(a, "POST", url, request)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var v struct {
		Run ProductionRun `json:"run"`
	}
	json.Unmarshal(w.Body.Bytes(), &v)
	if v.Run.InputHash == "" || v.Run.SnapshotHash == "" || strings.Contains(w.Body.String(), root) {
		t.Fatal(w.Body.String())
	}
	var manifest ExchangeManifest
	b, e := os.ReadFile(filepath.Join(root, v.Run.ManifestPath))
	if e != nil {
		t.Fatal(e)
	}
	json.Unmarshal(b, &manifest)
	if manifest.Narration.SHA256 == "" || manifest.Subtitles.Size <= 0 || len(manifest.Media) != 1 {
		t.Fatal(manifest)
	}
	// Export is a copy, never a hardlink back into private assets.
	os.WriteFile(filepath.Join(a.dir(p.ID), "workspace", "assets", asset.File), []byte("changed original"), 0600)
	exported, e := os.ReadFile(filepath.Join(root, v.Run.RelativeRunRoot, manifest.Media[0].Path))
	if e != nil || string(exported) == "changed original" {
		t.Fatal("original and export share writable bytes")
	}
	latest, _ := a.get(p.ID)
	latest.NarrationText = "changed"
	latest.Revision++
	a.save(&latest)
	a.projects[p.ID] = &latest
	w = call(a, "POST", url, request)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var repeat struct {
		Run ProductionRun `json:"run"`
	}
	json.Unmarshal(w.Body.Bytes(), &repeat)
	if repeat.Run.ID != v.Run.ID {
		t.Fatal("duplicate prepare")
	}
	request["revision"] = latest.Revision
	if w = call(a, "POST", url, request); w.Code != 409 {
		t.Fatal(w.Code, w.Body.String())
	}
	reloaded, e := newApp(a.data)
	if e != nil {
		t.Fatal(e)
	}
	saved, _ := reloaded.get(p.ID)
	if len(saved.ProductionRuns) != 1 || saved.ProductionRuns[0].InputHash != v.Run.InputHash {
		t.Fatal(saved.ProductionRuns)
	}
}
func TestProductionExchangeRejectsChangedFrozenInput(t *testing.T) {
	a, p, _, root := exchangeFixture(t)
	run, p := prepareExchange(t, a, p)
	os.WriteFile(filepath.Join(root, run.RelativeRunRoot, "input/narration.txt"), []byte("tampered"), 0600)
	w := call(a, "POST", "/api/projects/"+p.ID+"/production-runs/"+run.ID+"/ingest", map[string]any{"revision": p.Revision})
	if w.Code != 409 || !strings.Contains(w.Body.String(), "冻结输入") {
		t.Fatal(w.Code, w.Body.String())
	}
	p, _ = a.get(p.ID)
	if len(p.Renders) != 0 {
		t.Fatal("tampered run ingested")
	}
}
func TestProductionExchangeRootRejectsTraversalAndSymlink(t *testing.T) {
	root := t.TempDir()
	secret := filepath.Join(t.TempDir(), "secret")
	os.WriteFile(secret, []byte("secret"), 0600)
	os.Symlink(secret, filepath.Join(root, "outside"))
	os.WriteFile(filepath.Join(root, "inside"), []byte("inside"), 0600)
	os.Symlink("inside", filepath.Join(root, "link"))
	r, e := os.OpenRoot(root)
	if e != nil {
		t.Fatal(e)
	}
	defer r.Close()
	for _, name := range []string{"../secret", "/etc/passwd", "a/../inside", "a\\b", "C:evil", "outside", "link"} {
		if f, e := exchangeOpen(r, name, 1000); e == nil {
			f.Close()
			t.Fatalf("accepted %q", name)
		}
	}
}
func TestProductionExchangeRealAdapterArchiveLateResultAndFinalProof(t *testing.T) {
	for _, bin := range []string{"ffmpeg", "ffprobe", "python3"} {
		if _, e := exec.LookPath(bin); e != nil {
			t.Skip(e)
		}
	}
	adapter := os.Getenv("VIDEO_USE_TEST_ADAPTER")
	vendor := os.Getenv("VIDEO_USE_TEST_ROOT")
	if adapter == "" || vendor == "" {
		t.Skip("set VIDEO_USE_TEST_ADAPTER and VIDEO_USE_TEST_ROOT for the vendored offline renderer")
	}
	a, p, asset, root := exchangeFixture(t)
	run, p := prepareExchange(t, a, p)
	runRoot := filepath.Join(root, run.RelativeRunRoot)
	var m ExchangeManifest
	b, _ := os.ReadFile(filepath.Join(root, run.ManifestPath))
	json.Unmarshal(b, &m)
	edl := map[string]any{"schemaVersion": 1, "canvas": map[string]any{"width": 320, "height": 180, "fps": 30}, "clips": []any{map[string]any{"id": "picture", "assetId": asset.ID, "path": m.Media[0].Path, "type": "image", "start": 0, "duration": 1, "fit": "cover"}}}
	atomicJSON(filepath.Join(runRoot, "work/edl.json"), edl)
	cmd := exec.Command("python3", adapter, "render", "--manifest", filepath.Join(root, run.ManifestPath), "--edl", filepath.Join(runRoot, "work/edl.json"), "--out", filepath.Join(runRoot, "artifacts", "render-test"), "--video-use-root", vendor)
	if out, e := cmd.CombinedOutput(); e != nil {
		t.Fatal(e, string(out))
	}
	// A late result is valid history but cannot become current for changed text.
	p.NarrationText = "稿件已修改。"
	p.Revision++
	a.save(&p)
	a.projects[p.ID] = &p
	ingestURL := "/api/projects/" + p.ID + "/production-runs/" + run.ID + "/ingest"
	w := call(a, "POST", ingestURL, map[string]any{"revision": p.Revision})
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	q, _ := a.get(p.ID)
	if len(q.Renders) != 1 || externalRenderCurrent(q, q.Renders[0]) {
		t.Fatal("late result marked current")
	}
	archived := q.Renders[0]
	privateVideo := filepath.Join(a.dir(p.ID), "renders", archived.ID, "final.mp4")
	before, _, _ := hashLocal(privateVideo)
	var frozen Project
	b, _ = os.ReadFile(filepath.Join(a.dir(p.ID), "renders", archived.ID, "snapshot.json"))
	json.Unmarshal(b, &frozen)
	if frozen.NarrationText == q.NarrationText || frozen.NarrationText != "芯片发布。" {
		t.Fatal("late result archived wrong snapshot")
	}
	w = call(a, "POST", ingestURL, map[string]any{"revision": 0})
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	q, _ = a.get(p.ID)
	if len(q.Renders) != 1 {
		t.Fatal("duplicate ingest")
	}
	proof, e := readRenderProof(filepath.Dir(privateVideo))
	if e != nil || !a.externalArchiveIntact(q, archived, proof) {
		t.Fatal(e, "bad archive proof")
	}
	// Exchange mutation after commit cannot change the App's output or proof.
	os.WriteFile(filepath.Join(runRoot, "artifacts", "render-test", "final.mp4"), []byte("changed"), 0600)
	after, _, _ := hashLocal(privateVideo)
	if before != after {
		t.Fatal("private archive shares output inode")
	}
	q.NarrationText = "芯片发布。"
	if !externalRenderCurrent(q, archived) || !a.externalRenderInputsIntact(q, archived) {
		t.Fatal("restored unchanged inputs should match receipt")
	}
	os.WriteFile(filepath.Join(a.dir(p.ID), "workspace", "assets", asset.File), []byte("modified source"), 0600)
	if a.externalRenderInputsIntact(q, archived) {
		t.Fatal("original asset tampering ignored")
	}
	os.WriteFile(filepath.Join(filepath.Dir(privateVideo), "edit-ir.json"), []byte("{}"), 0600)
	if a.externalArchiveIntact(q, archived, proof) {
		t.Fatal("archived EDL tampering ignored")
	}
}
func TestProductionExchangeRejectsCompletionArtifactPaths(t *testing.T) {
	for _, bad := range []string{"../secret.mp4", "/tmp/video.mp4", "artifacts/../input/narration.wav", "input/narration.wav"} {
		t.Run(bad, func(t *testing.T) {
			a, p, _, root := exchangeFixture(t)
			run, p := prepareExchange(t, a, p)
			c := ExchangeCompletion{SchemaVersion: 1, ProjectID: p.ID, ProductionRunID: run.ID, SourceHash: run.SourceHash, InputHash: run.InputHash, Artifacts: []ExchangeArtifact{{ID: "video", Kind: "video", Path: bad, SHA256: strings.Repeat("a", 64), Size: 1}, {ID: "edit", Kind: "edit", Path: "artifacts/edl.json", SHA256: strings.Repeat("b", 64), Size: 1}, {ID: "validation", Kind: "validation", Path: "artifacts/validation.json", SHA256: strings.Repeat("c", 64), Size: 1}}}
			atomicJSON(filepath.Join(root, run.RelativeRunRoot, "completion.json"), c)
			w := call(a, "POST", "/api/projects/"+p.ID+"/production-runs/"+run.ID+"/ingest", map[string]any{"revision": p.Revision})
			if w.Code != 409 {
				t.Fatal(w.Code, w.Body.String())
			}
		})
	}
}

func TestProductionExchangeNewerEditAuthoritySurvivesOlderLateIngest(t *testing.T) {
	for _, bin := range []string{"ffmpeg", "ffprobe", "python3"} {
		if _, e := exec.LookPath(bin); e != nil {
			t.Skip(e)
		}
	}
	adapter, vendor := os.Getenv("VIDEO_USE_TEST_ADAPTER"), os.Getenv("VIDEO_USE_TEST_ROOT")
	if adapter == "" || vendor == "" {
		t.Skip("set VIDEO_USE_TEST_ADAPTER and VIDEO_USE_TEST_ROOT")
	}
	a, p, asset, root := exchangeFixture(t)
	old, p := prepareExchange(t, a, p)
	newer, p := prepareExchange(t, a, p)
	for _, run := range []ProductionRun{old, newer} {
		runRoot := filepath.Join(root, run.RelativeRunRoot)
		var m ExchangeManifest
		b, _ := os.ReadFile(filepath.Join(root, run.ManifestPath))
		json.Unmarshal(b, &m)
		fit := "cover"
		if run.ID == newer.ID {
			fit = "contain"
		}
		edl := map[string]any{"schemaVersion": 1, "canvas": map[string]any{"width": 320, "height": 180, "fps": 30}, "clips": []any{map[string]any{"id": "picture", "assetId": asset.ID, "path": m.Media[0].Path, "type": "image", "start": 0, "duration": 1, "fit": fit}}}
		atomicJSON(filepath.Join(runRoot, "work/edl.json"), edl)
		cmd := exec.Command("python3", adapter, "render", "--manifest", filepath.Join(root, run.ManifestPath), "--edl", filepath.Join(runRoot, "work/edl.json"), "--out", filepath.Join(runRoot, "artifacts", "render-test"), "--video-use-root", vendor)
		if out, e := cmd.CombinedOutput(); e != nil {
			t.Fatal(e, string(out))
		}
	}
	ingest := func(run ProductionRun) Render {
		t.Helper()
		q, _ := a.get(p.ID)
		w := call(a, "POST", "/api/projects/"+p.ID+"/production-runs/"+run.ID+"/ingest", map[string]any{"revision": q.Revision})
		if w.Code != 200 {
			t.Fatal(w.Code, w.Body.String())
		}
		var got struct {
			Render Render `json:"render"`
		}
		json.Unmarshal(w.Body.Bytes(), &got)
		return got.Render
	}
	accepted := ingest(newer)
	q, _ := a.get(p.ID)
	w := call(a, "POST", "/api/projects/"+p.ID+"/final", map[string]any{"revision": q.Revision, "renderId": accepted.ID, "reviewed": true})
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	delayed := ingest(old)
	q, _ = a.get(p.ID)
	if externalRenderCurrent(q, delayed) || !externalRenderCurrent(q, accepted) || !finalCurrent(q) {
		t.Fatal("late older edit replaced newer authority", q.ProductionRuns)
	}
	// A genuinely newer completed edit makes the previous human designation stale.
	q.ProductionRuns = append(q.ProductionRuns, ProductionRun{ID: uid(), State: "completed", InputBasisHash: externalProductionBasis(q), RenderID: uid(), OutputHash: strings.Repeat("a", 64), EditHash: strings.Repeat("b", 64)})
	if externalRenderCurrent(q, accepted) || finalCurrent(q) {
		t.Fatal("older final remained current after newer accepted edit")
	}
}
