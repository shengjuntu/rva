package main

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func indexTestWAV() []byte {
	// Match native IndexTTS's 22.05 kHz; exercise actual resampling, not just
	// acceptance of an already-normalized app-format WAV.
	const samples = 13230
	b := make([]byte, 44+samples*2)
	copy(b, "RIFF")
	binary.LittleEndian.PutUint32(b[4:], uint32(len(b)-8))
	copy(b[8:], "WAVEfmt ")
	binary.LittleEndian.PutUint32(b[16:], 16)
	binary.LittleEndian.PutUint16(b[20:], 1)
	binary.LittleEndian.PutUint16(b[22:], 1)
	binary.LittleEndian.PutUint32(b[24:], 22050)
	binary.LittleEndian.PutUint32(b[28:], 44100)
	binary.LittleEndian.PutUint16(b[32:], 2)
	binary.LittleEndian.PutUint16(b[34:], 16)
	copy(b[36:], "data")
	binary.LittleEndian.PutUint32(b[40:], samples*2)
	for i := 0; i < samples; i++ {
		binary.LittleEndian.PutUint16(b[44+i*2:], uint16(int16(7000*math.Sin(float64(i)*2*math.Pi*440/22050))))
	}
	return b
}
func indexConfig(url string) SpeechConfig {
	return SpeechConfig{Backend: "indextts", URL: url + "/v1/audio/speech", Model: "IndexTeam/IndexTTS-2.5", Voice: "news_zh", Language: "zh", TimeoutSeconds: 10, APIKey: "test-index-secret"}
}

func TestIndexTTSSynthesisAndAdoption(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-index-secret" {
			t.Error("missing configured authentication")
		}
		if r.URL.Path == "/v1/models" {
			writeJSON(w, 200, map[string]any{"data": []map[string]string{{"id": "IndexTeam/IndexTTS-2.5"}}})
			return
		}
		if r.URL.Path != "/v1/audio/speech" || r.Method != "POST" {
			t.Error("incorrect endpoint")
			w.WriteHeader(404)
			return
		}
		var v struct {
			Model, Input, Voice string
			Speed               float64
			Format              string `json:"response_format"`
			Extra               struct {
				Lang      string
				Normalize bool `json:"text_normalization"`
			} `json:"extra_params"`
		}
		if e := json.NewDecoder(r.Body).Decode(&v); e != nil {
			t.Error(e)
		}
		if v.Model != "IndexTeam/IndexTTS-2.5" || v.Voice != "news_zh" || v.Speed != 1.25 || v.Extra.Lang != "zh" || !v.Extra.Normalize || v.Format != "wav" || v.Input == "" {
			t.Errorf("invalid IndexTTS payload: %+v", v)
		}
		requests.Add(1)
		w.Header().Set("Content-Type", "audio/wav")
		w.Write(indexTestWAV())
	}))
	defer server.Close()
	a := testApp(t)
	a.tts = indexConfig(server.URL)
	a.espeak = "/missing/espeak"
	p := speechTestProject(t, a)
	base := "/api/projects/" + p.ID
	cap := call(a, "GET", "/api/speech/capabilities", nil)
	if cap.Code != 200 || !strings.Contains(cap.Body.String(), `"ready":true`) {
		t.Fatal(cap.Body.String())
	}
	in := SpeechRequest{Revision: p.Revision, RequestID: uid(), Speed: 1.25}
	w := call(a, "POST", base+"/speech", in)
	if w.Code != 202 {
		t.Fatal(w.Code, w.Body.String())
	}
	var job SpeechJob
	json.Unmarshal(w.Body.Bytes(), &job)
	job = waitSpeech(t, a, p.ID, job.ID)
	if job.State != "completed" || job.Backend != "indextts" || job.Progress != 3 || requests.Load() != 3 {
		t.Fatal(job, requests.Load())
	}
	b, e := os.ReadFile(filepath.Join(a.speechDir(p.ID, job.ID), "manifest.json"))
	if e != nil {
		t.Fatal(e)
	}
	var manifest SpeechManifest
	json.Unmarshal(b, &manifest)
	if manifest.Model != "IndexTeam/IndexTTS-2.5" || manifest.Speed != 1.25 || manifest.Language != "zh" || len(manifest.Cues) != 3 || math.Abs(manifest.Cues[0].End-.6) > .001 || math.Abs(manifest.Cues[1].Start-.6) > .001 || math.Abs(manifest.Shots[0].Duration-1.2) > .001 || math.Abs(manifest.Duration-3.4) > .001 {
		t.Fatal(manifest)
	}
	// Changes in the default provider do not turn a lost-response retry into a
	// new generation; request receipt remains authoritative after restart too.
	a.mu.Lock()
	a.tts = defaultSpeechConfig()
	a.mu.Unlock()
	w = call(a, "POST", base+"/speech", in)
	if w.Code != 200 || requests.Load() != 3 {
		t.Fatal(w.Code, w.Body.String())
	}
	in.Speed = 1.1
	if call(a, "POST", base+"/speech", in).Code != 409 {
		t.Fatal("idempotency conflict not detected")
	}
	restarted, e := newApp(a.data)
	if e != nil {
		t.Fatal(e)
	}
	restarted.token = a.token
	in.Speed = 1.25
	if call(restarted, "POST", base+"/speech", in).Code != 200 {
		t.Fatal("retry after restart failed")
	}
	w = call(a, "POST", base+"/speech/"+job.ID+"/adopt", map[string]any{"revision": p.Revision})
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var adopted Project
	json.Unmarshal(w.Body.Bytes(), &adopted)
	if adopted.Audio.Narration.AssetID != job.ID || adopted.Subtitles.Binding.Method != "synthesized_sentence_durations" || adopted.Subtitles.Status != "ready" {
		t.Fatal("IndexTTS adoption failed", adopted)
	}
	adopted.Shots[0].Text = "修改后的稿件。"
	adopted, e = a.edit(adopted.ID, adopted)
	if e != nil {
		t.Fatal(e)
	}
	if call(a, "POST", base+"/speech/"+job.ID+"/adopt", map[string]any{"revision": adopted.Revision}).Code != 409 {
		t.Fatal("stale IndexTTS result adopted")
	}
}

