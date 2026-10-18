package main

// Portable handoff from news2douyin. No remote URL fetching or shared databases.
import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"image/png"
	"io"
	"math"
	"mime/multipart"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const scriptLimit int64 = 4 << 20
const scriptBundleLimit int64 = 32 << 20

var newsImagePath = regexp.MustCompile(`^images/[a-f0-9]{64}\.(jpg|jpeg|png|webp)$`)

const videoLimit int64 = 256 << 20

var shaPattern = regexp.MustCompile(`^[a-f0-9]{64}$`)

type NewsFile struct {
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
}
type NewsOrigin struct {
	OutputLanguage string              `json:"outputLanguage,omitempty"`
	PackageKey     string              `json:"packageKey"`
	EventKey       string              `json:"eventKey"`
	ExportKey      string              `json:"exportKey"`
	Revision       int                 `json:"revision"`
	StateVersion   int                 `json:"stateVersion"`
	ContentHash    string              `json:"contentHash"`
	ArchiveSHA256  string              `json:"archiveSha256"`
	ImportedAt     string              `json:"importedAt"`
	TargetDuration int                 `json:"targetDuration"`
	Files          map[string]NewsFile `json:"files"`
	Video          *NewsVideo          `json:"video,omitempty"`
}
type NewsVideo struct {
	ID            string              `json:"id"`
	TaskID        string              `json:"taskId"`
	ArchiveSHA256 string              `json:"archiveSha256"`
	ImportedAt    string              `json:"importedAt"`
	Duration      float64             `json:"duration"`
	Width         int                 `json:"width"`
	Height        int                 `json:"height"`
	FPS           int                 `json:"fps"`
	Files         map[string]NewsFile `json:"files"`
}
type newsManifest struct {
	OutputLanguage string            `json:"output_language,omitempty"`
	Schema         int               `json:"schema_version"`
	Kind           string            `json:"kind"`
	ExportKey      string            `json:"export_key"`
	PackageKey     string            `json:"package_key"`
	EventKey       string            `json:"event_key"`
	Revision       int               `json:"revision"`
	StateVersion   int               `json:"state_version"`
	Status         string            `json:"status"`
	ContentHash    string            `json:"content_hash"`
	ScriptHash     string            `json:"script_hash"`
	TargetDuration int               `json:"target_duration_sec"`
	DurationKind   string            `json:"duration_kind"`
	TaskID         string            `json:"task_id"`
	Duration       float64           `json:"duration"`
	Width          int               `json:"width"`
	Height         int               `json:"height"`
	FPS            int               `json:"fps"`
	Files          map[string]string `json:"files"`
	Reviews        []struct {
		Revision     int             `json:"revision"`
		StateVersion int             `json:"state_version"`
		Action       string          `json:"action"`
		Status       string          `json:"status"`
		Checks       map[string]bool `json:"checks"`
	} `json:"review_history"`
}
type newsSnapshot struct {
	Schema   int `json:"schema_version"`
	Document struct {
		OutputLanguage string   `json:"output_language,omitempty"`
		Title          string   `json:"title"`
		Text           string   `json:"script_text"`
		Target         int      `json:"target_duration_sec"`
		Visual         string   `json:"visual_notes"`
		Notes          string   `json:"notes"`
		SourceKeys     []string `json:"source_keys"`
	} `json:"document"`
	Sources []map[string]any `json:"sources"`
}
type newsPackage struct {
	Dir           string
	Manifest      newsManifest
	Files         map[string]NewsFile
	ArchiveSHA256 string
}

func hashReader(r io.Reader) (string, int64, error) {
	h := sha256.New()
	n, e := io.Copy(h, r)
	return hex.EncodeToString(h.Sum(nil)), n, e
}
func hashBytes(b []byte) string { s := sha256.Sum256(b); return hex.EncodeToString(s[:]) }

