package main

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"math"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode"
)

const speechSampleRate = 24000

type SpeechJob struct {
	ProductionRequestID string `json:"productionRequestId,omitempty"`
	Source      string              `json:"source,omitempty"`
	Backend     string              `json:"backend,omitempty"`
	Model       string              `json:"model,omitempty"`
	Language    string              `json:"language,omitempty"`
	Speed       float64             `json:"speed,omitempty"`
	RequestHash string              `json:"requestHash,omitempty"`
	ID          string              `json:"id"`
	RequestID   string              `json:"requestId"`
	Revision    int                 `json:"revision"`
	State       string              `json:"state"`
	Created     string              `json:"created"`
	Voice       string              `json:"voice"`
	Rate        int                 `json:"rate"`
	ScriptHash  string              `json:"scriptHash"`
	Progress    int                 `json:"progress"`
	Total       int                 `json:"total"`
	Duration    float64             `json:"duration"`
	Error       string              `json:"error,omitempty"`
	Files       map[string]NewsFile `json:"files,omitempty"`
}
type SpeechRequest struct {
	ProductionRequestID string `json:"productionRequestId,omitempty"`
	Source    string  `json:"source,omitempty"`
	Text      string  `json:"text,omitempty"`
	Backend   string  `json:"backend,omitempty"`
	Language  string  `json:"language,omitempty"`
	Speed     float64 `json:"speed,omitempty"`
	Revision  int     `json:"revision"`
	RequestID string  `json:"requestId"`
	Voice     string  `json:"voice"`
	Rate      int     `json:"rate"`
}
type SpeechShot struct {
	ID       string  `json:"id"`
	Duration float64 `json:"duration"`
}
type SpeechManifest struct {
	Source     string        `json:"source,omitempty"`
	Model      string        `json:"model,omitempty"`
	Language   string        `json:"language,omitempty"`
	Speed      float64       `json:"speed,omitempty"`
	Schema     int           `json:"schemaVersion"`
	JobID      string        `json:"jobId"`
	ScriptHash string        `json:"scriptHash"`
	Backend    string        `json:"backend"`
	Voice      string        `json:"voice"`
	Rate       int           `json:"rate"`
	Alignment  string        `json:"alignment"`
	Duration   float64       `json:"duration"`
	Cues       []SubtitleCue `json:"cues"`
	Shots      []SpeechShot  `json:"shots"`
}

