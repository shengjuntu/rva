package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
	"unicode"
	"unicode/utf8"
)

// HyperFramesDocument is a versioned authoring snapshot, not a scene template.
// Source HTML/CSS/JavaScript remains freeform and is never executed by save/get.
// Timeline is optional Agent-authored editorial metadata, not measured timing.
type HyperFramesDocument struct {
	SchemaVersion  int                       `json:"schemaVersion"`
	EntryPoint     string                    `json:"entrypoint"`
	Canvas         HyperFramesCanvas         `json:"canvas"`
	HasAudio       bool                      `json:"hasAudio"`
	Files          []HyperFramesSourceFile   `json:"files"`
	Assets         []HyperFramesAssetBinding `json:"assets"`
	TextSourceHash string                    `json:"textSourceHash"`
	Timeline       []HyperFramesReviewClip   `json:"timeline,omitempty"`
}
type HyperFramesCanvas struct {
	Width    int     `json:"width"`
	Height   int     `json:"height"`
	FPS      int     `json:"fps"`
	Duration float64 `json:"duration"`
}
type HyperFramesSourceFile struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}
type HyperFramesAssetBinding struct {
	AssetID string `json:"assetId"`
	Path    string `json:"path"`
	SHA256  string `json:"sha256,omitempty"`
	Size    int64  `json:"size,omitempty"`
}
type HyperFramesReviewClip struct {
	ID       string          `json:"id"`
	Track    string          `json:"track"`
	Kind     string          `json:"kind,omitempty"`
	Label    string          `json:"label,omitempty"`
	Start    float64         `json:"start"`
	Duration float64         `json:"duration"`
	AssetID  string          `json:"assetId,omitempty"`
	TextRefs []TextReference `json:"textRefs,omitempty"`
}
type HyperFramesAssetFile struct {
	AssetID    string `json:"assetId"`
	Path       string `json:"path"`
	SHA256     string `json:"sha256"`
	Size       int64  `json:"size"`
	SourceFile string `json:"-"`
}
type HyperFramesRequest struct {
	Revision int                  `json:"revision"`
	Document *HyperFramesDocument `json:"document"`
}
type HyperFramesView struct {
	ProjectID    string                 `json:"projectId"`
	Revision     int                    `json:"revision"`
	SourceHash   string                 `json:"sourceHash"`
	Current      bool                   `json:"current"`
	State        string                 `json:"state"`
	Document     *HyperFramesDocument   `json:"document"`
	DocumentHash string                 `json:"documentHash,omitempty"`
	Assets       []HyperFramesAssetFile `json:"assets"`
	StaleReasons []string               `json:"staleReasons"`
}

// Reject duplicate object keys as well as unknown fields. This avoids silently
// accepting a different source/path than the human or Agent reviewed.
func hyperFramesUniqueJSON(d *json.Decoder) error {
	return hyperFramesUniqueJSONDepth(d, 0)
}
func hyperFramesUniqueJSONDepth(d *json.Decoder, depth int) error {
	if depth > 64 {
		return errors.New("HyperFrames JSON嵌套最多64层")
	}
	token, err := d.Token()
	if err != nil {
		return err
	}
	delimiter, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	switch delimiter {
	case '{':
		seen := map[string]bool{}
		for d.More() {
			key, err := d.Token()
			if err != nil {
				return err
			}
			name, ok := key.(string)
			if !ok || seen[name] {
				return errors.New("HyperFrames JSON对象包含重复字段")
			}
			seen[name] = true
			if err := hyperFramesUniqueJSONDepth(d, depth+1); err != nil {
				return err
			}
		}
	case '[':
		for d.More() {
			if err := hyperFramesUniqueJSONDepth(d, depth+1); err != nil {
				return err
			}
		}
	default:
		return errors.New("HyperFrames JSON结构无效")
	}
	_, err = d.Token()
	return err
}
func hyperFramesDecode(raw []byte, value any) error {
	if !utf8.Valid(raw) || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return errors.New("HyperFrames必须为有效UTF-8 JSON对象")
	}
	unique := json.NewDecoder(bytes.NewReader(raw))
	if err := hyperFramesUniqueJSON(unique); err != nil {
		return err
	}
	if _, err := unique.Token(); err != io.EOF {
		return errors.New("请求必须只有一个JSON对象")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	return decoder.Decode(value)
}
func (d *HyperFramesDocument) UnmarshalJSON(raw []byte) error {
	type plain HyperFramesDocument
	var value plain
	if err := hyperFramesDecode(raw, &value); err != nil {
		return err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return err
	}
	if audio, ok := fields["hasAudio"]; !ok || bytes.Equal(bytes.TrimSpace(audio), []byte("null")) {
		return errors.New("HyperFrames hasAudio必须明确声明是否期望成片含音轨")
	}
	*d = HyperFramesDocument(value)
	return nil
}

