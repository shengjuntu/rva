package main

import (
	"bytes"
	"context"
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"rundesk-video-app/comfyh3"
)

func TestComfyGenerationTargetValidation(t *testing.T) {
	t.Setenv("VIDEO_VIDEO_GEN_MODEL", "minimax_h3_fl2va")
	t.Setenv("VIDEO_VIDEO_GEN_KEY", "")
	t.Setenv("VIDEO_COMFY_H3_AUDIO_GAIN_DB", "")
	for _, raw := range []string{"comfy://user:secret@127.0.0.1:19897", "comfy://127.0.0.1:19897#x", "comfy://127.0.0.1:19897/path", "comfy://127.0.0.1:19897/", "comfy://127.0.0.1:19897?key=x", "comfy://127.0.0.1:19897?", "comfy:///no-host", "comfy://:19897", "comfy://127.0.0.1:abc"} {
		t.Run(raw, func(t *testing.T) {
			t.Setenv("VIDEO_VIDEO_GEN_URL", raw)
			if _, err := generationTarget("video_generation"); err == nil {
				t.Fatal("unsafe or discarded URL components accepted")
			}
		})
	}
	t.Setenv("VIDEO_VIDEO_GEN_URL", "comfy://127.0.0.1:19897")
	target, err := generationTarget("video_generation")
	if err != nil || target.AudioGainDB != 18 || generationProvider(target) != "comfyui_h3" {
		t.Fatal(target, err)
	}
	for _, raw := range []string{"NaN", "Inf", "31", "-31", "x"} {
		t.Setenv("VIDEO_COMFY_H3_AUDIO_GAIN_DB", raw)
		if _, err := generationTarget("video_generation"); err == nil {
			t.Fatal("invalid gain accepted", raw)
		}
	}
	t.Setenv("VIDEO_COMFY_H3_AUDIO_GAIN_DB", "0")
	target, err = generationTarget("video_generation")
	if err != nil || target.AudioGainDB != 0 {
		t.Fatal(target, err)
	}
	t.Setenv("VIDEO_VIDEO_GEN_MODEL", "unrelated-model")
	if _, err := generationTarget("video_generation"); err == nil {
		t.Fatal("unrelated model name selected the fixed H3 workflow")
	}
	t.Setenv("VIDEO_VIDEO_GEN_MODEL", comfyh3.DefaultModelSet().Diffusion)
	if _, err := generationTarget("video_generation"); err != nil {
		t.Fatal("actual diffusion filename alias rejected", err)
	}
	t.Setenv("VIDEO_IMAGE_GEN_URL", "comfy://127.0.0.1:19897")
	t.Setenv("VIDEO_IMAGE_GEN_MODEL", "minimax_h3_fl2va")
	if _, err := generationTarget("image_generation"); err == nil {
		t.Fatal("video-only workflow accepted image generation")
	}
	// Existing HTTP adapter URLs may have an endpoint path/query.
	t.Setenv("VIDEO_VIDEO_GEN_URL", "https://example.test/generate?version=1")
	t.Setenv("VIDEO_VIDEO_GEN_MODEL", "provider-model")
	if target, err := generationTarget("video_generation"); err != nil || generationProvider(target) != "http_binary_v1" {
		t.Fatal(target, err)
	}
}

func TestComfyEffectiveParametersAreExplicitAndDeterministic(t *testing.T) {
	for _, tc := range []struct {
		target   float64
		frames   int
		adjusted bool
	}{{1, 124, true}, {5, 124, true}, {6, 144, false}, {60, 362, true}} {
		j := GenerationJob{RequestID: "123456789012345678901234", Duration: &tc.target, AspectRatio: "9:16"}
		p, effective, warnings := comfyGenerationParams(j, 18)
		if p.Length != tc.frames || effective.FrameCount != tc.frames || effective.DurationSeconds != float64(tc.frames)/24 || effective.Width != 432 || effective.Height != 768 || effective.AudioGainDB != 18 || !effective.NativeAudio || (len(warnings) > 0) != tc.adjusted {
			t.Fatal(p, effective, warnings)
		}
		again, _, _ := comfyGenerationParams(j, 18)
		if again.Seed != p.Seed || p.Prefix != "h3_"+j.RequestID {
			t.Fatal("request seed/prefix are not stable or fully scoped")
		}
	}
}