func TestSpeechSettingsPersistenceAndSecretHandling(t *testing.T) {
	a := testApp(t)
	cfg := indexConfig("http://127.0.0.1:8092")
	w := call(a, "PUT", "/api/speech/settings", cfg)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), cfg.APIKey) || !strings.Contains(w.Body.String(), `"hasApiKey":true`) {
		t.Fatal("key leaked or not retained")
	}
	b, e := newApp(a.data)
	if e != nil || b.tts != cfg {
		t.Fatal(e, "settings not restored")
	}
	if info, e := os.Stat(filepath.Join(a.data, "speech-config.json")); e != nil || info.Mode().Perm()&0077 != 0 {
		t.Fatal("settings permissions", e)
	}
	cfg.APIKey = ""
	cfg.Voice = "other_voice"
	if call(a, "PUT", "/api/speech/settings", cfg).Code != 200 || a.tts.APIKey == "" {
		t.Fatal("blank key erased existing key")
	}
	cfg.URL = "http://other.local:8092/v1/audio/speech"
	if call(a, "PUT", "/api/speech/settings", cfg).Code != 400 {
		t.Fatal("credential silently reused on new service")
	}
	raw, _ := json.Marshal(cfg)
	var payload map[string]any
	json.Unmarshal(raw, &payload)
	payload["clearApiKey"] = true
	if call(a, "PUT", "/api/speech/settings", payload).Code != 200 || a.tts.APIKey != "" {
		t.Fatal("explicit key clear failed")
	}
	payload["url"] = "http://user:pass@localhost/v1/audio/speech"
	if call(a, "PUT", "/api/speech/settings", payload).Code != 400 {
		t.Fatal("URL credentials accepted")
	}
	payload["url"] = "file:///v1/audio/speech"
	if call(a, "PUT", "/api/speech/settings", payload).Code != 400 {
		t.Fatal("file URL accepted")
	}
	t.Setenv("VIDEO_TTS_BACKEND", "indextts")
	t.Setenv("VIDEO_TTS_URL", "http://127.0.0.1:9000/v1/audio/speech")
	t.Setenv("VIDEO_TTS_API_KEY", "environment-key")
	if e = a.speechEnvironment(); e != nil {
		t.Fatal(e)
	}
	if call(a, "PUT", "/api/speech/settings", cfg).Code != 409 {
		t.Fatal("environment config overwritten")
	}
	if strings.Contains(call(a, "GET", "/api/speech/settings", nil).Body.String(), "environment-key") {
		t.Fatal("environment key disclosed")
	}
	for _, env := range rendererEnvironment() {
		if strings.HasPrefix(env, "VIDEO_TTS_API_KEY=") {
			t.Fatal("key forwarded to render subprocess")
		}
	}
}

