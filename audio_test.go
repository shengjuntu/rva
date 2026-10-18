package main

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"math"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestAudioValidationAndCompatibility(t *testing.T) {
	a := testApp(t)
	p, _ := a.create("音轨项目")
	aid := uid()
	a.projects[p.ID].Assets = []Asset{{ID: aid, File: aid + ".wav", MIME: "audio/wav"}}
	p, _ = a.get(p.ID)
	p.Shots = []Shot{{Duration: 4, Title: "test"}}
	p.Audio = &AudioMix{Narration: AudioTrack{AssetID: aid, Volume: 1, Start: 0.5}, Music: AudioTrack{AssetID: aid, Volume: 0.2, Loop: true}}
	p, e := a.edit(p.ID, p)
	if e != nil {
		t.Fatal(e)
	}
	oldClient := p
	oldClient.Audio = nil
	oldClient.Brief = "旧客户端只改分镜"
	saved, e := a.edit(p.ID, oldClient)
	if e != nil || saved.Audio.Narration.AssetID != aid {
		t.Fatal(saved, e)
	}
	for _, mutate := range []func(*AudioMix){func(m *AudioMix) { m.Music.Volume = 2.01 }, func(m *AudioMix) { m.Narration.Start = 4 }, func(m *AudioMix) { m.Narration.SourceStart = -1 }, func(m *AudioMix) { m.Narration.Loop = true }, func(m *AudioMix) { m.Music.AssetID = uid() }} {
		bad := clone(&saved)
		mutate(bad.Audio)
		if _, e = a.edit(p.ID, bad); e == nil {
			t.Fatal("invalid audio accepted")
		}
	}
	saved.Audio = &AudioMix{}
	saved, e = a.edit(p.ID, saved)
	if e != nil || len(audioTracks(saved.Audio)) != 0 {
		t.Fatal(saved, e)
	}
	reloaded, e := newApp(a.data)
	if e != nil {
		t.Fatal(e)
	}
	got, _ := reloaded.get(p.ID)
	if got.Audio == nil {
		t.Fatal("audio state lost")
	}
}
func TestStartupFailureReleasesLock(t *testing.T) {
	for _, kind := range []string{"token", "url", "port"} {
		t.Run(kind, func(t *testing.T) {
			opts := serverOptions{Data: t.TempDir(), Listen: "127.0.0.1:3220", RunDesk: "http://127.0.0.1:3210"}
			t.Setenv("VIDEO_APP_TOKEN", "test-token-12345678901234567890")
			switch kind {
			case "token":
				t.Setenv("VIDEO_APP_TOKEN", "bad")
			case "url":
				opts.RunDesk = "file:///invalid"
			case "port":
				l, e := net.Listen("tcp", "127.0.0.1:0")
				if e != nil {
					t.Fatal(e)
				}
				defer l.Close()
				opts.Listen = l.Addr().String()
			}
			if e := serve(opts); e == nil {
				t.Fatal("expected startup failure")
			}
			if _, e := os.Stat(filepath.Join(opts.Data, "server.lock")); !os.IsNotExist(e) {
				t.Fatal("stale lock", e)
			}
		})
	}
}
func TestRendererDoesNotInheritAppTokens(t *testing.T) {
	t.Setenv("VIDEO_APP_TOKEN", "private-app-token")
	t.Setenv("RUNDESK_TOKEN", "private-rundesk-token")
	for _, s := range rendererEnvironment() {
		if strings.Contains(s, "private-app-token") || strings.Contains(s, "private-rundesk-token") {
			t.Fatal("token passed to renderer")
		}
	}
}
func TestMixAudioIntegration(t *testing.T) {
	for _, bin := range []string{"ffmpeg", "ffprobe"} {
		if _, e := exec.LookPath(bin); e != nil {
			t.Skip(bin + " not installed")
		}
	}
	dir := t.TempDir()
	os.Mkdir(filepath.Join(dir, "assets"), 0700)
	run := func(name string, args ...string) error {
		cmd := exec.Command(name, args...)
		cmd.Env = rendererEnvironment()
		raw, e := cmd.CombinedOutput()
		if e != nil {
			t.Log(string(raw))
		}
		return e
	}
	must := func(name string, args ...string) {
		t.Helper()
		if e := run(name, args...); e != nil {
			t.Fatal(e)
		}
	}
	must("ffmpeg", "-v", "error", "-f", "lavfi", "-i", "color=size=320x240:rate=30:duration=3", "-c:v", "libx264", "-preset", "ultrafast", "-pix_fmt", "yuv420p", filepath.Join(dir, "silent.mp4"))
	must("ffmpeg", "-v", "error", "-f", "lavfi", "-i", "sine=frequency=440:sample_rate=48000:duration=0.6", filepath.Join(dir, "assets", "voice.wav"))
	must("ffmpeg", "-v", "error", "-f", "lavfi", "-i", "sine=frequency=880:sample_rate=48000:duration=0.4", filepath.Join(dir, "assets", "music.wav"))
	p := Project{Assets: []Asset{{ID: "voice", Name: "voice", File: "voice.wav", MIME: "audio/wav"}, {ID: "music", Name: "music", File: "music.wav", MIME: "audio/wav"}}, Audio: &AudioMix{Narration: AudioTrack{AssetID: "voice", Volume: 0.8, Start: 0.5, SourceStart: 0.1}, Music: AudioTrack{AssetID: "music", Volume: 0.25, Start: 1.2, SourceStart: 0.1, Loop: true}}}
	if e := mixAudio(context.Background(), p, dir, 3, run); e != nil {
		t.Fatal(e)
	}
	raw, e := exec.Command("ffmpeg", "-v", "error", "-i", filepath.Join(dir, "final.mp4"), "-map", "0:a:0", "-ac", "1", "-ar", "48000", "-f", "f32le", "pipe:1").Output()
	if e != nil {
		t.Fatal(e)
	}
	rms := func(start, end float64) float64 {
		lo, hi := int(start*48000), int(end*48000)
		sum := 0.0
		for i := lo; i < hi; i++ {
			v := float64(math.Float32frombits(binary.LittleEndian.Uint32(raw[i*4 : i*4+4])))
			sum += v * v
		}
		return math.Sqrt(sum / float64(hi-lo))
	}
	if len(raw) < 3*48000*4 {
		t.Fatal("audio too short")
	}
	if v := rms(.1, .3); v > .001 {
		t.Fatal("audio before scheduled start", v)
	}
	if v := rms(.6, .8); v < .04 || v > .09 {
		t.Fatal("narration placement/gain", v)
	}
	if v := rms(1.06, 1.14); v > .001 {
		t.Fatal("narration did not finish", v)
	}
	if v := rms(2.7, 2.9); v < .01 || v > .04 {
		t.Fatal("music loop/gain", v)
	}
	b, e := exec.Command("ffprobe", "-v", "error", "-show_format", "-show_streams", "-of", "json", filepath.Join(dir, "final.mp4")).Output()
	if e != nil {
		t.Fatal(e)
	}
	var probe struct {
		Streams []struct {
			Type string `json:"codec_type"`
		} `json:"streams"`
	}
	json.Unmarshal(b, &probe)
	if len(probe.Streams) != 2 {
		t.Fatal(string(b))
	}
	p.Audio.Narration.SourceStart = 100
	if e = mixAudio(context.Background(), p, dir, 3, run); e == nil {
		t.Fatal("out-of-range trim accepted")
	}
	t.Log("PCM verified: initial silence, narration gain/start/end, music gain/loop; invalid trim rejected")
}

