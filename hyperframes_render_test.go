package main

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func authoredRenderFixture(t *testing.T) (*App, Project) {
	t.Helper()
	for _, binary := range []string{"node", "ffmpeg", "ffprobe"} {
		if _, e := exec.LookPath(binary); e != nil {
			t.Skip("requires node/ffmpeg/ffprobe")
		}
	}
	a := testApp(t)
	t.Cleanup(func() {
		a.mu.Lock()
		for _, cancel := range a.jobs {
			cancel()
		}
		a.mu.Unlock()
		a.wg.Wait()
	})
	p, e := a.create("自由动画测试")
	if e != nil {
		t.Fatal(e)
	}
	workflow := newProjectWorkflow()
	p.Workflow = &workflow
	p.NarrationText = "材料服从文字。"
	p.HyperFrames = &HyperFramesDocument{SchemaVersion: 1, EntryPoint: "index.html", Canvas: HyperFramesCanvas{Width: 320, Height: 180, FPS: 30, Duration: 2}, TextSourceHash: narrationSourceHash(p), Files: []HyperFramesSourceFile{{Path: "index.html", Content: "<!doctype html><html><body>Original</body></html>"}}}
	a.projects[p.ID] = &p
	if e = a.save(&p); e != nil {
		t.Fatal(e)
	}
	// A fake pinned CLI exercises scheduling/cancellation without a browser.
	// Real media verification is tested separately below.
	tool := t.TempDir()
	if e = os.MkdirAll(filepath.Join(tool, "bin"), 0700); e != nil {
		t.Fatal(e)
	}
	a.hf = filepath.Join(tool, "bin", "hyperframes.mjs")
	a.gsap, a.font = filepath.Join(tool, "gsap.min.js"), filepath.Join(tool, "font.ttf")
	for file, data := range map[string]string{filepath.Join(tool, "package.json"): `{"name":"hyperframes","version":"0.8.87"}`, a.hf: `import{writeFileSync}from'node:fs';if(process.argv[2]==='render'){writeFileSync('render-started','yes');setTimeout(()=>{},300000)}else{console.log('{"passed":true}');}`, a.gsap: "// GSAP fixture", a.font: "font fixture"} {
		if e = os.WriteFile(file, []byte(data), 0600); e != nil {
			t.Fatal(e)
		}
	}
	return a, p
}

func authoredRenderCall(a *App, p Project, request RenderRequest) *httptest.ResponseRecorder {
	raw, _ := json.Marshal(request)
	r := httptest.NewRequest(http.MethodPost, "/api/projects/"+p.ID+"/render", bytes.NewReader(raw))
	r.SetPathValue("id", p.ID)
	w := httptest.NewRecorder()
	a.startHyperFramesRender(w, r, request)
	return w
}

