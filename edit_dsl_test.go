package main

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"math"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func editTime(v float64) *float64 { return &v }
func simpleEdit() EditDocument {
	return EditDocument{Version: 1, Canvas: EditCanvas{Width: 320, Height: 180, FPS: 30}, Tracks: []EditTrack{{ID: "titles", Kind: "text"}}, Clips: []EditClip{{ID: "title", Track: "titles", Start: editTime(0), Duration: 1, Text: "自由内容"}}}
}
func TestEditCompilerResolvesCrossTrackAnchorsAndRejectsCycles(t *testing.T) {
	d := simpleEdit()
	d.Anchors = []EditAnchor{{ID: "title_end", Clip: "title", Edge: "end", Offset: .5}}
	d.Clips = append(d.Clips, EditClip{ID: "next", Track: "titles", Anchor: "title_end", Duration: 1, Text: "第二个画面"})
	ir, e := compileEdit(d, nil)
	if e != nil || ir.Clips[1].Start != 1.5 || ir.Duration != 2.5 {
		t.Fatal(ir, e)
	}
	d.Clips[0].Start = nil
	d.Clips[0].After = "next"
	if _, e = compileEdit(d, nil); e == nil || !strings.Contains(e.Error(), "循环") {
		t.Fatal("cycle accepted", e)
	}
	d = simpleEdit()
	d.Clips[0].Start = nil
	d.Clips[0].After = "missing"
	if _, e = compileEdit(d, nil); e == nil {
		t.Fatal("missing clip accepted")
	}
}
func TestEditCompilerRejectsIgnoredFieldsAndInvalidRanges(t *testing.T) {
	d := simpleEdit()
	raw, _ := json.Marshal(d)
	var object map[string]any
	json.Unmarshal(raw, &object)
	object["invented"] = true
	raw, _ = json.Marshal(object)
	var parsed EditDocument
	if json.Unmarshal(raw, &parsed) == nil {
		t.Fatal("unknown field ignored")
	}
	for _, mutate := range []func(*EditDocument){func(d *EditDocument) { d.Clips[0].Start = editTime(math.NaN()) }, func(d *EditDocument) { d.Clips[0].After = "other" }, func(d *EditDocument) { d.Clips[0].FadeOut = 2 }, func(d *EditDocument) { d.Canvas.Width = 321 }, func(d *EditDocument) { d.Clips[0].Layout.Fit = "invented" }, func(d *EditDocument) { d.Canvas.Duration = .5 }} {
		d = simpleEdit()
		mutate(&d)
		if _, e := compileEdit(d, nil); e == nil {
			t.Fatal("invalid edit accepted", d)
		}
	}
	d = EditDocument{Version: 1, Canvas: EditCanvas{}, Tracks: []EditTrack{{ID: "video", Kind: "video"}}, Clips: []EditClip{{ID: "clip", Track: "video", Asset: "asset", Start: editTime(0), Source: EditSource{In: 2, Out: editTime(6), Speed: 2}}}}
	ir, e := compileEdit(d, []Asset{{ID: "asset", MIME: "video/mp4"}})
	if e != nil || ir.Clips[0].Duration != 2 || ir.Clips[0].SourceOut != 6 {
		t.Fatal(ir, e)
	}
	d.Clips[0].Duration = 3
	if _, e = compileEdit(d, []Asset{{ID: "asset", MIME: "video/mp4"}}); e == nil {
		t.Fatal("conflicting duration ignored")
	}
}
func TestEditSaveIsVersionedIndependentAndPersistent(t *testing.T) {
	a := testApp(t)
	p, _ := a.create("自由剪辑")
	d := simpleEdit()
	w := call(a, "PUT", "/api/projects/"+p.ID+"/edit", EditRequest{Revision: p.Revision, DSL: &d})
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	q, _ := a.get(p.ID)
	if !q.EditActive || len(q.Shots) != 0 || q.EditDSL == nil {
		t.Fatal("DSL required legacy narration", q)
	}
	w = call(a, "PUT", "/api/projects/"+p.ID+"/edit", EditRequest{Revision: p.Revision, DSL: &d})
	if w.Code != 409 {
		t.Fatal("stale edit accepted")
	}
	reload, e := newApp(a.data)
	if e != nil {
		t.Fatal(e)
	}
	q, _ = reload.get(p.ID)
	if !q.EditActive || q.EditDSL.Clips[0].ID != "title" {
		t.Fatal("DSL lost on reload")
	}
	w = call(reload, "POST", "/api/projects/"+p.ID+"/edit/active", map[string]any{"revision": q.Revision, "active": false})
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	q, _ = reload.get(p.ID)
	if q.EditActive || q.EditDSL == nil {
		t.Fatal("switch deleted editing document")
	}
}
func editFixtureAsset(t *testing.T, a *App, p Project, name, mime string, data []byte) Asset {
	t.Helper()
	asset := Asset{ID: uid(), Name: name, MIME: mime, Size: int64(len(data))}
	asset.File = asset.ID + filepath.Ext(name)
	if e := os.WriteFile(filepath.Join(a.dir(p.ID), "workspace", "assets", asset.File), data, 0600); e != nil {
		t.Fatal(e)
	}
	a.mu.Lock()
	a.projects[p.ID].Assets = append(a.projects[p.ID].Assets, asset)
	a.mu.Unlock()
	return asset
}
func waitEditRender(t *testing.T, a *App, pid, rid string) Render {
	t.Helper()
	end := time.Now().Add(30 * time.Second)
	for time.Now().Before(end) {
		p, _ := a.get(pid)
		for _, j := range p.Renders {
			if j.ID == rid && j.State != "running" && j.State != "stopping" {
				return j
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("render timeout")
	return Render{}
}
func TestEditFFmpegActuallyRendersTracksTrimSpeedLayoutAndAudio(t *testing.T) {
	for _, name := range []string{"ffmpeg", "ffprobe"} {
		if _, e := exec.LookPath(name); e != nil {
			t.Skip(name + " unavailable")
		}
	}
	font := "/usr/share/fonts/truetype/dejavu/DejaVuSans.ttf"
	if !regularFile(font) {
		t.Skip("fixture font unavailable")
	}
	a := testApp(t)
	a.font = font
	p, _ := a.create("DSL真实渲染")
	redImage := image.NewRGBA(image.Rect(0, 0, 32, 32))
	blueImage := image.NewRGBA(image.Rect(0, 0, 32, 32))
	for y := 0; y < 32; y++ {
		for x := 0; x < 32; x++ {
			redImage.Set(x, y, color.RGBA{255, 0, 0, 255})
			blueImage.Set(x, y, color.RGBA{0, 0, 255, 255})
		}
	}
	var b bytes.Buffer
	png.Encode(&b, redImage)
	red := editFixtureAsset(t, a, p, "red.png", "image/png", b.Bytes())
	b.Reset()
	png.Encode(&b, blueImage)
	blue := editFixtureAsset(t, a, p, "blue.png", "image/png", b.Bytes())
	videoFile := filepath.Join(t.TempDir(), "green.mp4")
	cmd := exec.Command("ffmpeg", "-v", "error", "-f", "lavfi", "-i", "color=c=lime:s=320x180:r=30:d=2,drawbox=x=0:y=0:w=iw:h=ih:color=yellow:t=fill:enable='gte(t,1)'", "-c:v", "libx264", "-threads", "1", "-pix_fmt", "yuv420p", "-y", videoFile)
	if out, e := cmd.CombinedOutput(); e != nil {
		t.Fatal(e, string(out))
	}
	videoBytes, _ := os.ReadFile(videoFile)
	green := editFixtureAsset(t, a, p, "green.mp4", "video/mp4", videoBytes)
	audioFile := filepath.Join(t.TempDir(), "tone.wav")
	cmd = exec.Command("ffmpeg", "-v", "error", "-f", "lavfi", "-i", "sine=frequency=440:duration=2", "-y", audioFile)
	if out, e := cmd.CombinedOutput(); e != nil {
		t.Fatal(e, string(out))
	}
	audioBytes, _ := os.ReadFile(audioFile)
	audio := editFixtureAsset(t, a, p, "tone.wav", "audio/wav", audioBytes)
	p, _ = a.get(p.ID)
	d := EditDocument{Version: 1, Canvas: EditCanvas{Width: 320, Height: 180, FPS: 30}, Tracks: []EditTrack{{ID: "main", Kind: "video"}, {ID: "overlay", Kind: "video"}, {ID: "titles", Kind: "text"}, {ID: "voice", Kind: "audio"}}, Anchors: []EditAnchor{{ID: "end_green", Clip: "green", Edge: "end"}}, Clips: []EditClip{
		{ID: "red", Track: "main", Asset: red.ID, Start: editTime(0), Duration: 1},
		{ID: "green", Track: "main", Asset: green.ID, After: "red", Source: EditSource{In: .5, Out: editTime(1.5), Speed: 2}},
		{ID: "ending", Track: "main", Asset: red.ID, Anchor: "end_green", Duration: .5},
		{ID: "inset", Track: "overlay", Asset: blue.ID, Start: editTime(0), Duration: 2, Layout: EditLayout{X: 100, Y: 0, Width: 80, Height: 80, Opacity: editTime(.5), Fit: "contain"}},
		{ID: "title", Track: "titles", Start: editTime(0), Duration: 2, Text: "100% literal\nNEWS", FontSize: 18, Layout: EditLayout{X: 20, Y: 110, Width: 220, Height: 60}, FadeIn: .1, FadeOut: .1},
		{ID: "sound", Track: "voice", Asset: audio.ID, Start: editTime(.5), Source: EditSource{In: .1, Out: editTime(1.1)}, Volume: editTime(.5), FadeIn: .05, FadeOut: .05},
	}}
	w := call(a, "PUT", "/api/projects/"+p.ID+"/edit", EditRequest{Revision: p.Revision, DSL: &d})
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	p, _ = a.get(p.ID)
	w = call(a, "POST", "/api/projects/"+p.ID+"/jobs", JobStartRequest{Type: "render", Revision: p.Revision, RequestID: uid()})
	if w.Code != 202 {
		t.Fatal(w.Code, w.Body.String())
	}
	var view ProductionJob
	json.Unmarshal(w.Body.Bytes(), &view)
	job := waitEditRender(t, a, p.ID, view.NativeID)
	if job.State != "completed" {
		log, _ := os.ReadFile(filepath.Join(a.dir(p.ID), "renders", job.ID, "render.log"))
		t.Fatal(job.Error, string(log))
	}
	if job.Engine != "ffmpeg_edit" || !job.HasAudio || math.Abs(job.Duration-2) > .001 {
		t.Fatal(job)
	}
	final := filepath.Join(a.dir(p.ID), "renders", job.ID, "final.mp4")
	if export := os.Getenv("VIDEO_EDIT_TEST_EXPORT"); export != "" {
		for _, name := range []string{"final.mp4", "edit-ir.json", "filter.ffscript", "render.log"} {
			data, _ := os.ReadFile(filepath.Join(a.dir(p.ID), "renders", job.ID, name))
			suffix := "." + name
			if name == "final.mp4" {
				suffix = ""
			}
			os.WriteFile(export+suffix, data, 0600)
		}
	}
	for _, sample := range []struct {
		time  float64
		color string
	}{{.25, "red"}, {1.125, "green"}, {1.375, "yellow"}, {1.75, "red"}} {
		cmd = exec.Command("ffmpeg", "-v", "error", "-ss", decimal(sample.time), "-i", final, "-frames:v", "1", "-threads", "1", "-f", "image2pipe", "-c:v", "png", "pipe:1")
		raw, e := cmd.Output()
		if e != nil {
			t.Fatal(e)
		}
		frame, e := png.Decode(bytes.NewReader(raw))
		if e != nil {
			t.Fatal(e)
		}
		r, g, _, _ := frame.At(10, 10).RGBA()
		if sample.color == "red" && r < g || sample.color == "green" && (g < r || r > 16000) || sample.color == "yellow" && (r < 40000 || g < 40000) {
			t.Fatal("source trim/speed/order rendered wrong frame", sample)
		}

		if sample.time == .25 {
			rr, _, bb, _ := frame.At(120, 10).RGBA()
			if rr < 20000 || rr > 45000 || bb < 20000 || bb > 45000 {
				t.Fatal("inset position/opacity ignored", rr, bb)
			}
			white := 0
			for y := 110; y < 170; y++ {
				for x := 20; x < 240; x++ {
					r, g, b, _ := frame.At(x, y).RGBA()
					if r > 50000 && g > 50000 && b > 50000 {
						white++
					}
				}
			}
			if white < 20 {
				t.Fatal("literal text missing")
			}
		}
	}
	cmd = exec.Command("ffmpeg", "-v", "error", "-ss", "0.7", "-i", final, "-t", "0.1", "-f", "s16le", "-ac", "1", "pipe:1")
	pcm, e := cmd.Output()
	if e != nil || len(pcm) == 0 {
		t.Fatal("missing mixed audio", e)
	}
	var peak int
	for i := 0; i+1 < len(pcm); i += 2 {
		value := int(int16(binary.LittleEndian.Uint16(pcm[i:])))
		if value < 0 {
			value = -value
		}
		if value > peak {
			peak = value
		}
	}
	if peak < 100 {
		t.Fatal("audio track silent", peak, len(pcm))
	}
	cmd = exec.Command("ffmpeg", "-v", "error", "-ss", "0.1", "-i", final, "-t", "0.1", "-f", "s16le", "-ac", "1", "pipe:1")
	silence, e := cmd.Output()
	if e != nil {
		t.Fatal(e)
	}
	for i := 0; i+1 < len(silence); i += 2 {
		value := int(int16(binary.LittleEndian.Uint16(silence[i:])))
		if value > 10 || value < -10 {
			t.Fatal("audio placement ignored")
		}
	}
	if export := os.Getenv("VIDEO_EDIT_TEST_EXPORT"); export != "" {
		data, e := os.ReadFile(final)
		if e != nil {
			t.Fatal(e)
		}
		if e = os.WriteFile(export, data, 0600); e != nil {
			t.Fatal(e)
		}
	}
	w = call(a, "GET", "/api/projects/"+p.ID+"/jobs/"+view.ID, nil)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "edit-ir.json") {
		t.Fatal("missing editing artifact", w.Body.String())
	}
	// The completed snapshot remains independent of later edits.
	d.Clips[0].Duration = .9
	p, _ = a.get(p.ID)
	w = call(a, "PUT", "/api/projects/"+p.ID+"/edit", EditRequest{Revision: p.Revision, DSL: &d})
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	raw, _ := os.ReadFile(filepath.Join(a.dir(p.ID), "renders", job.ID, "edit-ir.json"))
	var snapshot EditIR
	json.Unmarshal(raw, &snapshot)
	if snapshot.Clips[0].Duration != 1 {
		t.Fatal("mutable render snapshot")
	}
}
func TestEditingMCPCompilesWithoutSaving(t *testing.T) {
	a := testApp(t)
	p, _ := a.create("MCP剪辑")
	d := simpleEdit()
	raw, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": map[string]any{"name": "video_edit_validate", "arguments": map[string]any{"projectId": p.ID, "dsl": d}}})
	var out bytes.Buffer
	runMCP(bytes.NewReader(raw), &out, "http://localhost", a.token, &http.Client{Transport: localMCPTransport{handler: a.routes()}})
	if strings.Contains(out.String(), `"isError":true`) || !strings.Contains(out.String(), "video.edit-ir.v1") {
		t.Fatal(out.String())
	}
	p, _ = a.get(p.ID)
	if p.EditDSL != nil {
		t.Fatal("validation saved document")
	}
}
