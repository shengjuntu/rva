package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type ReviewTextReference struct {
	Quote     string `json:"quote"`
	StartRune int    `json:"startRune"`
	EndRune   int    `json:"endRune"`
}
type ReviewClip struct {
	ID       string                `json:"id"`
	Start    float64               `json:"start"`
	End      float64               `json:"end"`
	Label    string                `json:"label"`
	Text     string                `json:"text,omitempty"`
	AssetID  string                `json:"assetId,omitempty"`
	TextRefs []ReviewTextReference `json:"textRefs,omitempty"`
}
type ReviewTrack struct {
	ID    string       `json:"id"`
	Kind  string       `json:"kind"`
	Label string       `json:"label"`
	Clips []ReviewClip `json:"clips"`
}
type ReviewTimeline struct {
	ProjectID  string        `json:"projectId"`
	Revision   int           `json:"revision"`
	Backend    string        `json:"backend"`
	RenderID   string        `json:"renderId,omitempty"`
	SourceHash string        `json:"sourceHash"`
	Source     string        `json:"source"`
	Current    bool          `json:"current"`
	Duration   float64       `json:"duration"`
	Text       string        `json:"text"`
	Warnings   []string      `json:"warnings"`
	Tracks     []ReviewTrack `json:"tracks"`
}