func TestIndexTTSErrorsRedirectAndCancellation(t *testing.T) {
	for _, tc := range []struct {
		name string
		code int
		body []byte
		size string
	}{
		{"credentials", 401, []byte("test-index-secret"), ""}, {"voice", 422, []byte("secret voice path"), ""}, {"busy", 503, nil, ""}, {"json", 200, []byte(`{"path":"/server/audio.wav"}`), ""}, {"truncated", 200, []byte("RIFF"), ""}, {"oversized", 200, indexTestWAV(), "40000000"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if tc.size != "" {
					w.Header().Set("Content-Length", tc.size)
				}
				w.WriteHeader(tc.code)
				w.Write(tc.body)
			}))
			defer server.Close()
			cfg := indexConfig(server.URL)
			job := SpeechJob{Model: cfg.Model, Voice: cfg.Voice, Language: "zh", Speed: 1}
			err := synthesizeIndexTTS(context.Background(), cfg, job, "你好", filepath.Join(t.TempDir(), "out.wav"))
			if err == nil || strings.Contains(err.Error(), "test-index-secret") || strings.Contains(err.Error(), "secret voice path") {
				t.Fatal(err)
			}
		})
	}
	var followed atomic.Bool
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { followed.Store(true) }))
	defer target.Close()
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, target.URL, 307) }))
	defer redirect.Close()
	cfg := indexConfig(redirect.URL)
	if e := synthesizeIndexTTS(context.Background(), cfg, SpeechJob{}, "x", filepath.Join(t.TempDir(), "out.wav")); e == nil || followed.Load() {
		t.Fatal("followed redirect", e)
	}
	started := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		close(started)
		<-r.Context().Done()
	}))
	defer server.Close()
	a := testApp(t)
	a.tts = indexConfig(server.URL)
	p := speechTestProject(t, a)
	base := "/api/projects/" + p.ID
	w := call(a, "POST", base+"/speech", SpeechRequest{Revision: p.Revision, RequestID: uid()})
	if w.Code != 202 {
		t.Fatal(w.Code, w.Body.String())
	}
	var job SpeechJob
	json.Unmarshal(w.Body.Bytes(), &job)
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("request did not start")
	}
	if call(a, "PUT", "/api/speech/settings", a.tts).Code != 409 {
		t.Fatal("running settings changed")
	}
	if call(a, "POST", base+"/speech/"+job.ID+"/stop", map[string]any{}).Code != 200 {
		t.Fatal("cancel failed")
	}
	job = waitSpeech(t, a, p.ID, job.ID)
	if job.State != "interrupted" || len(job.Files) != 0 {
		t.Fatal(job)
	}
	if call(a, "GET", base+"/speech/"+job.ID+"/files/narration.wav", nil).Code != 409 {
		t.Fatal("cancelled audio exposed")
	}
}

