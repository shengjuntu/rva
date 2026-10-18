package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func adoptedProject(t *testing.T) (*App, Project) {
	t.Helper()
	a := testApp(t)
	script := scriptFixture("审核稿测试。")
	p := importFixture(t, a, script)
	p = nativeWorkflowFixture(t, a, p)
	w := uploadNews(a, "/api/projects/"+p.ID+"/news/video", zipFixture(realVideoFixture(t, script), ""), p.Revision)
	if w.Code != 201 {
		t.Fatal(w.Code, w.Body.String())
	}
	json.Unmarshal(w.Body.Bytes(), &p)
	w = call(a, "POST", "/api/projects/"+p.ID+"/news/audio", map[string]any{"revision": p.Revision})
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	json.Unmarshal(w.Body.Bytes(), &p)
	return a, p
}

func TestSubtitleAdoptionBindingConflictAndRecovery(t *testing.T) {
	a, p := adoptedProject(t)
	base := "/api/projects/" + p.ID
	if p.Subtitles.Status != "ready" || p.Subtitles.Binding.Method != "news_package" || len(p.Assets) != 1 || p.Audio.Narration.AssetID != p.Assets[0].ID {
		t.Fatal(p)
	}
	if p.Subtitles.Binding.SHA256 != p.News.Video.Files["narration.wav"].SHA256 || projectDuration(p) < p.Subtitles.Binding.AudioDuration {
		t.Fatal("audio binding/duration")
	}
	if w := call(a, "POST", base+"/news/audio", map[string]any{"revision": p.Revision - 1}); w.Code != 409 {
		t.Fatal("stale adoption", w.Code)
	}
	origin := jsonBytes(p.News)
	srt := subtitleSRT(p.Subtitles.Cues)
	// A normal project save may not forge a subtitle review or remove captions.
	p.Subtitles = nil
	p.Shots[0].Text = "改写后的正文。"
	q, e := a.edit(p.ID, p)
	if e != nil {
		t.Fatal(e)
	}
	if q.Subtitles.Status != "stale" || !strings.Contains(q.Subtitles.Reason, "正文") {
		t.Fatal(q.Subtitles)
	}
	if w := call(a, "POST", base+"/render", map[string]any{}); w.Code != 409 || !strings.Contains(w.Body.String(), "正文") {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := call(a, "POST", base+"/news/audio", map[string]any{"revision": q.Revision}); w.Code != 409 {
		t.Fatal("changed script adopted", w.Code)
	}
	w := call(a, "PUT", base+"/subtitles", map[string]any{"revision": q.Revision - 1, "srt": srt, "enabled": true, "reviewed": true})
	if w.Code != 409 {
		t.Fatal("stale review accepted", w.Code)
	}
	// An explicit human review can rebind existing audio; it is not an ASR claim.
	w = call(a, "PUT", base+"/subtitles", map[string]any{"revision": q.Revision, "srt": srt, "enabled": true, "reviewed": true})
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	json.Unmarshal(w.Body.Bytes(), &q)
	if q.Subtitles.Status != "ready" || q.Subtitles.Binding.Method != "manual_review" {
		t.Fatal(q.Subtitles)
	}
	if string(jsonBytes(q.News)) != string(origin) {
		t.Fatal("immutable news modified")
	}
	// Cue edits require a new explicit review, even if the audio is unchanged.
	w = call(a, "PUT", base+"/subtitles", map[string]any{"revision": q.Revision, "srt": strings.ReplaceAll(srt, "测试", "测试修订"), "enabled": true, "reviewed": false})
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	json.Unmarshal(w.Body.Bytes(), &q)
	if q.Subtitles.Status != "unreviewed" {
		t.Fatal(q.Subtitles)
	}
	restored, e := newApp(a.data)
	if e != nil {
		t.Fatal(e)
	}
	rp, _ := restored.get(p.ID)
	if rp.Subtitles.Status != "unreviewed" || rp.Revision != q.Revision {
		t.Fatal("restart lost captions")
	}
	// A concurrent bridge/render metadata change is preserved at publish time.
	a.projects[p.ID].SessionID = "retained-session"
	a.projects[p.ID].Renders = append(a.projects[p.ID].Renders, Render{ID: uid(), State: "completed"})
	q, e = a.publishSubtitleProject(q)
	if e != nil {
		t.Fatal(e)
	}
	if q.SessionID != "retained-session" || len(q.Renders) != 1 {
		t.Fatal("lost metadata")
	}
	if _, e = a.publishSubtitleProject(rp); e == nil {
		t.Fatal("concurrent write accepted")
	}
}

func TestSubtitleTimingSnapshotAndTampering(t *testing.T) {
	a, p := adoptedProject(t)
	base := "/api/projects/" + p.ID
	p.Audio.Narration.SourceStart = .2
	q, e := a.edit(p.ID, p)
	if e != nil {
		t.Fatal(e)
	}
	if q.Subtitles.Status != "stale" {
		t.Fatal("trim not invalidated")
	}
	q.Audio.Narration.SourceStart = 0
	q, e = a.edit(q.ID, q)
	if e != nil {
		t.Fatal(e)
	}
	if q.Subtitles.Status != "ready" {
		t.Fatal("restored binding not recognized")
	}
	bad := "1\n00:00:00,000 --> 00:00:02,000\n超时\n"
	w := call(a, "PUT", base+"/subtitles", map[string]any{"revision": q.Revision, "srt": bad, "enabled": true, "reviewed": true})
	if w.Code != 422 {
		t.Fatal("out-of-video review accepted", w.Code)
	}
	// Plain text is escaped in HTML; snapshot SRT keeps exact words/times.
	srt := "1\n00:00:00,000 --> 00:00:01,000\n<script>unsafe</script> & 字幕\n"
	w = call(a, "PUT", base+"/subtitles", map[string]any{"revision": q.Revision, "srt": srt, "enabled": true, "reviewed": true})
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	json.Unmarshal(w.Body.Bytes(), &q)
	assets := t.TempDir()
	a.font = filepath.Join(assets, "font.otf")
	a.gsap = filepath.Join(assets, "gsap.js")
	os.WriteFile(a.font, []byte("fixture"), 0600)
	os.WriteFile(a.gsap, []byte("fixture"), 0600)
	dir := filepath.Join(t.TempDir(), "render")
	if e = a.compose(q, dir); e != nil {
		t.Fatal(e)
	}
	html, _ := os.ReadFile(filepath.Join(dir, "index.html"))
	if strings.Contains(string(html), "<script>unsafe</script>") || !strings.Contains(string(html), "&lt;script&gt;unsafe&lt;/script&gt; &amp; 字幕") || !strings.Contains(string(html), `data-track-index="3"`) {
		t.Fatal("unsafe/missing caption HTML")
	}
	raw, _ := os.ReadFile(filepath.Join(dir, "subtitles.srt"))
	if string(raw) != subtitleSRT(q.Subtitles.Cues) {
		t.Fatal("SRT snapshot mismatch")
	}
	if e = os.WriteFile(filepath.Join(dir, "assets", q.Assets[0].File), []byte("tampered"), 0600); e != nil {
		t.Fatal(e)
	}
	if verifySubtitleSnapshot(q, dir) == nil {
		t.Fatal("changed audio accepted")
	}
	// Turning captions off is explicit; old project PUT clients keep the track.
	w = call(a, "PUT", base+"/subtitles", map[string]any{"revision": q.Revision, "srt": srt, "enabled": false, "reviewed": false})
	if w.Code != 200 {
		t.Fatal(w.Code)
	}
	json.Unmarshal(w.Body.Bytes(), &q)
	if q.Subtitles.Status != "disabled" {
		t.Fatal(q.Subtitles)
	}
	// A manually imported SRT starts unreviewed even without a news binding.
	w = call(a, "PUT", base+"/subtitles", map[string]any{"revision": q.Revision, "srt": "", "enabled": false})
	if w.Code != 200 {
		t.Fatal(w.Code)
	}
	json.Unmarshal(w.Body.Bytes(), &q)
	w = call(a, "PUT", base+"/subtitles", map[string]any{"revision": q.Revision, "srt": srt, "enabled": true, "reviewed": false})
	if w.Code != 200 {
		t.Fatal(w.Code)
	}
	json.Unmarshal(w.Body.Bytes(), &q)
	if q.Subtitles.Status != "unreviewed" || q.Subtitles.Binding.AssetID != "" {
		t.Fatal(q.Subtitles)
	}
	q.Audio.Narration.Start = .5
	q.Shots[0].Duration += .5
	q, e = a.edit(q.ID, q)
	if e != nil {
		t.Fatal(e)
	}
	w = call(a, "PUT", base+"/subtitles", map[string]any{"revision": q.Revision, "srt": srt, "enabled": true, "reviewed": true})
	if w.Code != 422 || !strings.Contains(w.Body.String(), "播放区间") {
		t.Fatal("caption before audio accepted", w.Code, w.Body.String())
	}
	shifted := strings.ReplaceAll(strings.ReplaceAll(srt, "00:00:00,000", "00:00:00,500"), "00:00:01,000", "00:00:01,500")
	w = call(a, "PUT", base+"/subtitles", map[string]any{"revision": q.Revision, "srt": shifted, "enabled": true, "reviewed": true})
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	json.Unmarshal(w.Body.Bytes(), &q)
	if q.Subtitles.Status != "ready" {
		t.Fatal(q.Subtitles)
	}
	// A failed adoption may not leak the newly copied WAV or alter the project.
	q.Shots = append(q.Shots, Shot{Duration: 1})
	q, e = a.edit(q.ID, q)
	if e != nil {
		t.Fatal(e)
	}
	filesBefore, _ := os.ReadDir(filepath.Join(a.dir(q.ID), "workspace", "assets"))
	w = call(a, "POST", base+"/news/audio", map[string]any{"revision": q.Revision})
	if w.Code != 422 {
		t.Fatal(w.Code, w.Body.String())
	}
	filesAfter, _ := os.ReadDir(filepath.Join(a.dir(q.ID), "workspace", "assets"))
	after, _ := a.get(q.ID)
	if len(filesAfter) != len(filesBefore) || after.Revision != q.Revision {
		t.Fatal("failed adoption leaked changes")
	}
}

func TestSubtitleParserBoundaries(t *testing.T) {
	good := "\ufeff1\r\n00:00:00,010 --> 00:00:00,500\r\n第一行\r\n第二行\r\n\r\n2\r\n00:00:00,750 --> 00:00:01,000\r\n间隙之后\r\n"
	cues, e := parseSubtitles(good)
	if e != nil || len(cues) != 2 || cues[0].Start != .01 {
		t.Fatal(cues, e)
	}
	for _, raw := range []string{"", "1\n00:00:00,000 --> 00:00:00,000\n空时段", strings.ReplaceAll(good, "00:00:00,750", "00:00:00,400"), strings.ReplaceAll(good, "00:00:01,000", "00:10:00,001"), strings.ReplaceAll(good, "2\r\n", "4\r\n"), "1\n00:00:00,000 --> 00:00:01,000\n" + strings.Repeat("字", 501), "1\n00:00:00,000 --> 00:00:01,000\n\x00", string([]byte{0xff})} {
		if _, e = parseSubtitles(raw); e == nil {
			t.Fatalf("invalid SRT accepted %q", raw)
		}
	}
}