func TestComfyReferencesRejectedBeforeJobOrProvider(t *testing.T) {
	var requests atomic.Int32
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { requests.Add(1) }))
	defer s.Close()
	t.Setenv("VIDEO_VIDEO_GEN_URL", "comfy://"+strings.TrimPrefix(s.URL, "http://"))
	t.Setenv("VIDEO_VIDEO_GEN_MODEL", "minimax_h3_fl2va")
	t.Setenv("VIDEO_COMFY_H3_AUDIO_GAIN_DB", "0")
	a := testApp(t)
	p, _ := a.create("unsupported reference")
	w := call(a, "POST", "/api/projects/"+p.ID+"/jobs", JobStartRequest{Type: "video_generation", Revision: p.Revision, RequestID: uid(), Options: JobOptions{Prompt: "scene", ReferenceAssetIDs: []string{uid()}}})
	q, _ := a.get(p.ID)
	if w.Code != 400 || len(q.Generation) != 0 || requests.Load() != 0 || !strings.Contains(w.Body.String(), "文生视频") {
		t.Fatal(w.Code, w.Body.String(), q.Generation, requests.Load())
	}
}

func comfyFixtureObjectInfo() comfyh3.ObjectInfo {
	var oi comfyh3.ObjectInfo
	json.Unmarshal([]byte(`{
		"UNETLoader":{"input":{"required":{"unet_name":[["minimax_h3_fl2va_pruned_bf16.safetensors"]]}}},
		"CLIPLoader":{"input":{"required":{"clip_name":[["qwen3vl_32b_minimax_h3_int8_convrot.safetensors"]],"type":[["minimax"]]}}},
		"VAELoader":{"input":{"required":{"vae_name":[["minimax_h3_video_vae_fp16.safetensors","minimax_h3_audio_vae_fp32.safetensors"]]}}},
		"LoraLoader":{"input":{"required":{"lora_name":[["minimax_h3_fl2v_turbo_4step_v1.2_768p_comfyui_bf16.safetensors"]]}}},
		"MiniMaxH3ImageToVideo":{"input":{"required":{}}},
		"KSampler":{"input":{"required":{}}},"VAEDecode":{"input":{"required":{}}},
		"VAEDecodeAudio":{"input":{"required":{}}},"VHS_VideoCombine":{"input":{"required":{}}}
	}`), &oi)
	return oi
}