func speechScriptHash(p Project) string {
	type line struct{ ID, Text string }
	lines := []line{}
	for _, s := range p.Shots {
		lines = append(lines, line{s.ID, strings.TrimSpace(s.Text)})
	}
	raw, _ := json.Marshal(lines)
	return hashBytes(raw)
}
func speechChunks(text string) []string {
	// Bound each synthesis call and preserve word spaces. The subtitle timing
	// comes from this chunk's actual PCM, never a character-count estimate.
	text = strings.Join(strings.Fields(text), " ")
	var chunks []string
	runes := []rune(text)
	for len(runes) > 0 {
		end := len(runes)
		if end > 80 {
			end = 80
		}
		for i, r := range runes[:end] {
			if strings.ContainsRune("。！？!?；;", r) || (r == '.' && (i+1 == len(runes) || unicode.IsSpace(runes[i+1]))) {
				end = i + 1
				break
			}
		}
		if end == 80 && len(runes) > 80 {
			for i := 79; i >= 40; i-- {
				if unicode.IsSpace(runes[i]) || strings.ContainsRune("，,、：:", runes[i]) {
					end = i + 1
					break
				}
			}
		}
		chunk := strings.TrimSpace(string(runes[:end]))
		if chunk != "" {
			chunks = append(chunks, chunk)
		}
		runes = runes[end:]
	}
	return chunks
}
func (a *App) speechExecutable() (string, error) {
	name := a.espeak
	if name == "" {
		name = "espeak-ng"
	}
	return exec.LookPath(name)
}
func (a *App) speechCapabilities(w http.ResponseWriter, r *http.Request) {
	c := a.speechConfig()
	path, e := a.speechExecutable()
	_, fe := exec.LookPath("ffmpeg")
	_, pe := exec.LookPath("ffprobe")
	backend := r.URL.Query().Get("backend")
	if backend == "" {
		backend = c.Backend
	}
	result := map[string]any{"backend": backend, "ready": false, "executable": path, "espeakReady": e == nil, "ffmpegReady": fe == nil, "ffprobeReady": pe == nil, "rateMin": 80, "rateMax": 300, "speedMin": .5, "speedMax": 2}
	switch backend {
	case "espeak-ng":
		result["ready"] = e == nil && fe == nil && pe == nil
		result["offline"] = true
		result["quality"] = "mechanical_preview"
		result["voices"] = []string{"cmn", "en-us"}
	case "indextts":
		c.Backend = backend
		result["model"] = c.Model
		result["voice"] = c.Voice
		result["language"] = c.Language
		result["quality"] = "neural_tts"
		result["configured"] = validateSpeechConfig(c) == nil
		if e := validateSpeechConfig(c); e != nil {
			result["error"] = e.Error()
		} else {
			state, err := probeIndexTTS(r.Context(), c)
			result["probe"] = state
			result["ready"] = err == nil && fe == nil && pe == nil
			if err != nil {
				result["error"] = err.Error()
			}
		}
	default:
		fail(w, 400, errors.New("未知配音后端"))
		return
	}
	writeJSON(w, 200, result)
}
func (a *App) speechDir(pid, sid string) string { return filepath.Join(a.dir(pid), "speech", sid) }
func findSpeech(p Project, sid string) (SpeechJob, error) {
	for _, j := range p.Speech {
		if j.ID == sid {
			return j, nil
		}
	}
	return SpeechJob{}, errors.New("配音任务不存在")
}
func (a *App) startSpeech(w http.ResponseWriter, r *http.Request) {
	var in SpeechRequest
	if !decode(w, r, &in) {
		return
	}
	requestBytes, _ := json.Marshal(in)
	requestHash := hashBytes(requestBytes)
	a.mu.Lock()
	defer a.mu.Unlock()
	p := a.projects[r.PathValue("id")]
	if p == nil {
		fail(w, 404, errors.New("项目不存在"))
		return
	}
	for _, old := range p.Speech {
		if old.RequestID == in.RequestID {
			matches := old.RequestHash == requestHash
			if old.RequestHash == "" {
				legacy := in
				matches = legacy.ProductionRequestID == "" && legacy.Source != "text" && legacy.Text == "" && legacy.normalize(defaultSpeechConfig()) == nil && legacy.Backend == "espeak-ng" && old.Revision == legacy.Revision && old.Voice == legacy.Voice && old.Rate == legacy.Rate
			}
			if !matches {
				fail(w, 409, errors.New("同一 requestId 不能用于不同配音请求"))
				return
			}
			writeJSON(w, 200, old)
			return
		}
	}
	if e := validateProductionOwner(*p, in.ProductionRequestID); e != nil {
		fail(w, 409, e)
		return
	}
	cfg := a.tts
	if e := in.normalize(cfg); e != nil {
		fail(w, 400, e)
		return
	}
	if a.closing {
		fail(w, 503, errors.New("服务正在停止"))
		return
	}
	if p.Revision != in.Revision {
		fail(w, 409, errors.New("项目版本冲突，请重新载入"))
		return
	}
	if len(a.speechJobs) > 0 {
		fail(w, 409, errors.New("已有配音任务运行，请等待或取消"))
		return
	}
	if len(p.Speech) >= 100 {
		fail(w, 409, errors.New("当前项目已达 100 个配音任务，请使用新项目"))
		return
	}
	snapshot := clone(p)
	if in.Source == "narration" {
		snapshot.Shots = narrationShots(p.NarrationText)
	} else if in.Source == "text" {
		// This is a private synthesis projection. It never becomes the project's
		// editorial narration, storyboard, audio binding or editing document.
		snapshot.NarrationText = in.Text
		snapshot.Shots = narrationShots(in.Text)
	} else if in.Source != "" && in.Source != "shots" {
		fail(w, 400, errors.New("source 必须为 narration、shots 或 text"))
		return
	}
	total := 0
	for _, s := range snapshot.Shots {
		total += len(speechChunks(s.Text))
	}
	if total == 0 || total > 300 {
		fail(w, 422, errors.New("配音正文需能拆分为 1–300 段"))
		return
	}
	executable := ""
	var e error
	if in.Backend == "espeak-ng" {
		executable, e = a.speechExecutable()
		if e != nil {
			fail(w, 409, errors.New("请安装本机 eSpeak NG 或设置 --espeak 路径"))
			return
		}
	} else {
		cfg.Backend = "indextts"
		if e = validateSpeechConfig(cfg); e != nil {
			fail(w, 409, e)
			return
		}
	}
	for _, n := range []string{"ffmpeg", "ffprobe"} {
		if _, e = exec.LookPath(n); e != nil {
			fail(w, 409, fmt.Errorf("请先安装 %s", n))
			return
		}
	}
	job := SpeechJob{ProductionRequestID: in.ProductionRequestID, Source: in.Source, Backend: in.Backend, Language: in.Language, Speed: in.Speed, RequestHash: requestHash, ID: uid(), RequestID: in.RequestID, Revision: p.Revision, State: "running", Created: time.Now().UTC().Format(time.RFC3339Nano), Voice: in.Voice, Rate: in.Rate, ScriptHash: speechScriptHash(snapshot), Total: total}
	if in.Backend == "indextts" {
		job.Model = cfg.Model
	}
	dir := a.speechDir(p.ID, job.ID)
	if e = os.MkdirAll(dir, 0700); e != nil {
		fail(w, 500, e)
		return
	}
	if e = atomicJSON(filepath.Join(dir, "snapshot.json"), snapshot); e != nil {
		os.RemoveAll(dir)
		fail(w, 500, e)
		return
	}
	q := clone(p)
	q.Speech = append(q.Speech, job)
	if e = a.save(&q); e != nil {
		os.RemoveAll(dir)
		fail(w, 500, e)
		return
	}
	a.projects[q.ID] = &q
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	a.speechJobs[job.ID] = cancel
	a.wg.Add(1)
	go func() { defer a.wg.Done(); defer cancel(); a.runSpeech(ctx, snapshot, job, executable, cfg) }()
	writeJSON(w, 202, job)
}
func (a *App) stopSpeech(w http.ResponseWriter, r *http.Request) {
	a.mu.Lock()
	defer a.mu.Unlock()
	p := a.projects[r.PathValue("id")]
	if p == nil {
		fail(w, 404, errors.New("项目不存在"))
		return
	}
	q := clone(p)
	for i := range q.Speech {
		j := &q.Speech[i]
		if j.ID == r.PathValue("sid") && a.speechJobs[j.ID] != nil {
			j.State = "stopping"
			if e := a.save(&q); e != nil {
				fail(w, 500, e)
				return
			}
			a.projects[q.ID] = &q
			a.speechJobs[j.ID]()
			writeJSON(w, 200, j)
			return
		}
	}
	fail(w, 409, errors.New("没有对应的运行中配音任务"))
}
func (a *App) speechProgress(pid, sid string, done int) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	q := clone(a.projects[pid])
	for i := range q.Speech {
		if q.Speech[i].ID == sid {
			q.Speech[i].Progress = done
		}
	}
	if e := a.save(&q); e != nil {
		return e
	}
	a.projects[pid] = &q
	return nil
}
func (a *App) runSpeech(ctx context.Context, p Project, job SpeechJob, executable string, cfg SpeechConfig) {
	dir := a.speechDir(p.ID, job.ID)
	f, e := os.OpenFile(filepath.Join(dir, "speech.log"), os.O_CREATE|os.O_WRONLY, 0600)
	if f != nil {
		defer f.Close()
	}
	result := SpeechManifest{}
	files := map[string]NewsFile{}
	if e == nil {
		result, e = a.synthesizeSpeech(ctx, p, job, executable, dir, f, cfg)
	}
	if e == nil {
		for _, name := range []string{"narration.wav", "subtitles.srt", "manifest.json", "snapshot.json"} {
			file, err := os.Open(filepath.Join(dir, name))
			if err != nil {
				e = err
				break
			}
			sum, size, err := hashReader(file)
			file.Close()
			if err != nil {
				e = err
				break
			}
			files[name] = NewsFile{SHA256: sum, Size: size}
		}
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	delete(a.speechJobs, job.ID)
	q := clone(a.projects[p.ID])
	for i := range q.Speech {
		j := &q.Speech[i]
		if j.ID != job.ID {
			continue
		}
		j.State = "completed"
		j.Duration = result.Duration
		j.Files = files
		if e != nil {
			j.State = "failed"
			j.Error = e.Error()
			j.Files = nil
		}
		if ctx.Err() != nil {
			j.State = "interrupted"
			j.Error = "配音已取消或超时；可重新生成"
			j.Files = nil
		}
	}
	if e != nil && f != nil {
		fmt.Fprintln(f, "FAILED:", e)
	}
	if err := a.save(&q); err != nil {
		for i := range q.Speech {
			if q.Speech[i].ID == job.ID {
				q.Speech[i].State = "failed"
				q.Speech[i].Error = "保存配音任务失败：" + err.Error()
				q.Speech[i].Files = nil
			}
		}
		log.Print(q.Speech[len(q.Speech)-1].Error)
	}
	a.projects[p.ID] = &q
}
func writeSpeechWAVHeader(f *os.File, samples int64) error {
	var h [44]byte
	copy(h[:], "RIFF")
	binary.LittleEndian.PutUint32(h[4:], uint32(36+samples*2))
	copy(h[8:], "WAVEfmt ")
	binary.LittleEndian.PutUint32(h[16:], 16)
	binary.LittleEndian.PutUint16(h[20:], 1)
	binary.LittleEndian.PutUint16(h[22:], 1)
	binary.LittleEndian.PutUint32(h[24:], speechSampleRate)
	binary.LittleEndian.PutUint32(h[28:], speechSampleRate*2)
	binary.LittleEndian.PutUint16(h[32:], 2)
	binary.LittleEndian.PutUint16(h[34:], 16)
	copy(h[36:], "data")
	binary.LittleEndian.PutUint32(h[40:], uint32(samples*2))
	if _, e := f.Seek(0, io.SeekStart); e != nil {
		return e
	}
	_, e := f.Write(h[:])
	return e
}
func (a *App) synthesizeSpeech(ctx context.Context, p Project, job SpeechJob, executable, dir string, logfile io.Writer, cfg SpeechConfig) (SpeechManifest, error) {
	result := SpeechManifest{Source: job.Source, Schema: 1, JobID: job.ID, ScriptHash: job.ScriptHash, Backend: speechBackend(job.Backend), Model: job.Model, Language: job.Language, Speed: job.Speed, Voice: job.Voice, Rate: job.Rate, Alignment: "synthesized_sentence_durations", Cues: []SubtitleCue{}, Shots: []SpeechShot{}}
	run := func(name string, args ...string) error {
		if e := ctx.Err(); e != nil {
			return e
		}
		step, cancel := context.WithTimeout(ctx, 45*time.Second)
		defer cancel()
		cmd := exec.CommandContext(step, name, args...)
		cmd.Dir = dir
		cmd.Env = rendererEnvironment()
		cmd.Stdout = logfile
		cmd.Stderr = logfile
		configureProcess(cmd)
		if e := cmd.Run(); e != nil {
			return fmt.Errorf("配音子进程失败或超时 (%s)：%w", filepath.Base(name), e)
		}
		return nil
	}
	merged, e := os.OpenFile(filepath.Join(dir, "narration.wav"), os.O_CREATE|os.O_EXCL|os.O_RDWR, 0600)
	if e != nil {
		return result, e
	}
	defer merged.Close()
	if e = writeSpeechWAVHeader(merged, 0); e != nil {
		return result, e
	}
	var samples int64
	done := 0
	for _, shot := range p.Shots {
		if e = ctx.Err(); e != nil {
			return result, e
		}
		shotStart := samples
		chunks := speechChunks(shot.Text)
		for _, text := range chunks {
			input, raw, pcm := filepath.Join(dir, "input.txt"), filepath.Join(dir, "chunk.wav"), filepath.Join(dir, "chunk.pcm")
			for _, name := range []string{raw, pcm} {
				if err := os.Remove(name); err != nil && !os.IsNotExist(err) {
					return result, err
				}
			}
			fmt.Fprintf(logfile, "segment=%d backend=%s voice=%s language=%s speed=%g\n", done+1, speechBackend(job.Backend), job.Voice, job.Language, job.Speed)
			if job.Backend == "indextts" {
				e = synthesizeIndexTTS(ctx, cfg, job, text, raw)
			} else {
				if e = os.WriteFile(input, []byte(text), 0600); e != nil {
					return result, e
				}
				e = run(executable, "-b", "1", "-v", job.Voice, "-s", strconv.Itoa(job.Rate), "-w", raw, "-f", input)
			}
			if e != nil {
				return result, e
			}
			duration, err := audioDuration(ctx, raw)
			if err != nil {
				return result, err
			}
			if duration > 60 {
				return result, errors.New("单段配音超过 60 秒，请拆分正文或提高语速")
			}
			if e = run("ffmpeg", "-v", "error", "-nostdin", "-y", "-protocol_whitelist", "file,pipe", "-i", raw, "-map", "0:a:0", "-vn", "-ar", "24000", "-ac", "1", "-c:a", "pcm_s16le", "-t", "61", "-f", "s16le", pcm); e != nil {
				return result, e
			}
			info, err := os.Stat(pcm)
			if err != nil {
				return result, err
			}
			if info.Size() == 0 || info.Size()%2 != 0 || info.Size() > 60*speechSampleRate*2 {
				return result, errors.New("配音 PCM 长度无效")
			}
			data, err := os.ReadFile(pcm)
			if err != nil {
				return result, err
			}
			audible := false
			for i := 0; i < len(data); i += 2 {
				x := int16(binary.LittleEndian.Uint16(data[i:]))
				if x > 32 || x < -32 {
					audible = true
					break
				}
			}
			if !audible {
				return result, errors.New("配音片段只有静音，请检查语音与正文")
			}
			next := samples + int64(len(data)/2)
			if next > 600*speechSampleRate {
				return result, errors.New("配音超过 10 分钟，请拆分项目")
			}
			if _, e = merged.Write(data); e != nil {
				return result, e
			}
			result.Cues = append(result.Cues, SubtitleCue{Start: float64(samples) / speechSampleRate, End: float64(next) / speechSampleRate, Text: text})
			samples = next
			done++
			if e = a.speechProgress(p.ID, job.ID, done); e != nil {
				return result, e
			}
		}
		span := samples - shotStart
		if len(chunks) == 0 {
			span = int64(math.Ceil(shot.Duration*30)) * 800
		}
		if span < speechSampleRate {
			span = speechSampleRate
		}
		span = (span + 799) / 800 * 800 // 24 kHz / 30 fps = 800 samples per frame.
		if span > 60*speechSampleRate || shotStart+span > 600*speechSampleRate {
			return result, errors.New("实测镜头时长超过 60 秒或项目超过 10 分钟，请拆分")
		}
		padding := shotStart + span - samples
		if padding > 0 {
			if _, e = merged.Write(make([]byte, padding*2)); e != nil {
				return result, e
			}
		}
		samples = shotStart + span
		result.Shots = append(result.Shots, SpeechShot{shot.ID, float64(span) / speechSampleRate})
	}
	result.Duration = float64(samples) / speechSampleRate
	if e = writeSpeechWAVHeader(merged, samples); e != nil {
		return result, e
	}
	if e = merged.Sync(); e != nil {
		return result, e
	}
	if e = validateCues(result.Cues, result.Duration); e != nil {
		return result, e
	}
	if e = os.WriteFile(filepath.Join(dir, "subtitles.srt"), []byte(subtitleSRT(result.Cues)), 0600); e != nil {
		return result, e
	}
	if e = atomicJSON(filepath.Join(dir, "manifest.json"), result); e != nil {
		return result, e
	}
	for _, name := range []string{"input.txt", "chunk.wav", "chunk.pcm"} {
		_ = os.Remove(filepath.Join(dir, name))
	}
	return result, nil
}
func (a *App) readSpeechArtifact(p Project, j SpeechJob, name string) (*os.File, error) {
	entry, ok := j.Files[name]
	if !ok || j.State != "completed" {
		return nil, errors.New("配音产物尚未完成或不存在")
	}
	root, e := os.OpenRoot(a.speechDir(p.ID, j.ID))
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
		return nil, errors.New("配音产物校验失败")
	}
	if _, e = f.Seek(0, io.SeekStart); e != nil {
		f.Close()
		return nil, e
	}
	return f, nil
}
func (a *App) speechFile(w http.ResponseWriter, r *http.Request) {
	p, e := a.get(r.PathValue("id"))
	if e != nil {
		fail(w, 404, e)
		return
	}
	j, e := findSpeech(p, r.PathValue("sid"))
	if e != nil {
		fail(w, 404, e)
		return
	}
	name := r.PathValue("file")
	var f *os.File
	if name == "speech.log" {
		root, err := os.OpenRoot(a.speechDir(p.ID, j.ID))
		if err != nil {
			fail(w, 404, err)
			return
		}
		defer root.Close()
		f, e = root.Open(name)
	} else {
		f, e = a.readSpeechArtifact(p, j, name)
	}
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
	mime := "text/plain; charset=utf-8"
	if name == "narration.wav" {
		mime = "audio/wav"
	}
	if filepath.Ext(name) == ".json" {
		mime = "application/json"
	}
	w.Header().Set("Content-Type", mime)
	if r.URL.Query().Has("download") {
		w.Header().Set("Content-Disposition", `attachment; filename="`+name+`"`)
	}
	http.ServeContent(w, r, name, info.ModTime(), f)
}
func (a *App) adoptSpeech(w http.ResponseWriter, r *http.Request) {
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
	job, e := findSpeech(p, r.PathValue("sid"))
	if e != nil {
		fail(w, 404, e)
		return
	}
	if job.Source == "text" {
		fail(w, 409, errors.New("独立文本配音仅提供归档音频，不可覆盖项目分镜或音轨"))
		return
	}
	if job.Source == "narration" {
		fail(w, 409, errors.New("独立口播请使用 materialize，不覆盖旧分镜"))
		return
	}
	if job.State != "completed" || job.ScriptHash != speechScriptHash(p) {
		fail(w, 409, errors.New("配音未完成或分镜正文/结构已改变，请重新生成配音"))
		return
	}
	f, e := a.readSpeechArtifact(p, job, "manifest.json")
	if e != nil {
		fail(w, 409, e)
		return
	}
	var result SpeechManifest
	e = json.NewDecoder(io.LimitReader(f, 1<<20)).Decode(&result)
	f.Close()
	if e != nil || result.Schema != 1 || result.JobID != job.ID || result.ScriptHash != job.ScriptHash || len(result.Shots) != len(p.Shots) {
		fail(w, 409, errors.New("配音清单无效"))
		return
	}
	for i, s := range result.Shots {
		if s.ID != p.Shots[i].ID || !finite(s.Duration) || s.Duration < 1 || s.Duration > 60 {
			fail(w, 409, errors.New("配音镜头映射无效"))
			return
		}
		p.Shots[i].Duration = s.Duration
	}
	total := projectDuration(p)
	if total > 600 || !finite(result.Duration) || math.Abs(total-result.Duration) > .001 {
		fail(w, 409, errors.New("配音时长校验失败"))
		return
	}
	if e = validateCues(result.Cues, total); e != nil {
		fail(w, 409, e)
		return
	}
	asset := Asset{ID: job.ID, File: job.ID + ".wav", Name: "自动配音 · " + job.Voice + ".wav", MIME: "audio/wav", Size: job.Files["narration.wav"].Size}
	dest := filepath.Join(a.dir(p.ID), "workspace", "assets", asset.File)
	found := false
	for _, old := range p.Assets {
		if old.ID == asset.ID {
			found = true
			asset = old
		}
	}
	committed := false
	created := false
	defer func() {
		if created && !committed {
			_ = os.Remove(dest)
		}
	}()
	if !found {
		f, e = a.readSpeechArtifact(p, job, "narration.wav")
		if e != nil {
			fail(w, 409, e)
			return
		}
		defer f.Close()
		out, err := os.OpenFile(dest, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err != nil {
			fail(w, 409, err)
			return
		}
		created = true
		_, e = io.Copy(out, f)
		ce := out.Close()
		if e == nil {
			e = ce
		}
		if e != nil {
			fail(w, 500, e)
			return
		}
		p.Assets = append(p.Assets, asset)
	}
	if p.Audio == nil {
		p.Audio = &AudioMix{}
	}
	p.Audio.Narration = AudioTrack{AssetID: asset.ID, Volume: 1}
	if e = validateAudio(p.Audio, p.Assets, total); e != nil {
		fail(w, 422, e)
		return
	}
	p.Subtitles = &SubtitleTrack{Enabled: true, Cues: result.Cues}
	if e = a.bindSubtitles(r.Context(), &p, "synthesized_sentence_durations"); e != nil {
		fail(w, 422, e)
		return
	}
	if p.Subtitles.Binding.SHA256 != job.Files["narration.wav"].SHA256 || math.Abs(p.Subtitles.Binding.AudioDuration-total) > .001 {
		fail(w, 409, errors.New("配音副本哈希或实测时长不符"))
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