func hyperFramesPath(name string) bool {
	if name == "" || len(name) > 240 || !utf8.ValidString(name) || path.IsAbs(name) || path.Clean(name) != name || strings.ContainsAny(name, "\\:\x00?#%") {
		return false
	}
	for _, part := range strings.Split(name, "/") {
		if part == "" || part == "." || part == ".." || strings.HasSuffix(part, ".") || strings.HasSuffix(part, " ") {
			return false
		}
		base := strings.ToLower(strings.SplitN(part, ".", 2)[0])
		if base == "con" || base == "prn" || base == "aux" || base == "nul" || len(base) == 4 && (strings.HasPrefix(base, "com") || strings.HasPrefix(base, "lpt")) && base[3] >= '0' && base[3] <= '9' {
			return false
		}
		for _, r := range part {
			if unicode.IsControl(r) {
				return false
			}
		}
	}
	return true
}
func hyperFramesReservedPath(name string) bool {
	name = strings.ToLower(name)
	if name == "runtime" || strings.HasPrefix(name, "runtime/") {
		return true
	}
	for _, reserved := range []string{"snapshot.json", "render.log", "validation.json", "hyperframes-manifest.json", "final.mp4", "silent.mp4", "render-proof.json", "check-result.json"} {
		if name == reserved || strings.HasPrefix(name, reserved+"/") {
			return true
		}
	}
	return false
}
func validateHyperFramesProject(p Project, d HyperFramesDocument) error {
	if d.SchemaVersion != 1 {
		return errors.New("HyperFrames schemaVersion必须为1")
	}
	if d.TextSourceHash != narrationSourceHash(p) || !exchangeDigest.MatchString(d.TextSourceHash) {
		return errors.New("主播稿已变化，请重新检查作品与原文并更新textSourceHash")
	}
	c := d.Canvas
	if c.Width < 16 || c.Height < 16 || c.Width > 4096 || c.Height > 4096 || c.FPS < 1 || c.FPS > 60 || !finite(c.Duration) || c.Duration <= 0 || c.Duration > 600 {
		return errors.New("HyperFrames画布需16–4096像素、1–60fps、0–600秒")
	}
	if len(d.Files) == 0 || len(d.Files) > 128 || len(d.Assets) > 120 || len(d.Timeline) > 1000 {
		return errors.New("HyperFrames需1–128个源文件，最多120个素材映射和1000个审阅片段")
	}
	paths := map[string]bool{}
	addPath := func(name string) error {
		if !hyperFramesPath(name) || hyperFramesReservedPath(name) {
			return fmt.Errorf("HyperFrames文件路径无效或保留：%s", name)
		}
		folded := strings.ToLower(name)
		for old := range paths {
			if old == folded || strings.HasPrefix(old, folded+"/") || strings.HasPrefix(folded, old+"/") {
				return fmt.Errorf("HyperFrames文件路径重复或文件/目录冲突：%s", name)
			}
		}
		paths[folded] = true
		return nil
	}
	bytes := 0
	entry := false
	for _, source := range d.Files {
		if err := addPath(source.Path); err != nil {
			return err
		}
		if !utf8.ValidString(source.Content) || strings.ContainsRune(source.Content, 0) || len(source.Content) > 512<<10 {
			return fmt.Errorf("HyperFrames源文件%s需有效UTF-8且不超过512KiB", source.Path)
		}
		bytes += len(source.Content)
		if source.Path == d.EntryPoint {
			entry = true
		}
	}
	if bytes > 1<<20 || !entry || d.EntryPoint != "index.html" {
		return errors.New("HyperFrames入口须为已保存的index.html，全部源内容最多1MiB")
	}
	assets := map[string]bool{}
	for _, asset := range p.Assets {
		assets[asset.ID] = true
	}
	boundAssets := map[string]bool{}
	for _, binding := range d.Assets {
		if err := addPath(binding.Path); err != nil {
			return err
		}
		if !assets[binding.AssetID] || !identifier.MatchString(binding.AssetID) || binding.SHA256 != "" && !exchangeDigest.MatchString(binding.SHA256) || binding.Size < 0 || binding.Size > 256<<20 {
			return fmt.Errorf("HyperFrames引用不存在的素材或无效指纹：%s", binding.AssetID)
		}
		boundAssets[binding.AssetID] = true
	}
	clips := map[string]bool{}
	for _, clip := range d.Timeline {
		if !editID.MatchString(clip.ID) || clips[clip.ID] || !editID.MatchString(clip.Track) || !finite(clip.Start) || !finite(clip.Duration) || clip.Start < 0 || clip.Duration <= 0 || clip.Start+clip.Duration > c.Duration+0.001 || len(clip.Label) > 4000 || !utf8.ValidString(clip.Label) {
			return fmt.Errorf("HyperFrames审阅片段ID、轨道、时间或标签无效：%s", clip.ID)
		}
		clips[clip.ID] = true
		if clip.Kind != "" && clip.Kind != "video" && clip.Kind != "image" && clip.Kind != "audio" && clip.Kind != "voice" && clip.Kind != "subtitle" && clip.Kind != "music" && clip.Kind != "animation" {
			return fmt.Errorf("HyperFrames审阅片段类型无效：%s", clip.ID)
		}
		if clip.AssetID != "" && !boundAssets[clip.AssetID] {
			return fmt.Errorf("审阅片段%s引用的素材未映射到工程", clip.ID)
		}
		if err := validateTextReferences(p.NarrationText, clip.TextRefs); err != nil {
			return fmt.Errorf("审阅片段%s：%w", clip.ID, err)
		}
	}
	return nil
}
func hyperFramesDocumentHash(d HyperFramesDocument) string {
	raw, _ := json.Marshal(d)
	return hashBytes(raw)
}