func TestComfyGenerationReceiptAuthAndAssetPipeline(t *testing.T) {
	video, err := os.ReadFile("examples/edit-preview.mp4")
	if err != nil {
		t.Fatal(err)
	}
	var submits atomic.Int32
	var posted map[string]any
	const key = "fixture-auth-key"
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+key {
			t.Error("provider key was not sent", r.URL.Path)
		}
		switch r.URL.Path {
		case "/system_stats":
			w.Write([]byte(`{"system":{"comfyui_version":"fixture"}}`))
		case "/object_info":
			json.NewEncoder(w).Encode(comfyFixtureObjectInfo())
		case "/prompt":
			submits.Add(1)
			json.NewDecoder(r.Body).Decode(&posted)
			w.Write([]byte(`{"prompt_id":"fixture-prompt"}`))
		case "/history/fixture-prompt":
			w.Write([]byte(`{"fixture-prompt":{"status":{"completed":true,"status_str":"success"},"outputs":{"11":{"gifs":[{"filename":"fixture.mp4","subfolder":"","type":"output"}]}}}}`))
		case "/view":
			w.Write(video)
		default:
			t.Error("unexpected Comfy request", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer s.Close()
	t.Setenv("VIDEO_VIDEO_GEN_URL", "comfy://"+strings.TrimPrefix(s.URL, "http://"))
	t.Setenv("VIDEO_VIDEO_GEN_MODEL", "minimax_h3_fl2va")
	t.Setenv("VIDEO_VIDEO_GEN_KEY", key)
	t.Setenv("VIDEO_COMFY_H3_AUDIO_GAIN_DB", "0")
	a := testApp(t)
	p, _ := a.create("comfy fixture")
	duration := 5.0
	in := JobStartRequest{Type: "video_generation", Revision: p.Revision, RequestID: uid(), Options: JobOptions{Prompt: "scene", Duration: &duration, AspectRatio: "9:16"}}
	url := "/api/projects/" + p.ID + "/jobs"
	w := call(a, "POST", url, in)
	if w.Code != 202 {
		t.Fatal(w.Code, w.Body.String())
	}
	var receipt ProductionJob
	json.Unmarshal(w.Body.Bytes(), &receipt)
	if receipt.Provider != "comfyui_h3" || receipt.GenerationParameters == nil || receipt.GenerationParameters.FrameCount != 124 || len(receipt.Warnings) == 0 {
		t.Fatal("receipt hid provider/effective duration", receipt)
	}
	a.wg.Wait()
	q, _ := a.get(p.ID)
	if len(q.Assets) != 1 || q.Generation[0].State != "completed" || submits.Load() != 1 {
		t.Fatal(q.Generation, q.Assets, submits.Load())
	}
	asset := q.Assets[0]
	if asset.Generated.Provider != "comfyui_h3" || asset.Generated.Parameters.AudioGainDB != 0 || asset.Generated.Parameters.FrameCount != 124 || len(asset.Generated.Warnings) == 0 {
		t.Fatal(asset.Generated)
	}
	actual, err := os.ReadFile(a.dir(p.ID) + "/workspace/assets/" + asset.File)
	if err != nil || !bytes.Equal(actual, video) {
		t.Fatal("zero gain changed original media", err)
	}
	raw, _ := json.Marshal(q)
	if bytes.Contains(raw, []byte(key)) || bytes.Contains(raw, []byte(s.URL)) {
		t.Fatal("persisted provider credentials/address")
	}
	graph := posted["prompt"].(map[string]any)
	foundH3, foundAudio := false, false
	for _, raw := range graph {
		node := raw.(map[string]any)
		if node["class_type"] == "MiniMaxH3ImageToVideo" {
			inputs := node["inputs"].(map[string]any)
			foundH3 = inputs["length"] == float64(124) && inputs["width"] == float64(432) && inputs["height"] == float64(768)
		}
		if node["class_type"] == "VAEDecodeAudio" {
			foundAudio = true
		}
	}
	if !foundH3 || !foundAudio {
		t.Fatal("verified H3/native audio workflow changed", graph)
	}
	// Same receipt must remain idempotent across a process reload and config change.
	b, err := newApp(a.data)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("VIDEO_VIDEO_GEN_URL", "")
	w = call(b, "POST", url, in)
	if w.Code != 200 || submits.Load() != 1 {
		t.Fatal(w.Code, w.Body.String(), submits.Load())
	}
	in.Options.Prompt = "changed"
	w = call(b, "POST", url, in)
	if w.Code != 409 {
		t.Fatal("changed request reused old receipt", w.Code)
	}
}

func TestComfyCapabilityProbeIsMetadataOnly(t *testing.T) {
	var reads atomic.Int32
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reads.Add(1)
		if r.Header.Get("Authorization") != "Bearer fixture" {
			t.Error("metadata probe omitted key")
		}
		switch r.URL.Path {
		case "/system_stats":
			w.Write([]byte(`{"system":{"comfyui_version":"fixture"}}`))
		case "/object_info":
			json.NewEncoder(w).Encode(comfyFixtureObjectInfo())
		default:
			t.Error("probe attempted generation", r.URL.Path)
			w.WriteHeader(400)
		}
	}))
	defer s.Close()
	t.Setenv("VIDEO_VIDEO_GEN_URL", "comfy://"+strings.TrimPrefix(s.URL, "http://"))
	t.Setenv("VIDEO_VIDEO_GEN_MODEL", "minimax_h3_fl2va")
	t.Setenv("VIDEO_VIDEO_GEN_KEY", "fixture")
	t.Setenv("VIDEO_COMFY_H3_AUDIO_GAIN_DB", "18")
	a := testApp(t)
	for _, probe := range []bool{false, true} {
		envelope := a.capabilityEnvelope(context.Background(), probe)
		for _, capability := range envelope.Capabilities {
			if capability.ID != "video_generation" {
				continue
			}
			provider := capability.Providers[0]
			if provider.ID != "comfyui_h3" || !provider.Configured || capability.Ready || provider.Ready || capability.Limits["referenceImages"] != 0 {
				t.Fatal(capability)
			}
			want := "configuration_only"
			if probe {
				want = "comfyui_nodes_models"
			}
			if provider.Verified != want || provider.State != "configured" {
				t.Fatal(provider)
			}
		}
		if !probe && reads.Load() != 0 {
			t.Fatal("configuration check called remote service")
		}
	}
	if reads.Load() != 2 {
		t.Fatal("probe did not read both metadata APIs", reads.Load())
	}
}

