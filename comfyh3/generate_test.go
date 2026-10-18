package comfyh3

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func fixtureObjectInfo(t *testing.T) ObjectInfo {
	t.Helper()
	ms := DefaultModelSet()
	raw := map[string]any{}
	for _, class := range []string{"UNETLoader", "CLIPLoader", "VAELoader", "LoraLoader", "MiniMaxH3ImageToVideo", "KSampler", "VAEDecode", "VAEDecodeAudio", "VHS_VideoCombine"} {
		raw[class] = map[string]any{"input": map[string]any{"required": map[string]any{}}}
	}
	set := func(class, input string, choices ...string) {
		raw[class].(map[string]any)["input"].(map[string]any)["required"].(map[string]any)[input] = []any{choices}
	}
	set("UNETLoader", "unet_name", ms.Diffusion)
	set("CLIPLoader", "clip_name", ms.Clip)
	set("CLIPLoader", "type", ms.ClipType)
	set("VAELoader", "vae_name", ms.VideoVAE, ms.AudioVAE)
	set("LoraLoader", "lora_name", ms.Lora)
	encoded, err := json.Marshal(raw)
	if err != nil {
		t.Fatal(err)
	}
	var oi ObjectInfo
	if err := json.Unmarshal(encoded, &oi); err != nil {
		t.Fatal(err)
	}
	return oi
}
func fixtureParams() GenerateParams {
	return GenerateParams{Prompt: "A camera pans across a newsroom", Width: 768, Height: 432, Length: 124, Seed: 42, Steps: 4, Cfg: 4.5, Prefix: "h3_owned", Audio: true}
}

func TestValidateEnvironmentChecksVerifiedNodesAndModelEnums(t *testing.T) {
	ms := DefaultModelSet()
	if err := ValidateEnvironment(fixtureObjectInfo(t), ms, fixtureParams()); err != nil {
		t.Fatal(err)
	}
	for _, missing := range []string{"LoraLoader", "VAEDecodeAudio", "MiniMaxH3ImageToVideo", "VHS_VideoCombine"} {
		oi := fixtureObjectInfo(t)
		delete(oi, missing)
		if err := ValidateEnvironment(oi, ms, fixtureParams()); err == nil {
			t.Fatal("missing graph node accepted", missing)
		}
	}
	bad := ms
	bad.Clip = "private/unavailable-model.safetensors"
	if err := ValidateEnvironment(fixtureObjectInfo(t), bad, fixtureParams()); err == nil || strings.Contains(err.Error(), "private") {
		t.Fatal("model enum not checked or filename leaked", err)
	}
	oi := fixtureObjectInfo(t)
	node := oi["UNETLoader"]
	node.Input.Required = map[string]any{}
	oi["UNETLoader"] = node
	if err := ValidateEnvironment(oi, ms, fixtureParams()); err == nil {
		t.Fatal("incomplete model schema accepted")
	}
}

func TestGeneratePreservesVerifiedWorkflowAndDownloadsBinary(t *testing.T) {
	oi := fixtureObjectInfo(t)
	mp4 := []byte{0, 0, 0, 16, 'f', 't', 'y', 'p', 'i', 's', 'o', 'm', 0, 0, 0, 0}
	var submits, views atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/system_stats":
			json.NewEncoder(w).Encode(map[string]any{"system": map[string]string{"comfyui_version": "fixture"}})
		case "/object_info":
			json.NewEncoder(w).Encode(oi)
		case "/prompt":
			submits.Add(1)
			var request struct {
				Prompt map[string]struct {
					Class  string         `json:"class_type"`
					Inputs map[string]any `json:"inputs"`
				} `json:"prompt"`
			}
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Error(err)
			}
			byClass := map[string]map[string]any{}
			for _, node := range request.Prompt {
				byClass[node.Class] = node.Inputs
			}
			ms := DefaultModelSet()
			if byClass["UNETLoader"]["unet_name"] != ms.Diffusion || byClass["CLIPLoader"]["clip_name"] != ms.Clip || byClass["LoraLoader"]["lora_name"] != ms.Lora || byClass["MiniMaxH3ImageToVideo"]["length"] != 124.0 || byClass["VHS_VideoCombine"]["frame_rate"] != 24.0 || byClass["VHS_VideoCombine"]["format"] != "video/h264-mp4" || byClass["VHS_VideoCombine"]["audio"] == nil {
				t.Error("verified workflow changed", byClass)
			}
			w.Write([]byte(`{"prompt_id":"owned","node_errors":{}}`))
		case "/history/owned":
			w.Write([]byte(`{"owned":{"status":{"completed":true,"status_str":"success"},"outputs":{"10":{"gifs":[{"filename":"final.MP4","subfolder":"videos","type":"output"}]}}}}`))
		case "/view":
			views.Add(1)
			w.Write(mp4)
		default:
			t.Error("unexpected path", r.URL.Path)
			w.WriteHeader(500)
		}
	}))
	defer server.Close()
	c := NewClient(server.URL)
	c.PollEvery = time.Millisecond
	c.PollTimeout = time.Second
	result, err := c.Generate(context.Background(), DefaultModelSet(), fixtureParams())
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(mp4)
	if submits.Load() != 1 || views.Load() != 1 || result.PromptID != "owned" || result.OutputName != "final.MP4" || result.SHA256 != hex.EncodeToString(sum[:]) || result.Size != int64(len(mp4)) {
		t.Fatal(result, submits.Load(), views.Load())
	}
}

func TestGenerateCancellationDeletesOnlyOwnedPendingPrompt(t *testing.T) {
	oi := fixtureObjectInfo(t)
	started := make(chan struct{}, 1)
	var deletes atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/system_stats":
			w.Write([]byte(`{"system":{"comfyui_version":"fixture"}}`))
		case "/object_info":
			json.NewEncoder(w).Encode(oi)
		case "/prompt":
			w.Write([]byte(`{"prompt_id":"owned"}`))
		case "/history/owned":
			select {
			case started <- struct{}{}:
			default:
			}
			w.Write([]byte(`{}`))
		case "/queue":
			var payload struct {
				Delete []string `json:"delete"`
			}
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Error(err)
			}
			if len(payload.Delete) != 1 || payload.Delete[0] != "owned" {
				t.Error("cancellation not scoped", payload)
			}
			deletes.Add(1)
			w.Write([]byte(`{}`))
		default:
			t.Error("unexpected/global operation", r.URL.Path)
			w.WriteHeader(500)
		}
	}))
	defer server.Close()
	c := NewClient(server.URL)
	c.PollEvery = time.Second
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := c.Generate(ctx, DefaultModelSet(), fixtureParams()); done <- err }()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("generation never reached polling")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("cancellation did not return")
	}
	if deletes.Load() != 1 {
		t.Fatal("owned pending prompt was not deleted", deletes.Load())
	}
}
