package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"unicode"
	"unicode/utf8"
)

// TextReference quotes the exact authored narration. Occurrence is one-based
// and counts overlapping matches; it is required only for repeated quotes.
// It is editorial metadata, never a source of inferred speech timestamps.
type TextReference struct {
	Quote      string `json:"quote"`
	Occurrence int    `json:"occurrence,omitempty"`
}

func (v *TextReference) UnmarshalJSON(raw []byte) error {
	type plain TextReference
	var p plain
	if !utf8.Valid(raw) || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return errors.New("原文引用必须是有效UTF-8对象")
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if err := d.Decode(&p); err != nil {
		return err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return err
	}
	if value, ok := fields["occurrence"]; ok && (bytes.Equal(bytes.TrimSpace(value), []byte("null")) || p.Occurrence < 1) {
		return errors.New("occurrence如提供，必须是从1开始的整数")
	}
	if err := validateTextReferenceShape(TextReference(p)); err != nil {
		return err
	}
	*v = TextReference(p)
	return nil
}

func (v *EditClip) UnmarshalJSON(raw []byte) error {
	type plain EditClip
	var p plain
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if !utf8.Valid(raw) {
		return errors.New("片段必须使用有效UTF-8")
	}
	if err := d.Decode(&p); err != nil {
		return err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return err
	}
	if value, ok := fields["textRefs"]; ok && bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
		return errors.New("textRefs必须是引用数组，不能为null")
	}
	*v = EditClip(p)
	return nil
}

func validateTextReferenceShape(ref TextReference) error {
	if len(ref.Quote) == 0 || len(ref.Quote) > 16384 || !utf8.ValidString(ref.Quote) || strings.TrimSpace(ref.Quote) == "" || ref.Occurrence < 0 {
		return errors.New("原文引用需为1–16384字节有效UTF-8文本，occurrence为从1开始的整数")
	}
	for _, r := range ref.Quote {
		if unicode.IsControl(r) && r != '\t' && r != '\r' && r != '\n' {
			return errors.New("原文引用包含不支持的控制字符")
		}
	}
	return nil
}

func resolveTextReference(text string, ref TextReference) (int, int, int, error) {
	if err := validateTextReferenceShape(ref); err != nil {
		return -1, -1, 0, err
	}
	start, end, count := alignmentQuoteRange(text, ref.Quote, ref.Occurrence)
	if start < 0 {
		if count > 1 && ref.Occurrence == 0 {
			return -1, -1, count, fmt.Errorf("原文出现%d次，请指定从1开始的occurrence", count)
		}
		if count == 0 {
			return -1, -1, count, errors.New("原文中找不到完全一致的引用")
		}
		return -1, -1, count, fmt.Errorf("原文出现%d次，occurrence超出范围", count)
	}
	return utf8.RuneCountInString(text[:start]), utf8.RuneCountInString(text[:end]), count, nil
}

func validateTextReferences(text string, refs []TextReference) error {
	if len(refs) > 32 {
		return errors.New("每个片段最多32项原文引用")
	}
	for _, ref := range refs {
		if _, _, _, err := resolveTextReference(text, ref); err != nil {
			return err
		}
	}
	return nil
}
func validateEditTextReferenceShape(d EditDocument) error {
	anyRefs := false
	for _, clip := range d.Clips {
		if len(clip.TextRefs) > 32 {
			return editFailure("TEXT_REF", clip.ID, "每个片段最多32项原文引用")
		}
		for _, ref := range clip.TextRefs {
			anyRefs = true
			if err := validateTextReferenceShape(ref); err != nil {
				return editFailure("TEXT_REF", clip.ID, err.Error())
			}
		}
	}
	if d.TextSourceHash != "" && !exchangeDigest.MatchString(d.TextSourceHash) || anyRefs && d.TextSourceHash == "" {
		return editFailure("TEXT_SOURCE_HASH", "textSourceHash", "原文引用需要64位小写SHA256 textSourceHash")
	}
	return nil
}
func validateEditTextReferences(p Project, d EditDocument) error {
	if err := validateEditTextReferenceShape(d); err != nil {
		return err
	}
	hasRefs := false
	for _, clip := range d.Clips {
		if len(clip.TextRefs) > 0 {
			hasRefs = true
			break
		}
	}
	if !hasRefs {
		return nil
	}
	if !utf8.ValidString(p.NarrationText) || d.TextSourceHash != narrationSourceHash(p) {
		return editFailure("TEXT_SOURCE_STALE", "textSourceHash", "原文已改变，请按当前主播稿重新核对引用并更新textSourceHash")
	}
	for _, clip := range d.Clips {
		if err := validateTextReferences(p.NarrationText, clip.TextRefs); err != nil {
			return editFailure("TEXT_REF", clip.ID, err.Error())
		}
	}
	return nil
}
func compileProjectEdit(p Project, d EditDocument) (EditIR, error) {
	if err := validateEditTextReferences(p, d); err != nil {
		return EditIR{}, err
	}
	return compileEdit(d, p.Assets)
}