func TestHyperFramesFreezeCapturesSourcesAssetsAndRuntime(t *testing.T) {
	a, p := authoredRenderFixture(t)
	asset := Asset{ID: uid(), File: "photo.png", MIME: "image/png", Name: "photo"}
	if e := os.WriteFile(filepath.Join(a.dir(p.ID), "workspace", "assets", asset.File), []byte("original picture bytes"), 0600); e != nil {
		t.Fatal(e)
	}
	p.Assets = append(p.Assets, asset)
	p.HyperFrames.Assets = []HyperFramesAssetBinding{{AssetID: asset.ID, Path: "media/photo.png"}}
	files, e := a.hyperFramesAssetFiles(p, *p.HyperFrames)
	if e != nil {
		t.Fatal(e)
	}
	p.HyperFrames.Assets[0].SHA256, p.HyperFrames.Assets[0].Size = files[0].SHA256, files[0].Size
	dir := filepath.Join(a.dir(p.ID), "renders", uid())
	manifest, e := a.freezeAuthoredHyperFrames(p, dir)
	if e != nil {
		t.Fatal(e)
	}
	if manifest.DocumentHash != hyperFramesDocumentHash(*p.HyperFrames) || !hyperFramesFrozenInputsIntact(dir, manifest) {
		t.Fatal("missing frozen provenance", manifest)
	}
	fontCSS, e := os.ReadFile(filepath.Join(dir, "runtime", "font.css"))
	if e != nil || !strings.Contains(string(fontCSS), "VideoFont") || !strings.Contains(string(fontCSS), "font.ttf") {
		t.Fatal("stable font stylesheet missing", string(fontCSS), e)
	}
	if e = os.WriteFile(files[0].SourceFile, []byte("changed live bytes"), 0600); e != nil {
		t.Fatal(e)
	}
	if !hyperFramesFrozenInputsIntact(dir, manifest) {
		t.Fatal("live mutation affected frozen archive")
	}
	if _, e = a.freezeAuthoredHyperFrames(p, filepath.Join(a.dir(p.ID), "renders", uid())); e == nil {
		t.Fatal("changed saved asset accepted")
	}
	if e = os.WriteFile(filepath.Join(dir, "media", "photo.png"), []byte("corrupt archive"), 0600); e != nil {
		t.Fatal(e)
	}
	if hyperFramesFrozenInputsIntact(dir, manifest) {
		t.Fatal("changed archive accepted")
	}
}

func TestHyperFramesCheckIsRevisionBoundAndNotARender(t *testing.T) {
	a, p := authoredRenderFixture(t)
	for _, revision := range []int{0, p.Revision + 1, p.Revision} {
		raw, _ := json.Marshal(map[string]int{"revision": revision})
		r := httptest.NewRequest(http.MethodPost, "/check", bytes.NewReader(raw))
		r.SetPathValue("id", p.ID)
		w := httptest.NewRecorder()
		a.hyperFramesCheck(w, r)
		if revision == p.Revision {
			var result hyperFramesCheckResult
			if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &result) != nil || result.Status != "passed" || result.Rendered || result.SnapshotHash == "" || result.DocumentHash != hyperFramesDocumentHash(*p.HyperFrames) {
				t.Fatal(w.Code, w.Body.String())
			}
		} else if w.Code < 400 {
			t.Fatal("unbound/stale check accepted", w.Code, w.Body.String())
		}
	}
	q, _ := a.get(p.ID)
	if len(q.Renders) != 0 || len(a.jobs) != 0 || q.Final != nil {
		t.Fatal("check created a render/final or leaked active task", q.Renders)
	}
}

func TestHyperFramesCheckRejectsMutatedFrozenInputs(t *testing.T) {
	a, p := authoredRenderFixture(t)
	if e := os.WriteFile(a.hf, []byte(`import{writeFileSync}from'node:fs';writeFileSync('index.html','changed by checker');console.log('{"passed":true}');`), 0600); e != nil {
		t.Fatal(e)
	}
	raw, _ := json.Marshal(map[string]int{"revision": p.Revision})
	r := httptest.NewRequest(http.MethodPost, "/check", bytes.NewReader(raw))
	r.SetPathValue("id", p.ID)
	w := httptest.NewRecorder()
	a.hyperFramesCheck(w, r)
	var result hyperFramesCheckResult
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &result) != nil || result.Status != "failed" || result.Rendered || !strings.Contains(result.Error, "冻结输入") {
		t.Fatal("checker mutation was reported as passed", w.Code, w.Body.String())
	}
}