func TestIndexTTSValidationAndProbe(t *testing.T) {
	cfg := indexConfig("http://localhost:8092")
	for _, in := range []SpeechRequest{
		{RequestID: uid(), Speed: .4}, {RequestID: uid(), Speed: 2.1}, {RequestID: uid(), Rate: 170}, {RequestID: uid(), Voice: "../../secret"}, {RequestID: uid(), Language: "fr"}, {RequestID: "invalid"},
	} {
		if in.normalize(cfg) == nil {
			t.Fatal("accepted invalid request", in)
		}
	}
	for _, code := range []int{404, 401, 200} {
		s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(code)
			io.WriteString(w, `{"data":[{"id":"wrong-model"}]}`)
		}))
		if _, e := probeIndexTTS(context.Background(), indexConfig(s.URL)); e == nil {
			t.Fatal("invalid model probe accepted")
		}
		s.Close()
	}
	// Whole pipeline rejects a silent WAV even when the service reports 200.
	silent := indexTestWAV()
	copy(silent[44:], make([]byte, len(silent)-44))
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write(silent) }))
	defer s.Close()
	a := testApp(t)
	a.tts = indexConfig(s.URL)
	p := speechTestProject(t, a)
	w := call(a, "POST", "/api/projects/"+p.ID+"/speech", SpeechRequest{Revision: p.Revision, RequestID: uid()})
	var job SpeechJob
	json.Unmarshal(w.Body.Bytes(), &job)
	job = waitSpeech(t, a, p.ID, job.ID)
	if job.State != "failed" || !strings.Contains(job.Error, "静音") || len(job.Files) != 0 {
		t.Fatal(job)
	}
	if bytes.Contains(w.Body.Bytes(), []byte("test-index-secret")) {
		t.Fatal("secret in job")
	}
}

func TestIndexTTSMCPAndLegacyReceipt(t *testing.T) {
	var payload map[string]any
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&payload)
		w.Write(indexTestWAV())
	}))
	defer s.Close()
	a := testApp(t)
	a.tts = indexConfig(s.URL)
	p := speechTestProject(t, a)
	p.Shots = p.Shots[:1]
	p.Shots[0].Text = "Hello news."
	p, err := a.edit(p.ID, p)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": map[string]any{"name": "video_speech_start", "arguments": map[string]any{"projectId": p.ID, "revision": p.Revision, "requestId": uid(), "backend": "indextts", "voice": "news_en", "language": "en", "speed": 1.1}}})
	req := httptest.NewRequest("POST", "http://localhost/api/mcp", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+a.token)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	a.routes().ServeHTTP(w, req)
	if w.Code != 200 || strings.Contains(w.Body.String(), `"isError":true`) {
		t.Fatal(w.Code, w.Body.String())
	}
	q, _ := a.get(p.ID)
	if len(q.Speech) != 1 {
		t.Fatal(w.Body.String())
	}
	job := waitSpeech(t, a, p.ID, q.Speech[0].ID)
	if job.State != "completed" || job.Language != "en" || job.Speed != 1.1 || job.Voice != "news_en" {
		t.Fatal(job)
	}
	if payload["extra_params"].(map[string]any)["lang"] != "en" || payload["speed"] != 1.1 {
		t.Fatal(payload)
	}
	// Preserve 0.7 receipts that did not carry a backend or raw request hash.
	a.mu.Lock()
	old := SpeechJob{ID: uid(), RequestID: uid(), Revision: p.Revision, Voice: "cmn", Rate: 170, State: "completed"}
	a.projects[p.ID].Speech = append(a.projects[p.ID].Speech, old)
	a.mu.Unlock()
	w = call(a, "POST", "/api/projects/"+p.ID+"/speech", SpeechRequest{RequestID: old.RequestID, Revision: p.Revision})
	if w.Code != 200 {
		t.Fatal("legacy default receipt not preserved", w.Code, w.Body.String())
	}
	data, err := os.ReadFile("integrations/video-project/SKILL.md")
	if err != nil || string(data) != projectSkill {
		t.Fatal("embedded/distributed skill mismatch", err)
	}
}
