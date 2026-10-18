package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"regexp"
	"strings"
)

var editID = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_.-]{0,63}$`)
var editColor = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)

type EditCanvas struct {
	Width      int     `json:"width"`
	Height     int     `json:"height"`
	FPS        int     `json:"fps"`
	Background string  `json:"background,omitempty"`
	Duration   float64 `json:"duration,omitempty"`
}
type EditTrack struct {
	ID   string `json:"id"`
	Kind string `json:"kind"`
	Name string `json:"name,omitempty"`
}
type EditAnchor struct {
	ID     string   `json:"id"`
	Time   *float64 `json:"time,omitempty"`
	Clip   string   `json:"clip,omitempty"`
	Edge   string   `json:"edge,omitempty"`
	Offset float64  `json:"offset,omitempty"`
}
type EditSource struct {
	In    float64  `json:"in,omitempty"`
	Out   *float64 `json:"out,omitempty"`
	Speed float64  `json:"speed,omitempty"`
}
type EditLayout struct {
	X       int      `json:"x,omitempty"`
	Y       int      `json:"y,omitempty"`
	Width   int      `json:"width,omitempty"`
	Height  int      `json:"height,omitempty"`
	Fit     string   `json:"fit,omitempty"`
	Opacity *float64 `json:"opacity,omitempty"`
}
type EditClip struct {
	ID       string     `json:"id"`
	Track    string     `json:"track"`
	Asset    string     `json:"asset,omitempty"`
	Start    *float64   `json:"start,omitempty"`
	After    string     `json:"after,omitempty"`
	Anchor   string     `json:"anchor,omitempty"`
	Offset   float64    `json:"offset,omitempty"`
	Duration float64    `json:"duration,omitempty"`
	Source   EditSource `json:"source,omitempty"`
	Layout   EditLayout `json:"layout,omitempty"`
	Text     string     `json:"text,omitempty"`
	FontSize int        `json:"fontSize,omitempty"`
	Color    string     `json:"color,omitempty"`
	Volume   *float64   `json:"volume,omitempty"`
	FadeIn   float64    `json:"fadeIn,omitempty"`
	FadeOut  float64    `json:"fadeOut,omitempty"`
}
type EditDocument struct {
	Version int          `json:"version"`
	Canvas  EditCanvas   `json:"canvas"`
	Tracks  []EditTrack  `json:"tracks"`
	Anchors []EditAnchor `json:"anchors,omitempty"`
	Clips   []EditClip   `json:"clips"`
}

func (d *EditDocument) UnmarshalJSON(raw []byte) error {
	type plain EditDocument
	var value plain
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if e := decoder.Decode(&value); e != nil {
		return e
	}
	*d = EditDocument(value)
	return nil
}

type EditDiagnostic struct {
	Code    string `json:"code"`
	Target  string `json:"target"`
	Message string `json:"message"`
}

func (e EditDiagnostic) Error() string { return e.Target + ": " + e.Message }
func editFailure(code, target, message string) error {
	return EditDiagnostic{Code: code, Target: target, Message: message}
}

type EditIRClip struct {
	ID        string     `json:"id"`
	Track     string     `json:"track"`
	Kind      string     `json:"kind"`
	Layer     int        `json:"layer"`
	Asset     string     `json:"asset,omitempty"`
	Start     float64    `json:"start"`
	End       float64    `json:"end"`
	Duration  float64    `json:"duration"`
	SourceIn  float64    `json:"sourceIn"`
	SourceOut float64    `json:"sourceOut"`
	Speed     float64    `json:"speed"`
	Layout    EditLayout `json:"layout"`
	Text      string     `json:"text,omitempty"`
	FontSize  int        `json:"fontSize,omitempty"`
	Color     string     `json:"color,omitempty"`
	Volume    float64    `json:"volume"`
	FadeIn    float64    `json:"fadeIn,omitempty"`
	FadeOut   float64    `json:"fadeOut,omitempty"`
}
type EditIR struct {
	Schema   string             `json:"schema"`
	Canvas   EditCanvas         `json:"canvas"`
	Duration float64            `json:"duration"`
	Clips    []EditIRClip       `json:"clips"`
	Anchors  map[string]float64 `json:"anchors"`
	HasAudio bool               `json:"hasAudio"`
	HasText  bool               `json:"hasText"`
}

func compileEdit(d EditDocument, assets []Asset) (EditIR, error) {
	ir := EditIR{Schema: "video.edit-ir.v1", Canvas: d.Canvas, Clips: []EditIRClip{}, Anchors: map[string]float64{}}
	fail := func(code, target, msg string) (EditIR, error) { return ir, editFailure(code, target, msg) }
	if d.Version != 1 {
		return fail("VERSION", "version", "仅支持version=1")
	}
	c := &ir.Canvas
	if c.Width == 0 {
		c.Width = 1080
	}
	if c.Height == 0 {
		c.Height = 1920
	}
	if c.FPS == 0 {
		c.FPS = 30
	}
	if c.Background == "" {
		c.Background = "#11182a"
	}
	if c.Width < 32 || c.Width > 3840 || c.Height < 32 || c.Height > 3840 || c.Width%2 != 0 || c.Height%2 != 0 || c.Width*c.Height > 8294400 || c.FPS < 1 || c.FPS > 60 || !editColor.MatchString(c.Background) || !finite(c.Duration) || c.Duration < 0 || c.Duration > 600 {
		return fail("CANVAS", "canvas", "尺寸需为32–3840偶数且不超过4K像素数，帧率1–60，颜色为#RRGGBB，时长0–600秒")
	}
	if len(d.Tracks) < 1 || len(d.Tracks) > 20 || len(d.Clips) < 1 || len(d.Clips) > 240 || len(d.Anchors) > 300 {
		return fail("LIMIT", "document", "需要1–20轨、1–240片段，最多300锚点")
	}
	tracks := map[string]EditTrack{}
	layers := map[string]int{}
	assetsByID := map[string]Asset{}
	for _, a := range assets {
		assetsByID[a.ID] = a
	}
	for i, t := range d.Tracks {
		if !editID.MatchString(t.ID) || tracks[t.ID].ID != "" || (t.Kind != "video" && t.Kind != "audio" && t.Kind != "text") {
			return fail("TRACK", t.ID, "轨道ID须唯一，kind为video/audio/text")
		}
		tracks[t.ID] = t
		layers[t.ID] = i
	}
	clips := map[string]EditClip{}
	durations := map[string]float64{}
	speeds := map[string]float64{}
	for _, clip := range d.Clips {
		if !editID.MatchString(clip.ID) || clips[clip.ID].ID != "" {
			return fail("CLIP_ID", clip.ID, "片段ID无效或重复")
		}
		track, ok := tracks[clip.Track]
		if !ok {
			return fail("TRACK_REF", clip.ID, "引用的轨道不存在")
		}
		position := 0
		if clip.Start != nil {
			position++
		}
		if clip.After != "" {
			position++
		}
		if clip.Anchor != "" {
			position++
		}
		if position != 1 {
			return fail("PLACEMENT", clip.ID, "start、after、anchor必须且只能指定一个")
		}
		speed := clip.Source.Speed
		if speed == 0 {
			speed = 1
		}
		if !finite(speed) || speed < .5 || speed > 2 || !finite(clip.Source.In) || clip.Source.In < 0 || clip.Source.In > 86400 {
			return fail("SOURCE", clip.ID, "素材入点或速度无效；速度0.5–2")
		}
		dur := clip.Duration
		if clip.Source.Out != nil {
			out := *clip.Source.Out
			if !finite(out) || out <= clip.Source.In || out > 86400 {
				return fail("SOURCE", clip.ID, "素材出点必须大于入点且不超过86400秒")
			}
			derived := (out - clip.Source.In) / speed
			if dur == 0 {
				dur = derived
			} else if math.Abs(dur-derived) > 1e-6 {
				return fail("SOURCE_DURATION", clip.ID, "duration必须与素材入出点及速度一致")
			}
		}
		if !finite(dur) || dur < 1/float64(c.FPS) || dur > 600 || !finite(clip.Offset) {
			return fail("DURATION", clip.ID, "片段时长至少一帧且不超过600秒")
		}
		if !finite(clip.FadeIn) || !finite(clip.FadeOut) || clip.FadeIn < 0 || clip.FadeOut < 0 || clip.FadeIn+clip.FadeOut > dur {
			return fail("FADE", clip.ID, "淡入淡出非负且合计不超过片段时长")
		}
		if track.Kind == "text" {
			if clip.Layout.Fit != "" {
				return fail("TEXT_FIT", clip.ID, "文字布局不接受图片fit；请用宽高及换行控制")
			}
			if clip.Asset != "" || strings.TrimSpace(clip.Text) == "" || strings.ContainsRune(clip.Text, 0) || len([]rune(clip.Text)) > 4000 || clip.Source.In != 0 || clip.Source.Out != nil || speed != 1 || clip.Volume != nil {
				return fail("TEXT", clip.ID, "文字片段需要非空text，不接受素材、音量或源裁剪")
			}
		} else {
			asset, ok := assetsByID[clip.Asset]
			if !ok {
				return fail("ASSET_REF", clip.ID, "素材ID不存在")
			}
			if clip.Text != "" || clip.FontSize != 0 || clip.Color != "" {
				return fail("MEDIA_FIELDS", clip.ID, "媒体片段不接受文字样式")
			}
			if track.Kind == "audio" {
				if !strings.HasPrefix(asset.MIME, "audio/") {
					return fail("ASSET_TYPE", clip.ID, "音轨必须引用音频素材")
				}
				if clip.Layout != (EditLayout{}) {
					return fail("AUDIO_LAYOUT", clip.ID, "音轨不接受画面布局")
				}
			} else {
				if !strings.HasPrefix(asset.MIME, "image/") && !strings.HasPrefix(asset.MIME, "video/") {
					return fail("ASSET_TYPE", clip.ID, "视频轨必须引用图片或视频")
				}
				if clip.Volume != nil {
					return fail("VIDEO_VOLUME", clip.ID, "视频源音默认静音，请单独使用音频轨")
				}
				if strings.HasPrefix(asset.MIME, "image/") && (clip.Source.In != 0 || clip.Source.Out != nil || speed != 1) {
					return fail("IMAGE_SOURCE", clip.ID, "静态图片不接受源裁剪或速度")
				}
			}
		}
		if clip.Volume != nil && (!finite(*clip.Volume) || *clip.Volume < 0 || *clip.Volume > 2) {
			return fail("VOLUME", clip.ID, "音量范围0–2")
		}
		clips[clip.ID] = clip
		durations[clip.ID] = dur
		speeds[clip.ID] = speed
	}
	anchors := map[string]EditAnchor{}
	for _, anchor := range d.Anchors {
		if !editID.MatchString(anchor.ID) || anchors[anchor.ID].ID != "" || !finite(anchor.Offset) {
			return fail("ANCHOR", anchor.ID, "锚点ID或偏移无效")
		}
		if (anchor.Time == nil) == (anchor.Clip == "") {
			return fail("ANCHOR", anchor.ID, "锚点必须指定time或clip，不能同时指定")
		}
		if anchor.Time != nil && anchor.Edge != "" {
			return fail("ANCHOR", anchor.ID, "绝对锚点不接受edge")
		}
		if anchor.Edge != "" && anchor.Edge != "start" && anchor.Edge != "end" {
			return fail("ANCHOR", anchor.ID, "edge须为start/end")
		}
		anchors[anchor.ID] = anchor
	}
	states := map[string]int{}
	starts := map[string]float64{}
	var start func(string) (float64, error)
	var anchorTime func(string) (float64, error)
	enter := func(key string) error {
		if states[key] == 1 {
			return editFailure("CYCLE", key, "时间引用形成循环")
		}
		states[key] = 1
		return nil
	}
	anchorTime = func(id string) (float64, error) {
		if states["anchor:"+id] == 2 {
			return ir.Anchors[id], nil
		}
		a, ok := anchors[id]
		if !ok {
			return 0, editFailure("ANCHOR_REF", id, "锚点不存在")
		}
		if e := enter("anchor:" + id); e != nil {
			return 0, e
		}
		t := 0.0
		if a.Time != nil {
			t = *a.Time
		} else {
			var e error
			t, e = start(a.Clip)
			if e != nil {
				return 0, e
			}
			if a.Edge != "start" {
				t += durations[a.Clip]
			}
		}
		t += a.Offset
		if !finite(t) || t < 0 || t > 600 {
			return 0, editFailure("ANCHOR_TIME", id, "锚点时间超出0–600秒")
		}
		ir.Anchors[id] = t
		states["anchor:"+id] = 2
		return t, nil
	}
	start = func(id string) (float64, error) {
		if states["clip:"+id] == 2 {
			return starts[id], nil
		}
		clip, ok := clips[id]
		if !ok {
			return 0, editFailure("CLIP_REF", id, "引用片段不存在")
		}
		if e := enter("clip:" + id); e != nil {
			return 0, e
		}
		t := 0.0
		var e error
		if clip.Start != nil {
			t = *clip.Start
		} else if clip.After != "" {
			t, e = start(clip.After)
			t += durations[clip.After]
		} else {
			t, e = anchorTime(clip.Anchor)
		}
		if e != nil {
			return 0, e
		}
		t += clip.Offset
		if !finite(t) || t < 0 || t+durations[id] > 600 {
			return 0, editFailure("CLIP_TIME", id, "片段起止时间须在0–600秒")
		}
		starts[id] = t
		states["clip:"+id] = 2
		return t, nil
	}
	for _, clip := range d.Clips {
		t, e := start(clip.ID)
		if e != nil {
			return ir, e
		}
		kind := tracks[clip.Track].Kind
		layout := clip.Layout
		if kind != "audio" {
			if layout.Width == 0 {
				layout.Width = c.Width
			}
			if layout.Height == 0 {
				layout.Height = c.Height
			}
			if layout.Fit == "" {
				layout.Fit = "cover"
			}
			if layout.Fit != "cover" && layout.Fit != "contain" {
				return fail("FIT", clip.ID, "fit须为cover/contain")
			}
			if layout.Width < 1 || layout.Height < 1 || layout.Width > c.Width*2 || layout.Height > c.Height*2 || layout.X < -c.Width || layout.X > c.Width || layout.Y < -c.Height || layout.Y > c.Height {
				return fail("LAYOUT", clip.ID, "布局范围超出画布限制")
			}
			if layout.Opacity != nil && (!finite(*layout.Opacity) || *layout.Opacity < 0 || *layout.Opacity > 1) {
				return fail("OPACITY", clip.ID, "透明度范围0–1")
			}
		}
		fontSize := clip.FontSize
		if fontSize == 0 {
			fontSize = 44
		}
		color := clip.Color
		if color == "" {
			color = "#ffffff"
		}
		if kind == "text" && (fontSize < 8 || fontSize > 240 || !editColor.MatchString(color)) {
			return fail("TEXT_STYLE", clip.ID, "字号8–240，颜色#RRGGBB")
		}
		volume := 1.0
		if clip.Volume != nil {
			volume = *clip.Volume
		}
		ir.Clips = append(ir.Clips, EditIRClip{ID: clip.ID, Track: clip.Track, Kind: kind, Layer: layers[clip.Track], Asset: clip.Asset, Start: t, End: t + durations[clip.ID], Duration: durations[clip.ID], SourceIn: clip.Source.In, SourceOut: clip.Source.In + durations[clip.ID]*speeds[clip.ID], Speed: speeds[clip.ID], Layout: layout, Text: clip.Text, FontSize: fontSize, Color: color, Volume: volume, FadeIn: clip.FadeIn, FadeOut: clip.FadeOut})
		ir.Duration = math.Max(ir.Duration, t+durations[clip.ID])
		ir.HasAudio = ir.HasAudio || kind == "audio"
		ir.HasText = ir.HasText || kind == "text"
	}
	for id := range anchors {
		if _, e := anchorTime(id); e != nil {
			return ir, e
		}
	}
	if c.Duration > 0 {
		if c.Duration < ir.Duration {
			return fail("CANVAS_DURATION", "canvas.duration", "画布时长不能截断片段")
		}
		ir.Duration = c.Duration
	}
	ir.Duration = math.Ceil(ir.Duration*float64(c.FPS)-1e-9) / float64(c.FPS)
	return ir, nil
}

type EditRequest struct {
	Revision int           `json:"revision"`
	DSL      *EditDocument `json:"dsl"`
}

func editErrorResponse(w http.ResponseWriter, e error) {
	diagnostic, ok := e.(EditDiagnostic)
	if !ok {
		diagnostic = EditDiagnostic{Code: "INVALID", Target: "document", Message: e.Error()}
	}
	writeJSON(w, 422, map[string]any{"ok": false, "diagnostics": []EditDiagnostic{diagnostic}})
}
func (a *App) editGet(w http.ResponseWriter, r *http.Request) {
	p, e := a.get(r.PathValue("id"))
	if e != nil {
		fail(w, 404, e)
		return
	}
	result := map[string]any{"revision": p.Revision, "dsl": p.EditDSL, "active": p.EditActive}
	if p.EditDSL != nil {
		ir, e := compileEdit(*p.EditDSL, p.Assets)
		if e != nil {
			editErrorResponse(w, e)
			return
		}
		result["ir"] = ir
	}
	writeJSON(w, 200, result)
}
func (a *App) editValidate(w http.ResponseWriter, r *http.Request) {
	var in EditRequest
	if !decodeStrict(w, r, &in) {
		return
	}
	p, e := a.get(r.PathValue("id"))
	if e != nil {
		fail(w, 404, e)
		return
	}
	if in.DSL == nil {
		fail(w, 400, fmt.Errorf("需要dsl对象"))
		return
	}
	ir, e := compileEdit(*in.DSL, p.Assets)
	if e != nil {
		editErrorResponse(w, e)
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true, "revision": p.Revision, "ir": ir})
}
func (a *App) editSave(w http.ResponseWriter, r *http.Request) {
	var in EditRequest
	if !decodeStrict(w, r, &in) {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	p := a.projects[r.PathValue("id")]
	if p == nil {
		fail(w, 404, fmt.Errorf("项目不存在"))
		return
	}
	if in.Revision != p.Revision {
		fail(w, 409, fmt.Errorf("项目版本冲突，请重新读取后合并"))
		return
	}
	if in.DSL == nil {
		fail(w, 400, fmt.Errorf("需要dsl对象"))
		return
	}
	ir, e := compileEdit(*in.DSL, p.Assets)
	if e != nil {
		editErrorResponse(w, e)
		return
	}
	q := clone(p)
	q.EditDSL = in.DSL
	q.EditActive = true
	q.Revision++
	if e = a.save(&q); e != nil {
		fail(w, 500, e)
		return
	}
	a.projects[q.ID] = &q
	writeJSON(w, 200, map[string]any{"revision": q.Revision, "dsl": q.EditDSL, "ir": ir})
}

func usesEditDSL(p Project) bool { return p.EditDSL != nil && p.EditActive }
func (a *App) editActivate(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Revision int  `json:"revision"`
		Active   bool `json:"active"`
	}
	if !decodeStrict(w, r, &in) {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	p := a.projects[r.PathValue("id")]
	if p == nil {
		fail(w, 404, fmt.Errorf("项目不存在"))
		return
	}
	if in.Revision != p.Revision {
		fail(w, 409, fmt.Errorf("项目版本冲突"))
		return
	}
	if in.Active && p.EditDSL == nil {
		fail(w, 409, fmt.Errorf("请先保存剪辑DSL"))
		return
	}
	q := clone(p)
	q.EditActive = in.Active
	q.Revision++
	if e := a.save(&q); e != nil {
		fail(w, 500, e)
		return
	}
	a.projects[q.ID] = &q
	writeJSON(w, 200, q)
}