type EditLocateRequest struct {
	Quote      string `json:"quote"`
	Occurrence int    `json:"occurrence,omitempty"`
	RenderID   string `json:"renderId,omitempty"`
}

func (v *EditLocateRequest) UnmarshalJSON(raw []byte) error {
	type plain EditLocateRequest
	var p plain
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if err := d.Decode(&p); err != nil {
		return err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return err
	}
	if value, ok := fields["occurrence"]; ok && (bytes.Equal(bytes.TrimSpace(value), []byte("null")) || p.Occurrence < 1) {
		return errors.New("occurrence如提供，必须是从1开始的整数")
	}
	if !utf8.Valid(raw) {
		return errors.New("引用必须使用有效UTF-8")
	}
	if err := validateTextReferenceShape(TextReference{p.Quote, p.Occurrence}); err != nil {
		return err
	}
	*v = EditLocateRequest(p)
	return nil
}

type EditLocateQuery struct {
	Quote      string `json:"quote"`
	Occurrence int    `json:"occurrence"`
	StartRune  int    `json:"startRune"`
	EndRune    int    `json:"endRune"`
}
type EditLocateMatch struct {
	ClipID      string          `json:"clipId"`
	TrackID     string          `json:"trackId,omitempty"`
	Start       float64         `json:"start"`
	End         float64         `json:"end"`
	AssetID     string          `json:"assetId,omitempty"`
	TextRefs    []TextReference `json:"textRefs"`
	MatchedRefs []TextReference `json:"matchedRefs"`
}
type EditLocateResult struct {
	ProjectID       string            `json:"projectId"`
	Revision        int               `json:"revision"`
	Backend         string            `json:"backend"`
	SourceHash      string            `json:"sourceHash"`
	Current         bool              `json:"current"`
	RenderID        string            `json:"renderId,omitempty"`
	ProductionRunID string            `json:"productionRunId,omitempty"`
	Query           EditLocateQuery   `json:"query"`
	Matches         []EditLocateMatch `json:"matches"`
	UnmappedClipIDs []string          `json:"unmappedClipIds"`
	Warning         string            `json:"warning,omitempty"`
}

func (a *App) editLocate(w http.ResponseWriter, r *http.Request) {
	var in EditLocateRequest
	if !decodeStrict(w, r, &in) {
		return
	}
	p, err := a.get(r.PathValue("id"))
	if err != nil {
		fail(w, 404, err)
		return
	}
	result, source, clips, status, err := a.editLocateSource(p, in.RenderID)
	if err != nil {
		fail(w, status, err)
		return
	}
	ref := TextReference{in.Quote, in.Occurrence}
	start, end, count, err := resolveTextReference(source, ref)
	if err != nil {
		writeJSON(w, 422, map[string]any{"error": err.Error(), "code": "TEXT_QUERY", "occurrenceCount": count, "sourceHash": result.SourceHash, "current": result.Current})
		return
	}
	occurrence := in.Occurrence
	if occurrence == 0 {
		occurrence = 1
	}
	result.Query = EditLocateQuery{in.Quote, occurrence, start, end}
	for _, clip := range clips {
		if len(clip.TextRefs) == 0 {
			result.UnmappedClipIDs = append(result.UnmappedClipIDs, clip.ClipID)
			continue
		}
		matched := []TextReference{}
		for _, candidate := range clip.TextRefs {
			cs, ce, _, e := resolveTextReference(source, candidate)
			if e != nil {
				fail(w, 409, errors.New("归档剪辑包含无效原文引用"))
				return
			}
			if cs < end && start < ce {
				matched = append(matched, candidate)
			}
		}
		if len(matched) > 0 {
			clip.MatchedRefs = matched
			result.Matches = append(result.Matches, clip)
		}
	}
	writeJSON(w, 200, result)
}

