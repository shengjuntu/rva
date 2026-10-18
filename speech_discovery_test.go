package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

func TestSpeechVoiceCatalog(t *testing.T) {
	cases := []struct {
		name, body         string
		status             int
		supported, wantErr bool
		voices             int
	}{
		{"native-vllm", `{"voices":["news_zh","news_en","news_zh"],"uploaded_voices":[{"name":"news_zh","ref_text":"private transcript"}]}`, 200, true, false, 2},
		{"empty", `{"voices":[]}`, 200, true, false, 0},
		{"unsupported", `secret`, 404, false, false, 0},
		{"method-unsupported", `secret`, 405, false, false, 0},
		{"auth", `test-index-secret`, 401, false, true, 0},
		{"busy", `test-index-secret`, 503, false, true, 0},
		{"html", `<html>Gradio</html>`, 200, false, true, 0},
		{"missing", `{}`, 200, false, true, 0},
		{"null", `{"voices":null}`, 200, false, true, 0},
		{"object", `{"voices":[{"name":"news_zh"}]}`, 200, false, true, 0},
		{"path", `{"voices":["/srv/private.wav"]}`, 200, false, true, 0},
		{"trailing-json", `{"voices":[]} {}`, 200, false, true, 0},
		{"huge", strings.Repeat(" ", 1<<20) + `{"voices":[]}`, 200, false, true, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/prefix/v1/audio/voices" || r.Method != "GET" || r.Header.Get("Authorization") != "Bearer test-index-secret" {
					t.Error("wrong request")
				}
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer server.Close()
			result, err := listSpeechVoices(context.Background(), indexConfig(server.URL+"/prefix"))
			if (err != nil) != tc.wantErr || result.Supported != tc.supported || len(result.Voices) != tc.voices {
				t.Fatalf("%+v %v", result, err)
			}
			b, _ := json.Marshal(result)
			if strings.Contains(string(b), "private") || (err != nil && strings.Contains(err.Error(), "secret")) {
				t.Fatal("remote metadata leaked")
			}
		})
	}
}

func TestSpeechVoiceDiscoveryDraftAndKeyIsolation(t *testing.T) {
	var hits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if r.Header.Get("Authorization") != "Bearer test-index-secret" {
			t.Error("key not retained")
		}
		writeJSON(w, 200, map[string]any{"voices": []string{"news_zh"}})
	}))
	defer server.Close()
	a := testApp(t)
	a.tts = indexConfig(server.URL)
	draft := a.tts
	draft.APIKey = ""
	draft.Voice = ""
	draft.Model = "" // first-time discovery needs neither
	w := call(a, "POST", "/api/speech/voices", draft)
	if w.Code != 200 || hits.Load() != 1 {
		t.Fatal(w.Code, w.Body.String())
	}
	if a.tts.Voice != "news_zh" {
		t.Fatal("discovery modified saved settings")
	}
	if _, err := os.Stat(filepath.Join(a.data, "speech-config.json")); !os.IsNotExist(err) {
		t.Fatal("discovery wrote config")
	}
	draft.URL = server.URL + "/new/v1/audio/speech"
	w = call(a, "POST", "/api/speech/voices", draft)
	if w.Code != 400 || hits.Load() != 1 {
		t.Fatal("forwarded saved credential to different endpoint")
	}
	a.ttsManaged = true
	w = call(a, "POST", "/api/speech/voices", a.tts)
	if w.Code != 409 || hits.Load() != 1 {
		t.Fatal("draft overrode managed settings")
	}
	w = call(a, "GET", "/api/speech/voices", nil)
	if w.Code != 200 || hits.Load() != 2 {
		t.Fatal(w.Code, w.Body.String())
	}
	req := httptest.NewRequest("GET", "/api/speech/voices", nil)
	response := httptest.NewRecorder()
	a.routes().ServeHTTP(response, req)
	if response.Code == 200 || hits.Load() != 2 {
		t.Fatal("unauthenticated discovery reached provider")
	}
}

func TestSpeechVoiceDiscoveryRedirectAndCancellation(t *testing.T) {
	var redirected atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { redirected.Add(1) }))
	defer target.Close()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, target.URL, 307) }))
	defer server.Close()
	if _, err := listSpeechVoices(context.Background(), indexConfig(server.URL)); err == nil || redirected.Load() != 0 {
		t.Fatal("followed redirect")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := listSpeechVoices(ctx, indexConfig(server.URL)); err == nil {
		t.Fatal("ignored cancellation")
	}
}

func TestSpeechDoctorReadOnlyAndSanitized(t *testing.T) {
	for _, name := range []string{"VIDEO_TTS_BACKEND", "VIDEO_TTS_URL", "VIDEO_TTS_MODEL", "VIDEO_TTS_VOICE", "VIDEO_TTS_LANGUAGE", "VIDEO_TTS_TIMEOUT_SECONDS", "VIDEO_TTS_API_KEY"} {
		t.Setenv(name, "")
	}
	var synthesis atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			synthesis.Add(1)
			w.WriteHeader(500)
			return
		}
		switch r.URL.Path {
		case "/v1/models":
			writeJSON(w, 200, map[string]any{"data": []map[string]string{{"id": "IndexTeam/IndexTTS-2.5"}}})
		case "/v1/audio/voices":
			writeJSON(w, 200, map[string]any{"voices": []string{"news_zh"}})
		default:
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	dir := t.TempDir()
	c := indexConfig(server.URL)
	if err := atomicJSON(filepath.Join(dir, "speech-config.json"), c); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(filepath.Join(dir, "speech-config.json"))
	var out, stderr bytes.Buffer
	if code := runSpeechDoctor([]string{"--data", dir}, &out, &stderr); code != 0 {
		t.Fatal(code, out.String(), stderr.String())
	}
	if strings.Contains(out.String(), "secret") || synthesis.Load() != 0 {
		t.Fatal("key leak or unexpected synthesis")
	}
	var report speechDoctorReport
	if err := json.Unmarshal(out.Bytes(), &report); err != nil || !report.Ready || report.SynthesisTested || len(report.Voices) != 1 {
		t.Fatal(out.String())
	}
	after, _ := os.ReadFile(filepath.Join(dir, "speech-config.json"))
	entries, _ := os.ReadDir(dir)
	if !bytes.Equal(before, after) || len(entries) != 1 {
		t.Fatal("doctor changed data")
	}
	t.Setenv("VIDEO_TTS_VOICE", "missing")
	out.Reset()
	if code := runSpeechDoctor([]string{"--data", dir}, &out, &stderr); code != 1 {
		t.Fatal("missing voice did not fail")
	}
	if !strings.Contains(out.String(), "默认音色未在") {
		t.Fatal(out.String())
	}
}