func TestLegacyProjectAndBusyToolUpgrade(t *testing.T) {
	a := testApp(t)
	p, _ := a.create("v0.1 项目")
	p.SessionID = uid()
	p.WorkspaceID = uid()
	p.ToolsVersion = ""
	a.save(&p)
	a.cfg.InstanceID = uid()
	b, e := newApp(a.data)
	if e != nil {
		t.Fatal(e)
	}
	legacy, _ := b.get(p.ID)
	if legacy.Audio != nil || legacy.Revision != 1 || legacy.SessionID != p.SessionID {
		t.Fatal("legacy state changed", legacy)
	}
	a.projects[p.ID] = &p
	rd := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" && r.URL.Path == "/api/v1/meta" {
			writeJSON(w, 200, v1Meta())
			return
		}
		if r.Method != "GET" || r.URL.Path != "/api/v1/sessions" {
			t.Errorf("configuration changed during active turn: %s %s", r.Method, r.URL.Path)
			http.Error(w, "unexpected", 500)
			return
		}
		writeJSON(w, 200, []map[string]string{{"instanceId": a.cfg.InstanceID, "status": "waiting"}})
	}))
	defer rd.Close()
	a.rd = rd.URL
	w := call(a, "POST", "/api/projects/"+p.ID+"/connect?refresh=1", map[string]any{})
	if w.Code != 409 {
		t.Fatal(w.Code, w.Body.String())
	}
	w = call(a, "POST", "/api/projects/"+p.ID+"/messages", map[string]string{"text": "继续", "requestId": "upgrade-test"})
	if w.Code != 409 || !strings.Contains(w.Body.String(), "升级项目工具") {
		t.Fatal(w.Code, w.Body.String())
	}
}