func TestHyperFramesRenderFreezesBeforeAcceptanceAndCancelsIdempotently(t *testing.T) {
	a, p := authoredRenderFixture(t)
	request := RenderRequest{Revision: p.Revision, Kind: "draft", RequestID: uid()}
	w := authoredRenderCall(a, p, request)
	var job Render
	if w.Code != 202 || json.Unmarshal(w.Body.Bytes(), &job) != nil {
		t.Fatal(w.Code, w.Body.String())
	}
	dir := filepath.Join(a.dir(p.ID), "renders", job.ID)
	if _, e := os.Stat(filepath.Join(dir, "snapshot.json")); e != nil {
		t.Fatal("render accepted before immutable snapshot", e)
	}
	manifest, e := readHyperFramesManifest(dir)
	if e != nil || !hyperFramesFrozenInputsIntact(dir, manifest) {
		t.Fatal("invalid accepted snapshot", e)
	}
	p.HyperFrames.Files[0].Content = "changed after submit"
	w = authoredRenderCall(a, p, request)
	var repeated Render
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &repeated) != nil || repeated.ID != job.ID {
		t.Fatal("idempotent retry duplicated task", w.Code, w.Body.String())
	}
	changed := request
	changed.Kind = ""
	if w = authoredRenderCall(a, p, changed); w.Code != 409 {
		t.Fatal("requestId reuse with new payload accepted", w.Code, w.Body.String())
	}
	r := httptest.NewRequest(http.MethodDelete, "/render", nil)
	r.SetPathValue("id", p.ID)
	r.SetPathValue("rid", job.ID)
	w = httptest.NewRecorder()
	a.stopRender(w, r)
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	a.wg.Wait()
	q, _ := a.get(p.ID)
	if len(q.Renders) != 1 || q.Renders[0].State != "interrupted" || len(a.jobs) != 0 {
		t.Fatal("cancel did not stop authored process", q.Renders)
	}
	if !hyperFramesFrozenInputsIntact(dir, manifest) {
		t.Fatal("cancel/mutable source affected snapshot")
	}
}

func TestHyperFramesRenderRejectsUnpinnedRuntimeAndStaleText(t *testing.T) {
	a, p := authoredRenderFixture(t)
	if e := os.WriteFile(filepath.Join(filepath.Dir(a.hf), "..", "package.json"), []byte(`{"name":"hyperframes","version":"0.0.0"}`), 0600); e != nil {
		t.Fatal(e)
	}
	w := authoredRenderCall(a, p, RenderRequest{Revision: p.Revision})
	if w.Code != 422 || !strings.Contains(w.Body.String(), "0.8.87") {
		t.Fatal("floating runtime accepted", w.Code, w.Body.String())
	}
	q, _ := a.get(p.ID)
	if len(q.Renders) != 0 || len(a.jobs) != 0 {
		t.Fatal("rejected request created job")
	}
	p.NarrationText += "正文变化。"
	a.projects[p.ID] = &p
	w = authoredRenderCall(a, p, RenderRequest{Revision: p.Revision})
	if w.Code != 422 || !strings.Contains(w.Body.String(), "主播稿") {
		t.Fatal("stale authoring text accepted", w.Code, w.Body.String())
	}
}

func TestHyperFramesDirectRenderRequiresBackendButReplaysExistingRequest(t *testing.T) {
	a, p := authoredRenderFixture(t)
	selectBackend := func(backend string) Project {
		t.Helper()
		a.mu.Lock()
		defer a.mu.Unlock()
		q := clone(a.projects[p.ID])
		workflow := workflowFor(q)
		workflow.EditingBackend = backend
		q.Workflow = &workflow
		q.Revision++
		if e := a.save(&q); e != nil {
			t.Fatal(e)
		}
		a.projects[q.ID] = &q
		return q
	}
	native := selectBackend("native")
	w := authoredRenderCall(a, native, RenderRequest{Revision: native.Revision})
	if w.Code != 409 || len(a.jobs) != 0 {
		t.Fatal("direct helper selected wrong backend", w.Code, w.Body.String())
	}
	selected := selectBackend("hyperframes")
	request := RenderRequest{Revision: selected.Revision, RequestID: uid(), Kind: "draft"}
	w = authoredRenderCall(a, selected, request)
	var job Render
	if w.Code != 202 || json.Unmarshal(w.Body.Bytes(), &job) != nil {
		t.Fatal(w.Code, w.Body.String())
	}
	native = selectBackend("native")
	w = authoredRenderCall(a, native, request)
	var replay Render
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &replay) != nil || replay.ID != job.ID {
		t.Fatal("backend change lost accepted request receipt", w.Code, w.Body.String())
	}
	w = authoredRenderCall(a, native, RenderRequest{Revision: native.Revision, RequestID: uid()})
	if w.Code != 409 {
		t.Fatal("fresh authored render bypassed selected native backend", w.Code, w.Body.String())
	}
}

