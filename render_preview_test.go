package main

import (
	"bytes"
	"image/png"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func inspectRenderRequest(a *App, pid, rid, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest("POST", "/inspect", strings.NewReader(body))
	r.SetPathValue("id", pid)
	r.SetPathValue("rid", rid)
	w := httptest.NewRecorder()
	a.renderInspect(w, r)
	return w
}

func TestRenderInspectValidatesOwnershipStateAndTime(t *testing.T) {
	a := testApp(t)
	p, err := a.create("检查产物")
	if err != nil {
		t.Fatal(err)
	}
	other, err := a.create("另一个项目")
	if err != nil {
		t.Fatal(err)
	}
	completed, running, foreign := uid(), uid(), uid()
	a.mu.Lock()
	a.projects[p.ID].Renders = []Render{{ID: completed, State: "completed", Duration: 2}, {ID: running, State: "running", Duration: 2}}
	a.projects[other.ID].Renders = []Render{{ID: foreign, State: "completed", Duration: 2}}
	a.mu.Unlock()
	for _, test := range []struct {
		name, pid, rid, body string
		status               int
	}{
		{"foreign render", p.ID, foreign, `{"time":0}`, 404},
		{"unknown project", uid(), completed, `{"time":0}`, 404},
		{"unknown render", p.ID, uid(), `{"time":0}`, 404},
		{"running render", p.ID, running, `{"time":0}`, 409},
		{"missing time", p.ID, completed, `{}`, 422},
		{"null time", p.ID, completed, `{"time":null}`, 422},
		{"negative time", p.ID, completed, `{"time":-0.01}`, 422},
		{"at end", p.ID, completed, `{"time":2}`, 422},
		{"beyond end", p.ID, completed, `{"time":2.01}`, 422},
		{"non-finite json", p.ID, completed, `{"time":NaN}`, 400},
		{"non-number", p.ID, completed, `{"time":"0"}`, 400},
		{"arbitrary path", p.ID, completed, `{"time":0,"path":"/tmp/final.mp4"}`, 400},
		{"trailing json", p.ID, completed, `{"time":0} {}`, 400},
		{"missing output", p.ID, completed, `{"time":0}`, 409},
	} {
		t.Run(test.name, func(t *testing.T) {
			w := inspectRenderRequest(a, test.pid, test.rid, test.body)
			if w.Code != test.status || w.Header().Get("Content-Type") == "image/png" {
				t.Fatalf("status %d, wanted %d: %s", w.Code, test.status, w.Body.String())
			}
		})
	}
}

func TestRenderInspectReturnsActualTimeBoundedFrame(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg unavailable")
	}
	a := testApp(t)
	p, err := a.create("真实成片检查")
	if err != nil {
		t.Fatal(err)
	}
	rid := uid()
	dir := filepath.Join(a.dir(p.ID), "renders", rid)
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("ffmpeg", "-v", "error", "-f", "lavfi", "-i", "color=c=red:s=1280x720:r=10:d=2,drawbox=x=0:y=0:w=iw:h=ih:color=blue:t=fill:enable='gte(t,1)'", "-c:v", "libx264", "-threads", "1", "-pix_fmt", "yuv420p", "-y", filepath.Join(dir, "final.mp4"))
	if raw, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("fixture: %v %s", err, raw)
	}
	a.mu.Lock()
	// A partial render's timestamp is local to the output, independent of the
	// original timeline offset.
	a.projects[p.ID].Renders = []Render{{ID: rid, State: "completed", Duration: 2, Range: &RenderRange{Start: 10, End: 12}}}
	a.mu.Unlock()
	for _, sample := range []struct {
		body string
		red  bool
	}{{`{"time":0.25}`, true}, {`{"time":1.25}`, false}} {
		w := inspectRenderRequest(a, p.ID, rid, sample.body)
		if w.Code != 200 || w.Header().Get("Content-Type") != "image/png" || w.Header().Get("X-Inspection-Limit") != "single_frame" {
			t.Fatalf("invalid frame response: %d %s", w.Code, w.Body.String())
		}
		frame, err := png.Decode(bytes.NewReader(w.Body.Bytes()))
		if err != nil {
			t.Fatal(err)
		}
		if frame.Bounds().Dx() != 960 || frame.Bounds().Dy() != 540 || w.Body.Len() > 4<<20 {
			t.Fatal("frame size is not bounded", frame.Bounds(), w.Body.Len())
		}
		r, _, b, _ := frame.At(100, 100).RGBA()
		if sample.red && (r < 50000 || b > 10000) || !sample.red && (b < 50000 || r > 10000) {
			t.Fatal("frame did not come from requested output time", sample.body, r, b)
		}
	}
}

func TestRenderInspectRejectsEscapingOutputSymlink(t *testing.T) {
	a := testApp(t)
	p, _ := a.create("路径检查")
	rid := uid()
	dir := filepath.Join(a.dir(p.ID), "renders", rid)
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "outside.mp4")
	if err := os.WriteFile(outside, []byte("outside project root"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(dir, "final.mp4")); err != nil {
		t.Skip("symlinks unavailable", err)
	}
	a.mu.Lock()
	a.projects[p.ID].Renders = []Render{{ID: rid, State: "completed", Duration: 2}}
	a.mu.Unlock()
	w := inspectRenderRequest(a, p.ID, rid, `{"time":0}`)
	if w.Code != 409 || !strings.Contains(w.Body.String(), "无法读取渲染产物") {
		t.Fatal("escaped output was not rejected before FFmpeg", w.Code, w.Body.String())
	}
}

func TestRenderInspectionOutputIsBounded(t *testing.T) {
	var out renderInspectionOutput
	if n, err := out.Write(make([]byte, 4<<20)); err != nil || n != 4<<20 {
		t.Fatal(n, err)
	}
	if n, err := out.Write([]byte{1}); err == nil || n != 0 || len(out.Bytes()) != 4<<20 {
		t.Fatal("output limit was not enforced", n, err)
	}
}