// Actual private files are available only to render/check internals. Opening
// beneath os.Root plus Lstat/SameFile rejects symlinks and replacement races.
func (a *App) hyperFramesAssetFiles(p Project, d HyperFramesDocument) ([]HyperFramesAssetFile, error) {
	out := []HyperFramesAssetFile{}
	if len(d.Assets) == 0 {
		return out, nil
	}
	for _, dir := range []string{a.dir(p.ID), filepath.Join(a.dir(p.ID), "workspace"), filepath.Join(a.dir(p.ID), "workspace", "assets")} {
		stat, err := os.Lstat(dir)
		if err != nil || !stat.IsDir() {
			return nil, errors.New("HyperFrames素材目录不存在或包含符号链接")
		}
	}
	root, err := os.OpenRoot(filepath.Join(a.dir(p.ID), "workspace", "assets"))
	if err != nil {
		return nil, err
	}
	defer root.Close()
	assets := map[string]Asset{}
	for _, asset := range p.Assets {
		assets[asset.ID] = asset
	}
	total := int64(0)
	for _, binding := range d.Assets {
		asset, ok := assets[binding.AssetID]
		if !ok || filepath.Base(asset.File) != asset.File || !hyperFramesPath(asset.File) {
			return nil, fmt.Errorf("HyperFrames素材%s不存在或存储路径无效", binding.AssetID)
		}
		prior, err := root.Lstat(asset.File)
		if err != nil || !prior.Mode().IsRegular() || prior.Size() <= 0 || prior.Size() > 256<<20 {
			return nil, fmt.Errorf("HyperFrames素材%s不是1字节–256MiB普通文件", binding.AssetID)
		}
		total += prior.Size()
		if total > 1<<30 {
			return nil, errors.New("HyperFrames全部素材映射最多1GiB")
		}
		file, err := root.Open(asset.File)
		if err != nil {
			return nil, fmt.Errorf("无法读取HyperFrames素材%s", binding.AssetID)
		}
		stat, statErr := file.Stat()
		if statErr != nil || !stat.Mode().IsRegular() || !os.SameFile(prior, stat) {
			file.Close()
			return nil, errors.New("HyperFrames素材在读取时变化")
		}
		digest, size, hashErr := hashReader(io.LimitReader(file, (256<<20)+1))
		after, afterErr := file.Stat()
		file.Close()
		if hashErr != nil || afterErr != nil || size != stat.Size() || size != after.Size() || !stat.ModTime().Equal(after.ModTime()) || size > 256<<20 {
			return nil, errors.New("HyperFrames素材读取失败或在读取时变化")
		}
		if binding.SHA256 != "" && binding.SHA256 != digest || binding.Size != 0 && binding.Size != size {
			return nil, fmt.Errorf("HyperFrames素材%s字节已改变，请重新核对并保存映射", binding.AssetID)
		}
		out = append(out, HyperFramesAssetFile{AssetID: binding.AssetID, Path: binding.Path, SHA256: digest, Size: size, SourceFile: filepath.Join(a.dir(p.ID), "workspace", "assets", asset.File)})
	}
	return out, nil
}
func (a *App) hyperFramesView(p Project) HyperFramesView {
	v := HyperFramesView{ProjectID: p.ID, Revision: p.Revision, SourceHash: narrationSourceHash(p), State: "empty", Document: p.HyperFrames, Assets: []HyperFramesAssetFile{}, StaleReasons: []string{}}
	if p.HyperFrames == nil {
		return v
	}
	v.State, v.DocumentHash = "stale", hyperFramesDocumentHash(*p.HyperFrames)
	if err := validateHyperFramesProject(p, *p.HyperFrames); err != nil {
		v.StaleReasons = append(v.StaleReasons, err.Error())
	}
	assets, err := a.hyperFramesAssetFiles(p, *p.HyperFrames)
	if err != nil {
		v.StaleReasons = append(v.StaleReasons, err.Error())
	} else {
		v.Assets = assets
	}
	if len(v.StaleReasons) == 0 {
		v.State, v.Current = "ready", true
	}
	return v
}
func (a *App) hyperFramesGet(w http.ResponseWriter, r *http.Request) {
	p, err := a.get(r.PathValue("id"))
	if err != nil {
		fail(w, 404, err)
		return
	}
	writeJSON(w, 200, a.hyperFramesView(p))
}
func (a *App) hyperFramesUpdate(w http.ResponseWriter, r *http.Request) {
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 2<<20))
	if err != nil {
		fail(w, 413, errors.New("HyperFrames保存请求最多2MiB"))
		return
	}
	var in HyperFramesRequest
	if err := hyperFramesDecode(raw, &in); err != nil {
		fail(w, 400, err)
		return
	}
	p, err := a.get(r.PathValue("id"))
	if err != nil {
		fail(w, 404, err)
		return
	}
	if p.Revision != in.Revision {
		fail(w, 409, errors.New("项目版本冲突，请重新读取合并"))
		return
	}
	if in.Document == nil {
		fail(w, 422, errors.New("需要完整HyperFrames document"))
		return
	}
	if in.Document.TextSourceHash != narrationSourceHash(p) {
		fail(w, 409, errors.New("主播稿已变化，请重新读取并核对作品"))
		return
	}
	if err := validateHyperFramesProject(p, *in.Document); err != nil {
		fail(w, 422, err)
		return
	}
	assets, err := a.hyperFramesAssetFiles(p, *in.Document)
	if err != nil {
		fail(w, 422, err)
		return
	}
	for i := range in.Document.Assets {
		in.Document.Assets[i].SHA256, in.Document.Assets[i].Size = assets[i].SHA256, assets[i].Size
	}
	if in.Document.Assets == nil {
		in.Document.Assets = []HyperFramesAssetBinding{}
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	current := a.projects[p.ID]
	if current == nil || current.Revision != in.Revision || narrationSourceHash(*current) != in.Document.TextSourceHash {
		fail(w, 409, errors.New("保存期间项目已变化，请重新读取合并"))
		return
	}
	q := clone(current)
	q.HyperFrames = in.Document
	q.Revision++
	if err := a.save(&q); err != nil {
		fail(w, 500, err)
		return
	}
	a.projects[q.ID] = &q
	// Do not execute source or rehash large assets while holding the project lock.
	writeJSON(w, 200, HyperFramesView{ProjectID: q.ID, Revision: q.Revision, SourceHash: narrationSourceHash(q), Current: true, State: "ready", Document: q.HyperFrames, DocumentHash: hyperFramesDocumentHash(*q.HyperFrames), Assets: assets, StaleReasons: []string{}})
}