func TestHyperFramesArchiveIntegrityIsSeparateFromLiveInputs(t *testing.T) {
	a, p := authoredRenderFixture(t)
	render := Render{ID: uid(), Revision: p.Revision, Engine: authoredHyperFramesEngine, State: "completed", Duration: p.HyperFrames.Canvas.Duration, SourceHash: narrationSourceHash(p), ContentHash: productionHash(p)}
	dir := filepath.Join(a.dir(p.ID), "renders", render.ID)
	manifest, e := a.freezeAuthoredHyperFrames(p, dir)
	if e != nil {
		t.Fatal(e)
	}
	render.CacheKey = manifest.SnapshotHash
	p.Renders = append(p.Renders, render)
	a.projects[p.ID] = &p
	if e = a.save(&p); e != nil {
		t.Fatal(e)
	}
	cmd := exec.Command("ffmpeg", "-v", "error", "-f", "lavfi", "-i", "color=c=blue:s=320x180:r=30:d=2", "-c:v", "libx264", "-threads", "1", "-pix_fmt", "yuv420p", filepath.Join(dir, "final.mp4"))
	if raw, e := cmd.CombinedOutput(); e != nil {
		t.Fatal(e, string(raw))
	}
	if e = verifyAuthoredHyperFramesOutput(context.Background(), dir, p.HyperFrames.Canvas, false); e != nil {
		t.Fatal(e)
	}
	if e = a.saveRenderProof(p.ID, render.ID, dir, manifest.SnapshotHash, ""); e != nil {
		t.Fatal(e)
	}
	proof, e := readRenderProof(dir)
	if e != nil || !a.hyperFramesRenderInputsIntact(p, render) || !a.hyperFramesArchiveIntact(p, render, proof) {
		t.Fatal("valid archive failed integrity", e)
	}
	if e = os.WriteFile(a.font, []byte("new live font"), 0600); e != nil {
		t.Fatal(e)
	}
	if a.hyperFramesRenderInputsIntact(p, render) || !a.hyperFramesArchiveIntact(p, render, proof) {
		t.Fatal("live runtime change conflated with archive corruption")
	}
	if e = os.WriteFile(filepath.Join(dir, "index.html"), []byte("tampered archived source"), 0600); e != nil {
		t.Fatal(e)
	}
	if a.hyperFramesArchiveIntact(p, render, proof) {
		t.Fatal("tampered frozen authoring source accepted")
	}
}

func TestHyperFramesOutputValidationUsesActualMedia(t *testing.T) {
	for _, binary := range []string{"ffmpeg", "ffprobe"} {
		if _, e := exec.LookPath(binary); e != nil {
			t.Skip("requires ffmpeg/ffprobe")
		}
	}
	dir := t.TempDir()
	cmd := exec.Command("ffmpeg", "-v", "error", "-f", "lavfi", "-i", "color=c=blue:s=320x180:r=30:d=1", "-f", "lavfi", "-i", "sine=frequency=440:sample_rate=48000:duration=1", "-c:v", "libx264", "-threads", "1", "-pix_fmt", "yuv420p", "-c:a", "aac", "-t", "1", filepath.Join(dir, "final.mp4"))
	if raw, e := cmd.CombinedOutput(); e != nil {
		t.Fatal(e, string(raw))
	}
	canvas := HyperFramesCanvas{Width: 320, Height: 180, FPS: 30, Duration: 1}
	if e := verifyAuthoredHyperFramesOutput(context.Background(), dir, canvas, true); e != nil {
		t.Fatal(e)
	}
	for _, mutate := range []func(*HyperFramesCanvas){func(c *HyperFramesCanvas) { c.Width = 640 }, func(c *HyperFramesCanvas) { c.FPS = 24 }, func(c *HyperFramesCanvas) { c.Duration = 3 }} {
		invalid := canvas
		mutate(&invalid)
		if e := verifyAuthoredHyperFramesOutput(context.Background(), dir, invalid, true); e == nil {
			t.Fatal("incorrect declared media metadata accepted", invalid)
		}
	}
	if e := verifyAuthoredHyperFramesOutput(context.Background(), dir, canvas, false); e == nil {
		t.Fatal("unexpected audio track accepted")
	}
}

