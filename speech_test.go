package main

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"math"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func speechTestProject(t *testing.T, a *App) Project {
	t.Helper()
	p, e := a.create("自动配音验证")
	if e != nil {
		t.Fatal(e)
	}
	p.Shots = []Shot{{Title: "第一镜", Text: "这是本机配音验证。字幕来自实际音频。", Duration: 2}, {Title: "第二镜", Text: "生成以后可以试听。", Duration: 2}, {Title: "无口播画面", Text: "", Duration: 1.2}}
	p, e = a.edit(p.ID, p)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() {
		a.mu.Lock()
		for _, cancel := range a.speechJobs {
			cancel()
		}
		a.mu.Unlock()
		a.wg.Wait()
	})
	return p
}
func waitSpeech(t *testing.T, a *App, pid, sid string) SpeechJob {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		p, e := a.get(pid)
		if e != nil {
			t.Fatal(e)
		}
		j, e := findSpeech(p, sid)
		if e != nil {
			t.Fatal(e)
		}
		if j.State != "running" && j.State != "stopping" {
			return j
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("speech timed out")
	return SpeechJob{}
}
func TestSpeechSnapshotGenerationAndAdoption(t *testing.T) {
	executable := os.Getenv("VIDEO_TEST_ESPEAK")
	if executable == "" {
		t.Skip("set VIDEO_TEST_ESPEAK for real local TTS integration")
	}
	a := testApp(t)
	a.espeak = executable
	p := speechTestProject(t, a)
	base := "/api/projects/" + p.ID
	in := SpeechRequest{Revision: p.Revision, RequestID: uid(), Voice: "cmn", Rate: 170}
	w := call(a, "POST", base+"/speech", in)
	if w.Code != 202 {
		t.Fatal(w.Code, w.Body.String())
	}
	var job SpeechJob
	json.Unmarshal(w.Body.Bytes(), &job)
	job = waitSpeech(t, a, p.ID, job.ID)
	if job.State != "completed" {
		t.Fatal(job)
	}
	if job.Progress != 3 || job.Total != 3 || job.Duration <= 4 {
		t.Fatal(job)
	}
	before, _ := a.get(p.ID)
	if before.Revision != p.Revision || before.Audio != nil || before.Shots[0].Duration != 2 {
		t.Fatal("generation overwrote editor")
	}
	// Identical retry returns the existing job after completion, too.
	w = call(a, "POST", base+"/speech", in)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var again SpeechJob
	json.Unmarshal(w.Body.Bytes(), &again)
	if again.ID != job.ID {
		t.Fatal("retry duplicated job")
	}
	raw, e := os.ReadFile(filepath.Join(a.speechDir(p.ID, job.ID), "narration.wav"))
	if e != nil {
		t.Fatal(e)
	}
	if string(raw[:4]) != "RIFF" || binary.LittleEndian.Uint32(raw[24:]) != 24000 || int(binary.LittleEndian.Uint32(raw[40:])) != len(raw)-44 {
		t.Fatal("invalid PCM WAV")
	}
	var manifest SpeechManifest
	b, _ := os.ReadFile(filepath.Join(a.speechDir(p.ID, job.ID), "manifest.json"))
	json.Unmarshal(b, &manifest)
	if manifest.Alignment != "synthesized_sentence_durations" || len(manifest.Cues) != 3 || len(manifest.Shots) != 3 {
		t.Fatal(manifest)
	}
	if manifest.Cues[2].Start != manifest.Shots[0].Duration || math.Abs(manifest.Duration-float64(len(raw)-44)/48000) > .00001 {
		t.Fatal("cue/PCM timing drift")
	}
	silenceAt := 44 + int(math.Round((manifest.Shots[0].Duration+manifest.Shots[1].Duration)*24000))*2
	if !bytes.Equal(raw[silenceAt:], make([]byte, len(raw)-silenceAt)) {
		t.Fatal("visual-only shot is not silent")
	}
	for _, s := range manifest.Shots {
		if math.Abs(s.Duration*30-math.Round(s.Duration*30)) > 1e-6 {
			t.Fatal("shot not on frame boundary")
		}
	}
	// Visual changes do not change speech, and are preserved on adoption.
	before.Shots[0].Title = "保留的新标题"
	before.Brief = "保留的说明"
	before, e = a.edit(p.ID, before)
	if e != nil {
		t.Fatal(e)
	}
	w = call(a, "POST", base+"/speech/"+job.ID+"/adopt", map[string]any{"revision": p.Revision})
	if w.Code != 409 {
		t.Fatal("stale revision accepted")
	}
	w = call(a, "POST", base+"/speech/"+job.ID+"/adopt", map[string]any{"revision": before.Revision})
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var q Project
	json.Unmarshal(w.Body.Bytes(), &q)
	if q.Audio.Narration.AssetID != job.ID || q.Subtitles.Status != "ready" || q.Subtitles.Binding.Method != "synthesized_sentence_durations" || q.Shots[0].Title != before.Shots[0].Title || q.Brief != before.Brief {
		t.Fatal(q)
	}
	if len(q.Assets) != 1 || math.Abs(projectDuration(q)-job.Duration) > 1e-6 {
		t.Fatal("adoption duration/assets")
	}
	w = call(a, "POST", base+"/speech/"+job.ID+"/adopt", map[string]any{"revision": q.Revision})
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	json.Unmarshal(w.Body.Bytes(), &q)
	if len(q.Assets) != 1 {
		t.Fatal("re-adoption duplicated asset")
	}
	// New text cannot silently reuse a completed task; old immutable results survive.
	q.Shots[0].Text = "新的口播。"
	q.Speech = nil
	q, e = a.edit(q.ID, q)
	if e != nil {
		t.Fatal(e)
	}
	if q.Subtitles.Status != "stale" || len(q.Speech) != 1 {
		t.Fatal("edited speech state lost")
	}
	w = call(a, "POST", base+"/speech/"+job.ID+"/adopt", map[string]any{"revision": q.Revision})
	if w.Code != 409 {
		t.Fatal("changed script accepted")
	}
	download := call(a, "GET", base+"/speech/"+job.ID+"/files/narration.wav", nil)
	if download.Code != 200 || !bytes.Equal(download.Body.Bytes(), raw) {
		t.Fatal("speech artifact changed")
	}
	// Downloads verify immutable result bytes again.
	raw[len(raw)-1] ^= 1
	os.WriteFile(filepath.Join(a.speechDir(p.ID, job.ID), "narration.wav"), raw, 0600)
	if call(a, "GET", base+"/speech/"+job.ID+"/files/narration.wav", nil).Code != 409 {
		t.Fatal("corrupt speech downloaded")
	}
}
func TestSpeechCancelRetryFailureAndRestart(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix process cancellation fixture")
	}
	a := testApp(t)
	p := speechTestProject(t, a)
	base := "/api/projects/" + p.ID
	dir := t.TempDir()
	a.espeak = filepath.Join(dir, "slow-espeak")
	os.WriteFile(a.espeak, []byte("#!/bin/sh\nsleep 20\n"), 0700)
	in := SpeechRequest{Revision: p.Revision, RequestID: uid(), Voice: "cmn", Rate: 170}
	w := call(a, "POST", base+"/speech", in)
	if w.Code != 202 {
		t.Fatal(w.Code, w.Body.String())
	}
	var job SpeechJob
	json.Unmarshal(w.Body.Bytes(), &job)
	if call(a, "POST", base+"/speech", in).Code != 200 {
		t.Fatal("in-flight retry not idempotent")
	}
	in.Rate = 200
	if call(a, "POST", base+"/speech", in).Code != 409 {
		t.Fatal("request ID reused with other options")
	}
	in.RequestID = uid()
	if call(a, "POST", base+"/speech", in).Code != 409 {
		t.Fatal("second active task accepted")
	}
	if call(a, "GET", base+"/speech/"+job.ID+"/files/narration.wav", nil).Code != 409 {
		t.Fatal("partial result exposed")
	}
	// Editing during the job is independent; cancellation retains that edit.
	q, _ := a.get(p.ID)
	q.Shots[0].Text = "生成期间的新编辑。"
	q, e := a.edit(q.ID, q)
	if e != nil {
		t.Fatal(e)
	}
	if call(a, "POST", base+"/speech/"+job.ID+"/stop", map[string]any{}).Code != 200 {
		t.Fatal("stop failed")
	}
	stopped := waitSpeech(t, a, p.ID, job.ID)
	if stopped.State != "interrupted" {
		t.Fatal(stopped)
	}
	current, _ := a.get(p.ID)
	if current.Shots[0].Text != q.Shots[0].Text || current.Revision != q.Revision {
		t.Fatal("cancel overwrote new draft")
	}
	a.wg.Wait()
	// A provider error may not fall back to a previous chunk or publish success.
	a.espeak = filepath.Join(dir, "failed-espeak")
	os.WriteFile(a.espeak, []byte("#!/bin/sh\necho 'fixture provider failure' >&2\nexit 7\n"), 0700)
	in.Revision = q.Revision
	in.RequestID = uid()
	w = call(a, "POST", base+"/speech", in)
	if w.Code != 202 {
		t.Fatal(w.Code, w.Body.String())
	}
	json.Unmarshal(w.Body.Bytes(), &job)
	failed := waitSpeech(t, a, p.ID, job.ID)
	if failed.State != "failed" || failed.Files != nil {
		t.Fatal(failed)
	}
	a.wg.Wait()
	a.mu.Lock()
	a.projects[p.ID].Speech[0].State = "running"
	e = a.save(a.projects[p.ID])
	a.mu.Unlock()
	if e != nil {
		t.Fatal(e)
	}
	restored, e := newApp(a.data)
	if e != nil {
		t.Fatal(e)
	}
	saved, _ := restored.get(p.ID)
	if saved.Speech[0].State != "interrupted" || saved.Speech[1].State != "failed" {
		t.Fatal("restart incorrectly recovered jobs")
	}
	r := httptest.NewRequest("POST", "http://127.0.0.1:3220"+base+"/speech", strings.NewReader("{}"))
	unauth := httptest.NewRecorder()
	a.routes().ServeHTTP(unauth, r)
	if unauth.Code != 401 {
		t.Fatal("unauthenticated speech")
	}
}
func TestSpeechChunkBoundariesAndStrictBinding(t *testing.T) {
	text := "第一句。第二句！Keep words and decimal 3.14 together. " + strings.Repeat("长", 170)
	chunks := speechChunks(text)
	if len(chunks) != 6 || chunks[2] != "Keep words and decimal 3.14 together." {
		t.Fatal(chunks)
	}
	for _, c := range chunks {
		if len([]rune(c)) > 80 || strings.TrimSpace(c) == "" {
			t.Fatal(c)
		}
	}
	if compactNewsText(strings.Join(chunks, "")) != compactNewsText(text) {
		t.Fatal("chunking dropped text")
	}
	p := Project{Shots: []Shot{{ID: uid(), Text: "now here", Duration: 2}}, Audio: &AudioMix{Narration: AudioTrack{AssetID: uid(), Volume: 1}}}
	p.Subtitles = &SubtitleTrack{Enabled: true, Cues: []SubtitleCue{{0, 1, "now here"}}, Binding: SubtitleBinding{AssetID: p.Audio.Narration.AssetID, SHA256: "fixture", AudioDuration: 1, TextHash: hashBytes([]byte(storyboardText(p))), ScriptHash: speechScriptHash(p)}}
	refreshSubtitleState(&p)
	if p.Subtitles.Status != "ready" {
		t.Fatal(p.Subtitles)
	}
	p.Shots[0].Text = "nowhere"
	refreshSubtitleState(&p)
	if p.Subtitles.Status != "stale" {
		t.Fatal("word boundary edit not detected")
	}
}