// Read only private archived bytes through a rooted regular-file descriptor;
// locating never relies on the mutable agent exchange directory.
func readLocateBytes(root *os.Root, name string, max int64, wantHash string) ([]byte, error) {
	f, err := exchangeOpen(root, name, max)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, max+1))
	if err != nil || int64(len(b)) > max {
		return nil, errors.New("归档文件超出限制")
	}
	if wantHash != "" && hashBytes(b) != wantHash {
		return nil, errors.New("归档文件指纹已改变")
	}
	return b, nil
}
func nativeLocateClips(p Project) ([]EditLocateMatch, error) {
	if p.EditDSL == nil {
		return nil, errors.New("尚无剪辑DSL")
	}
	ir, err := compileProjectEdit(p, *p.EditDSL)
	if err != nil {
		return nil, err
	}
	refs := map[string][]TextReference{}
	for _, clip := range p.EditDSL.Clips {
		refs[clip.ID] = clip.TextRefs
	}
	clips := []EditLocateMatch{}
	for _, clip := range ir.Clips {
		clips = append(clips, EditLocateMatch{ClipID: clip.ID, TrackID: clip.Track, Start: clip.Start, End: clip.End, AssetID: clip.Asset, TextRefs: refs[clip.ID], MatchedRefs: []TextReference{}})
	}
	return clips, nil
}

// HyperFrames review clips describe the author's intended edit. They are not
// extracted from HTML/JavaScript or measured from rendered frames.
const hyperFramesLocateWarning = "HyperFrames片段时间和文字关联来自作者保存的审阅标注；未逐帧核验自由动画，也不代表逐字语音对齐或语义验证。"

func hyperFramesLocateClips(p Project) ([]EditLocateMatch, error) {
	if p.HyperFrames == nil {
		return nil, errors.New("尚未保存自由HyperFrames工程")
	}
	if err := validateHyperFramesProject(p, *p.HyperFrames); err != nil {
		return nil, err
	}
	clips := []EditLocateMatch{}
	for _, clip := range p.HyperFrames.Timeline {
		clips = append(clips, EditLocateMatch{ClipID: clip.ID, TrackID: clip.Track, Start: clip.Start, End: clip.Start + clip.Duration, AssetID: clip.AssetID, TextRefs: clip.TextRefs, MatchedRefs: []TextReference{}})
	}
	return clips, nil
}

