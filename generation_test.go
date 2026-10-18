package main

import (
	"bytes"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"
)

func TestGenerationImageReceiptAndImport(t *testing.T) {
	var data bytes.Buffer
	im := image.NewRGBA(image.Rect(0, 0, 16, 16))
	im.Set(1, 1, color.White)
	png.Encode(&data, im)
	testGenerated(t, "image_generation", data.Bytes())
}
func TestGenerationVideoImport(t *testing.T) {
	data, e := os.ReadFile("examples/edit-preview.mp4")
	if e != nil {
		t.Fatal(e)
	}
	testGenerated(t, "video_generation", data)
}
func testGenerated(t *testing.T, kind string, data []byte) {
	t.Helper()
	posts := 0
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		posts++
		if r.Header.Get("Idempotency-Key") == "" {
			t.Error("no request key")
		}
		var request map[string]any
		json.NewDecoder(r.Body).Decode(&request)
		if request["schema"] != "video.generation-request.v1" || request["prompt"] != "test scene" {
			t.Error(request)
		}
		w.Write(data)
	}))
	defer s.Close()
	prefix := "VIDEO_IMAGE_GEN_"
	if kind == "video_generation" {
		prefix = "VIDEO_VIDEO_GEN_"
	}
	t.Setenv(prefix+"URL", s.URL)
	t.Setenv(prefix+"MODEL", "fixture")
	a := testApp(t)
	p, _ := a.create("generate")
	in := JobStartRequest{Type: kind, Revision: p.Revision, RequestID: uid(), Options: JobOptions{Prompt: "test scene"}}
	url := "/api/projects/" + p.ID + "/jobs"
	w := call(a, "POST", url, in)
	if w.Code != 202 {
		t.Fatal(w.Code, w.Body.String())
	}
	var job ProductionJob
	json.Unmarshal(w.Body.Bytes(), &job)
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		q, _ := a.get(p.ID)
		job, _ = findJob(q, job.ID)
		if jobTerminal(job.State) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	a.wg.Wait()
	if job.State != "succeeded" || len(job.Artifacts) != 1 || job.Artifacts[0].SHA256 == "" {
		t.Fatal(job)
	}
	q, _ := a.get(p.ID)
	if len(q.Assets) != 1 || q.Revision != p.Revision+1 {
		t.Fatal(q)
	}
	w = call(a, "POST", url, in)
	if w.Code != 200 || posts != 1 {
		t.Fatal(w.Code, posts)
	}
	in.Options.Prompt = "changed"
	w = call(a, "POST", url, in)
	if w.Code != 409 {
		t.Fatal(w.Code)
	}
	w = call(a, "POST", url+"/"+job.ID+"/cancel", nil)
	if w.Code != 200 {
		t.Fatal(w.Code)
	}
	reload, e := newApp(a.data)
	if e != nil {
		t.Fatal(e)
	}
	q, _ = reload.get(p.ID)
	if q.Generation[0].State != "completed" {
		t.Fatal(q.Generation)
	}
}
func TestGenerationInvalidOutputAndRestart(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("not an image")) }))
	defer s.Close()
	t.Setenv("VIDEO_IMAGE_GEN_URL", s.URL)
	t.Setenv("VIDEO_IMAGE_GEN_MODEL", "fixture")
	a := testApp(t)
	p, _ := a.create("bad")
	w := call(a, "POST", "/api/projects/"+p.ID+"/jobs", JobStartRequest{Type: "image_generation", Revision: p.Revision, RequestID: uid(), Options: JobOptions{Prompt: "x"}})
	if w.Code != 202 {
		t.Fatal(w.Body.String())
	}
	a.wg.Wait()
	q, _ := a.get(p.ID)
	if q.Generation[0].State != "failed" || len(q.Assets) != 0 {
		t.Fatal(q.Generation)
	}
	q.Generation[0].State = "running"
	a.save(&q)
	b, e := newApp(a.data)
	if e != nil {
		t.Fatal(e)
	}
	q, _ = b.get(p.ID)
	if q.Generation[0].State != "interrupted" {
		t.Fatal(q.Generation)
	}
}
func TestGenerationCancel(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	defer s.Close()
	defer close(release)
	t.Setenv("VIDEO_IMAGE_GEN_URL", s.URL)
	t.Setenv("VIDEO_IMAGE_GEN_MODEL", "fixture")
	a := testApp(t)
	p, _ := a.create("cancel")
	url := "/api/projects/" + p.ID + "/jobs"
	w := call(a, "POST", url, JobStartRequest{Type: "image_generation", Revision: p.Revision, RequestID: uid(), Options: JobOptions{Prompt: "x"}})
	var job ProductionJob
	json.Unmarshal(w.Body.Bytes(), &job)
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("not started")
	}
	w = call(a, "POST", url+"/"+job.ID+"/cancel", nil)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	a.wg.Wait()
	q, _ := a.get(p.ID)
	if q.Generation[0].State != "interrupted" || len(q.Assets) != 0 {
		t.Fatal(q.Generation)
	}
}
