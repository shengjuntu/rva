package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestMCPVisualEvidenceIsRealImage(t *testing.T) {
	if _, e := exec.LookPath("ffmpeg"); e != nil {
		t.Skip("ffmpeg unavailable")
	}
	a := testApp(t)
	p, _ := a.create("素材")
	asset := Asset{ID: uid(), Name: "红色图片.png", MIME: "image/png"}
	asset.File = asset.ID + ".png"
	img := image.NewRGBA(image.Rect(0, 0, 16, 16))
	for y := 0; y < 16; y++ {
		for x := 0; x < 16; x++ {
			img.Set(x, y, color.RGBA{R: 255, A: 255})
		}
	}
	f, e := os.Create(filepath.Join(a.dir(p.ID), "workspace", "assets", asset.File))
	if e != nil {
		t.Fatal(e)
	}
	if e = png.Encode(f, img); e != nil {
		t.Fatal(e)
	}
	f.Close()
	a.mu.Lock()
	a.projects[p.ID].Assets = append(a.projects[p.ID].Assets, asset)
	a.mu.Unlock()
	var output bytes.Buffer
	request, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": map[string]any{"name": "video_asset_inspect", "arguments": map[string]any{"projectId": p.ID, "assetId": asset.ID}}})
	runMCP(bytes.NewReader(request), &output, "http://localhost", a.token, &http.Client{Transport: localMCPTransport{handler: a.routes()}})
	var result struct {
		Result struct {
			IsError bool `json:"isError"`
			Content []struct {
				Type string `json:"type"`
				Data string `json:"data"`
			} `json:"content"`
		} `json:"result"`
	}
	if e = json.Unmarshal(output.Bytes(), &result); e != nil {
		t.Fatal(e)
	}
	if result.Result.IsError || len(result.Result.Content) != 2 || result.Result.Content[1].Type != "image" || result.Result.Content[1].Data == "" {
		t.Fatal(output.String())
	}
}