func TestHyperFramesOutputRejectsIndependentStreamDurationMismatch(t *testing.T) {
	for _, binary := range []string{"ffmpeg", "ffprobe"} {
		if _, e := exec.LookPath(binary); e != nil {
			t.Skip("requires ffmpeg/ffprobe")
		}
	}
	for _, durations := range []struct{ video, audio string }{{"1", "3"}, {"3", "1"}} {
		dir := t.TempDir()
		cmd := exec.Command("ffmpeg", "-v", "error", "-f", "lavfi", "-i", "color=c=blue:s=320x180:r=30:d="+durations.video, "-f", "lavfi", "-i", "sine=frequency=440:sample_rate=48000:duration="+durations.audio, "-c:v", "libx264", "-threads", "1", "-pix_fmt", "yuv420p", "-c:a", "aac", filepath.Join(dir, "final.mp4"))
		if raw, e := cmd.CombinedOutput(); e != nil {
			t.Fatal(e, string(raw))
		}
		canvas := HyperFramesCanvas{Width: 320, Height: 180, FPS: 30, Duration: 3}
		if e := verifyAuthoredHyperFramesOutput(context.Background(), dir, canvas, true); e == nil {
			t.Fatal("container duration hid mismatched independent stream", durations)
		}
	}
}

// Opt-in integration acceptance uses the installed pinned CLI and a real
// browser, animation, registered still image, and independently generated tone.
// It never invokes a model or a paid generation provider.
func TestHyperFramesRealAnimationAudioRender(t *testing.T) {
	if os.Getenv("VIDEO_HYPERFRAMES_REAL_SMOKE") != "1" {
		t.Skip("set VIDEO_HYPERFRAMES_REAL_SMOKE=1 for real browser rendering")
	}
	a := testApp(t)
	if root := os.Getenv("VIDEO_HYPERFRAMES_SMOKE_DIR"); root != "" {
		var e error
		a, e = newApp(root)
		if e != nil {
			t.Fatal(e)
		}
	}
	wd, _ := os.Getwd()
	a.hf = filepath.Join(wd, "runtime", "node_modules", "hyperframes", "bin", "hyperframes.mjs")
	a.gsap = filepath.Join(wd, "runtime", "node_modules", "gsap", "dist", "gsap.min.js")
	a.font = "/usr/share/fonts/truetype/dejavu/DejaVuSans.ttf"
	a.token = "real-smoke-fixture-access-token"
	if e := a.authoredHyperFramesReady(); e != nil {
		t.Fatal(e)
	}
	p, e := a.create("Real authored animation + audio acceptance")
	if e != nil {
		t.Fatal(e)
	}
	p.NarrationText = "Source text drives the scene."
	assetDir := filepath.Join(a.dir(p.ID), "workspace", "assets")
	wave, photo := Asset{ID: uid(), File: "tone.wav", MIME: "audio/wav", Name: "440 Hz test tone"}, Asset{ID: uid(), File: "picture.png", MIME: "image/png", Name: "test picture"}
	cmd := exec.Command("ffmpeg", "-v", "error", "-f", "lavfi", "-i", "sine=frequency=440:sample_rate=48000:duration=2", "-y", filepath.Join(assetDir, wave.File))
	if raw, e := cmd.CombinedOutput(); e != nil {
		t.Fatal(e, string(raw))
	}
	img := image.NewRGBA(image.Rect(0, 0, 32, 32))
	for y := 0; y < 32; y++ {
		for x := 0; x < 32; x++ {
			img.Set(x, y, color.RGBA{uint8(x * 8), uint8(y * 8), 180, 255})
		}
	}
	f, e := os.Create(filepath.Join(assetDir, photo.File))
	if e != nil {
		t.Fatal(e)
	}
	e = png.Encode(f, img)
	f.Close()
	if e != nil {
		t.Fatal(e)
	}
	p.Assets = []Asset{wave, photo}
	doc := HyperFramesDocument{SchemaVersion: 1, EntryPoint: "index.html", Canvas: HyperFramesCanvas{Width: 320, Height: 180, FPS: 30, Duration: 2}, HasAudio: true, TextSourceHash: narrationSourceHash(p), Assets: []HyperFramesAssetBinding{{AssetID: wave.ID, Path: "media/tone.wav"}, {AssetID: photo.ID, Path: "media/picture.png"}}, Files: []HyperFramesSourceFile{{Path: "index.html", Content: authoredHyperFramesSmokeHTML}}, Timeline: []HyperFramesReviewClip{
		{ID: "picture", Track: "visual", Kind: "image", Start: 0, Duration: 2, AssetID: photo.ID, TextRefs: []TextReference{{Quote: p.NarrationText}}},
		{ID: "bar", Track: "graphics", Kind: "animation", Start: 0, Duration: 2},
		{ID: "voice", Track: "voice", Kind: "voice", Start: 0, Duration: 2, AssetID: wave.ID, TextRefs: []TextReference{{Quote: p.NarrationText}}},
		{ID: "caption", Track: "subtitle", Kind: "subtitle", Start: 0, Duration: 2, TextRefs: []TextReference{{Quote: p.NarrationText}}},
	}}
	files, e := a.hyperFramesAssetFiles(p, doc)
	if e != nil {
		t.Fatal(e)
	}
	for i := range files {
		doc.Assets[i].SHA256, doc.Assets[i].Size = files[i].SHA256, files[i].Size
	}
	p.HyperFrames = &doc
	a.projects[p.ID] = &p
	if e = a.save(&p); e != nil {
		t.Fatal(e)
	}
	w := authoredRenderCall(a, p, RenderRequest{Revision: p.Revision, Kind: "draft", RequestID: uid()})
	var job Render
	if w.Code != 202 || json.Unmarshal(w.Body.Bytes(), &job) != nil {
		t.Fatal(w.Code, w.Body.String())
	}
	deadline := time.Now().Add(3 * time.Minute)
	for time.Now().Before(deadline) {
		q, _ := a.get(p.ID)
		for _, candidate := range q.Renders {
			if candidate.ID == job.ID {
				job = candidate
			}
		}
		if job.State != "running" && job.State != "stopping" {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if job.State != "completed" {
		a.mu.Lock()
		cancel := a.jobs[job.ID]
		a.mu.Unlock()
		if cancel != nil {
			cancel()
		}
		a.wg.Wait()
		raw, _ := os.ReadFile(filepath.Join(a.dir(p.ID), "renders", job.ID, "render.log"))
		t.Fatal("real render failed", job, string(raw))
	}
	a.wg.Wait()
	q, _ := a.get(p.ID)
	proof, e := readRenderProof(filepath.Join(a.dir(p.ID), "renders", job.ID))
	if e != nil || !a.hyperFramesArchiveIntact(q, job, proof) || !a.hyperFramesRenderInputsIntact(q, job) {
		t.Fatal("real render lacks intact proof", proof, e)
	}
	output := filepath.Join(a.dir(p.ID), "renders", job.ID, "final.mp4")
	frames := []image.Image{}
	for _, at := range []string{"0.2", "1.5"} {
		cmd := exec.Command("ffmpeg", "-v", "error", "-ss", at, "-i", output, "-frames:v", "1", "-f", "image2pipe", "-vcodec", "png", "-")
		raw, e := cmd.Output()
		if e != nil {
			t.Fatal("decode actual animation sample", e)
		}
		frame, e := png.Decode(bytes.NewReader(raw))
		if e != nil {
			t.Fatal(e)
		}
		frames = append(frames, frame)
	}
	green := func(frame image.Image, x, y int) bool {
		r, g, b, _ := frame.At(x, y).RGBA()
		return g > r+5000 && g > b+5000
	}
	if !green(frames[0], 50, 116) || green(frames[1], 50, 116) || !green(frames[1], 175, 116) {
		t.Fatal("actual output did not contain expected moving GSAP bar")
	}
	cmd = exec.Command("ffmpeg", "-v", "error", "-i", output, "-vn", "-ac", "1", "-ar", "48000", "-t", "2", "-f", "s16le", "-")
	pcm, e := cmd.Output()
	if e != nil || len(pcm) < 48000*2 {
		t.Fatal("actual AAC output could not be decoded", len(pcm), e)
	}
	peak := 0
	for i := 0; i+1 < len(pcm); i += 2 {
		sample := int(int16(binary.LittleEndian.Uint16(pcm[i : i+2])))
		if sample < 0 {
			sample = -sample
		}
		if sample > peak {
			peak = sample
		}
	}
	if peak < 64 {
		t.Fatal("actual authored audio was silent", peak)
	}

	// Explicit reviewed=true is a fixture exercising the final designation
	// contract; this test makes no claim that a human reviewed a news video.
	base := "/api/projects/" + p.ID
	w = call(a, http.MethodPost, base+"/final", map[string]any{"revision": q.Revision, "renderId": job.ID, "reviewed": true})
	if w.Code != 200 {
		t.Fatal("real completed render could not be designated final", w.Code, w.Body.String())
	}
	q, _ = a.get(p.ID)
	if !finalCurrent(q) || q.Final == nil || q.Final.RenderID != job.ID {
		t.Fatal("new final did not become current", q.Final)
	}
	type verification struct {
		InputsCurrent bool `json:"inputsCurrent"`
		OutputIntact  bool `json:"outputIntact"`
		Verified      bool `json:"verified"`
	}
	verify := func() verification {
		t.Helper()
		w := call(a, http.MethodGet, base+"/final/verify", nil)
		var result verification
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &result) != nil {
			t.Fatal("final verification could not be read", w.Code, w.Body.String())
		}
		return result
	}
	initialFinal := verify()
	if !initialFinal.InputsCurrent || !initialFinal.OutputIntact || !initialFinal.Verified {
		t.Fatal("new real final did not verify", initialFinal)
	}
	// Live source mutation invalidates reuse, while the archived output and its
	// original source bytes remain valid evidence of the earlier production.
	livePicture := filepath.Join(assetDir, photo.File)
	originalPicture, e := os.ReadFile(livePicture)
	if e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(livePicture, append(append([]byte(nil), originalPicture...), []byte("live input changed")...), 0600); e != nil {
		t.Fatal(e)
	}
	assetChanged := verify()
	if assetChanged.InputsCurrent || !assetChanged.OutputIntact || assetChanged.Verified {
		t.Fatal("changed live asset was conflated with archived output", assetChanged)
	}
	if e = os.WriteFile(livePicture, originalPicture, 0600); e != nil {
		t.Fatal(e)
	}
	if restored := verify(); !restored.InputsCurrent || !restored.OutputIntact || !restored.Verified {
		t.Fatal("restored live source did not recover input validity", restored)
	}
	originalNarration := p.NarrationText
	w = call(a, http.MethodPut, base+"/narration", map[string]any{"revision": q.Revision, "text": originalNarration + " Updated source requires a new edit."})
	if w.Code != 200 {
		t.Fatal("could not update real project narration", w.Code, w.Body.String())
	}
	q, _ = a.get(p.ID)
	if finalCurrent(q) {
		t.Fatal("final remained current after source narration changed")
	}
	textChanged := verify()
	if textChanged.InputsCurrent || !textChanged.OutputIntact || textChanged.Verified {
		t.Fatal("changed narration was conflated with archived output", textChanged)
	}
	w = call(a, http.MethodGet, base+"/review-timeline?renderId="+job.ID, nil)
	var historical ReviewTimeline
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &historical) != nil || historical.Current || historical.Text != originalNarration || historical.SourceHash != job.SourceHash || historical.RenderID != job.ID || historical.Source != "authored" {
		t.Fatal("historical review did not preserve original narration", w.Code, w.Body.String())
	}
	if len(historical.Tracks) < 4 || len(historical.Tracks[0].Clips) == 0 || historical.Tracks[0].Clips[0].ID != "picture" || len(historical.Tracks[0].Clips[0].TextRefs) != 1 || historical.Tracks[0].Clips[0].TextRefs[0].Quote != originalNarration {
		t.Fatal("historical review lost original text-to-picture reference", historical.Tracks)
	}
	result := map[string]any{"status": "passed", "projectId": p.ID, "renderId": job.ID, "engine": job.Engine, "engineVersion": authoredHyperFramesVersion, "width": 320, "height": 180, "fps": 30, "duration": 2, "hasAudio": true, "audioDecodedNonSilent": true, "decodedAudioPeak": peak, "motionSamplesVerified": []float64{.2, 1.5}, "videoHash": proof.VideoHash, "snapshotHash": proof.SnapshotHash, "inputHash": proof.Key, "finalId": q.Final.ID, "finalVerified": true, "assetChangeInputsCurrent": assetChanged.InputsCurrent, "assetChangeOutputIntact": assetChanged.OutputIntact, "textChangeInputsCurrent": textChanged.InputsCurrent, "textChangeOutputIntact": textChanged.OutputIntact, "finalCurrentAfterTextChange": finalCurrent(q), "historicalTextPreserved": true, "modelTested": false, "indexTTSTested": false, "output": output}
	if e = atomicJSON(filepath.Join(a.data, "hyperframes-real-smoke.json"), result); e != nil {
		t.Fatal(e)
	}
	t.Logf("real acceptance: %+v", result)
}