func reviewKind(kind, track string) string {
	switch kind {
	case "music":
		return "music"
	case "audio", "voice", "narration":
		if strings.Contains(strings.ToLower(track), "music") || strings.Contains(track, "音乐") {
			return "music"
		}
		return "narration"
	case "text", "subtitle":
		return "subtitle"
	case "animation":
		return "animation"
	default:
		return "visual"
	}
}
func reviewReferences(text string, refs []TextReference) []ReviewTextReference {
	var out []ReviewTextReference
	for _, ref := range refs {
		start, end, _, err := resolveTextReference(text, ref)
		if err == nil {
			out = append(out, ReviewTextReference{Quote: ref.Quote, StartRune: start, EndRune: end})
		}
	}
	return out
}
func reviewTimelineFor(p Project) ReviewTimeline {
	v := ReviewTimeline{ProjectID: p.ID, Revision: p.Revision, Backend: workflowFor(p).EditingBackend, SourceHash: narrationSourceHash(p), Source: "compiled", Current: true, Text: p.NarrationText, Warnings: []string{}, Tracks: []ReviewTrack{}}
	labels := map[string]string{"visual": "画面", "narration": "配音", "subtitle": "字幕", "music": "音乐", "animation": "动画"}
	for _, kind := range []string{"visual", "narration", "subtitle", "music"} {
		v.Tracks = append(v.Tracks, ReviewTrack{ID: kind, Kind: kind, Label: labels[kind], Clips: []ReviewClip{}})
	}
	add := func(kind string, clip ReviewClip) {
		if !finite(clip.Start) || !finite(clip.End) || clip.Start < 0 || clip.End <= clip.Start {
			v.Warnings = append(v.Warnings, "片段时间无效："+clip.ID)
			return
		}
		if clip.Label == "" {
			clip.Label = clip.ID
		}
		for i := range v.Tracks {
			if v.Tracks[i].Kind == kind {
				v.Tracks[i].Clips = append(v.Tracks[i].Clips, clip)
				if clip.End > v.Duration {
					v.Duration = clip.End
				}
				return
			}
		}
		v.Tracks = append(v.Tracks, ReviewTrack{ID: kind, Kind: kind, Label: labels[kind], Clips: []ReviewClip{clip}})
		if clip.End > v.Duration {
			v.Duration = clip.End
		}
	}
	if v.Backend == "hyperframes" {
		v.Source = "authored"
		v.Warnings = append(v.Warnings, "HyperFrames 轨道是与工程一起保存的创作标注；未逐帧核验其与自由动画代码完全一致。")
		if p.HyperFrames == nil {
			v.Current = false
			v.Warnings = append(v.Warnings, "尚未提交 HyperFrames 工程")
			return v
		}
		v.Duration = p.HyperFrames.Canvas.Duration
		v.Current = p.HyperFrames.TextSourceHash == v.SourceHash
		if !v.Current {
			v.Warnings = append(v.Warnings, "工程对应旧正文，请重新核对配音、字幕和文字关联。")
		}
		for _, c := range p.HyperFrames.Timeline {
			add(reviewKind(c.Kind, c.Track), ReviewClip{ID: c.ID, Start: c.Start, End: c.Start + c.Duration, Label: c.Label, AssetID: c.AssetID, TextRefs: reviewReferences(p.NarrationText, c.TextRefs)})
		}
		if len(p.HyperFrames.Timeline) == 0 {
			v.Warnings = append(v.Warnings, "工程尚未提供审阅片段标注，无法从任意 JavaScript 推断剪辑意图。")
		}
		return v
	}
	if v.Backend == "video_use" {
		v.Source = "archived_edl"
		v.Current = false
		v.Warnings = append(v.Warnings, "选择一份已归档成片查看其冻结 EDL。")
		return v
	}
	if usesEditDSL(p) {
		ir, err := compileProjectEdit(p, *p.EditDSL)
		if err != nil {
			v.Current = false
			v.Warnings = append(v.Warnings, err.Error())
			return v
		}
		v.Duration = ir.Duration
		refs := map[string][]TextReference{}
		for _, c := range p.EditDSL.Clips {
			refs[c.ID] = c.TextRefs
		}
		for _, c := range ir.Clips {
			kind := reviewKind(c.Kind, c.Track)
			if p.Audio != nil && c.Asset == p.Audio.Music.AssetID {
				kind = "music"
			}
			add(kind, ReviewClip{ID: c.ID, Start: c.Start, End: c.End, Label: c.ID, Text: c.Text, AssetID: c.Asset, TextRefs: reviewReferences(p.NarrationText, refs[c.ID])})
		}
		return v
	}
	v.Source = "legacy_storyboard"
	position := 0.0
	for _, shot := range p.Shots {
		add("visual", ReviewClip{ID: shot.ID, Start: position, End: position + shot.Duration, Label: shot.Title, Text: shot.Text, AssetID: shot.AssetID})
		position += shot.Duration
	}
	if p.Audio != nil {
		for _, audio := range []struct {
			kind  string
			value AudioTrack
		}{{"narration", p.Audio.Narration}, {"music", p.Audio.Music}} {
			if audio.value.AssetID != "" && audio.value.Start < position {
				add(audio.kind, ReviewClip{ID: audio.kind, Start: audio.value.Start, End: position, Label: labels[audio.kind], AssetID: audio.value.AssetID})
			}
		}
	}
	if p.Subtitles != nil && p.Subtitles.Enabled {
		for i, c := range p.Subtitles.Cues {
			add("subtitle", ReviewClip{ID: fmt.Sprintf("subtitle-%d", i+1), Start: c.Start, End: c.End, Label: c.Text, Text: c.Text})
		}
		if p.Subtitles.Status != "ready" {
			v.Warnings = append(v.Warnings, "字幕关联尚未就绪："+p.Subtitles.Reason)
		}
	}
	return v
}

