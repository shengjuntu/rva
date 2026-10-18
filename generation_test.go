package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
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
	if g := q.Assets[0].Generated; g == nil || g.JobID != q.Generation[0].ID || g.Prompt != "test scene" || g.Provider != "http_binary_v1" || g.Model != "fixture" {
		t.Fatalf("missing generation provenance: %+v", g)
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

func TestGenerationVideoOptionsAndReferenceSnapshot(t *testing.T) {
	a := testApp(t)
	p, _ := a.create("reference")
	var data bytes.Buffer
	if err := png.Encode(&data, image.NewRGBA(image.Rect(0, 0, 12, 10))); err != nil {
		t.Fatal(err)
	}
	asset := Asset{ID: uid(), Name: "reference.png", MIME: "image/png", Size: int64(data.Len())}
	asset.File = asset.ID + ".png"
	if err := os.WriteFile(filepath.Join(a.dir(p.ID), "workspace", "assets", asset.File), data.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	a.mu.Lock()
	a.projects[p.ID].Assets = append(a.projects[p.ID].Assets, asset)
	a.mu.Unlock()
	video, err := os.ReadFile("examples/edit-preview.mp4")
	if err != nil {
		t.Fatal(err)
	}
	var posts atomic.Int32
	requests := make(chan map[string]any, 1)
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		posts.Add(1)
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Error(err)
		}
		requests <- payload
		w.Write(video)
	}))
	defer s.Close()
	t.Setenv("VIDEO_VIDEO_GEN_URL", s.URL)
	t.Setenv("VIDEO_VIDEO_GEN_MODEL", "fixture-i2v")
	duration := 6.0
	in := JobStartRequest{Type: "video_generation", Revision: p.Revision, RequestID: uid(), Options: JobOptions{Prompt: "animate reference", Duration: &duration, AspectRatio: "9:16", ReferenceAssetIDs: []string{asset.ID}}}
	w := call(a, "POST", "/api/projects/"+p.ID+"/jobs", in)
	if w.Code != 202 {
		t.Fatal(w.Code, w.Body.String())
	}
	payload := <-requests
	if payload["duration"] != 6.0 || payload["aspectRatio"] != "9:16" || payload["model"] != "fixture-i2v" {
		t.Fatal(payload)
	}
	refs, ok := payload["referenceImages"].([]any)
	if !ok || len(refs) != 1 {
		t.Fatal(payload)
	}
	ref := refs[0].(map[string]any)
	decoded, err := base64.StdEncoding.DecodeString(ref["data"].(string))
	if err != nil || !bytes.Equal(decoded, data.Bytes()) || ref["sha256"] != hashBytes(data.Bytes()) || ref["assetId"] != asset.ID || ref["mimeType"] != "image/png" {
		t.Fatal(ref, err)
	}
	if _, exists := ref["file"]; exists {
		t.Fatal("provider receives private filesystem path")
	}
	a.wg.Wait()
	q, _ := a.get(p.ID)
	if len(q.Assets) != 2 || q.Generation[0].State != "completed" {
		t.Fatal(q.Generation)
	}
	g := q.Assets[1].Generated
	if g == nil || g.Duration == nil || *g.Duration != 6 || g.AspectRatio != "9:16" || len(g.References) != 1 || g.References[0].SHA256 != hashBytes(data.Bytes()) {
		t.Fatalf("bad generation metadata: %+v", g)
	}
	raw, _ := json.Marshal(q)
	if bytes.Contains(raw, []byte(base64.StdEncoding.EncodeToString(data.Bytes()))) || strings.Contains(string(raw), s.URL) {
		t.Fatal("project persisted reference payload or provider URL")
	}
	w = call(a, "POST", "/api/projects/"+p.ID+"/jobs", in)
	if w.Code != 200 || posts.Load() != 1 {
		t.Fatal(w.Code, posts.Load())
	}
	in.Options.AspectRatio = "16:9"
	w = call(a, "POST", "/api/projects/"+p.ID+"/jobs", in)
	if w.Code != 409 {
		t.Fatal("changed retry accepted", w.Code)
	}
}

