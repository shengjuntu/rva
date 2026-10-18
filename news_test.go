package main

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"fmt"
	"image"
	"image/png"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
)

func jsonBytes(v any) []byte {
	b, e := json.Marshal(v)
	if e != nil {
		panic(e)
	}
	return b
}
func scriptFixture(text string) map[string][]byte {
	source := map[string]any{"article_key": "article-demo", "title": "验证资料", "url": "https://example.invalid/news", "published_at": "2026-09-29T00:00:00Z", "content_excerpt": "测试内容，不是真实新闻。"}
	snapshot := map[string]any{"schema_version": 1, "document": map[string]any{"title": "审核稿演示", "script_text": text, "target_duration_sec": 10, "source_keys": []string{"article-demo"}, "visual_notes": "标题卡"}, "sources": []any{source}}
	files := map[string][]byte{"script.txt": []byte(text), "revision.json": jsonBytes(snapshot), "sources.json": jsonBytes([]any{source}), "script.json": jsonBytes(snapshot), "script.md": []byte(text), "assets_manifest.json": []byte(`{}`)}
	m := newsManifest{Schema: 1, Kind: "news2douyin.script_package", PackageKey: "pkg_demo", EventKey: "event_demo", ExportKey: "exp_demo", Revision: 2, StateVersion: 4, Status: "approved", ContentHash: hashBytes(files["revision.json"]), TargetDuration: 10, DurationKind: "editorial_target"}
	m.Reviews = append(m.Reviews, struct {
		Revision     int             `json:"revision"`
		StateVersion int             `json:"state_version"`
		Action       string          `json:"action"`
		Status       string          `json:"status"`
		Checks       map[string]bool `json:"checks"`
	}{2, 4, "approve", "approved", map[string]bool{"sources_checked": true, "wording_checked": true}})
	files["manifest.json"] = jsonBytes(m)
	rehashFixture(files)
	return files
}
func rehashFixture(files map[string][]byte) {
	var m newsManifest
	_ = json.Unmarshal(files["manifest.json"], &m)
	m.Files = map[string]string{}
	for n, b := range files {
		if n != "manifest.json" {
			m.Files[n] = hashBytes(b)
		}
	}
	files["manifest.json"] = jsonBytes(m)
}
func zipFixture(files map[string][]byte, duplicate string) []byte {
	var b bytes.Buffer
	z := zip.NewWriter(&b)
	for n, data := range files {
		f, e := z.Create(n)
		if e != nil {
			panic(e)
		}
		f.Write(data)
	}
	if duplicate != "" {
		f, _ := z.Create(duplicate)
		f.Write(files[duplicate])
	}
	if e := z.Close(); e != nil {
		panic(e)
	}
	return b.Bytes()
}
func uploadNews(a *App, path string, raw []byte, revision int) *httptest.ResponseRecorder {
	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	f, _ := form.CreateFormFile("file", "package.zip")
	f.Write(raw)
	if revision > 0 {
		form.WriteField("revision", strconv.Itoa(revision))
	}
	form.Close()
	r := httptest.NewRequest("POST", "http://127.0.0.1:3220"+path, &body)
	r.Header.Set("Authorization", "Bearer "+a.token)
	r.Header.Set("Content-Type", form.FormDataContentType())
	w := httptest.NewRecorder()
	a.routes().ServeHTTP(w, r)
	return w
}
func importFixture(t *testing.T, a *App, files map[string][]byte) Project {
	t.Helper()
	w := uploadNews(a, "/api/news/import", zipFixture(files, ""), 0)
	if w.Code != 201 {
		t.Fatal(w.Code, w.Body.String())
	}
	var p Project
	if e := json.Unmarshal(w.Body.Bytes(), &p); e != nil {
		t.Fatal(e)
	}
	return p
}
func TestNewsImportPreservesNarrationAndProvenance(t *testing.T) {
	a := testApp(t)
	text := "第一句。金额12.5元。\n" + strings.Repeat("长文本和来源均须保留。", 80)
	files := scriptFixture(text)
	raw := zipFixture(files, "")
	w := uploadNews(a, "/api/news/import", raw, 0)
	if w.Code != 201 {
		t.Fatal(w.Code, w.Body.String())
	}
	var p Project
	json.Unmarshal(w.Body.Bytes(), &p)
	joined := ""
	for _, s := range p.Shots {
		joined += s.Text
		if len([]rune(s.Text)) > 220 || s.Duration < 1 || s.Duration > 60 {
			t.Fatal(s)
		}
	}
	if joined != text || p.News.ContentHash != hashBytes(files["revision.json"]) || p.News.Revision != 2 {
		t.Fatal(p)
	}
	p.News.ContentHash = "forged"
	p.Shots[0].Text = "用户的新草稿"
	updated, e := a.edit(p.ID, p)
	if e != nil {
		t.Fatal(e)
	}
	if updated.News.ContentHash == "forged" {
		t.Fatal("origin overwritten")
	}
	w = uploadNews(a, "/api/news/import", raw, 0)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var same Project
	json.Unmarshal(w.Body.Bytes(), &same)
	if same.ID != p.ID || same.Shots[0].Text != "用户的新草稿" {
		t.Fatal("retry overwrote edits")
	}
	detail := call(a, "GET", "/api/projects/"+p.ID+"/news", nil)
	if detail.Code != 200 || !strings.Contains(detail.Body.String(), "production_draft") || !strings.Contains(detail.Body.String(), "12.5") {
		t.Fatal(detail.Body.String())
	}
	restored, e := newApp(a.data)
	if e != nil {
		t.Fatal(e)
	}
	restored.token = a.token
	if uploadNews(restored, "/api/news/import", raw, 0).Code != 200 {
		t.Fatal("dedup lost on restart")
	}
}
func TestNewsRejectsInvalidPackagesWithoutPublishing(t *testing.T) {
	for _, scenario := range []string{"draft", "missing_review", "wrong_review_revision", "hash", "snapshot_hash", "script_mismatch", "source_mismatch", "path", "duplicate", "unknown_version", "capacity"} {
		t.Run(scenario, func(t *testing.T) {
			a := testApp(t)
			files := scriptFixture("审核稿测试。")
			var m newsManifest
			json.Unmarshal(files["manifest.json"], &m)
			duplicate := ""
			switch scenario {
			case "draft":
				m.Status = "draft"
				files["manifest.json"] = jsonBytes(m)
			case "missing_review":
				m.Reviews = nil
				files["manifest.json"] = jsonBytes(m)
			case "wrong_review_revision":
				m.Reviews[0].Revision = 1
				files["manifest.json"] = jsonBytes(m)
			case "hash":
				files["script.txt"] = []byte("changed without checksum")
			case "snapshot_hash":
				m.ContentHash = strings.Repeat("0", 64)
				files["manifest.json"] = jsonBytes(m)
			case "script_mismatch":
				files["script.txt"] = []byte("另一正文")
				rehashFixture(files)
			case "source_mismatch":
				files["sources.json"] = []byte(`[]`)
				rehashFixture(files)
			case "path":
				files["../escape"] = files["script.md"]
				delete(files, "script.md")
			case "duplicate":
				duplicate = "manifest.json"
			case "unknown_version":
				m.Schema = 2
				files["manifest.json"] = jsonBytes(m)
			case "capacity":
				files = scriptFixture(strings.Repeat("字", 18000))
			}
			w := uploadNews(a, "/api/news/import", zipFixture(files, duplicate), 0)
			if w.Code < 400 {
				t.Fatal(scenario, w.Code)
			}
			entries, _ := os.ReadDir(filepath.Join(a.data, "projects"))
			if len(entries) != 0 || len(a.projects) != 0 {
				t.Fatal("partial project published")
			}
			entries, _ = os.ReadDir(a.data)
			for _, e := range entries {
				if strings.HasPrefix(e.Name(), "news-") {
					t.Fatal("staging not cleaned", e.Name())
				}
			}
		})
	}
}
func TestNewsConcurrentImportCreatesOneProject(t *testing.T) {
	a := testApp(t)
	raw := zipFixture(scriptFixture("并发导入。"), "")
	var wg sync.WaitGroup
	results := make(chan *httptest.ResponseRecorder, 6)
	for i := 0; i < 6; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); results <- uploadNews(a, "/api/news/import", raw, 0) }()
	}
	wg.Wait()
	close(results)
	created := 0
	id := ""
	for w := range results {
		if w.Code == 201 {
			created++
		} else if w.Code != 200 {
			t.Fatal(w.Code, w.Body.String())
		}
		var p Project
		json.Unmarshal(w.Body.Bytes(), &p)
		if id != "" && p.ID != id {
			t.Fatal("duplicate projects")
		}
		id = p.ID
	}
	if created != 1 || len(a.projects) != 1 {
		t.Fatal(created, len(a.projects))
	}
}
func TestNewsDownloadRejectsCorruptionAndSymlinks(t *testing.T) {
	a := testApp(t)
	p := importFixture(t, a, scriptFixture("文件校验。"))
	route := "/api/projects/" + p.ID + "/news/script/files/script.txt"
	w := call(a, "GET", route, nil)
	if w.Code != 200 || w.Body.String() != "文件校验。" {
		t.Fatal(w.Code)
	}
	file := filepath.Join(a.dir(p.ID), "news", "script.txt")
	os.WriteFile(file, []byte("tampered"), 0600)
	if call(a, "GET", route, nil).Code != 409 {
		t.Fatal("corruption accepted")
	}
	secret := filepath.Join(a.data, "private.txt")
	os.WriteFile(secret, []byte("secret"), 0600)
	os.Remove(file)
	if e := os.Symlink(secret, file); e == nil {
		w = call(a, "GET", route, nil)
		if w.Code != 409 || strings.Contains(w.Body.String(), "secret") {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	if call(a, "GET", "/api/projects/"+p.ID+"/news/script/files/other.txt", nil).Code != 404 {
		t.Fatal("unknown file accepted")
	}
}
func TestNewsArchiveBoundsAndSymlinkEntry(t *testing.T) {
	for _, mode := range []string{"oversized", "symlink"} {
		t.Run(mode, func(t *testing.T) {
			a := testApp(t)
			files := scriptFixture("测试。")
			if mode == "oversized" {
				files["script.md"] = []byte(strings.Repeat("x", int(scriptLimit)+1))
				rehashFixture(files)
			}
			var b bytes.Buffer
			z := zip.NewWriter(&b)
			for n, data := range files {
				h := &zip.FileHeader{Name: n, Method: zip.Deflate}
				if mode == "symlink" && n == "script.md" {
					h.SetMode(os.ModeSymlink | 0600)
				}
				f, _ := z.CreateHeader(h)
				f.Write(data)
			}
			z.Close()
			if uploadNews(a, "/api/news/import", b.Bytes(), 0).Code < 400 {
				t.Fatal("unsafe archive accepted")
			}
		})
	}
}
func realVideoFixture(t *testing.T, script map[string][]byte) map[string][]byte {
	t.Helper()
	if _, e := exec.LookPath("ffmpeg"); e != nil {
		t.Skip("needs ffmpeg")
	}
	if _, e := exec.LookPath("ffprobe"); e != nil {
		t.Skip("needs ffprobe")
	}
	dir := t.TempDir()
	video := filepath.Join(dir, "video.mp4")
	cmd := exec.Command("ffmpeg", "-v", "error", "-f", "lavfi", "-i", "color=c=blue:s=360x640:r=12:d=1", "-f", "lavfi", "-i", "anullsrc=r=24000:cl=mono", "-t", "1", "-c:v", "libx264", "-threads", "1", "-pix_fmt", "yuv420p", "-c:a", "aac", video)
	if raw, e := cmd.CombinedOutput(); e != nil {
		t.Fatal(e, string(raw))
	}
	data, e := os.ReadFile(video)
	if e != nil {
		t.Fatal(e)
	}
	// This fixture uses real silent PCM, not a synthesized human voice.
	wav := filepath.Join(dir, "narration.wav")
	if raw, e := exec.Command("ffmpeg", "-v", "error", "-i", video, "-vn", "-ac", "1", "-ar", "24000", "-c:a", "pcm_s16le", wav).CombinedOutput(); e != nil {
		t.Fatal(e, string(raw))
	}
	voice, e := os.ReadFile(wav)
	if e != nil {
		t.Fatal(e)
	}
	var cover bytes.Buffer
	if e := png.Encode(&cover, image.NewRGBA(image.Rect(0, 0, 360, 640))); e != nil {
		t.Fatal(e)
	}
	files := map[string][]byte{"video.mp4": data, "narration.wav": voice, "cover.png": cover.Bytes(), "subtitles.srt": []byte("1\n00:00:00,000 --> 00:00:01,000\n审核稿测试。\n"), "script_snapshot.json": script["revision.json"]}
	m := newsManifest{Schema: 1, Kind: "news2douyin.video", TaskID: "task-demo", ExportKey: "exp_demo", Revision: 2, ScriptHash: hashBytes(script["revision.json"]), Duration: 1, Width: 360, Height: 640, FPS: 12}
	files["manifest.json"] = jsonBytes(m)
	rehashFixture(files)
	return files
}
func TestNewsVideoBindingProbeRangeAndRestart(t *testing.T) {
	a := testApp(t)
	script := scriptFixture("审核稿测试。")
	p := importFixture(t, a, script)
	files := realVideoFixture(t, script)
	raw := zipFixture(files, "")
	route := "/api/projects/" + p.ID + "/news/video"
	if uploadNews(a, route, raw, p.Revision+1).Code != 409 {
		t.Fatal("stale revision accepted")
	}
	w := uploadNews(a, route, raw, p.Revision)
	if w.Code != 201 {
		t.Fatal(w.Code, w.Body.String())
	}
	if uploadNews(a, route, raw, p.Revision).Code != 200 {
		t.Fatal("same video retry not idempotent")
	}
	var got Project
	json.Unmarshal(w.Body.Bytes(), &got)
	if got.News.Video == nil || got.Revision != p.Revision+1 || len(got.Renders) != 0 {
		t.Fatal(got)
	}
	r := httptest.NewRequest("GET", "http://127.0.0.1:3220"+route+"/files/video.mp4", nil)
	r.Header.Set("Authorization", "Bearer "+a.token)
	r.Header.Set("Range", "bytes=0-63")
	w = httptest.NewRecorder()
	a.routes().ServeHTTP(w, r)
	if w.Code != 206 || w.Body.Len() != 64 || !bytes.Equal(w.Body.Bytes(), files["video.mp4"][:64]) {
		t.Fatal(w.Code, w.Body.Len())
	}
	restored, e := newApp(a.data)
	if e != nil {
		t.Fatal(e)
	}
	restored.token = a.token
	if call(restored, "GET", route+"/files/subtitles.srt", nil).Code != 200 {
		t.Fatal("video lost after restart")
	}
	changed := got
	changed.Shots[0].Text = "新草稿"
	updated, e := a.edit(p.ID, changed)
	if e != nil {
		t.Fatal(e)
	}
	if updated.News.Video.ArchiveSHA256 != got.News.Video.ArchiveSHA256 {
		t.Fatal("source video changed")
	}
}
func TestNewsVideoRejectsWrongScriptAndShortStreams(t *testing.T) {
	script := scriptFixture("审核稿测试。")
	base := realVideoFixture(t, script)
	for _, scenario := range []string{"wrong_export", "wrong_hash", "short_stream", "wrong_subtitles", "invalid_wav", "invalid_cover"} {
		t.Run(scenario, func(t *testing.T) {
			a := testApp(t)
			p := importFixture(t, a, script)
			files := map[string][]byte{}
			for k, v := range base {
				files[k] = append([]byte(nil), v...)
			}
			var m newsManifest
			json.Unmarshal(files["manifest.json"], &m)
			switch scenario {
			case "wrong_export":
				m.ExportKey = "different"
			case "wrong_hash":
				m.ScriptHash = strings.Repeat("0", 64)
			case "short_stream":
				m.Duration = 2
			case "wrong_subtitles":
				files["subtitles.srt"] = []byte("1\n00:00:00,000 --> 00:00:01,000\n错误文本\n")
			case "invalid_wav":
				files["narration.wav"] = []byte("not a WAV")
			case "invalid_cover":
				files["cover.png"] = []byte("not a PNG")
			}
			files["manifest.json"] = jsonBytes(m)
			rehashFixture(files)
			w := uploadNews(a, "/api/projects/"+p.ID+"/news/video", zipFixture(files, ""), p.Revision)
			if w.Code != 422 {
				t.Fatal(w.Code, w.Body.String())
			}
			got, _ := a.get(p.ID)
			if got.News.Video != nil {
				t.Fatal("invalid video published")
			}
		})
	}
}
func TestNewsImportAuthentication(t *testing.T) {
	a := testApp(t)
	r := httptest.NewRequest("POST", "http://127.0.0.1:3220/api/news/import", strings.NewReader("not zip"))
	w := httptest.NewRecorder()
	a.routes().ServeHTTP(w, r)
	if w.Code != http.StatusUnauthorized {
		t.Fatal(w.Code)
	}
}
func TestNewsSRTValidation(t *testing.T) {
	for _, s := range []string{"1\n00:00:00,000 --> 00:00:02,000\n文本", "1\n00:00:00,000 --> 00:00:01,000\n错误", "1\n00:00:00,000 --> 00:00:00,500\n文\n\n2\n00:00:00,400 --> 00:00:01,000\n本"} {
		if validateNewsSRT(s, "文本", 1) == nil {
			t.Fatal("invalid srt accepted")
		}
	}
	if e := validateNewsSRT("1\r\n00:00:00,000 --> 00:00:01,000\r\n文 本\r\n", "文本", 1); e != nil {
		t.Fatal(e)
	}
}
func Example_newsPackageWorkflow() {
	fmt.Println("POST /api/news/import -> project.news -> GET /api/projects/{id}/news -> video_news_get")
	// Output: POST /api/news/import -> project.news -> GET /api/projects/{id}/news -> video_news_get
}
