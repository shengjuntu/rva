package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const authoredHyperFramesEngine = "hyperframes_authored"
const authoredHyperFramesVersion = "0.8.87"

type hyperFramesFrozenFile struct {
	Path    string `json:"path"`
	SHA256  string `json:"sha256"`
	Size    int64  `json:"size"`
	Kind    string `json:"kind"`
	AssetID string `json:"assetId,omitempty"`
}

type hyperFramesManifest struct {
	SchemaVersion  int                     `json:"schemaVersion"`
	Engine         string                  `json:"engine"`
	EngineVersion  string                  `json:"engineVersion"`
	ProjectID      string                  `json:"projectId"`
	Revision       int                     `json:"revision"`
	DocumentHash   string                  `json:"documentHash"`
	TextSourceHash string                  `json:"textSourceHash"`
	SnapshotHash   string                  `json:"snapshotHash"`
	Files          []hyperFramesFrozenFile `json:"files"`
}

type hyperFramesCheckResult struct {
	ID             string `json:"id"`
	Status         string `json:"status"`
	Revision       int    `json:"revision"`
	DocumentHash   string `json:"documentHash"`
	TextSourceHash string `json:"textSourceHash"`
	SnapshotHash   string `json:"snapshotHash"`
	Engine         string `json:"engine"`
	EngineVersion  string `json:"engineVersion"`
	Rendered       bool   `json:"rendered"`
	Log            string `json:"log"`
	Error          string `json:"error,omitempty"`
}

// The version is a deployment contract, not a floating npm package selector.
// No dependency installation or network access is performed by a render request.
func (a *App) authoredHyperFramesReady() error {
	for _, binary := range []string{"node", "ffmpeg", "ffprobe"} {
		if _, e := exec.LookPath(binary); e != nil {
			return fmt.Errorf("自由 HyperFrames 制作缺少 %s", binary)
		}
	}
	for name, path := range map[string]string{"HyperFrames CLI": a.hf, "GSAP": a.gsap, "字体": a.font} {
		if !regularFile(path) {
			return fmt.Errorf("自由 HyperFrames 制作需要配置本地 %s 文件", name)
		}
	}
	raw, e := os.ReadFile(filepath.Join(filepath.Dir(a.hf), "..", "package.json"))
	if e != nil {
		return errors.New("无法核对 HyperFrames 安装版本，请运行 scripts/setup-renderer.sh")
	}
	var pkg struct{ Name, Version string }
	if json.Unmarshal(raw, &pkg) != nil || pkg.Name != "hyperframes" || pkg.Version != authoredHyperFramesVersion {
		return fmt.Errorf("自由工程需要固定的 HyperFrames %s，请运行 scripts/setup-renderer.sh", authoredHyperFramesVersion)
	}
	return nil
}