func TestComfyAudioGainPreservesVideoAndDoesNotFallback(t *testing.T) {
	data, err := os.ReadFile("examples/edit-preview.mp4")
	if err != nil {
		t.Fatal(err)
	}
	unchanged, err := applyComfyAudioGain(context.Background(), data, 0)
	if err != nil || !bytes.Equal(unchanged, data) {
		t.Fatal(err)
	}
	for _, gain := range []float64{math.NaN(), 31, -31} {
		if _, err := applyComfyAudioGain(context.Background(), data, gain); err == nil {
			t.Fatal("bad gain accepted")
		}
	}
	if _, err := applyComfyAudioGain(context.Background(), []byte("playlist"), 18); err == nil {
		t.Fatal("non-MP4 entered audio processing")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := applyComfyAudioGain(ctx, data, 0); err == nil {
		t.Fatal("canceled call returned raw fallback")
	}
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("FFmpeg not installed")
	}
	processed, err := applyComfyAudioGain(context.Background(), data, 18)
	if err != nil {
		t.Fatal(err)
	}
	if len(processed) == 0 || bytes.Equal(processed, data) {
		t.Fatal("audio processing silently returned original")
	}
	// Audio processing must not re-encode or replace the generated video stream.
	videoHash := func(media []byte, name string) []byte {
		t.Helper()
		file := filepath.Join(t.TempDir(), name)
		if err := os.WriteFile(file, media, 0600); err != nil {
			t.Fatal(err)
		}
		cmd := exec.Command("ffmpeg", "-v", "error", "-i", file, "-map", "0:v:0", "-c", "copy", "-f", "hash", "-hash", "sha256", "pipe:1")
		out, err := cmd.Output()
		if err != nil {
			t.Fatal("cannot verify copied video packets", err)
		}
		return out
	}
	if !bytes.Equal(videoHash(data, "original.mp4"), videoHash(processed, "processed.mp4")) {
		t.Fatal("gain processing changed the original encoded video stream")
	}
	a := testApp(t)
	p, _ := a.create("audio result")
	if _, _, err := validateGenerated(context.Background(), a, p.ID, GenerationJob{Type: "video_generation"}, processed); err != nil {
		t.Fatal(err)
	}
	// A missing executable is a failed requested conversion, never success with raw bytes.
	t.Setenv("PATH", t.TempDir())
	if out, err := applyComfyAudioGain(context.Background(), data, 18); err == nil || out != nil {
		t.Fatal("failed conversion fell back to raw media")
	}
}