func TestAssetVideoInspectActualTimestampedFrames(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg unavailable")
	}
	a := testApp(t)
	p, _ := a.create("video inspection")
	asset := Asset{ID: uid(), Name: "red then blue.mp4", MIME: "video/mp4"}
	asset.File = asset.ID + ".mp4"
	video := filepath.Join(a.dir(p.ID), "workspace", "assets", asset.File)
	cmd := exec.Command("ffmpeg", "-nostdin", "-v", "error", "-f", "lavfi", "-i", "color=red:s=64x64:r=10:d=1", "-f", "lavfi", "-i", "color=blue:s=64x64:r=10:d=1", "-filter_complex", "[0:v][1:v]concat=n=2:v=1:a=0[v]", "-map", "[v]", "-c:v", "libx264", "-threads", "1", "-pix_fmt", "yuv420p", video)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatal(err, string(out))
	}
	a.mu.Lock()
	a.projects[p.ID].Assets = append(a.projects[p.ID].Assets, asset)
	a.mu.Unlock()
	inspect := func(body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", "/", strings.NewReader(body))
		r.SetPathValue("id", p.ID)
		r.SetPathValue("aid", asset.ID)
		w := httptest.NewRecorder()
		a.assetVideoInspect(w, r)
		return w
	}
	w := inspect(`{"times":[0,1.25]}`)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var result struct {
		Duration        float64                             `json:"duration"`
		SampleCount     int                                 `json:"sampleCount"`
		InspectionLimit string                              `json:"inspectionLimit"`
		Content         []struct{ Type, Text, Data string } `json:"content"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Duration < 1.9 || result.SampleCount != 2 || result.InspectionLimit != "sampled_frames_no_audio" || len(result.Content) != 5 || !strings.Contains(result.Content[3].Text, "1.250s") {
		t.Fatal(w.Body.String())
	}
	for i, at := range []int{2, 4} {
		data, err := base64.StdEncoding.DecodeString(result.Content[at].Data)
		if err != nil || result.Content[at].Type != "image" {
			t.Fatal(err)
		}
		img, err := png.Decode(bytes.NewReader(data))
		if err != nil {
			t.Fatal(err)
		}
		r, _, b, _ := img.At(10, 10).RGBA()
		if (i == 0 && r < b) || (i == 1 && b < r) {
			t.Fatal("timestamps did not select actual distinct video frames")
		}
	}
	for _, body := range []string{`{}`, `{"times":[]}`, `{"times":[null]}`, `{"times":[-1]}`, `{"times":[0,0]}`, `{"times":[1,0]}`, `{"times":[2]}`, `{"times":[0,0.1,0.2,0.3,0.4,0.5,0.6,0.7,0.8]}`, `{"times":[0],"extra":true}`} {
		w := inspect(body)
		if w.Code < 400 {
			t.Fatal("invalid inspection accepted", body, w.Body.String())
		}
	}
}

func TestAssetVideoInspectionRootBoundary(t *testing.T) {
	a := testApp(t)
	p, _ := a.create("root")
	out := filepath.Join(t.TempDir(), "private.mp4")
	if err := os.WriteFile(out, []byte("private"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(out, filepath.Join(a.dir(p.ID), "workspace", "assets", "outside.mp4")); err != nil {
		t.Skip("symlinks unavailable")
	}
	if name, err := a.assetInspectionCopy(p.ID, "outside.mp4"); err == nil {
		os.Remove(name)
		t.Fatal("inspection followed outside symlink")
	}
}

func TestAssetInspectionRejectsPlaylistBeforeExternalProcess(t *testing.T) {
	a := testApp(t)
	p, _ := a.create("playlist boundary")
	private := filepath.Join(t.TempDir(), "outside.mp4")
	if err := os.WriteFile(private, []byte("outside scope"), 0600); err != nil {
		t.Fatal(err)
	}
	playlist := []byte("ffconcat version 1.0\nfile '" + private + "'\n")
	asset := Asset{ID: uid(), MIME: "video/mp4", Name: "disguised.mp4", Size: int64(len(playlist))}
	asset.File = asset.ID + ".mp4"
	if err := os.WriteFile(filepath.Join(a.dir(p.ID), "workspace", "assets", asset.File), playlist, 0600); err != nil {
		t.Fatal(err)
	}
	a.mu.Lock()
	a.projects[p.ID].Assets = append(a.projects[p.ID].Assets, asset)
	a.mu.Unlock()
	// If either tool executes at all, record it. A bad playlist must be rejected
	// by content checks, before FFmpeg can follow anything named by its contents.
	bin := t.TempDir()
	marker := filepath.Join(bin, "executed")
	for _, tool := range []string{"ffmpeg", "ffprobe"} {
		script := "#!/bin/sh\nprintf invoked > '" + marker + "'\nexit 1\n"
		if err := os.WriteFile(filepath.Join(bin, tool), []byte(script), 0700); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", bin)
	for _, multi := range []bool{false, true} {
		r := httptest.NewRequest("POST", "/", strings.NewReader(`{"times":[0]}`))
		r.SetPathValue("id", p.ID)
		r.SetPathValue("aid", asset.ID)
		w := httptest.NewRecorder()
		if multi {
			a.assetVideoInspect(w, r)
		} else {
			a.assetPreview(w, r)
		}
		if w.Code != 409 || !strings.Contains(w.Body.String(), "实际内容") {
			t.Fatal("playlist was not rejected by initial format check", w.Code, w.Body.String())
		}
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("inspection executed a media parser before rejecting a playlist", err)
	}
}

func TestAssetInspectionForcedFormatsRemainUsable(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg unavailable")
	}
	a := testApp(t)
	p, _ := a.create("supported inspection formats")
	var jpegBytes bytes.Buffer
	if err := jpeg.Encode(&jpegBytes, image.NewRGBA(image.Rect(0, 0, 16, 16)), nil); err != nil {
		t.Fatal(err)
	}
	for _, fixture := range []struct{ mime, ext string }{{"image/jpeg", ".jpg"}, {"image/webp", ".webp"}, {"video/webm", ".webm"}} {
		t.Run(fixture.mime, func(t *testing.T) {
			asset := Asset{ID: uid(), MIME: fixture.mime, Name: "fixture" + fixture.ext}
			asset.File = asset.ID + fixture.ext
			path := filepath.Join(a.dir(p.ID), "workspace", "assets", asset.File)
			if fixture.mime == "image/jpeg" {
				if err := os.WriteFile(path, jpegBytes.Bytes(), 0600); err != nil {
					t.Fatal(err)
				}
			} else {
				args := []string{"-nostdin", "-v", "error", "-f", "lavfi", "-i", "color=green:s=32x32:r=10:d=0.3"}
				if fixture.mime == "image/webp" {
					args = append(args, "-frames:v", "1", "-c:v", "libwebp")
				} else {
					args = append(args, "-c:v", "libvpx-vp9")
				}
				args = append(args, "-threads", "1", path)
				if out, err := exec.Command("ffmpeg", args...).CombinedOutput(); err != nil {
					t.Fatal(err, string(out))
				}
			}
			a.mu.Lock()
			a.projects[p.ID].Assets = append(a.projects[p.ID].Assets, asset)
			a.mu.Unlock()
			r := httptest.NewRequest("GET", "/", nil)
			r.SetPathValue("id", p.ID)
			r.SetPathValue("aid", asset.ID)
			w := httptest.NewRecorder()
			a.assetPreview(w, r)
			if w.Code != 200 {
				t.Fatal("supported format failed preview", w.Code, w.Body.String())
			}
			if fixture.mime == "video/webm" {
				r = httptest.NewRequest("POST", "/", strings.NewReader(`{"times":[0,0.1]}`))
				r.SetPathValue("id", p.ID)
				r.SetPathValue("aid", asset.ID)
				w = httptest.NewRecorder()
				a.assetVideoInspect(w, r)
				if w.Code != 200 {
					t.Fatal("supported WebM failed forced probe", w.Code, w.Body.String())
				}
			}
		})
	}
}
func TestMCPProductionToolsHaveWorkingContracts(t *testing.T) {
	a := testApp(t)
	p, _ := a.create("正文")
	var output bytes.Buffer
	request, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": map[string]any{"name": "video_production_text", "arguments": map[string]any{"projectId": p.ID, "revision": p.Revision, "text": "第一条事实。"}}})
	runMCP(bytes.NewReader(request), &output, "http://localhost", a.token, &http.Client{Transport: localMCPTransport{handler: a.routes()}})
	q, e := a.get(p.ID)
	if e != nil || q.Production == nil || q.Production.Text != "第一条事实。" {
		t.Fatal(output.String(), e)
	}
}