// freezeAuthoredHyperFrames publishes a complete directory with one rename.
// Before it returns, every mutable input has been copied and checked against
// the author's saved fingerprints. The asynchronous worker never reads assets
// from the live project directory.
func (a *App) freezeAuthoredHyperFrames(p Project, destination string) (hyperFramesManifest, error) {
	manifest := hyperFramesManifest{SchemaVersion: 1, Engine: authoredHyperFramesEngine, EngineVersion: authoredHyperFramesVersion, ProjectID: p.ID, Revision: p.Revision}
	if p.HyperFrames == nil {
		return manifest, errors.New("尚未保存自由 HyperFrames 工程")
	}
	d := *p.HyperFrames
	if e := validateHyperFramesProject(p, d); e != nil {
		return manifest, e
	}
	if d.EntryPoint != "index.html" {
		return manifest, errors.New("当前 HyperFrames check/render 统一使用 index.html 入口")
	}
	assets, e := a.hyperFramesAssetFiles(p, d)
	if e != nil {
		return manifest, e
	}
	if e = a.authoredHyperFramesReady(); e != nil {
		return manifest, e
	}
	parent := filepath.Dir(destination)
	if e = os.MkdirAll(parent, 0700); e != nil {
		return manifest, e
	}
	staging, e := os.MkdirTemp(parent, ".hyperframes-")
	if e != nil {
		return manifest, e
	}
	defer os.RemoveAll(staging)
	manifest.DocumentHash, manifest.TextSourceHash = hyperFramesDocumentHash(d), d.TextSourceHash
	writeSource := func(path string, content []byte, kind string) error {
		dst := filepath.Join(staging, filepath.FromSlash(path))
		if e := os.MkdirAll(filepath.Dir(dst), 0700); e != nil {
			return e
		}
		f, e := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if e != nil {
			return e
		}
		_, e = f.Write(content)
		ce := f.Close()
		if e != nil {
			return e
		}
		if ce != nil {
			return ce
		}
		manifest.Files = append(manifest.Files, hyperFramesFrozenFile{Path: path, SHA256: hashBytes(content), Size: int64(len(content)), Kind: kind})
		return nil
	}
	for _, file := range d.Files {
		if e = writeSource(file.Path, []byte(file.Content), "source"); e != nil {
			return manifest, e
		}
	}
	projectRoot, e := os.OpenRoot(a.dir(p.ID))
	if e != nil {
		return manifest, e
	}
	defer projectRoot.Close()
	for _, asset := range assets {
		rel, e := filepath.Rel(a.dir(p.ID), asset.SourceFile)
		if e != nil || !filepath.IsLocal(rel) {
			return manifest, errors.New("素材不属于当前项目")
		}
		in, e := projectRoot.Open(rel)
		if e != nil {
			return manifest, e
		}
		item, e := freezeHyperFramesFile(in, staging, asset.Path, "asset", asset.AssetID)
		in.Close()
		if e != nil {
			return manifest, e
		}
		if item.SHA256 != asset.SHA256 || item.Size != asset.Size {
			return manifest, errors.New("冻结时素材已改变，请重新保存工程")
		}
		manifest.Files = append(manifest.Files, item)
	}
	for _, dep := range []struct{ source, target string }{{a.gsap, "runtime/gsap.min.js"}, {a.font, "runtime/font" + strings.ToLower(filepath.Ext(a.font))}} {
		in, e := os.Open(dep.source)
		if e != nil {
			return manifest, e
		}
		item, e := freezeHyperFramesFile(in, staging, dep.target, "runtime", "")
		in.Close()
		if e != nil {
			return manifest, e
		}
		manifest.Files = append(manifest.Files, item)
	}
	if e = writeSource("runtime/font.css", []byte(fmt.Sprintf("@font-face{font-family:VideoFont;src:url('font%s')}\n", strings.ToLower(filepath.Ext(a.font)))), "runtime"); e != nil {
		return manifest, e
	}
	if e = atomicJSON(filepath.Join(staging, "snapshot.json"), p); e != nil {
		return manifest, e
	}
	projectHash, projectSize, e := hashLocal(filepath.Join(staging, "snapshot.json"))
	if e != nil {
		return manifest, e
	}
	manifest.Files = append(manifest.Files, hyperFramesFrozenFile{Path: "snapshot.json", SHA256: projectHash, Size: projectSize, Kind: "project"})
	// Bind all original evidence, including the project snapshot, before the
	// engine runs. A later rewritten manifest cannot replace this admission key.
	canonical, _ := json.Marshal(manifest)
	manifest.SnapshotHash = hashBytes(canonical)
	if e = atomicJSON(filepath.Join(staging, "hyperframes-manifest.json"), manifest); e != nil {
		return manifest, e
	}
	if _, e = os.Lstat(destination); e == nil {
		return manifest, errors.New("快照目录已存在")
	} else if !os.IsNotExist(e) {
		return manifest, e
	}
	if e = os.Rename(staging, destination); e != nil {
		return manifest, e
	}
	return manifest, nil
}

func freezeHyperFramesFile(in *os.File, dir, path, kind, assetID string) (hyperFramesFrozenFile, error) {
	item := hyperFramesFrozenFile{Path: path, Kind: kind, AssetID: assetID}
	if !filepath.IsLocal(filepath.FromSlash(path)) || strings.Contains(path, "\\") {
		return item, errors.New("快照文件路径无效")
	}
	info, e := in.Stat()
	if e != nil || !info.Mode().IsRegular() {
		return item, errors.New("快照输入必须是普通文件")
	}
	dst := filepath.Join(dir, filepath.FromSlash(path))
	if e = os.MkdirAll(filepath.Dir(dst), 0700); e != nil {
		return item, e
	}
	out, e := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if e != nil {
		return item, e
	}
	written, e := io.Copy(out, io.LimitReader(in, info.Size()+1))
	ce := out.Close()
	if e != nil {
		return item, e
	}
	if ce != nil {
		return item, ce
	}
	after, e := in.Stat()
	if e != nil || written != info.Size() || after.Size() != info.Size() || !after.ModTime().Equal(info.ModTime()) {
		return item, errors.New("快照输入在复制期间发生变化")
	}
	frozen, e := os.Open(dst)
	if e != nil {
		return item, e
	}
	item.SHA256, item.Size, e = hashReader(frozen)
	frozen.Close()
	return item, e
}