const authoredHyperFramesSmokeHTML = `<!doctype html><html lang="en"><head><meta charset="utf-8"><link rel="stylesheet" href="runtime/font.css"><style>*{box-sizing:border-box}body{margin:0;background:#101827}#stage{width:320px;height:180px;position:relative;overflow:hidden;background:#101827;color:white;font-family:VideoFont,sans-serif}#picture{position:absolute;left:18px;top:18px;width:72px;height:72px;object-fit:cover}#bar{position:absolute;left:18px;top:108px;width:48px;height:16px;background:#36d399}#caption{position:absolute;left:110px;top:24px;width:190px;font-size:18px;line-height:1.5}</style><script src="runtime/gsap.min.js"></script></head><body><main id="stage" data-composition-id="news" data-start="0" data-duration="2" data-width="320" data-height="180" data-fps="30"><img id="picture" class="clip" src="media/picture.png" data-start="0" data-duration="2" data-track-index="1" alt="Test gradient"><div id="bar" class="clip" data-start="0" data-duration="2" data-track-index="2"></div><div id="caption" class="clip" data-start="0" data-duration="2" data-track-index="3">Source text<br>drives the scene.</div><audio id="voice" src="media/tone.wav" data-start="0" data-duration="2" data-end="2" data-track-index="4" data-volume="0.15"></audio></main><script>const tl=gsap.timeline({paused:true});tl.to('#bar',{x:180,duration:2,ease:'none'},0);window.__timelines={news:tl};</script></body></html>`