// Validate flat names, entry types, CRCs, sizes and every manifest digest before
// moving anything into a project. Never invoke an external unzip executable.
func unpackNews(f multipart.File, size int64, dir, kind string) (newsPackage, error) {
	out := newsPackage{Dir: dir, Files: map[string]NewsFile{}}
	limit := scriptBundleLimit
	names := []string{"manifest.json", "script.txt", "script.json", "revision.json", "sources.json", "assets_manifest.json", "script.md"}
	if kind == "news2douyin.video" {
		limit = videoLimit
		names = []string{"manifest.json", "video.mp4", "narration.wav", "subtitles.srt", "cover.png", "script_snapshot.json"}
	}
	if size <= 0 || size > limit {
		return out, errors.New("制作包超过大小限制")
	}
	z, e := zip.NewReader(f, size)
	if e != nil {
		return out, errors.New("无效的 ZIP 制作包")
	}
	allowed := map[string]bool{}
	for _, n := range names {
		allowed[n] = true
	}
	if len(z.File) < len(names) || kind == "news2douyin.video" && len(z.File) != len(names) || kind != "news2douyin.video" && len(z.File) > len(names)+6 {
		return out, errors.New("制作包文件数量不符")
	}
	total := uint64(0)
	seen := map[string]bool{}
	for _, entry := range z.File {
		optional := kind == "news2douyin.script_package" && (entry.Name == "storyboard.json" || entry.Name == "evidence_snapshot.json" || newsImagePath.MatchString(entry.Name))
		if (!allowed[entry.Name] && !optional) || seen[entry.Name] || !entry.Mode().IsRegular() {
			return out, errors.New("制作包含重复、未知或非普通文件")
		}
		seen[entry.Name] = true
		total += entry.UncompressedSize64
		if kind == "news2douyin.script_package" && entry.UncompressedSize64 > uint64(scriptLimit) {
			return out, errors.New("制作包单文件超过 4 MiB")
		}
		if entry.UncompressedSize64 > uint64(limit) || total > uint64(limit) {
			return out, errors.New("制作包解压大小超限")
		}
		// Metadata and captions remain small even in a media bundle.
		if entry.Name == "manifest.json" && entry.UncompressedSize64 > 128<<10 {
			return out, errors.New("制作清单过大")
		}
		if (strings.HasSuffix(entry.Name, ".json") || strings.HasSuffix(entry.Name, ".srt")) && entry.UncompressedSize64 > uint64(scriptLimit) {
			return out, errors.New("制作包文字资料过大")
		}
		if newsImagePath.MatchString(entry.Name) {
			if entry.UncompressedSize64 > 4<<20 {
				return out, errors.New("图片超过 4 MiB")
			}
			if e = os.MkdirAll(filepath.Join(dir, "images"), 0700); e != nil {
				return out, e
			}
		}
		src, err := entry.Open()
		if err != nil {
			return out, err
		}
		dest, err := os.OpenFile(filepath.Join(dir, entry.Name), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err != nil {
			src.Close()
			return out, err
		}
		h := sha256.New()
		n, copyErr := io.Copy(io.MultiWriter(dest, h), io.LimitReader(src, int64(entry.UncompressedSize64)+1))
		closeErr := dest.Close()
		src.Close()
		if copyErr != nil {
			return out, copyErr
		}
		if closeErr != nil {
			return out, closeErr
		}
		if uint64(n) != entry.UncompressedSize64 {
			return out, errors.New("ZIP 文件长度不符")
		}
		out.Files[entry.Name] = NewsFile{hex.EncodeToString(h.Sum(nil)), n}
	}
	for _, name := range names {
		if !seen[name] {
			return out, errors.New("制作包缺少必要文件")
		}
	}
	raw, e := os.ReadFile(filepath.Join(dir, "manifest.json"))
	if e != nil {
		return out, e
	}
	if e = json.Unmarshal(raw, &out.Manifest); e != nil {
		return out, errors.New("制作清单格式错误")
	}
	m := out.Manifest
	if m.Schema != 1 || m.Kind != kind || len(m.Files) != len(out.Files)-1 {
		return out, errors.New("不支持的制作包版本或类型")
	}
	for name, file := range out.Files {
		if name == "manifest.json" {
			continue
		}
		if !shaPattern.MatchString(m.Files[name]) || file.SHA256 != m.Files[name] {
			return out, fmt.Errorf("文件校验失败：%s", name)
		}
	}
	if _, e = f.Seek(0, io.SeekStart); e != nil {
		return out, e
	}
	dest, e := os.OpenFile(filepath.Join(dir, "bundle.zip"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if e != nil {
		return out, e
	}
	h := sha256.New()
	n, e := io.Copy(io.MultiWriter(dest, h), io.LimitReader(f, limit+1))
	ce := dest.Close()
	if e != nil {
		return out, e
	}
	if ce != nil {
		return out, ce
	}
	if n != size {
		return out, errors.New("上传包长度不符")
	}
	out.ArchiveSHA256 = hex.EncodeToString(h.Sum(nil))
	out.Files["bundle.zip"] = NewsFile{out.ArchiveSHA256, n}
	return out, nil
}

func validateNewsScript(pkg newsPackage) (newsSnapshot, error) {
	var s newsSnapshot
	m := pkg.Manifest
	if m.Status != "approved" || m.Revision < 1 || m.StateVersion < 1 || m.PackageKey == "" || m.ExportKey == "" || len(m.PackageKey) > 200 || len(m.ExportKey) > 200 || len(m.EventKey) > 200 || m.DurationKind != "editorial_target" {
		return s, errors.New("仅接受带版本和来源的已审核脚本制作包")
	}
	if !shaPattern.MatchString(m.ContentHash) || pkg.Files["revision.json"].SHA256 != m.ContentHash {
		return s, errors.New("审核稿快照哈希不符")
	}
	approved := false
	for _, r := range m.Reviews {
		if r.Revision == m.Revision && r.StateVersion == m.StateVersion && r.Action == "approve" && r.Status == "approved" && r.Checks["sources_checked"] && r.Checks["wording_checked"] {
			approved = true
		}
	}
	if !approved {
		return s, errors.New("缺少当前状态版本的审核记录")
	}
	raw, e := os.ReadFile(filepath.Join(pkg.Dir, "revision.json"))
	if e != nil {
		return s, e
	}
	if !utf8.Valid(raw) || json.Unmarshal(raw, &s) != nil || s.Schema != 1 {
		return s, errors.New("脚本快照格式不正确")
	}
	d := s.Document
	if d.OutputLanguage != m.OutputLanguage {
		return s, errors.New("脚本语言与清单不一致")
	}
	switch d.OutputLanguage {
	case "", "zh-CN", "en", "ja", "source":
	default:
		return s, errors.New("不支持的脚本语言")
	}
	if strings.TrimSpace(d.Title) == "" || utf8.RuneCountInString(d.Title) > 300 || strings.TrimSpace(d.Text) == "" || utf8.RuneCountInString(d.Text) > 50000 || d.Target < 10 || d.Target > 600 || d.Target != m.TargetDuration {
		return s, errors.New("脚本标题、正文或目标时长不合法")
	}
	text, e := os.ReadFile(filepath.Join(pkg.Dir, "script.txt"))
	if e != nil {
		return s, e
	}
	if string(text) != d.Text {
		return s, errors.New("口播正文与审核稿不一致")
	}
	selected := map[string]bool{}
	if len(d.SourceKeys) == 0 || len(d.SourceKeys) > 32 {
		return s, errors.New("脚本来源数量不正确")
	}
	for _, key := range d.SourceKeys {
		if key == "" || selected[key] {
			return s, errors.New("来源标识重复或为空")
		}
		selected[key] = true
	}
	expected := []map[string]any{}
	seen := map[string]bool{}
	for _, source := range s.Sources {
		key, ok := source["article_key"].(string)
		if !ok || key == "" || seen[key] {
			return s, errors.New("快照中的来源标识无效")
		}
		seen[key] = true
		if selected[key] {
			expected = append(expected, source)
		}
	}
	var sources []map[string]any
	raw, e = os.ReadFile(filepath.Join(pkg.Dir, "sources.json"))
	if e != nil {
		return s, e
	}
	if json.Unmarshal(raw, &sources) != nil || len(expected) != len(selected) || !reflect.DeepEqual(sources, expected) {
		return s, errors.New("所选来源与审核快照不一致")
	}
	return s, nil
}

func shortTitle(text string) string {
	r := []rune(text)
	if len(r) > 80 {
		return string(r[:79]) + "…"
	}
	return text
}
func newsShots(s newsSnapshot) ([]Shot, error) {
	// All narration characters are retained. Timing is only an editable estimate.
	remaining := []rune(s.Document.Text)
	parts := []string{}
	for len(remaining) > 0 {
		cut := len(remaining)
		if cut > 220 {
			cut = 220
			for i := 219; i >= 100; i-- {
				if strings.ContainsRune("。！？；\n", remaining[i]) {
					cut = i + 1
					break
				}
			}
		}
		parts = append(parts, string(remaining[:cut]))
		remaining = remaining[cut:]
	}
	if len(parts) > 80 {
		return nil, errors.New("正文超过视频 APP 的 80 镜头容量，请在新闻工作台拆分脚本")
	}
	shots := []Shot{}
	used := 0.0
	for i, part := range parts {
		seconds := float64(s.Document.Target) * float64(utf8.RuneCountInString(part)) / float64(utf8.RuneCountInString(s.Document.Text))
		seconds = math.Round(math.Min(60, math.Max(1, seconds))*1000) / 1000
		seconds = math.Min(seconds, 600-used-float64(len(parts)-i-1))
		shots = append(shots, Shot{ID: uid(), Title: shortTitle(s.Document.Title), Text: part, Duration: seconds})
		used += seconds
	}
	return shots, nil
}

func (a *App) receiveNews(w http.ResponseWriter, r *http.Request, kind string) (newsPackage, func(), error) {
	limit := scriptBundleLimit
	if kind == "news2douyin.video" {
		limit = videoLimit
	}
	cleanup := func() {}
	r.Body = http.MaxBytesReader(w, r.Body, limit+(1<<20))
	if e := r.ParseMultipartForm(1 << 20); e != nil {
		if r.MultipartForm != nil {
			r.MultipartForm.RemoveAll()
		}
		return newsPackage{}, cleanup, e
	}
	cleanup = func() { r.MultipartForm.RemoveAll() }
	f, h, e := r.FormFile("file")
	if e != nil {
		return newsPackage{}, cleanup, e
	}
	defer f.Close()
	dir, e := os.MkdirTemp(a.data, "news-upload-")
	if e != nil {
		return newsPackage{}, cleanup, e
	}
	cleanup = func() { r.MultipartForm.RemoveAll(); os.RemoveAll(dir) }
	pkg, e := unpackNews(f, h.Size, dir, kind)
	return pkg, cleanup, e
}
func (a *App) importNews(w http.ResponseWriter, r *http.Request) {
	pkg, cleanup, e := a.receiveNews(w, r, "news2douyin.script_package")
	defer cleanup()
	if e != nil {
		fail(w, 400, e)
		return
	}
	snapshot, e := validateNewsScript(pkg)
	if e != nil {
		fail(w, 422, e)
		return
	}
	images, e := newsImages(pkg)
	if e != nil {
		fail(w, 422, e)
		return
	}
	shots, e := newsShots(snapshot)
	if e != nil {
		fail(w, 422, e)
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, p := range a.projects {
		if p.News == nil {
			continue
		}
		if p.News.ArchiveSHA256 == pkg.ArchiveSHA256 {
			writeJSON(w, 200, clone(p))
			return
		}
		if p.News.PackageKey == pkg.Manifest.PackageKey && p.News.ExportKey == pkg.Manifest.ExportKey {
			fail(w, 409, errors.New("相同导出标识已有不同制作包，拒绝覆盖"))
			return
		}
	}
	if a.closing {
		fail(w, 503, errors.New("服务正在停止"))
		return
	}
	m := pkg.Manifest
	wf := newProjectWorkflow()
	p := Project{Workflow: &wf, NarrationText: snapshot.Document.Text, ID: uid(), Name: shortTitle(snapshot.Document.Title), Brief: snapshot.Document.Visual + "\n" + snapshot.Document.Notes, Revision: 1, Shots: shots, Assets: []Asset{}, Renders: []Render{}, News: &NewsOrigin{OutputLanguage: m.OutputLanguage, PackageKey: m.PackageKey, EventKey: m.EventKey, ExportKey: m.ExportKey, Revision: m.Revision, StateVersion: m.StateVersion, ContentHash: m.ContentHash, ArchiveSHA256: pkg.ArchiveSHA256, ImportedAt: time.Now().UTC().Format(time.RFC3339Nano), TargetDuration: m.TargetDuration, Files: pkg.Files}}
	// Stage the complete directory. The single rename publishes only a complete
	// project; a crash before it leaves an ignored import directory, not a project.
	staged, e := os.MkdirTemp(a.data, "news-project-")
	if e != nil {
		fail(w, 500, e)
		return
	}
	defer os.RemoveAll(staged)
	if e = os.MkdirAll(filepath.Join(staged, "workspace", "assets"), 0700); e == nil {
		e = os.Rename(pkg.Dir, filepath.Join(staged, "news"))
	}
	if e == nil {
		for _, asset := range images {
			if e = copyFile(filepath.Join(staged, "news", asset.Source.ArchivePath), filepath.Join(staged, "workspace", "assets", asset.File)); e != nil {
				break
			}
			p.Assets = append(p.Assets, asset)
		}
	}
	p.Updated = p.News.ImportedAt
	if e == nil {
		e = atomicJSON(filepath.Join(staged, "project.json"), p)
	}
	if e == nil {
		e = os.Rename(staged, a.dir(p.ID))
	}
	if e != nil {
		fail(w, 500, e)
		return
	}
	a.projects[p.ID] = &p
	writeJSON(w, 201, p)
}

func readNewsFile(pdir, group, name string, entry NewsFile) (*os.File, error) {
	root, e := os.OpenRoot(filepath.Join(pdir, "news", group))
	if e != nil {
		return nil, e
	}
	defer root.Close()
	f, e := root.Open(name)
	if e != nil {
		return nil, e
	}
	sum, size, e := hashReader(f)
	if e != nil || sum != entry.SHA256 || size != entry.Size {
		f.Close()
		return nil, errors.New("新闻交接文件校验失败")
	}
	if _, e = f.Seek(0, io.SeekStart); e != nil {
		f.Close()
		return nil, e
	}
	return f, nil
}
func (a *App) newsDetail(w http.ResponseWriter, r *http.Request) {
	p, e := a.get(r.PathValue("id"))
	if e != nil || p.News == nil {
		fail(w, 404, errors.New("项目没有新闻来源包"))
		return
	}
	data := map[string]any{"origin": p.News, "storyboardStatus": "production_draft", "timing": "estimated_not_audio_aligned"}
	for name, key := range map[string]string{"manifest.json": "manifest", "revision.json": "snapshot", "sources.json": "sources"} {
		f, e := readNewsFile(a.dir(p.ID), "", name, p.News.Files[name])
		if e != nil {
			fail(w, 409, e)
			return
		}
		raw, e := io.ReadAll(f)
		f.Close()
		if e != nil {
			fail(w, 500, e)
			return
		}
		data[key] = json.RawMessage(raw)
	}
	writeJSON(w, 200, data)
}
func (a *App) newsFile(w http.ResponseWriter, r *http.Request) {
	p, e := a.get(r.PathValue("id"))
	if e != nil || p.News == nil {
		fail(w, 404, errors.New("项目没有新闻来源包"))
		return
	}
	group, name := r.PathValue("group"), r.PathValue("file")
	files := p.News.Files
	folder := ""
	if group == "video" && p.News.Video != nil && identifier.MatchString(p.News.Video.ID) {
		files = p.News.Video.Files
		folder = filepath.Join("videos", p.News.Video.ID)
	} else if group != "script" {
		fail(w, 404, errors.New("文件不存在"))
		return
	}
	entry, ok := files[name]
	if !ok || filepath.Base(name) != name {
		fail(w, 404, errors.New("文件不存在"))
		return
	}
	f, e := readNewsFile(a.dir(p.ID), folder, name, entry)
	if e != nil {
		fail(w, 409, e)
		return
	}
	defer f.Close()
	info, e := f.Stat()
	if e != nil {
		fail(w, 500, e)
		return
	}
	mime := map[string]string{".mp4": "video/mp4", ".wav": "audio/wav", ".png": "image/png", ".zip": "application/zip", ".json": "application/json", ".srt": "application/x-subrip", ".txt": "text/plain; charset=utf-8", ".md": "text/plain; charset=utf-8"}[filepath.Ext(name)]
	w.Header().Set("Content-Type", mime)
	w.Header().Set("ETag", `"`+entry.SHA256+`"`)
	if r.URL.Query().Has("download") {
		w.Header().Set("Content-Disposition", `attachment; filename="`+name+`"`)
	}
	http.ServeContent(w, r, name, info.ModTime(), f)
}

func compactNewsText(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) {
			return -1
		}
		return r
	}, s)
}

var srtTime = regexp.MustCompile(`^(\d{2,}):([0-5]\d):([0-5]\d)[,.](\d{3}) --> (\d{2,}):([0-5]\d):([0-5]\d)[,.](\d{3})$`)

func validateNewsSRT(raw, text string, duration float64) error {
	raw = strings.ReplaceAll(strings.TrimSpace(strings.TrimPrefix(raw, "\ufeff")), "\r\n", "\n")
	blocks := regexp.MustCompile(`\n[\t ]*\n`).Split(raw, -1)
	if len(blocks) == 0 || len(blocks) > 300 {
		return errors.New("字幕分段数量不符")
	}
	end := 0.0
	var body strings.Builder
	for _, block := range blocks {
		lines := strings.Split(strings.TrimSpace(block), "\n")
		if len(lines) < 3 {
			return errors.New("SRT 格式无效")
		}
		if _, e := strconv.Atoi(lines[0]); e != nil {
			return errors.New("SRT 序号无效")
		}
		match := srtTime.FindStringSubmatch(lines[1])
		if match == nil {
			return errors.New("SRT 时间无效")
		}
		times := []float64{}
		for _, base := range []int{1, 5} {
			v := 0.0
			for i, mult := range []float64{3600, 60, 1, .001} {
				n, _ := strconv.ParseFloat(match[base+i], 64)
				v += n * mult
			}
			times = append(times, v)
		}
		if !finite(times[0]) || !finite(times[1]) || times[0] < end || times[1] <= times[0] || times[1] > duration+.15 {
			return errors.New("SRT 时间重叠或超出成片")
		}
		content := strings.Join(lines[2:], "\n")
		if strings.TrimSpace(content) == "" {
			return errors.New("SRT 字幕为空")
		}
		body.WriteString(content)
		end = times[1]
	}
	if compactNewsText(body.String()) != compactNewsText(text) {
		return errors.New("字幕正文与审核稿不一致")
	}
	return nil
}

func verifyNewsVideo(ctx context.Context, pkg newsPackage, origin *NewsOrigin) error {
	m := pkg.Manifest
	if m.ExportKey != origin.ExportKey || m.Revision != origin.Revision || m.ScriptHash != origin.ContentHash || pkg.Files["script_snapshot.json"].SHA256 != origin.ContentHash || m.TaskID == "" || len(m.TaskID) > 200 {
		return errors.New("成片不属于此项目的审核稿版本")
	}
	if !finite(m.Duration) || m.Duration < .1 || m.Duration > 600.3 || !((m.Width == 360 && m.Height == 640 && m.FPS == 12) || (m.Width == 720 && m.Height == 1280 && m.FPS == 24) || (m.Width == 1080 && m.Height == 1920 && m.FPS == 24)) {
		return errors.New("成片规格不在支持范围内")
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "ffprobe", "-v", "error", "-protocol_whitelist", "file,pipe", "-show_entries", "stream=codec_type,codec_name,width,height,duration,avg_frame_rate,nb_frames:format=duration", "-of", "json", filepath.Join(pkg.Dir, "video.mp4"))
	cmd.Env = rendererEnvironment()
	raw, e := cmd.Output()
	if e != nil {
		return errors.New("FFprobe 成片检查失败，请确认已安装 FFprobe")
	}
	var media struct {
		Streams []struct {
			Type     string `json:"codec_type"`
			Codec    string `json:"codec_name"`
			Width    int    `json:"width"`
			Height   int    `json:"height"`
			Duration string `json:"duration"`
			FPS      string `json:"avg_frame_rate"`
			Frames   string `json:"nb_frames"`
		} `json:"streams"`
		Format struct {
			Duration string `json:"duration"`
		} `json:"format"`
	}
	if json.Unmarshal(raw, &media) != nil || len(media.Streams) != 2 {
		return errors.New("成片媒体流不正确")
	}
	video, audio := false, false
	for _, s := range media.Streams {
		duration, e := strconv.ParseFloat(s.Duration, 64)
		if e != nil || !finite(duration) || math.Abs(duration-m.Duration) > .15 {
			return errors.New("音视频流时长不完整")
		}
		if s.Type == "video" {
			frames, _ := strconv.Atoi(s.Frames)
			ratio := strings.Split(s.FPS, "/")
			fps := 0.0
			if len(ratio) == 2 {
				n, _ := strconv.ParseFloat(ratio[0], 64)
				d, _ := strconv.ParseFloat(ratio[1], 64)
				if d != 0 {
					fps = n / d
				}
			}
			video = s.Codec == "h264" && s.Width == m.Width && s.Height == m.Height && math.Abs(fps-float64(m.FPS)) < .001 && frames >= int(m.Duration*float64(m.FPS))-1
		}
		if s.Type == "audio" {
			audio = s.Codec == "aac"
		}
	}
	duration, e := strconv.ParseFloat(media.Format.Duration, 64)
	if !video || !audio || e != nil || !finite(duration) || math.Abs(duration-m.Duration) > .15 {
		return errors.New("成片时长、编码或分辨率不符")
	}
	wav, e := os.Open(filepath.Join(pkg.Dir, "narration.wav"))
	if e != nil {
		return e
	}
	header := make([]byte, 12)
	_, e = io.ReadFull(wav, header)
	wav.Close()
	if e != nil || string(header[:4]) != "RIFF" || string(header[8:12]) != "WAVE" {
		return errors.New("配音文件不是有效 WAV")
	}
	spokenDuration, e := audioDuration(ctx, filepath.Join(pkg.Dir, "narration.wav"))
	if e != nil || math.Abs(spokenDuration-m.Duration) > .15 {
		return errors.New("配音时长与成片不符")
	}
	cover, e := os.Open(filepath.Join(pkg.Dir, "cover.png"))
	if e != nil {
		return e
	}
	dimensions, e := png.DecodeConfig(cover)
	cover.Close()
	if e != nil || dimensions.Width != m.Width || dimensions.Height != m.Height {
		return errors.New("封面格式或尺寸与成片不符")
	}
	snapshotRaw, e := os.ReadFile(filepath.Join(pkg.Dir, "script_snapshot.json"))
	if e != nil {
		return e
	}
	var snapshot newsSnapshot
	if json.Unmarshal(snapshotRaw, &snapshot) != nil {
		return errors.New("成片脚本快照无效")
	}
	subtitles, e := os.ReadFile(filepath.Join(pkg.Dir, "subtitles.srt"))
	if e != nil {
		return e
	}
	return validateNewsSRT(string(subtitles), snapshot.Document.Text, m.Duration)
}
func (a *App) importNewsVideo(w http.ResponseWriter, r *http.Request) {
	p, e := a.get(r.PathValue("id"))
	if e != nil || p.News == nil {
		fail(w, 404, errors.New("请先导入对应的审核脚本制作包"))
		return
	}
	pkg, cleanup, e := a.receiveNews(w, r, "news2douyin.video")
	defer cleanup()
	if e != nil {
		fail(w, 400, e)
		return
	}
	if p.News.Video != nil && p.News.Video.ArchiveSHA256 == pkg.ArchiveSHA256 {
		writeJSON(w, 200, p)
		return
	}
	revision, e := strconv.Atoi(r.FormValue("revision"))
	if e != nil || revision != p.Revision {
		fail(w, 409, errors.New("项目已变化，请刷新后重新导入成片"))
		return
	}
	if p.News.Video != nil {
		fail(w, 409, errors.New("已有交接成片；同一来源包仅保留一份原始成片"))
		return
	}
	if e = verifyNewsVideo(r.Context(), pkg, p.News); e != nil {
		fail(w, 422, e)
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	q := clone(a.projects[p.ID])
	if q.News.Video != nil && q.News.Video.ArchiveSHA256 == pkg.ArchiveSHA256 {
		writeJSON(w, 200, q)
		return
	}
	if q.Revision != revision || q.News.Video != nil {
		fail(w, 409, errors.New("项目已变化，请刷新后重新导入成片"))
		return
	}
	m := pkg.Manifest
	id := uid()
	dest := filepath.Join(a.dir(p.ID), "news", "videos", id)
	if e = os.MkdirAll(filepath.Dir(dest), 0700); e == nil {
		e = os.Rename(pkg.Dir, dest)
	}
	if e != nil {
		fail(w, 500, e)
		return
	}
	q.News.Video = &NewsVideo{ID: id, TaskID: m.TaskID, ArchiveSHA256: pkg.ArchiveSHA256, ImportedAt: time.Now().UTC().Format(time.RFC3339Nano), Duration: m.Duration, Width: m.Width, Height: m.Height, FPS: m.FPS, Files: pkg.Files}
	q.Revision++
	if e = a.save(&q); e != nil {
		os.RemoveAll(dest)
		fail(w, 500, e)
		return
	}
	a.projects[q.ID] = &q
	writeJSON(w, 201, q)
}
