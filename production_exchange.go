package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"math"
	"net/http"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// ProductionRun is a frozen exchange receipt, not the RunDesk agent task itself.
// Paths are relative to the configured workspace; no host paths or secrets are exported.
type ProductionRun struct {
	ProductionRequestID string `json:"productionRequestId,omitempty"`
	PresetID                string         `json:"presetId,omitempty"`
	WorkspaceID             string         `json:"workspaceId,omitempty"`
	ExchangeRootFingerprint string         `json:"exchangeRootFingerprint,omitempty"`
	ID                      string         `json:"id"`
	RequestID               string         `json:"requestId"`
	RequestHash             string         `json:"requestHash"`
	Revision                int            `json:"revision"`
	SessionID               string         `json:"sessionId"`
	RelativeRunRoot         string         `json:"relativeRunRoot"`
	ManifestPath            string         `json:"manifestPath"`
	State                   string         `json:"state"`
	SourceHash              string         `json:"sourceHash"`
	InputHash               string         `json:"inputHash"`
	InputBasisHash          string         `json:"inputBasisHash"`
	SnapshotHash            string         `json:"snapshotHash"`
	Created                 string         `json:"created"`
	SpeechID                string         `json:"speechId"`
	Duration                float64        `json:"duration"`
	RenderID                string         `json:"renderId,omitempty"`
	EditHash                string         `json:"editHash,omitempty"`
	OutputHash              string         `json:"outputHash,omitempty"`
	Files                   []ExchangeFile `json:"files"`
}
type ExchangeFile struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
	Source string `json:"source,omitempty"`
}
type ExchangeMedia struct {
	AssetID  string `json:"assetId"`
	Path     string `json:"path"`
	Type     string `json:"type"`
	MIMEType string `json:"mimeType"`
	SHA256   string `json:"sha256"`
	Size     int64  `json:"size"`
}
type ExchangeNarration struct {
	Path     string  `json:"path"`
	Duration float64 `json:"duration"`
	JobID    string  `json:"jobId"`
	SHA256   string  `json:"sha256"`
	Size     int64   `json:"size"`
}
type ExchangeManifest struct {
	SchemaVersion   int               `json:"schemaVersion"`
	ProjectID       string            `json:"projectId"`
	ProductionRunID string            `json:"productionRunId"`
	SourceHash      string            `json:"sourceHash"`
	InputHash       string            `json:"inputHash"`
	TextPath        string            `json:"textPath"`
	AlignmentPath   string            `json:"alignmentPath"`
	Narration       ExchangeNarration `json:"narration"`
	Subtitles       ExchangeFile      `json:"subtitles"`
	TimingPath      string            `json:"timingPath"`
	Media           []ExchangeMedia   `json:"media"`
}
type ExchangeArtifact struct {
	ID       string `json:"id"`
	Kind     string `json:"kind"`
	Path     string `json:"path"`
	SHA256   string `json:"sha256"`
	Size     int64  `json:"size"`
	MIMEType string `json:"mimeType"`
}
type ExchangeCompletion struct {
	SchemaVersion   int                `json:"schemaVersion"`
	ProjectID       string             `json:"projectId"`
	ProductionRunID string             `json:"productionRunId"`
	SourceHash      string             `json:"sourceHash"`
	InputHash       string             `json:"inputHash"`
	Artifacts       []ExchangeArtifact `json:"artifacts"`
	Video           json.RawMessage    `json:"video,omitempty"`
	Engine          string             `json:"engine,omitempty"`
	UpstreamCommit  string             `json:"upstreamCommit,omitempty"`
}