func TestGenerationOptionAndReferenceValidation(t *testing.T) {
	a := testApp(t)
	p, _ := a.create("validation")
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { t.Error("invalid generation reached provider") }))
	defer s.Close()
	t.Setenv("VIDEO_IMAGE_GEN_URL", s.URL)
	t.Setenv("VIDEO_IMAGE_GEN_MODEL", "fixture")
	t.Setenv("VIDEO_VIDEO_GEN_URL", s.URL)
	t.Setenv("VIDEO_VIDEO_GEN_MODEL", "fixture")
	zero, over := 0.0, 61.0
	tests := []struct {
		name, kind string
		options    JobOptions
	}{
		{"image duration", "image_generation", JobOptions{Prompt: "x", Duration: &over}},
		{"zero duration", "video_generation", JobOptions{Prompt: "x", Duration: &zero}},
		{"long duration", "video_generation", JobOptions{Prompt: "x", Duration: &over}},
		{"aspect", "video_generation", JobOptions{Prompt: "x", AspectRatio: "8:8"}},
		{"missing ref", "video_generation", JobOptions{Prompt: "x", ReferenceAssetIDs: []string{uid()}}},
		{"bad ref", "image_generation", JobOptions{Prompt: "x", ReferenceAssetIDs: []string{"../escape"}}},
		{"too many refs", "video_generation", JobOptions{Prompt: "x", ReferenceAssetIDs: []string{uid(), uid(), uid(), uid(), uid()}}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			w := call(a, "POST", "/api/projects/"+p.ID+"/jobs", JobStartRequest{Type: tc.kind, Revision: p.Revision, RequestID: uid(), Options: tc.options})
			if w.Code != 400 {
				t.Fatal(w.Code, w.Body.String())
			}
		})
	}
	q, _ := a.get(p.ID)
	if len(q.Generation) != 0 {
		t.Fatal("invalid requests persisted jobs")
	}
	asset := Asset{ID: uid(), MIME: "image/png", File: "outside.png"}
	out := filepath.Join(t.TempDir(), "outside.png")
	if err := os.WriteFile(out, []byte("private outside file"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(out, filepath.Join(a.dir(p.ID), "workspace", "assets", asset.File)); err != nil {
		t.Skip("symlinks unavailable")
	}
	q.Assets = append(q.Assets, asset)
	if _, err := a.generationReferences(q, []string{asset.ID}); err == nil {
		t.Fatal("reference symlink escaped asset root")
	}
}

func TestGenerationOnlyOptionsRejectedByOtherJobs(t *testing.T) {
	a := testApp(t)
	p, _ := a.create("option scopes")
	duration := 5.0
	for _, kind := range []string{"tts", "render"} {
		for _, option := range []JobOptions{{Duration: &duration}, {AspectRatio: "16:9"}, {ReferenceAssetIDs: []string{uid()}}} {
			w := call(a, "POST", "/api/projects/"+p.ID+"/jobs", JobStartRequest{Type: kind, Revision: p.Revision, RequestID: uid(), Options: option})
			if w.Code != 400 {
				t.Fatal(kind, w.Code, w.Body.String())
			}
		}
	}
}

func TestGeneratedVideoRejectsPlaylistAndWrongContainer(t *testing.T) {
	a := testApp(t)
	p, _ := a.create("generated container")
	for _, data := range [][]byte{
		[]byte("ffconcat version 1.0\nfile '/outside/private.mp4'\n"),
		append([]byte{0, 0, 0, 12, 'f', 't', 'y', 'p', 'i', 's', 'o', 'm'}, []byte("ffconcat version 1.0\nfile '/outside/private.mp4'\n")...),
	} {
		if _, _, err := validateGenerated(context.Background(), a, p.ID, GenerationJob{Type: "video_generation"}, data); err == nil {
			t.Fatal("generated playlist passed MP4 validation")
		}
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