func (a *App) editLocateSource(p Project, renderID string) (EditLocateResult, string, []EditLocateMatch, int, error) {
	out := EditLocateResult{ProjectID: p.ID, Revision: p.Revision, Backend: workflowFor(p).EditingBackend, SourceHash: narrationSourceHash(p), Current: true, Matches: []EditLocateMatch{}, UnmappedClipIDs: []string{}}
	if renderID == "" && out.Backend == "hyperframes" {
		out.Warning = hyperFramesLocateWarning
		clips, err := hyperFramesLocateClips(p)
		if err != nil {
			out.Current = false
			return out, "", nil, 409, err
		}
		// A saved binding whose private bytes changed is no longer a current input.
		if _, err = a.hyperFramesAssetFiles(p, *p.HyperFrames); err != nil {
			out.Current = false
			return out, "", nil, 409, err
		}
		return out, p.NarrationText, clips, 200, nil
	}
	if renderID == "" && out.Backend != "video_use" {
		clips, err := nativeLocateClips(p)
		out.Backend = "native"
		out.Current = usesEditDSL(p)
		return out, p.NarrationText, clips, 409, err
	}
	var found *Render
	for i := range p.Renders {
		job := &p.Renders[i]
		if renderID != "" && job.ID == renderID || renderID == "" && externalRenderCurrent(p, *job) {
			found = job
			if renderID != "" {
				break
			}
		}
	}
	if found == nil {
		if renderID != "" {
			return out, "", nil, 404, errors.New("该项目没有指定的渲染")
		}
		return out, "", nil, 409, errors.New("尚无当前输入对应的已归档video_use剪辑；可指定renderId查询历史版本")
	}
	if found.State != "completed" || found.Range != nil || found.Engine != "video_use" && found.Engine != "ffmpeg_edit" && found.Engine != authoredHyperFramesEngine {
		return out, "", nil, 409, errors.New("只能定位已完成的完整剪辑渲染")
	}
	root, err := os.OpenRoot(filepath.Join(a.dir(p.ID), "renders", found.ID))
	if err != nil {
		return out, "", nil, 409, errors.New("渲染归档不可读")
	}
	defer root.Close()
	proof, err := readRenderProof(filepath.Join(a.dir(p.ID), "renders", found.ID))
	if err != nil {
		return out, "", nil, 409, errors.New("渲染校验记录不可读")
	}
	snapshotHash := proof.SnapshotHash
	var run ProductionRun
	if found.Engine == "video_use" {
		ok := false
		for _, v := range p.ProductionRuns {
			if v.RenderID == found.ID && v.State == "completed" {
				run = v
				ok = true
				break
			}
		}
		if !ok || run.SourceHash != found.SourceHash || run.SnapshotHash != snapshotHash || !exchangeDigest.MatchString(run.EditHash) {
			return out, "", nil, 409, errors.New("渲染缺少匹配的冻结输入记录")
		}
	}
	if !exchangeDigest.MatchString(snapshotHash) {
		return out, "", nil, 409, errors.New("渲染快照指纹无效")
	}
	raw, err := readLocateBytes(root, "snapshot.json", 4<<20, snapshotHash)
	if err != nil {
		return out, "", nil, 409, err
	}
	var snapshot Project
	if err = json.Unmarshal(raw, &snapshot); err != nil || snapshot.ID != p.ID || narrationSourceHash(snapshot) != found.SourceHash {
		return out, "", nil, 409, errors.New("归档原文与渲染记录不一致")
	}
	out.RenderID = found.ID
	out.SourceHash = found.SourceHash
	if found.Engine == authoredHyperFramesEngine {
		if snapshot.HyperFrames == nil || found.ContentHash != hyperFramesDocumentHash(*snapshot.HyperFrames) || !a.hyperFramesArchiveIntact(p, *found, proof) {
			return out, "", nil, 409, errors.New("HyperFrames冻结工程或归档产物已改变")
		}
		out.Backend = "hyperframes"
		out.Current = renderCurrent(p, *found)
		if out.Current {
			out.Current = a.hyperFramesView(p).Current
		}
		out.Warning = hyperFramesLocateWarning
		if !out.Current {
			out.Warning += "按该渲染冻结时的原文定位；结果不代表当前剪辑。"
		}
		clips, err := hyperFramesLocateClips(snapshot)
		return out, snapshot.NarrationText, clips, 409, err
	}
	if found.Engine == "ffmpeg_edit" {
		out.Backend = "native"
		out.Current = workflowFor(p).EditingBackend == "native" && usesEditDSL(p) && productionHash(snapshot) == productionHash(p) && narrationSourceHash(p) == found.SourceHash
		clips, err := nativeLocateClips(snapshot)
		if !out.Current {
			out.Warning = "按该渲染归档时的原文定位；结果不代表当前剪辑。"
		}
		return out, snapshot.NarrationText, clips, 409, err
	}
	raw, err = readLocateBytes(root, "edit-ir.json", 4<<20, run.EditHash)
	if err != nil {
		return out, "", nil, 409, err
	}
	var edl exchangeEDL
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if err = d.Decode(&edl); err != nil {
		return out, "", nil, 409, errors.New("归档EDL无效")
	}
	out.Backend = "video_use"
	out.ProductionRunID = run.ID
	out.Current = externalRenderCurrent(p, *found)
	if !out.Current {
		out.Warning = "按该渲染冻结时的原文定位；结果不代表当前剪辑。"
	}
	clips := []EditLocateMatch{}
	for _, clip := range edl.Clips {
		if clip.Start == nil || !finite(*clip.Start) || !finite(clip.Duration) || clip.Duration <= 0 {
			return out, "", nil, 409, errors.New("归档镜头时序无效")
		}
		if err := validateTextReferences(snapshot.NarrationText, clip.TextRefs); err != nil {
			return out, "", nil, 409, err
		}
		clips = append(clips, EditLocateMatch{ClipID: clip.ID, Start: *clip.Start, End: *clip.Start + clip.Duration, AssetID: clip.AssetID, TextRefs: clip.TextRefs, MatchedRefs: []TextReference{}})
	}
	return out, snapshot.NarrationText, clips, 200, nil
}