var exchangeSessionID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,127}$`)
var exchangeDigest = regexp.MustCompile(`^[a-f0-9]{64}$`)

const exchangeFileLimit int64 = 2 << 30
const exchangeTotalLimit int64 = 4 << 30

func exchangeRelative(name string) bool {
	return name != "" && len(name) < 512 && !strings.ContainsAny(name, "\\\x00:") && !strings.HasPrefix(name, "/") && path.Clean(name) == name && name != "." && name != ".." && !strings.HasPrefix(name, "../")
}
func exchangeOpen(root *os.Root, name string, max int64) (*os.File, error) {
	if !exchangeRelative(name) {
		return nil, errors.New("交换文件必须是规范的相对路径")
	}
	// Reject symlinks even inside the root. Root also prevents escape during races.
	parts := strings.Split(name, "/")
	for i := range parts {
		st, e := root.Lstat(strings.Join(parts[:i+1], "/"))
		if e != nil {
			return nil, e
		}
		if st.Mode()&os.ModeSymlink != 0 {
			return nil, errors.New("交换文件不允许符号链接")
		}
	}
	f, e := root.Open(name)
	if e != nil {
		return nil, e
	}
	st, e := f.Stat()
	if e != nil || !st.Mode().IsRegular() || st.Size() < 0 || st.Size() > max {
		f.Close()
		return nil, errors.New("交换文件不是普通文件或超过大小限制")
	}
	return f, nil
}
func exchangeJSON(root *os.Root, name string, max int64, v any) error {
	f, e := exchangeOpen(root, name, max)
	if e != nil {
		return e
	}
	defer f.Close()
	d := json.NewDecoder(io.LimitReader(f, max+1))
	d.DisallowUnknownFields()
	if e = d.Decode(v); e != nil {
		return e
	}
	var rest any
	if e = d.Decode(&rest); e != io.EOF {
		return errors.New("JSON 文件包含多余内容")
	}
	return nil
}
func exchangeCopy(src *os.File, destRoot *os.Root, dest string, max int64) (ExchangeFile, error) {
	var out ExchangeFile
	if !exchangeRelative(dest) {
		return out, errors.New("目标路径无效")
	}
	if e := destRoot.MkdirAll(path.Dir(dest), 0700); e != nil {
		return out, e
	}
	f, e := destRoot.OpenFile(dest, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if e != nil {
		return out, e
	}
	h := sha256.New()
	n, e := io.Copy(io.MultiWriter(f, h), io.LimitReader(src, max+1))
	if e == nil && n > max {
		e = errors.New("交换文件超过大小限制")
	}
	if e == nil {
		e = f.Sync()
	}
	ce := f.Close()
	if e == nil {
		e = ce
	}
	if e != nil {
		destRoot.Remove(dest)
		return out, e
	}
	return ExchangeFile{Path: dest, SHA256: hex.EncodeToString(h.Sum(nil)), Size: n}, nil
}
func exchangeWrite(root *os.Root, name string, b []byte) (ExchangeFile, error) {
	if !exchangeRelative(name) {
		return ExchangeFile{}, errors.New("目标路径无效")
	}
	if e := root.MkdirAll(path.Dir(name), 0700); e != nil {
		return ExchangeFile{}, e
	}
	f, e := root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if e != nil {
		return ExchangeFile{}, e
	}
	_, e = f.Write(b)
	if e == nil {
		e = f.Sync()
	}
	ce := f.Close()
	if e == nil {
		e = ce
	}
	return ExchangeFile{Path: name, SHA256: hashBytes(b), Size: int64(len(b))}, e
}
func latestNarrationSpeech(p Project) (SpeechJob, bool) {
	projected := p
	projected.Shots = narrationShots(p.NarrationText)
	h := speechScriptHash(projected)
	for i := len(p.Speech) - 1; i >= 0; i-- {
		j := p.Speech[i]
		if j.Source == "narration" && j.State == "completed" && j.ScriptHash == h && finite(j.Duration) && j.Duration > 0 && j.Duration <= 600 {
			return j, true
		}
	}
	return SpeechJob{}, false
}
func externalProductionBasis(p Project) string {
	var alignment any
	ids := []string{}
	if p.MediaAlignment != nil {
		v := *p.MediaAlignment
		v.Revision = 0
		v.Updated = ""
		alignment = v
		ids = alignmentAssetIDs(v.Units)
	}
	assets := []Asset{}
	for _, id := range ids {
		for _, a := range p.Assets {
			if a.ID == id {
				assets = append(assets, a)
				break
			}
		}
	}
	sort.Slice(assets, func(i, j int) bool { return assets[i].ID < assets[j].ID })
	speech, _ := latestNarrationSpeech(p)
	b, _ := json.Marshal(struct {
		Narration string
		Workflow  ProductionWorkflow
		Alignment any
		Assets    []Asset
		Speech    any
	}{p.NarrationText, workflowFor(p), alignment, assets, struct {
		ID, ScriptHash string
		Duration       float64
		Files          map[string]NewsFile
	}{speech.ID, speech.ScriptHash, speech.Duration, speech.Files}})
	basis := hashBytes(b)
	if p.RuntimeBinding != nil && p.RuntimeBinding.PresetID != "" {
		bound, _ := json.Marshal(struct{ Basis, PresetID, InstanceID, WorkspaceID string }{basis, p.RuntimeBinding.PresetID, p.RuntimeBinding.InstanceID, p.RuntimeBinding.WorkspaceID})
		return hashBytes(bound)
	}
	return basis
}
func externalRenderCurrent(p Project, r Render) bool {
	if r.Engine != "video_use" || r.State != "completed" || r.Range != nil || workflowFor(p).EditingBackend != "video_use" || r.SourceHash != narrationSourceHash(p) {
		return false
	}
	basis := externalProductionBasis(p)
	// Preparation order is editorial authority. A delayed older job must not
	// supersede an already accepted newer edit of the very same source inputs.
	for i := len(p.ProductionRuns) - 1; i >= 0; i-- {
		run := p.ProductionRuns[i]
		if run.State != "completed" || run.RenderID == "" || run.InputBasisHash != basis {
			continue
		}
		return run.RenderID == r.ID && r.ContentHash == run.InputBasisHash && run.OutputHash != "" && run.EditHash != ""
	}
	return false
}
func findProductionRun(p Project, id string) (ProductionRun, bool) {
	for _, run := range p.ProductionRuns {
		if run.ID == id {
			return run, true
		}
	}
	return ProductionRun{}, false
}
func (a *App) externalRenderInputsIntact(p Project, r Render) bool {
	run, ok := ProductionRun{}, false
	for _, v := range p.ProductionRuns {
		if v.RenderID == r.ID {
			run = v
			ok = true
			break
		}
	}
	if !ok || !externalRenderCurrent(p, r) {
		return false
	}
	for _, f := range run.Files {
		if strings.HasPrefix(f.Source, "asset:") {
			id := strings.TrimPrefix(f.Source, "asset:")
			found := false
			for _, asset := range p.Assets {
				if asset.ID == id && filepath.Base(asset.File) == asset.File {
					h, n, e := hashLocal(filepath.Join(a.dir(p.ID), "workspace", "assets", asset.File))
					if e != nil || h != f.SHA256 || n != f.Size {
						return false
					}
					found = true
					break
				}
			}
			if !found {
				return false
			}
		} else if strings.HasPrefix(f.Source, "speech:") {
			name := strings.TrimPrefix(f.Source, "speech:")
			j, err := findSpeech(p, run.SpeechID)
			if err != nil {
				return false
			}
			file, err := a.readSpeechArtifact(p, j, name)
			if err != nil {
				return false
			}
			h, n, err := hashReader(file)
			file.Close()
			if err != nil || h != f.SHA256 || n != f.Size {
				return false
			}
		}
	}
	return true
}

// Verify App-private archived bytes; shared work files are never authoritative
// after ingestion and may be removed without invalidating the archived result.
func (a *App) externalArchiveIntact(p Project, r Render, proof RenderProof) bool {
	run, ok := ProductionRun{}, false
	for _, v := range p.ProductionRuns {
		if v.RenderID == r.ID {
			run, ok = v, true
			break
		}
	}
	if !ok || proof.Key != run.InputHash || proof.VideoHash != run.OutputHash || proof.SnapshotHash != run.SnapshotHash {
		return false
	}
	dir := filepath.Join(a.dir(p.ID), "renders", r.ID)
	for _, f := range []struct {
		name, hash string
		size       int64
	}{{"final.mp4", run.OutputHash, proof.VideoBytes}, {"snapshot.json", run.SnapshotHash, -1}, {"edit-ir.json", run.EditHash, -1}} {
		h, n, e := hashLocal(filepath.Join(dir, f.name))
		if e != nil || h != f.hash || (f.size >= 0 && n != f.size) {
			return false
		}
	}
	return true
}
func exchangeRoot() (*os.Root, error) {
	selected, err := exchangeSelectionForProject(Project{})
	if err != nil {
		return nil, err
	}
	return openSelectedExchangeRoot(selected)
}
func productionRunResponse(p Project, run ProductionRun) map[string]any {
	current := run.InputBasisHash == externalProductionBasis(p)
	if run.RenderID != "" {
		current = false
		for _, render := range p.Renders {
			if render.ID == run.RenderID {
				current = externalRenderCurrent(p, render)
				break
			}
		}
	}
	return map[string]any{"projectId": p.ID, "revision": p.Revision, "run": run, "current": current, "adapterCLI": "python3 /opt/rundesk-profile/tools/video_use_adapter.py render --manifest " + run.ManifestPath + " --edl " + run.RelativeRunRoot + "/work/edl.json --out " + run.RelativeRunRoot + "/artifacts/render-<new-id>"}
}
func (a *App) productionRunGet(w http.ResponseWriter, r *http.Request) {
	p, e := a.get(r.PathValue("id"))
	if e != nil {
		fail(w, 404, e)
		return
	}
	run, ok := findProductionRun(p, r.PathValue("prid"))
	if !ok {
		fail(w, 404, errors.New("制作运行不存在"))
		return
	}
	writeJSON(w, 200, productionRunResponse(p, run))
}
func (a *App) productionRunPrepare(w http.ResponseWriter, r *http.Request) {
	var in struct {
		ProductionRequestID string `json:"productionRequestId,omitempty"`
		Revision  int    `json:"revision"`
		RequestID string `json:"requestId"`
	}
	if !decodeStrict(w, r, &in) {
		return
	}
	if !identifier.MatchString(in.RequestID) {
		fail(w, 422, errors.New("requestId 必须为24位小写十六进制"))
		return
	}
	b, _ := json.Marshal(in)
	requestHash := hashBytes(b)
	a.mu.Lock()
	defer a.mu.Unlock()
	p := a.projects[r.PathValue("id")]
	if p == nil {
		fail(w, 404, errors.New("项目不存在"))
		return
	}
	for _, run := range p.ProductionRuns {
		if run.RequestID == in.RequestID {
			if run.RequestHash != requestHash {
				fail(w, 409, errors.New("相同 requestId 的请求内容不同"))
				return
			}
			if run.RenderID == "" {
				selected, err := exchangeSelectionForProject(*p)
				if err != nil {
					fail(w, 409, err)
					return
				}
				if err = exchangeSelectionMatchesRun(*p, run, selected); err != nil {
					fail(w, 409, err)
					return
				}
			}
			writeJSON(w, 200, productionRunResponse(*p, run))
			return
		}
	}
	if e := validateProductionOwner(*p, in.ProductionRequestID); e != nil {
		fail(w, 409, e)
		return
	}
	if p.Revision != in.Revision {
		fail(w, 409, errors.New("项目版本冲突"))
		return
	}
	if workflowFor(*p).EditingBackend != "video_use" || !exchangeSessionID.MatchString(p.SessionID) {
		fail(w, 409, errors.New("需要 video_use 后端和有效的 RunDesk 会话"))
		return
	}
	if strings.TrimSpace(p.NarrationText) == "" || p.MediaAlignment == nil || a.mediaAlignmentView(*p).State != "ready" {
		fail(w, 409, errors.New("请先完成当前正文的素材对齐"))
		return
	}
	for _, u := range p.MediaAlignment.Units {
		if u.Role == "textcard" && len(u.AssetIDs) == 0 && len(u.Selections) == 0 {
			fail(w, 409, errors.New("video_use 当前需要文字卡对应的实际图片素材，请先生成文字卡并关联 assetId"))
			return
		}
	}
	j, ok := latestNarrationSpeech(*p)
	if !ok {
		fail(w, 409, errors.New("需要当前正文的已完成独立配音"))
		return
	}
	selected, e := exchangeSelectionForProject(*p)
	if e != nil {
		fail(w, 409, e)
		return
	}
	root, e := openSelectedExchangeRoot(selected)
	if e != nil {
		fail(w, 409, e)
		return
	}
	defer root.Close()
	if p.RuntimeBinding != nil && p.RuntimeBinding.PresetID != "" {
		before := clone(p)
		a.mu.Unlock()
		probeErr := a.verifyPresetExchange(r.Context(), before, root)
		a.mu.Lock()
		latest := a.projects[before.ID]
		if probeErr != nil {
			fail(w, 409, probeErr)
			return
		}
		if latest == nil || latest.Revision != before.Revision || latest.SessionID != before.SessionID || latest.WorkspaceID != before.WorkspaceID || projectRuntimeBinding(latest, a.cfg) != projectRuntimeBinding(&before, a.cfg) || externalProductionBasis(*latest) != externalProductionBasis(before) {
			fail(w, 409, errors.New("共享目录验证期间项目绑定或内容已改变，请重读项目后重试"))
			return
		}
		current, err := exchangeSelectionForProject(*latest)
		if err != nil || current.Fingerprint != selected.Fingerprint {
			fail(w, 409, errors.New("共享目录验证期间挂载映射已改变，未导出素材"))
			return
		}
		p = latest
	}
	presetID, workspaceID := exchangeRunBinding(*p, selected)
	run := ProductionRun{ProductionRequestID: in.ProductionRequestID, PresetID: presetID, WorkspaceID: workspaceID, ExchangeRootFingerprint: selected.Fingerprint, ID: uid(), RequestID: in.RequestID, RequestHash: requestHash, Revision: p.Revision, SessionID: p.SessionID, State: "prepared", SourceHash: narrationSourceHash(*p), InputBasisHash: externalProductionBasis(*p), Created: time.Now().UTC().Format(time.RFC3339Nano), SpeechID: j.ID, Duration: j.Duration, Files: []ExchangeFile{}}
	run.RelativeRunRoot = "outputs/" + p.SessionID + "/runs/" + run.ID
	run.ManifestPath = run.RelativeRunRoot + "/input/manifest.json"
	if e = root.MkdirAll(path.Dir(run.RelativeRunRoot), 0700); e != nil {
		fail(w, 409, errors.New("交换目录不可写"))
		return
	}
	if e = root.Mkdir(run.RelativeRunRoot, 0700); e != nil {
		fail(w, 409, e)
		return
	}
	committed := false
	defer func() {
		if !committed {
			root.RemoveAll(run.RelativeRunRoot)
		}
	}()
	rr, e := root.OpenRoot(run.RelativeRunRoot)
	if e != nil {
		fail(w, 409, e)
		return
	}
	defer rr.Close()
	for _, d := range []string{"input/media", "work", "artifacts"} {
		if e = rr.MkdirAll(d, 0700); e != nil {
			fail(w, 409, e)
			return
		}
	}
	addBytes := func(name string, b []byte) error {
		f, err := exchangeWrite(rr, name, b)
		if err == nil {
			run.Files = append(run.Files, f)
		}
		return err
	}
	if e = addBytes("input/narration.txt", []byte(p.NarrationText)); e != nil {
		fail(w, 409, e)
		return
	}
	alignBytes, _ := json.MarshalIndent(p.MediaAlignment, "", "  ")
	if e = addBytes("input/alignment.json", alignBytes); e != nil {
		fail(w, 409, e)
		return
	}
	manifest := ExchangeManifest{SchemaVersion: 1, ProjectID: p.ID, ProductionRunID: run.ID, SourceHash: run.SourceHash, TextPath: "input/narration.txt", AlignmentPath: "input/alignment.json", TimingPath: "input/speech-manifest.json", Media: []ExchangeMedia{}}
	total := int64(0)
	for _, name := range []string{"narration.wav", "subtitles.srt", "manifest.json"} {
		source, err := a.readSpeechArtifact(*p, j, name)
		if err != nil {
			fail(w, 409, err)
			return
		}
		dest := "input/" + name
		if name == "manifest.json" {
			dest = "input/speech-manifest.json"
		}
		f, err := exchangeCopy(source, rr, dest, exchangeFileLimit)
		source.Close()
		if err != nil || f.SHA256 != j.Files[name].SHA256 || f.Size != j.Files[name].Size {
			fail(w, 409, errors.New("配音导出校验失败"))
			return
		}
		f.Source = "speech:" + name
		run.Files = append(run.Files, f)
		total += f.Size
		if name == "narration.wav" {
			manifest.Narration = ExchangeNarration{Path: f.Path, Duration: j.Duration, JobID: j.ID, SHA256: f.SHA256, Size: f.Size}
		}
		if name == "subtitles.srt" {
			manifest.Subtitles = ExchangeFile{Path: f.Path, SHA256: f.SHA256, Size: f.Size}
		}
	}
	ar, e := os.OpenRoot(filepath.Join(a.dir(p.ID), "workspace", "assets"))
	if e != nil {
		fail(w, 409, e)
		return
	}
	defer ar.Close()
	for _, id := range alignmentAssetIDs(p.MediaAlignment.Units) {
		var asset Asset
		for _, v := range p.Assets {
			if v.ID == id {
				asset = v
				break
			}
		}
		if asset.ID == "" || filepath.Base(asset.File) != asset.File || (!strings.HasPrefix(asset.MIME, "image/") && !strings.HasPrefix(asset.MIME, "video/")) {
			fail(w, 409, errors.New("关联素材不是有效图片或视频"))
			return
		}
		src, err := exchangeOpen(ar, asset.File, exchangeFileLimit)
		if err != nil {
			fail(w, 409, err)
			return
		}
		ext := strings.ToLower(filepath.Ext(asset.File))
		if len(ext) > 12 || strings.ContainsAny(ext, "/\\:") {
			src.Close()
			fail(w, 409, errors.New("素材扩展名无效"))
			return
		}
		dest := "input/media/" + asset.ID + ext
		f, err := exchangeCopy(src, rr, dest, exchangeFileLimit)
		src.Close()
		if err != nil {
			fail(w, 409, err)
			return
		}
		if f.Size != asset.Size {
			fail(w, 409, errors.New("素材文件大小已改变"))
			return
		}
		f.Source = "asset:" + asset.ID
		run.Files = append(run.Files, f)
		total += f.Size
		if total > exchangeTotalLimit {
			fail(w, 422, errors.New("交换快照总大小超过4GiB"))
			return
		}
		kind := "image"
		if strings.HasPrefix(asset.MIME, "video/") {
			kind = "video"
		}
		manifest.Media = append(manifest.Media, ExchangeMedia{AssetID: asset.ID, Path: f.Path, Type: kind, MIMEType: asset.MIME, SHA256: f.SHA256, Size: f.Size})
	}
	if len(manifest.Media) == 0 {
		fail(w, 409, errors.New("video_use 需要至少一个已关联的图片或视频素材"))
		return
	}
	// The manifest's inputHash is computed before that field is populated; all file
	// hashes are covered and the final serialized manifest is separately recorded.
	raw, _ := json.Marshal(struct {
		Manifest ExchangeManifest
		Files    []ExchangeFile
		Basis    string
	}{manifest, run.Files, run.InputBasisHash})
	run.InputHash = hashBytes(raw)
	manifest.InputHash = run.InputHash
	mb, _ := json.MarshalIndent(manifest, "", "  ")
	mf, e := exchangeWrite(rr, "input/manifest.json", mb)
	if e != nil {
		fail(w, 409, e)
		return
	}
	run.Files = append(run.Files, mf)
	// Preserve the original project privately. A delayed result must never be
	// archived with a newer project's snapshot.
	snapshot, e := json.MarshalIndent(p, "", "  ")
	if e != nil {
		fail(w, 500, e)
		return
	}
	run.SnapshotHash = hashBytes(snapshot)
	privateDir := filepath.Join(a.dir(p.ID), "production-runs", run.ID)
	if e = os.MkdirAll(privateDir, 0700); e != nil {
		fail(w, 500, e)
		return
	}
	defer func() {
		if !committed {
			os.RemoveAll(privateDir)
		}
	}()
	if e = os.WriteFile(filepath.Join(privateDir, "snapshot.json"), snapshot, 0600); e != nil {
		fail(w, 500, e)
		return
	}
	if a.mediaAlignmentView(*p).State != "ready" {
		fail(w, 409, errors.New("导出期间素材已变化，请重新对齐"))
		return
	}
	q := clone(p)
	q.ProductionRuns = append(q.ProductionRuns, run)
	q.Revision++
	if e = a.save(&q); e != nil {
		fail(w, 500, e)
		return
	}
	a.projects[q.ID] = &q
	committed = true
	writeJSON(w, 200, productionRunResponse(q, run))
}

func verifyExchangeFiles(root *os.Root, files []ExchangeFile) error {
	for _, item := range files {
		f, e := exchangeOpen(root, item.Path, exchangeFileLimit)
		if e != nil {
			return e
		}
		h, n, e := hashReader(io.LimitReader(f, exchangeFileLimit+1))
		f.Close()
		if e != nil || h != item.SHA256 || n != item.Size {
			return errors.New("冻结输入已被修改；请创建新的制作运行")
		}
	}
	return nil
}
func probeExchangeVideo(ctx context.Context, file string, want float64, canvas exchangeCanvas) (float64, error) {
	step, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(step, "ffprobe", "-v", "error", "-protocol_whitelist", "file,pipe", "-format_whitelist", "mov", "-f", "mov", "-enable_drefs", "0", "-show_entries", "stream=codec_type,codec_name,width,height,duration,avg_frame_rate:format=duration", "-of", "json", file)
	cmd.Env = rendererEnvironment()
	configureProcess(cmd)
	b, e := cmd.Output()
	if e != nil {
		return 0, errors.New("成片无法解码")
	}
	var v struct {
		Streams []struct {
			Type      string `json:"codec_type"`
			Codec     string `json:"codec_name"`
			Width     int    `json:"width"`
			Height    int    `json:"height"`
			Duration  string `json:"duration"`
			FrameRate string `json:"avg_frame_rate"`
		} `json:"streams"`
		Format struct {
			Duration string `json:"duration"`
		} `json:"format"`
	}
	if json.Unmarshal(b, &v) != nil {
		return 0, errors.New("成片媒体信息无效")
	}
	containerDuration, e := strconv.ParseFloat(v.Format.Duration, 64)
	if !finite(canvas.FPS) || canvas.FPS < 1 || canvas.FPS > 60 || !finite(want) || want <= 0 || want > 600 {
		return 0, errors.New("成片检查缺少有效的EDL画布或配音时长")
	}
	frameTolerance := 1/canvas.FPS + .001
	videoCount, audioCount := 0, 0
	videoDuration, audioDuration := 0.0, 0.0
	for _, stream := range v.Streams {
		if stream.Type == "video" {
			videoCount++
			var de error
			videoDuration, de = strconv.ParseFloat(stream.Duration, 64)
			fps, fe := exchangeFrameRate(stream.FrameRate)
			if de != nil || !finite(videoDuration) || videoDuration <= 0 || math.Abs(videoDuration-want) > frameTolerance || stream.Width != canvas.Width || stream.Height != canvas.Height || stream.Codec != "h264" || fe != nil || math.Abs(fps-canvas.FPS) >= .001 {
				return 0, errors.New("成片画面轨的时长、尺寸或帧率与冻结EDL不一致")
			}
		}
		if stream.Type == "audio" {
			audioCount++
			var de error
			audioDuration, de = strconv.ParseFloat(stream.Duration, 64)
			if de != nil || !finite(audioDuration) || math.Abs(audioDuration-want) > .2 || stream.Codec != "aac" {
				return 0, errors.New("成片需包含完整独立AAC配音")
			}
		}
	}
	if e != nil || !finite(containerDuration) || containerDuration <= 0 || containerDuration > 600+math.Max(.2, frameTolerance) || videoCount != 1 || audioCount != 1 || math.Abs(containerDuration-want) > math.Max(.2, frameTolerance) || math.Abs(containerDuration-math.Max(videoDuration, audioDuration)) > .05 {
		return 0, errors.New("成片需为单路H.264/AAC，画面与完整配音时长一致")
	}
	return videoDuration, nil
}
func (a *App) productionRunIngest(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Revision int `json:"revision"`
	}
	if !decodeStrict(w, r, &in) {
		return
	}
	p, e := a.get(r.PathValue("id"))
	if e != nil {
		fail(w, 404, e)
		return
	}
	run, ok := findProductionRun(p, r.PathValue("prid"))
	if !ok {
		fail(w, 404, errors.New("制作运行不存在"))
		return
	}
	if run.RenderID != "" {
		writeJSON(w, 200, productionRunResponse(p, run))
		return
	}
	if p.Revision != in.Revision {
		fail(w, 409, errors.New("项目版本冲突"))
		return
	}
	selected, e := exchangeSelectionForProject(p)
	if e != nil {
		fail(w, 409, e)
		return
	}
	if e = exchangeSelectionMatchesRun(p, run, selected); e != nil {
		fail(w, 409, e)
		return
	}
	root, e := openSelectedExchangeRoot(selected)
	if e != nil {
		fail(w, 409, e)
		return
	}
	defer root.Close()
	rr, e := root.OpenRoot(run.RelativeRunRoot)
	if e != nil {
		fail(w, 409, e)
		return
	}
	defer rr.Close()
	if e = verifyExchangeFiles(rr, run.Files); e != nil {
		fail(w, 409, e)
		return
	}
	var completion ExchangeCompletion
	if e = exchangeJSON(rr, "completion.json", 1<<20, &completion); e != nil {
		fail(w, 409, e)
		return
	}
	if completion.SchemaVersion != 1 || (completion.Engine != "" && completion.Engine != "video_use") || completion.ProjectID != p.ID || completion.ProductionRunID != run.ID || completion.SourceHash != run.SourceHash || completion.InputHash != run.InputHash || len(completion.Artifacts) < 3 || len(completion.Artifacts) > 30 {
		fail(w, 409, errors.New("完成清单与冻结输入不一致"))
		return
	}
	rid := uid()
	renders := filepath.Join(a.dir(p.ID), "renders")
	if e = os.MkdirAll(renders, 0700); e != nil {
		fail(w, 500, e)
		return
	}
	staging, e := os.MkdirTemp(renders, ".ingest-")
	if e != nil {
		fail(w, 500, e)
		return
	}
	defer os.RemoveAll(staging)
	sr, e := os.OpenRoot(staging)
	if e != nil {
		fail(w, 500, e)
		return
	}
	defer sr.Close()
	seenIDs, seenPaths, kinds := map[string]bool{}, map[string]bool{}, map[string]bool{}
	total := int64(0)
	outputHash, editHash := "", ""
	for _, item := range completion.Artifacts {
		if item.ID == "" || len(item.ID) > 128 || seenIDs[item.ID] || seenPaths[item.Path] || !exchangeRelative(item.Path) || !strings.HasPrefix(item.Path, "artifacts/") || !exchangeDigest.MatchString(item.SHA256) || item.Size <= 0 || item.Size > exchangeFileLimit {
			fail(w, 409, errors.New("产物ID、路径、大小或摘要无效"))
			return
		}
		seenIDs[item.ID] = true
		seenPaths[item.Path] = true
		dest := ""
		switch item.Kind {
		case "video":
			dest = "final.mp4"
		case "edit":
			dest = "edit-ir.json"
		case "validation":
			dest = "validation.json"
		case "probe", "frame":
			dest = "evidence/" + item.ID + path.Ext(item.Path)
		default:
			fail(w, 409, errors.New("未知产物类别"))
			return
		}
		if strings.ContainsAny(item.ID, "/\\:\x00") || item.ID == "." || item.ID == ".." {
			fail(w, 409, errors.New("产物ID无效"))
			return
		}
		if item.Kind != "probe" && item.Kind != "frame" {
			if kinds[item.Kind] {
				fail(w, 409, errors.New("成片、EDL和验证报告只能各有一份"))
				return
			}
			kinds[item.Kind] = true
		}
		max := exchangeFileLimit
		if item.Kind == "edit" || item.Kind == "validation" || item.Kind == "probe" {
			max = 4 << 20
		}
		if item.Kind == "frame" {
			max = 8 << 20
		}
		src, err := exchangeOpen(rr, item.Path, max)
		if err != nil {
			fail(w, 409, err)
			return
		}
		f, err := exchangeCopy(src, sr, dest, max)
		src.Close()
		if err != nil || f.SHA256 != item.SHA256 || f.Size != item.Size {
			fail(w, 409, errors.New("产物复制校验失败"))
			return
		}
		total += f.Size
		if total > exchangeTotalLimit {
			fail(w, 409, errors.New("产物总大小超过限制"))
			return
		}
		if item.Kind == "video" {
			outputHash = f.SHA256
		}
		if item.Kind == "edit" {
			editHash = f.SHA256
		}
	}
	if !kinds["video"] || !kinds["edit"] || !kinds["validation"] {
		fail(w, 409, errors.New("需要成片、EDL和验证报告"))
		return
	}
	if e = validateProductionExchange(r.Context(), sr, rr, run, completion, editHash); e != nil {
		fail(w, 409, e)
		return
	}
	var archivedEDL exchangeEDL
	if e = exchangeJSON(sr, "edit-ir.json", 4<<20, &archivedEDL); e != nil {
		fail(w, 409, e)
		return
	}
	duration, e := probeExchangeVideo(r.Context(), filepath.Join(staging, "final.mp4"), run.Duration, archivedEDL.Canvas)
	if e != nil {
		fail(w, 409, e)
		return
	}
	// Verify again after all copies/probes; no mutable exchange file is served.
	if e = verifyExchangeFiles(rr, run.Files); e != nil {
		fail(w, 409, e)
		return
	}
	if sub, e := exchangeOpen(rr, "input/subtitles.srt", 4<<20); e == nil {
		_, e = exchangeCopy(sub, sr, "subtitles.srt", 4<<20)
		sub.Close()
		if e != nil {
			fail(w, 409, e)
			return
		}
	}
	privateRoot, e := os.OpenRoot(filepath.Join(a.dir(p.ID), "production-runs", run.ID))
	if e != nil {
		fail(w, 409, e)
		return
	}
	privateSnapshot, e := exchangeOpen(privateRoot, "snapshot.json", 4<<20)
	privateRoot.Close()
	if e != nil {
		fail(w, 409, e)
		return
	}
	snapshotCopy, e := exchangeCopy(privateSnapshot, sr, "snapshot.json", 4<<20)
	privateSnapshot.Close()
	if e != nil || snapshotCopy.SHA256 != run.SnapshotHash {
		fail(w, 409, errors.New("冻结项目快照校验失败"))
		return
	}
	snapshotHash, _, e := hashLocal(filepath.Join(staging, "snapshot.json"))
	if e != nil {
		fail(w, 500, e)
		return
	}
	proof := map[string]any{"schema": "video.render-proof.v1", "key": run.InputHash, "videoHash": outputHash, "videoBytes": func() int64 {
		for _, i := range completion.Artifacts {
			if i.Kind == "video" {
				return i.Size
			}
		}
		return 0
	}(), "snapshotHash": snapshotHash, "engine": "video_use", "productionRunId": run.ID, "sourceHash": run.SourceHash, "inputHash": run.InputHash, "inputBasisHash": run.InputBasisHash, "editHash": editHash}
	if e = atomicJSON(filepath.Join(staging, "render-proof.json"), proof); e != nil {
		fail(w, 500, e)
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	latest := a.projects[p.ID]
	if latest == nil || latest.Revision != in.Revision {
		fail(w, 409, errors.New("产物校验期间项目已改变，请使用当前revision重试"))
		return
	}
	currentSelection, selectionErr := exchangeSelectionForProject(*latest)
	if selectionErr != nil || exchangeSelectionMatchesRun(*latest, run, currentSelection) != nil {
		fail(w, 409, errors.New("产物校验期间工作区映射或项目绑定已改变，未归档共享产物"))
		return
	}
	q := clone(latest)
	for _, v := range q.ProductionRuns {
		if v.ID == run.ID && v.RenderID != "" {
			writeJSON(w, 200, productionRunResponse(q, v))
			return
		}
	}
	job := Render{ProductionRequestID: run.ProductionRequestID, ID: rid, Engine: "video_use", SourceHash: run.SourceHash, ContentHash: run.InputBasisHash, Kind: "draft", Revision: run.Revision, State: "completed", Created: time.Now().UTC().Format(time.RFC3339Nano), Duration: duration, HasAudio: true, HasSubtitles: true, CacheKey: run.InputHash}
	run.State = "completed"
	run.RenderID = rid
	run.OutputHash = outputHash
	run.EditHash = editHash
	for i := range q.ProductionRuns {
		if q.ProductionRuns[i].ID == run.ID {
			q.ProductionRuns[i] = run
		}
	}
	q.Renders = append(q.Renders, job)
	q.Revision++
	dest := filepath.Join(renders, rid)
	if e = os.Rename(staging, dest); e != nil {
		fail(w, 500, e)
		return
	}
	if e = a.save(&q); e != nil {
		os.RemoveAll(dest)
		fail(w, 500, e)
		return
	}
	a.projects[q.ID] = &q
	out := productionRunResponse(q, run)
	out["render"] = job
	writeJSON(w, 200, out)
}
