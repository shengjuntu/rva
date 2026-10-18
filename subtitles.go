package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"
)

// Cue times are absolute seconds in the output video, independent of shots.
type SubtitleCue struct {
	Start float64 `json:"start"`
	End   float64 `json:"end"`
	Text  string  `json:"text"`
}
type SubtitleBinding struct {
	ScriptHash    string  `json:"scriptHash,omitempty"`
	AssetID       string  `json:"assetId"`
	SHA256        string  `json:"sha256"`
	AudioDuration float64 `json:"audioDuration"`
	Start         float64 `json:"start"`
	SourceStart   float64 `json:"sourceStart"`
	TextHash      string  `json:"textHash"`
	Method        string  `json:"method"` // news_package or manual_review; never an ASR claim
}
type SubtitleTrack struct {
	Enabled bool            `json:"enabled"`
	Cues    []SubtitleCue   `json:"cues"`
	Draft   bool            `json:"draft"`
	Binding SubtitleBinding `json:"binding"`
	Status  string          `json:"status"` // derived; cannot be set by project PUT
	Reason  string          `json:"reason"`
}

func projectDuration(p Project) float64 {
	total := 0.0
	for _, s := range p.Shots {
		total += s.Duration
	}
	return total
}
func storyboardText(p Project) string {
	var b strings.Builder
	for _, s := range p.Shots {
		b.WriteString(s.Text)
	}
	return compactNewsText(b.String())
}
func parseSubtitles(raw string) ([]SubtitleCue, error) {
	if len(raw) > 128<<10 || !utf8.ValidString(raw) || strings.ContainsRune(raw, 0) {
		return nil, errors.New("SRT 必须为 UTF-8 文本，最大 128 KiB")
	}
	raw = strings.ReplaceAll(strings.TrimSpace(strings.TrimPrefix(raw, "\ufeff")), "\r\n", "\n")
	blocks := regexp.MustCompile(`\n[\t ]*\n`).Split(raw, -1)
	if len(blocks) > 300 {
		return nil, errors.New("字幕最多 300 段")
	}
	cues := []SubtitleCue{}
	for i, block := range blocks {
		lines := strings.Split(strings.TrimSpace(block), "\n")
		if len(lines) < 3 {
			return nil, errors.New("SRT 格式无效")
		}
		n, err := strconv.Atoi(lines[0])
		if err != nil || n != i+1 {
			return nil, errors.New("SRT 序号须从 1 连续递增")
		}
		m := srtTime.FindStringSubmatch(lines[1])
		if m == nil {
			return nil, errors.New("SRT 时间无效")
		}
		times := [2]float64{}
		for j, base := range []int{1, 5} {
			for k, mult := range []float64{3600, 60, 1, .001} {
				x, _ := strconv.ParseFloat(m[base+k], 64)
				times[j] += x * mult
			}
		}
		cues = append(cues, SubtitleCue{times[0], times[1], strings.Join(lines[2:], "\n")})
	}
	return cues, validateCues(cues, 600)
}
func validateCues(cues []SubtitleCue, duration float64) error {
	if len(cues) == 0 || len(cues) > 300 {
		return errors.New("字幕需要 1–300 段")
	}
	end := 0.0
	for _, c := range cues {
		if !finite(c.Start) || !finite(c.End) || c.Start < end || c.End <= c.Start || c.End > duration+1e-6 {
			return errors.New("字幕时间重叠、倒序或超出分镜总长")
		}
		if strings.TrimSpace(c.Text) == "" || len([]rune(c.Text)) > 120 || strings.Count(c.Text, "\n") > 2 {
			return errors.New("每段字幕需 1–120 字、最多 3 行")
		}
		end = c.End
	}
	return nil
}
func srtStamp(seconds float64) string {
	ms := int64(math.Round(seconds * 1000))
	return fmt.Sprintf("%02d:%02d:%02d,%03d", ms/3600000, ms/60000%60, ms/1000%60, ms%1000)
}
func subtitleSRT(cues []SubtitleCue) string {
	var b strings.Builder
	for i, c := range cues {
		fmt.Fprintf(&b, "%d\n%s --> %s\n%s\n\n", i+1, srtStamp(c.Start), srtStamp(c.End), c.Text)
	}
	return b.String()
}
func refreshSubtitleState(p *Project) {
	t := p.Subtitles
	if t == nil {
		return
	}
	t.Status, t.Reason = "disabled", "字幕未启用"
	if !t.Enabled {
		return
	}
	t.Status = "stale"
	if e := validateCues(t.Cues, projectDuration(*p)); e != nil {
		t.Reason = e.Error()
		return
	}
	if p.Audio == nil || p.Audio.Narration.AssetID == "" || p.Audio.Narration.Volume <= 0 {
		t.Reason = "请先选择可听的配音，并核对字幕"
		return
	}
	n, b := p.Audio.Narration, t.Binding
	if t.Draft && b.AssetID == "" {
		t.Status, t.Reason = "unreviewed", "字幕草稿尚未试听核对"
		return
	}
	if n.AssetID != b.AssetID || n.Start != b.Start || n.SourceStart != b.SourceStart || n.Loop {
		t.Reason = "配音或音频起点已改变，请重新对齐并核对字幕"
		return
	}
	if hashBytes([]byte(storyboardText(*p))) != b.TextHash {
		t.Reason = "分镜正文已改写，请重新配音或核对现有配音与字幕"
		return
	}
	if b.ScriptHash != "" && b.ScriptHash != speechScriptHash(*p) {
		t.Reason = "分镜正文或结构已改变，请重新配音或核对字幕"
		return
	}
	if e := subtitleAudioBounds(*p, b.AudioDuration); e != nil {
		t.Reason = e.Error()
		return
	}
	if t.Draft || b.SHA256 == "" {
		t.Status, t.Reason = "unreviewed", "字幕草稿尚未试听核对"
		return
	}
	t.Status, t.Reason = "ready", "字幕已绑定当前正文与配音；尚未自动转写核验"
}
func subtitleAudioBounds(p Project, duration float64) error {
	if p.Audio == nil || p.Audio.Narration.AssetID == "" || p.Audio.Narration.Volume <= 0 {
		return errors.New("请先选择可听的配音")
	}
	n := p.Audio.Narration
	end := n.Start + duration - n.SourceStart
	if !finite(duration) || duration <= n.SourceStart || end > projectDuration(p)+.034 {
		return errors.New("分镜总长不足以播放完整配音，请调整时长或截取起点")
	}
	for _, c := range p.Subtitles.Cues {
		if c.Start < n.Start-.001 || c.End > end+.15 {
			return errors.New("字幕超出配音播放区间，请重新对齐")
		}
	}
	return nil
}