func (a *App) reviewTimeline(w http.ResponseWriter, r *http.Request) {
	p, err := a.get(r.PathValue("id"))
	if err != nil {
		fail(w, 404, err)
		return
	}
	original := p
	rid := r.URL.Query().Get("renderId")
	var found *Render
	if rid != "" {
		if !identifier.MatchString(rid) {
			fail(w, 400, errors.New("renderId无效"))
			return
		}
		for _, j := range p.Renders {
			if j.ID == rid {
				copy := j
				found = &copy
				break
			}
		}
		if found == nil {
			fail(w, 404, errors.New("渲染不存在"))
			return
		}
		if found.State != "completed" {
			fail(w, 409, errors.New("需选择已完成的成片"))
			return
		}
		root, e := os.OpenRoot(filepath.Join(a.dir(p.ID), "renders", rid))
		if e != nil {
			fail(w, 409, e)
			return
		}
		defer root.Close()
		f, e := root.Open("snapshot.json")
		if e != nil {
			fail(w, 409, e)
			return
		}
		raw, e := io.ReadAll(io.LimitReader(f, (4<<20)+1))
		f.Close()
		if e != nil || len(raw) > 4<<20 || json.Unmarshal(raw, &p) != nil || p.ID != original.ID {
			fail(w, 409, errors.New("渲染快照不可读取"))
			return
		}
		if proof, e := readRenderProof(filepath.Join(a.dir(p.ID), "renders", rid)); e != nil || proof.SnapshotHash != hashBytes(raw) {
			fail(w, 409, errors.New("渲染快照缺少完整性证据或已改变"))
			return
		}
	}
	v := reviewTimelineFor(p)
	if rid != "" {
		v.RenderID = rid
		v.Current = renderCurrent(original, *found)
		if found.Range != nil {
			if v.Duration < found.Range.End {
				v.Duration = found.Range.End
			}
			v.Warnings = append(v.Warnings, "局部预览保持工程时间坐标；播放器从范围起点计算本地时间。")
		} else {
			v.Duration = found.Duration
		}
		if found.Engine == authoredHyperFramesEngine {
			proof, e := readRenderProof(filepath.Join(a.dir(p.ID), "renders", rid))
			if e != nil || !a.hyperFramesArchiveIntact(original, *found, proof) {
				fail(w, 409, errors.New("HyperFrames冻结工程或归档产物已改变"))
				return
			}
			if v.Current {
				live := a.hyperFramesView(original)
				v.Current = live.Current
				v.Warnings = append(v.Warnings, live.StaleReasons...)
			}
		}
		if found.Engine == "video_use" {
			root, e := os.OpenRoot(filepath.Join(a.dir(p.ID), "renders", rid))
			if e != nil {
				fail(w, 409, e)
				return
			}
			defer root.Close()
			var edl exchangeEDL
			editHash := ""
			for _, run := range original.ProductionRuns {
				if run.RenderID == rid && run.State == "completed" && run.SourceHash == found.SourceHash {
					editHash = run.EditHash
					break
				}
			}
			if !exchangeDigest.MatchString(editHash) {
				fail(w, 409, errors.New("归档缺少匹配的EDL摘要"))
				return
			}
			raw, e := readLocateBytes(root, "edit-ir.json", 4<<20, editHash)
			if e != nil || json.Unmarshal(raw, &edl) != nil {
				fail(w, 409, errors.New("归档EDL已改变或不可读取"))
				return
			}
			v.Warnings = []string{}
			v.Source = "archived_edl"
			for _, c := range edl.Clips {
				if c.Start != nil {
					v.Tracks[0].Clips = append(v.Tracks[0].Clips, ReviewClip{ID: c.ID, Start: *c.Start, End: *c.Start + c.Duration, Label: c.Note, AssetID: c.AssetID, TextRefs: reviewReferences(p.NarrationText, c.TextRefs)})
				}
			}
			v.Warnings = append(v.Warnings, "此归档仅保存画面 EDL；未从缺失数据推断音频、音乐或字幕轨。")
		}
		if !v.Current {
			v.Warnings = append(v.Warnings, "当前查看历史成片，其文字和轨道来自该版快照。")
		}
	} else if p.HyperFrames != nil && v.Backend == "hyperframes" {
		view := a.hyperFramesView(p)
		v.Current = view.Current
		v.Warnings = append(v.Warnings, view.StaleReasons...)
	}
	for i := range v.Tracks {
		sort.SliceStable(v.Tracks[i].Clips, func(x, y int) bool { return v.Tracks[i].Clips[x].Start < v.Tracks[i].Clips[y].Start })
	}
	writeJSON(w, 200, v)
}