func (a *App) hyperFramesCheck(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Revision int `json:"revision"`
	}
	if !decodeStrict(w, r, &in) {
		return
	}
	if in.Revision < 1 {
		fail(w, 400, errors.New("check 需要当前 revision"))
		return
	}
	id, checkID := r.PathValue("id"), uid()
	dir := filepath.Join(a.dir(id), "checks", checkID)
	ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
	defer cancel()
	a.mu.Lock()
	p := a.projects[id]
	if p == nil {
		a.mu.Unlock()
		fail(w, 404, errors.New("项目不存在"))
		return
	}
	if a.closing {
		a.mu.Unlock()
		fail(w, 503, errors.New("服务正在停止"))
		return
	}
	if p.Revision != in.Revision {
		a.mu.Unlock()
		fail(w, 409, errors.New("项目版本冲突，请重新载入"))
		return
	}
	if len(a.jobs) > 0 {
		a.mu.Unlock()
		fail(w, 409, errors.New("已有检查或渲染任务运行"))
		return
	}
	manifest, e := a.freezeAuthoredHyperFrames(clone(p), dir)
	if e != nil {
		a.mu.Unlock()
		fail(w, 422, e)
		return
	}
	a.jobs[checkID] = cancel
	a.wg.Add(1)
	a.mu.Unlock()
	defer func() {
		a.mu.Lock()
		delete(a.jobs, checkID)
		a.mu.Unlock()
		a.wg.Done()
	}()
	result := hyperFramesCheckResult{ID: checkID, Status: "passed", Revision: manifest.Revision, DocumentHash: manifest.DocumentHash, TextSourceHash: manifest.TextSourceHash, SnapshotHash: manifest.SnapshotHash, Engine: manifest.Engine, EngineVersion: manifest.EngineVersion}
	log, e := os.OpenFile(filepath.Join(dir, "render.log"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if e == nil {
		e = a.runAuthoredHyperFrames(ctx, dir, log, "check", dir, "--json")
		log.Close()
	}
	if e == nil {
		checkedManifest, readErr := readHyperFramesManifest(dir)
		if readErr != nil || checkedManifest.SnapshotHash != manifest.SnapshotHash || !hyperFramesFrozenInputsIntact(dir, checkedManifest) {
			e = errors.New("检查期间冻结输入已改变")
		}
	}
	if raw, readErr := os.ReadFile(filepath.Join(dir, "render.log")); readErr == nil {
		if len(raw) > 128<<10 {
			raw = raw[len(raw)-(128<<10):]
		}
		result.Log = string(raw)
	}
	if e != nil {
		result.Status, result.Error = "failed", e.Error()
	}
	if e = atomicJSON(filepath.Join(dir, "check-result.json"), result); e != nil {
		fail(w, 500, e)
		return
	}
	// A successful browser check is not a rendered, validated, or final video.
	writeJSON(w, 200, result)
}

// Called by startRender after strict decoding and before the legacy paths.
func (a *App) startHyperFramesRender(w http.ResponseWriter, r *http.Request, request RenderRequest) {
	if request.Revision < 1 || (request.RequestID != "" && !identifier.MatchString(request.RequestID)) {
		fail(w, 400, errors.New("自由工程渲染需要当前 revision；requestId 必须为24位标识"))
		return
	}
	if request.Kind != "" && request.Kind != "draft" {
		fail(w, 400, errors.New("kind 必须为 draft 或省略"))
		return
	}
	if request.Range != nil {
		fail(w, 422, errors.New("自由 HyperFrames 工程当前仅支持完整渲染；短片预览请保存独立短版工程"))
		return
	}
	raw, _ := json.Marshal(struct {
		Engine  string
		Request RenderRequest
	}{authoredHyperFramesEngine, request})
	requestHash := hashBytes(raw)
	id := r.PathValue("id")
	a.mu.Lock()
	p := a.projects[id]
	if p == nil {
		a.mu.Unlock()
		fail(w, 404, errors.New("项目不存在"))
		return
	}
	if request.RequestID != "" {
		for _, job := range p.Renders {
			if job.RequestID == request.RequestID {
				a.mu.Unlock()
				if job.RequestHash != requestHash {
					fail(w, 409, errors.New("同一 requestId 不能用于不同渲染请求"))
					return
				}
				writeJSON(w, 200, job)
				return
			}
		}
	}
	if workflowFor(*p).EditingBackend != "hyperframes" {
		a.mu.Unlock()
		fail(w, 409, errors.New("当前项目未选择自由 HyperFrames 后端，请重新读取制作配置"))
		return
	}
	if a.closing {
		a.mu.Unlock()
		fail(w, 503, errors.New("服务正在停止"))
		return
	}
	if request.Revision != p.Revision {
		a.mu.Unlock()
		fail(w, 409, errors.New("项目版本冲突，请重新载入"))
		return
	}
	if len(a.jobs) > 0 {
		a.mu.Unlock()
		fail(w, 409, errors.New("已有检查或渲染任务运行"))
		return
	}
	q := clone(p)
	if q.HyperFrames == nil {
		a.mu.Unlock()
		fail(w, 409, errors.New("尚未保存自由 HyperFrames 工程"))
		return
	}
	job := Render{ID: uid(), Revision: q.Revision, Engine: authoredHyperFramesEngine, Kind: request.Kind, RequestID: request.RequestID, RequestHash: requestHash, State: "running", Created: time.Now().UTC().Format(time.RFC3339Nano), Duration: q.HyperFrames.Canvas.Duration, HasAudio: q.HyperFrames.HasAudio, SourceHash: q.HyperFrames.TextSourceHash, ContentHash: productionHash(q)}
	dir := filepath.Join(a.dir(id), "renders", job.ID)
	manifest, e := a.freezeAuthoredHyperFrames(q, dir)
	if e != nil {
		a.mu.Unlock()
		fail(w, 422, e)
		return
	}
	job.CacheKey = manifest.SnapshotHash
	q.Renders = append(q.Renders, job)
	if e := a.save(&q); e != nil {
		os.RemoveAll(dir)
		a.mu.Unlock()
		fail(w, 500, e)
		return
	}
	a.projects[id] = &q
	ctx, cancel := context.WithCancel(context.Background())
	a.jobs[job.ID] = cancel
	a.wg.Add(1)
	a.mu.Unlock()
	go func() {
		defer a.wg.Done()
		defer cancel()
		a.renderAuthoredHyperFrames(ctx, q, job)
	}()
	writeJSON(w, 202, job)
}

func (a *App) runAuthoredHyperFrames(ctx context.Context, dir string, log *os.File, args ...string) error {
	fmt.Fprintln(log, "hyperframes", authoredHyperFramesVersion, strings.Join(args, " "))
	cmd := exec.CommandContext(ctx, "node", append([]string{a.hf}, args...)...)
	cmd.Dir, cmd.Env, cmd.Stdout, cmd.Stderr = dir, rendererEnvironment(), log, log
	configureProcess(cmd)
	return cmd.Run()
}

func (a *App) renderAuthoredHyperFrames(ctx context.Context, p Project, job Render) {
	dir := filepath.Join(a.dir(p.ID), "renders", job.ID)
	log, e := os.OpenFile(filepath.Join(dir, "render.log"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if log != nil {
		defer log.Close()
	}
	if e == nil {
		e = a.runAuthoredHyperFrames(ctx, dir, log, "check", dir, "--json")
	}
	if e == nil {
		e = a.runAuthoredHyperFrames(ctx, dir, log, "render", dir, "--quality", "draft", "--workers", "1", "--fps", strconv.Itoa(p.HyperFrames.Canvas.FPS), "--format", "mp4", "--output", filepath.Join(dir, "final.mp4"))
	}
	if e == nil {
		e = verifyAuthoredHyperFramesOutput(ctx, dir, p.HyperFrames.Canvas, job.HasAudio)
	}
	if e == nil {
		manifest, readErr := readHyperFramesManifest(dir)
		if readErr != nil || manifest.SnapshotHash != job.CacheKey || !hyperFramesFrozenInputsIntact(dir, manifest) {
			e = errors.New("渲染期间冻结输入已改变")
		} else {
			e = a.saveRenderProof(p.ID, job.ID, dir, job.CacheKey, "")
		}
	}
	if e != nil && log != nil {
		fmt.Fprintln(log, "FAILED:", e)
	}
	a.finishRender(ctx, p.ID, job.ID, e)
}

func verifyAuthoredHyperFramesOutput(ctx context.Context, dir string, canvas HyperFramesCanvas, wantAudio bool) error {
	step, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(step, "ffprobe", "-v", "error", "-protocol_whitelist", "file,pipe", "-format_whitelist", "mov", "-f", "mov", "-enable_drefs", "0", "-show_format", "-show_streams", "-of", "json", filepath.Join(dir, "final.mp4"))
	cmd.Env = rendererEnvironment()
	configureProcess(cmd)
	raw, e := cmd.Output()
	if e != nil {
		return fmt.Errorf("无法读取 HyperFrames 成片：%w", e)
	}
	if e = os.WriteFile(filepath.Join(dir, "validation.json"), raw, 0600); e != nil {
		return e
	}
	var result struct {
		Format struct {
			Duration string `json:"duration"`
		} `json:"format"`
		Streams []struct {
			Type     string `json:"codec_type"`
			Codec    string `json:"codec_name"`
			Width    int    `json:"width"`
			Height   int    `json:"height"`
			FPS      string `json:"avg_frame_rate"`
			Duration string `json:"duration"`
			Frames   string `json:"nb_frames"`
		} `json:"streams"`
	}
	if e = json.Unmarshal(raw, &result); e != nil {
		return e
	}
	videoCount, audioCount := 0, 0
	for _, stream := range result.Streams {
		if stream.Type == "audio" {
			audioCount++
			duration, de := strconv.ParseFloat(stream.Duration, 64)
			if stream.Codec != "aac" || de != nil || !finite(duration) || duration <= 0 || math.Abs(duration-canvas.Duration) > .2 {
				return errors.New("HyperFrames 成片音轨编码或时长与工程声明不符")
			}
		}
		if stream.Type == "video" {
			videoCount++
			fps, e := exchangeFrameRate(stream.FPS)
			duration, de := strconv.ParseFloat(stream.Duration, 64)
			frames, fe := strconv.ParseInt(stream.Frames, 10, 64)
			if e != nil || de != nil || fe != nil || !finite(duration) || duration <= 0 || math.Abs(duration-canvas.Duration) > 1/float64(canvas.FPS)+.001 || frames <= 0 || math.Abs(float64(frames)-math.Round(canvas.Duration*float64(canvas.FPS))) > 1 || math.Abs(fps-float64(canvas.FPS)) >= .001 || stream.Width != canvas.Width || stream.Height != canvas.Height || stream.Codec != "h264" {
				return errors.New("HyperFrames 成片画面时长、帧数、尺寸、帧率或编码与工程声明不符")
			}
		}
	}
	duration, e := strconv.ParseFloat(result.Format.Duration, 64)
	if e != nil || !finite(duration) || duration <= 0 || math.Abs(duration-canvas.Duration) > math.Max(.2, 1/float64(canvas.FPS)+.001) || videoCount != 1 || audioCount > 1 || (audioCount == 1) != wantAudio {
		return errors.New("HyperFrames 成片时长、视频轨或音轨与工程声明不符")
	}
	return nil
}

func readHyperFramesManifest(dir string) (hyperFramesManifest, error) {
	var manifest hyperFramesManifest
	root, e := os.OpenRoot(dir)
	if e != nil {
		return manifest, e
	}
	defer root.Close()
	f, e := root.Open("hyperframes-manifest.json")
	if e != nil {
		return manifest, e
	}
	defer f.Close()
	e = json.NewDecoder(io.LimitReader(f, 2<<20)).Decode(&manifest)
	if e != nil {
		return manifest, e
	}
	storedHash := manifest.SnapshotHash
	manifest.SnapshotHash = ""
	raw, _ := json.Marshal(manifest)
	manifest.SnapshotHash = storedHash
	if manifest.SchemaVersion != 1 || manifest.Engine != authoredHyperFramesEngine || manifest.EngineVersion != authoredHyperFramesVersion || len(manifest.Files) == 0 || hashBytes(raw) != storedHash {
		return manifest, errors.New("HyperFrames 冻结清单校验失败")
	}
	return manifest, nil
}

func hyperFramesFrozenInputsIntact(dir string, manifest hyperFramesManifest) bool {
	root, e := os.OpenRoot(dir)
	if e != nil {
		return false
	}
	defer root.Close()
	seen := map[string]bool{}
	for _, item := range manifest.Files {
		if !filepath.IsLocal(filepath.FromSlash(item.Path)) || strings.Contains(item.Path, "\\") || seen[item.Path] {
			return false
		}
		seen[item.Path] = true
		// An archive must contain its own original bytes, not symlinks to live inputs.
		components := strings.Split(filepath.ToSlash(item.Path), "/")
		for i := range components {
			info, err := root.Lstat(filepath.Join(components[:i+1]...))
			if err != nil || info.Mode()&os.ModeSymlink != 0 || (i == len(components)-1 && !info.Mode().IsRegular()) {
				return false
			}
		}
		f, e := root.Open(filepath.FromSlash(item.Path))
		if e != nil {
			return false
		}
		h, n, e := hashReader(f)
		f.Close()
		if e != nil || h != item.SHA256 || n != item.Size {
			return false
		}
	}
	return true
}

// Currentness is separate from archive integrity: editing a live project must
// not make the original render's frozen inputs disappear or silently change.
func (a *App) hyperFramesRenderInputsIntact(p Project, render Render) bool {
	if p.HyperFrames == nil || render.Engine != authoredHyperFramesEngine || !renderCurrent(p, render) || a.authoredHyperFramesReady() != nil {
		return false
	}
	dir := filepath.Join(a.dir(p.ID), "renders", render.ID)
	manifest, e := readHyperFramesManifest(dir)
	if e != nil || manifest.DocumentHash != hyperFramesDocumentHash(*p.HyperFrames) || manifest.TextSourceHash != narrationSourceHash(p) || manifest.SnapshotHash != render.CacheKey {
		return false
	}
	assets, e := a.hyperFramesAssetFiles(p, *p.HyperFrames)
	if e != nil {
		return false
	}
	byPath := map[string]hyperFramesFrozenFile{}
	for _, item := range manifest.Files {
		byPath[item.Path] = item
	}
	for _, asset := range assets {
		item, ok := byPath[asset.Path]
		if !ok || item.Kind != "asset" || item.AssetID != asset.AssetID || item.SHA256 != asset.SHA256 || item.Size != asset.Size {
			return false
		}
	}
	for _, dep := range []struct{ source, target string }{{a.gsap, "runtime/gsap.min.js"}, {a.font, "runtime/font" + strings.ToLower(filepath.Ext(a.font))}} {
		item, ok := byPath[dep.target]
		h, n, e := hashLocal(dep.source)
		if !ok || e != nil || item.Kind != "runtime" || item.SHA256 != h || item.Size != n {
			return false
		}
	}
	return true
}

func (a *App) hyperFramesArchiveIntact(p Project, render Render, proof RenderProof) bool {
	if render.Engine != authoredHyperFramesEngine || render.State != "completed" || render.Range != nil {
		return false
	}
	dir := filepath.Join(a.dir(p.ID), "renders", render.ID)
	manifest, e := readHyperFramesManifest(dir)
	if e != nil || manifest.ProjectID != p.ID || manifest.Revision != render.Revision || manifest.SnapshotHash != render.CacheKey || proof.Key != manifest.SnapshotHash || !hyperFramesFrozenInputsIntact(dir, manifest) {
		return false
	}
	vh, size, ve := hashLocal(filepath.Join(dir, "final.mp4"))
	sh, _, se := hashLocal(filepath.Join(dir, "snapshot.json"))
	if ve != nil || se != nil || vh != proof.VideoHash || size != proof.VideoBytes || sh != proof.SnapshotHash {
		return false
	}
	root, e := os.OpenRoot(dir)
	if e != nil {
		return false
	}
	defer root.Close()
	snap, e := root.Open("snapshot.json")
	if e != nil {
		return false
	}
	defer snap.Close()
	var frozen Project
	if json.NewDecoder(io.LimitReader(snap, 8<<20)).Decode(&frozen) != nil || frozen.HyperFrames == nil || frozen.ID != p.ID || frozen.Revision != render.Revision || hyperFramesDocumentHash(*frozen.HyperFrames) != manifest.DocumentHash || frozen.HyperFrames.TextSourceHash != manifest.TextSourceHash || frozen.HyperFrames.HasAudio != render.HasAudio || frozen.HyperFrames.Canvas.Duration != render.Duration {
		return false
	}
	return true
}