// Inspect a private copy so a workspace edit cannot change bytes between hash
// verification and ffprobe. Publishing still rechecks the project revision.
func (a *App) bindSubtitles(ctx context.Context, p *Project, method string) error {
	if e := validateCues(p.Subtitles.Cues, projectDuration(*p)); e != nil {
		return e
	}
	if p.Audio == nil || p.Audio.Narration.AssetID == "" {
		return errors.New("请先选择配音")
	}
	dir, e := os.MkdirTemp(a.data, "subtitle-review-")
	if e != nil {
		return e
	}
	defer os.RemoveAll(dir)
	if e = os.Mkdir(filepath.Join(dir, "assets"), 0700); e != nil {
		return e
	}
	var asset Asset
	for _, x := range p.Assets {
		if x.ID == p.Audio.Narration.AssetID {
			asset = x
		}
	}
	if asset.ID == "" {
		return errors.New("配音素材不存在")
	}
	if e = snapshotAsset(a.dir(p.ID), dir, asset); e != nil {
		return e
	}
	file := filepath.Join(dir, "assets", asset.File)
	duration, e := audioDuration(ctx, file)
	if e != nil {
		return e
	}
	if e = subtitleAudioBounds(*p, duration); e != nil {
		return e
	}
	f, e := os.Open(file)
	if e != nil {
		return e
	}
	sum, _, e := hashReader(f)
	f.Close()
	if e != nil {
		return e
	}
	n := p.Audio.Narration
	p.Subtitles.Binding = SubtitleBinding{ScriptHash: speechScriptHash(*p), AssetID: n.AssetID, SHA256: sum, AudioDuration: duration, Start: n.Start, SourceStart: n.SourceStart, TextHash: hashBytes([]byte(storyboardText(*p))), Method: method}
	p.Subtitles.Draft = false
	return nil
}
func (a *App) publishSubtitleProject(p Project) (Project, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	current := a.projects[p.ID]
	if current == nil || current.Revision != p.Revision {
		return Project{}, errors.New("项目已被其他操作修改，请重新载入后合并")
	}
	// Render/bridge metadata may change without a revision bump.
	q := clone(current)
	q.Subtitles, q.Audio, q.Shots, q.Assets = p.Subtitles, p.Audio, p.Shots, p.Assets
	q.Revision++
	if e := a.save(&q); e != nil {
		return Project{}, e
	}
	a.projects[q.ID] = &q
	return clone(&q), nil
}
func (a *App) updateSubtitles(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Revision int    `json:"revision"`
		SRT      string `json:"srt"`
		Enabled  bool   `json:"enabled"`
		Reviewed bool   `json:"reviewed"`
	}
	if !decode(w, r, &in) {
		return
	}
	p, e := a.get(r.PathValue("id"))
	if e != nil {
		fail(w, 404, e)
		return
	}
	if p.Revision != in.Revision {
		fail(w, 409, errors.New("字幕修改版本冲突，请重新载入"))
		return
	}
	if !in.Enabled && strings.TrimSpace(in.SRT) == "" {
		p.Subtitles = nil
	} else {
		cues, e := parseSubtitles(in.SRT)
		if e != nil {
			fail(w, 422, e)
			return
		}
		t := &SubtitleTrack{Enabled: in.Enabled, Cues: cues, Draft: true}
		if p.Subtitles != nil {
			t.Binding = p.Subtitles.Binding
			if subtitleSRT(cues) == subtitleSRT(p.Subtitles.Cues) {
				t.Draft = p.Subtitles.Draft
			}
		}
		p.Subtitles = t
		if in.Reviewed {
			if e = a.bindSubtitles(r.Context(), &p, "manual_review"); e != nil {
				fail(w, 422, e)
				return
			}
		}
	}
	q, e := a.publishSubtitleProject(p)
	if e != nil {
		fail(w, 409, e)
		return
	}
	writeJSON(w, 200, q)
}
func (a *App) subtitleFile(w http.ResponseWriter, r *http.Request) {
	p, e := a.get(r.PathValue("id"))
	if e != nil || p.Subtitles == nil {
		fail(w, 404, errors.New("项目没有字幕"))
		return
	}
	w.Header().Set("Content-Type", "application/x-subrip; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="subtitles.srt"`)
	_, _ = io.WriteString(w, subtitleSRT(p.Subtitles.Cues))
}

func (a *App) adoptNewsAudio(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Revision int `json:"revision"`
	}
	if !decode(w, r, &in) {
		return
	}
	p, e := a.get(r.PathValue("id"))
	if e != nil {
		fail(w, 404, e)
		return
	}
	if p.Revision != in.Revision {
		fail(w, 409, errors.New("项目版本冲突，请重新载入"))
		return
	}
	if p.News == nil || p.News.Video == nil {
		fail(w, 409, errors.New("请先接收对应的原始成片"))
		return
	}
	group := filepath.Join("videos", p.News.Video.ID)
	f, e := readNewsFile(a.dir(p.ID), group, "subtitles.srt", p.News.Video.Files["subtitles.srt"])
	if e != nil {
		fail(w, 409, e)
		return
	}
	raw, e := io.ReadAll(io.LimitReader(f, (128<<10)+1))
	f.Close()
	if e != nil {
		fail(w, 409, e)
		return
	}
	cues, e := parseSubtitles(string(raw))
	if e != nil {
		fail(w, 422, e)
		return
	}
	var text strings.Builder
	for _, c := range cues {
		text.WriteString(c.Text)
	}
	if compactNewsText(text.String()) != storyboardText(p) {
		fail(w, 409, errors.New("当前正文已不同于原稿，不能直接采用原稿配音；请重新配音并导入对应字幕"))
		return
	}
	f, e = readNewsFile(a.dir(p.ID), group, "narration.wav", p.News.Video.Files["narration.wav"])
	if e != nil {
		fail(w, 409, e)
		return
	}
	defer f.Close()
	asset := Asset{ID: uid(), Name: "原稿配音.wav", MIME: "audio/wav", Size: p.News.Video.Files["narration.wav"].Size}
	asset.File = asset.ID + ".wav"
	dest := filepath.Join(a.dir(p.ID), "workspace", "assets", asset.File)
	out, e := os.OpenFile(dest, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if e != nil {
		fail(w, 500, e)
		return
	}
	_, e = io.Copy(out, f)
	ce := out.Close()
	if e == nil {
		e = ce
	}
	committed := false
	defer func() {
		if !committed {
			os.Remove(dest)
		}
	}()
	if e != nil {
		fail(w, 500, e)
		return
	}
	duration, e := audioDuration(r.Context(), dest)
	if e != nil {
		fail(w, 422, e)
		return
	}
	// Visual cuts are proportional estimates; only SRT cues keep measured times.
	total := math.Ceil(math.Max(duration, cues[len(cues)-1].End)*30) / 30
	old := projectDuration(p)
	if old <= 0 || total > 600 {
		fail(w, 422, errors.New("分镜或配音总长无效"))
		return
	}
	for i := range p.Shots {
		p.Shots[i].Duration *= total / old
		if p.Shots[i].Duration < 1 || p.Shots[i].Duration > 60 {
			fail(w, 422, errors.New("按配音缩放后镜头超出 1–60 秒，请先合并或拆分镜头"))
			return
		}
	}
	p.Assets = append(p.Assets, asset)
	if p.Audio == nil {
		p.Audio = &AudioMix{}
	}
	p.Audio.Narration = AudioTrack{AssetID: asset.ID, Volume: 1}
	if e = validateAudio(p.Audio, p.Assets, total); e != nil {
		fail(w, 422, e)
		return
	}
	p.Subtitles = &SubtitleTrack{Enabled: true, Cues: cues}
	if e = a.bindSubtitles(r.Context(), &p, "news_package"); e != nil {
		fail(w, 422, e)
		return
	}
	if p.Subtitles.Binding.SHA256 != p.News.Video.Files["narration.wav"].SHA256 {
		fail(w, 409, errors.New("配音复制时发生改变"))
		return
	}
	q, e := a.publishSubtitleProject(p)
	if e != nil {
		fail(w, 409, e)
		return
	}
	committed = true
	writeJSON(w, 200, q)
}

func draftSubtitlePreviewAllowed(p Project) bool {
	if p.Subtitles == nil || !p.Subtitles.Enabled {
		return true
	}
	// Evaluate only structural binding; this does not record human review.
	t := *p.Subtitles
	t.Draft = false
	p.Subtitles = &t
	refreshSubtitleState(&p)
	return t.Status == "ready"
}
func verifySubtitleSnapshot(p Project, dir string, draft ...bool) error {
	if p.Subtitles == nil || !p.Subtitles.Enabled {
		return nil
	}
	refreshSubtitleState(&p)
	if p.Subtitles.Status != "ready" && !(len(draft) > 0 && draft[0] && draftSubtitlePreviewAllowed(p)) {
		return errors.New(p.Subtitles.Reason)
	}
	for _, asset := range p.Assets {
		if asset.ID != p.Subtitles.Binding.AssetID {
			continue
		}
		f, e := os.Open(filepath.Join(dir, "assets", asset.File))
		if e != nil {
			return e
		}
		sum, _, e := hashReader(f)
		f.Close()
		if e != nil {
			return e
		}
		if sum != p.Subtitles.Binding.SHA256 {
			return errors.New("配音文件已改变，请重新核对字幕")
		}
		return nil
	}
	return errors.New("字幕绑定的配音素材不存在")
}
