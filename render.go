package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

type RenderRequest struct {
	Range     *RenderRange `json:"range,omitempty"`
	Kind      string       `json:"kind,omitempty"`
	Revision  int          `json:"revision,omitempty"`
	RequestID string       `json:"requestId,omitempty"`
}

func (a *App) startRender(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var request RenderRequest
	if !decodeStrict(w, r, &request) {
		return
	}
	if request.Kind != "" && request.Kind != "draft" {
		fail(w, 400, errors.New("kind must be draft or omitted"))
		return
	}
	if request.RequestID != "" && (!identifier.MatchString(request.RequestID) || request.Revision < 1) {
		fail(w, 400, errors.New("幂等渲染需要24位requestId和revision"))
		return
	}
	raw, _ := json.Marshal(request)
	requestHash := hashBytes(raw)
	a.mu.Lock()
	if request.RequestID != "" {
		if p := a.projects[id]; p != nil {
			for _, j := range p.Renders {
				if j.RequestID == request.RequestID {
					a.mu.Unlock()
					if j.RequestHash != requestHash {
						fail(w, 409, errors.New("同一requestId不能用于不同渲染请求"))
						return
					}
					writeJSON(w, 200, j)
					return
				}
			}
		}
	}
	if a.closing {
		a.mu.Unlock()
		fail(w, 503, errors.New("服务正在停止"))
		return
	}
	p := a.projects[id]
	if p == nil {
		a.mu.Unlock()
		fail(w, 404, errors.New("项目不存在"))
		return
	}
	if request.Revision != 0 && request.Revision != p.Revision {
		a.mu.Unlock()
		fail(w, 409, errors.New("项目版本冲突，请重新载入"))
		return
	}
	if !usesEditDSL(*p) && len(p.Shots) == 0 {
		a.mu.Unlock()
		fail(w, 409, errors.New("请先保存至少一个镜头"))
		return
	}
	refreshSubtitleState(p)
	if !usesEditDSL(*p) && p.Subtitles != nil && p.Subtitles.Enabled && p.Subtitles.Status != "ready" && !(request.Kind == "draft" && draftSubtitlePreviewAllowed(*p)) {
		reason := p.Subtitles.Reason
		a.mu.Unlock()
		fail(w, 409, errors.New(reason))
		return
	}
	if len(a.jobs) > 0 {
		a.mu.Unlock()
		fail(w, 409, errors.New("已有渲染任务运行，原型版一次渲染一个任务"))
		return
	}
	ir := EditIR{}
	if usesEditDSL(*p) {
		var e error
		ir, e = compileEdit(*p.EditDSL, p.Assets)
		if e != nil {
			a.mu.Unlock()
			editErrorResponse(w, e)
			return
		}
		for _, name := range []string{"ffmpeg", "ffprobe"} {
			if _, e := exec.LookPath(name); e != nil {
				a.mu.Unlock()
				fail(w, 409, fmt.Errorf("缺少%s", name))
				return
			}
		}
		if ir.HasText && !regularFile(a.font) {
			a.mu.Unlock()
			fail(w, 409, errors.New("文字片段需要配置字体文件"))
			return
		}
	}
	if !usesEditDSL(*p) {
		for _, path := range []string{a.hf, a.font, a.gsap} {
			if info, e := os.Stat(path); e != nil || info.IsDir() {
				a.mu.Unlock()
				fail(w, 409, errors.New("请先配置 HyperFrames CLI、中文字体和 GSAP 文件"))
				return
			}
		}
	}
	if request.Range != nil {
		v := request.Range
		if !usesEditDSL(*p) || request.Kind != "draft" || !finite(v.Start) || !finite(v.End) || v.Start < 0 || v.End <= v.Start || v.End-v.Start > 60 || v.End > ir.Duration || v.End-v.Start < 1/float64(ir.Canvas.FPS) {
			a.mu.Unlock()
			fail(w, 422, errors.New("局部预览需draft、DSL模式及有效范围，最长60秒"))
			return
		}
	}
	q := clone(p)
	job := Render{Range: request.Range, RequestID: request.RequestID, RequestHash: requestHash, Kind: request.Kind, ContentHash: productionHash(*p), HasSubtitles: p.Subtitles != nil && p.Subtitles.Enabled, HasAudio: len(audioTracks(p.Audio)) > 0, ID: uid(), Revision: p.Revision, State: "running", Created: time.Now().UTC().Format(time.RFC3339Nano)}
	if usesEditDSL(*p) {
		job.Engine = "ffmpeg_edit"
		job.Duration = ir.Duration
		if request.Range != nil {
			job.Duration = request.Range.End - request.Range.Start
		}
		job.HasAudio = ir.HasAudio
		job.HasSubtitles = false
	} else {
		job.Engine = "hyperframes"
		for _, s := range p.Shots {
			job.Duration += s.Duration
		}
	}
	q.Renders = append(q.Renders, job)
	if e := a.save(&q); e != nil {
		a.mu.Unlock()
		fail(w, 500, e)
		return
	}
	a.projects[id] = &q
	snapshot := clone(&q)
	ctx, cancel := context.WithCancel(context.Background())
	a.jobs[job.ID] = cancel
	a.wg.Add(1)
	a.mu.Unlock()
	go func() { defer a.wg.Done(); defer cancel(); a.render(ctx, snapshot, job) }()
	writeJSON(w, 202, job)
}
func (a *App) stopRender(w http.ResponseWriter, r *http.Request) {
	id, rid := r.PathValue("id"), r.PathValue("rid")
	a.mu.Lock()
	defer a.mu.Unlock()
	p := a.projects[id]
	if p == nil {
		fail(w, 404, errors.New("项目不存在"))
		return
	}
	for i := range p.Renders {
		if p.Renders[i].ID == rid {
			if cancel := a.jobs[rid]; cancel != nil {
				q := clone(p)
				q.Renders[i].State = "stopping"
				if e := a.save(&q); e != nil {
					fail(w, 500, e)
					return
				}
				a.projects[id] = &q
				cancel()
				writeJSON(w, 200, q.Renders[i])
				return
			}
		}
	}
	fail(w, 409, errors.New("没有对应的运行中渲染任务"))
}
func copyFile(src, dst string) error {
	in, e := os.Open(src)
	if e != nil {
		return e
	}
	defer in.Close()
	out, e := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if e != nil {
		return e
	}
	_, e = io.Copy(out, in)
	ce := out.Close()
	if e != nil {
		return e
	}
	return ce
}
func (a *App) compose(p Project, dir string) error {
	if e := os.MkdirAll(filepath.Join(dir, "assets"), 0700); e != nil {
		return e
	}
	if e := copyFile(a.font, filepath.Join(dir, "font"+filepath.Ext(a.font))); e != nil {
		return e
	}
	if e := copyFile(a.gsap, filepath.Join(dir, "gsap.min.js")); e != nil {
		return e
	}
	assets := map[string]Asset{}
	for _, x := range p.Assets {
		assets[x.ID] = x
	}
	copied := map[string]bool{}
	var clips strings.Builder
	var anim strings.Builder
	start := 0.0
	for i, s := range p.Shots {
		if s.AssetID != "" {
			x := assets[s.AssetID]
			if x.MIME != "image/png" && x.MIME != "image/jpeg" && x.MIME != "image/webp" && x.MIME != "video/mp4" && x.MIME != "video/webm" {
				return errors.New("镜头素材只能是图片或视频；音频/文档可作为 Agent 参考")
			}
			if !copied[x.ID] {
				root, e := os.OpenRoot(filepath.Join(a.dir(p.ID), "workspace", "assets"))
				if e != nil {
					return e
				}
				f, e := root.Open(x.File)
				root.Close()
				if e != nil {
					return e
				}
				out, e := os.OpenFile(filepath.Join(dir, "assets", x.File), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
				if e != nil {
					f.Close()
					return e
				}
				_, e = io.Copy(out, f)
				f.Close()
				out.Close()
				if e != nil {
					return e
				}
				copied[x.ID] = true
			}
			if strings.HasPrefix(x.MIME, "video/") {
				fmt.Fprintf(&clips, `<video id="media%d" class="clip media" src="assets/%s" data-start="%.3f" data-duration="%.3f" data-track-index="1" muted playsinline></video>`, i, x.File, start, s.Duration)
			} else {
				fmt.Fprintf(&clips, `<img class="clip media" src="assets/%s" data-start="%.3f" data-duration="%.3f" data-track-index="1" alt="">`, x.File, start, s.Duration)
			}
		}
		sceneClass := ""
		if s.AssetID == "" {
			sceneClass += " no-media"
		}
		if len([]rune(s.Title)) > 30 || len([]rune(s.Text)) > 100 {
			sceneClass += " dense"
		}
		fmt.Fprintf(&clips, `<section class="clip scene%s" data-start="%.3f" data-duration="%.3f" data-track-index="2"><div class="content" id="content%d"><div class="kicker">%02d / %02d</div><h1>%s</h1><p>%s</p><div class="rule"></div></div></section>`, sceneClass, start, s.Duration, i, i+1, len(p.Shots), html.EscapeString(s.Title), html.EscapeString(s.Text))
		fmt.Fprintf(&anim, `tl.fromTo("#content%d",{opacity:0,y:36},{opacity:1,y:0,duration:0.35},%.3f);`, i, start)
		start += s.Duration
	}
	if p.Subtitles != nil && p.Subtitles.Enabled {
		for _, c := range p.Subtitles.Cues {
			fontSize := 44
			if len([]rune(c.Text)) > 100 {
				fontSize = 32
			}
			fmt.Fprintf(&clips, `<div class="clip subtitle" data-start="%.3f" data-duration="%.3f" data-track-index="3" style="font-size:%dpx"><span>%s</span></div>`, c.Start, c.End-c.Start, fontSize, html.EscapeString(c.Text))
		}
		if e := os.WriteFile(filepath.Join(dir, "subtitles.srt"), []byte(subtitleSRT(p.Subtitles.Cues)), 0600); e != nil {
			return e
		}
	}
	body := fmt.Sprintf(`<!doctype html><html lang="zh-CN"><head><meta charset="utf-8"><style>@font-face{font-family:VideoFont;src:url('font%s')}*{box-sizing:border-box}body{margin:0;background:#11182a}#root{width:100%%;height:100%%;position:relative;overflow:hidden;background:#11182a;color:#ffffff;font-family:VideoFont,sans-serif}.media{position:absolute;inset:0;width:100%%;height:100%%;object-fit:cover}.scene{position:absolute;inset:0;padding:130px 88px;display:flex;align-items:flex-end;background:linear-gradient(transparent 15%%,rgba(6,13,29,.94) 65%%)}.content{width:100%%;padding:32px;background:#11182a;border-radius:24px}.kicker{font-size:24px;letter-spacing:8px;color:#bac8f8}h1{font-size:62px;line-height:1.24;margin:30px 0;overflow-wrap:anywhere;white-space:pre-wrap}p{font-size:34px;line-height:1.6;color:#e3e9ff;margin:0;overflow-wrap:anywhere;white-space:pre-wrap}.no-media{align-items:center;background:radial-gradient(ellipse at 20%% 20%%,#253754,#11182a 70%%)}.no-media .content{background:transparent}.no-media h1{font-size:96px;line-height:1.3}.no-media p{font-size:44px}.dense h1,.dense.no-media h1{font-size:52px}.dense p,.dense.no-media p{font-size:30px}.subtitle{position:absolute;left:88px;right:88px;bottom:145px;text-align:center;line-height:1.5;white-space:pre-wrap;overflow-wrap:anywhere;z-index:3}.subtitle span{background:rgba(6,13,29,.95);box-decoration-break:clone;padding:8px 18px;border-radius:12px}.scene:has(~.subtitle){padding-bottom:510px}.rule{height:8px;background:#91abff;width:100px;margin-top:42px}</style><script src="gsap.min.js"></script></head><body><div id="root" data-composition-id="video" data-start="0" data-duration="%.3f" data-width="1080" data-height="1920" data-fps="30">%s</div><script>const tl=gsap.timeline({paused:true});%s window.__timelines=window.__timelines||{};window.__timelines.video=tl;</script></body></html>`, filepath.Ext(a.font), start, clips.String(), anim.String())
	if e := os.WriteFile(filepath.Join(dir, "index.html"), []byte(body), 0600); e != nil {
		return e
	}
	if e := a.snapshotAudio(p, dir); e != nil {
		return e
	}
	if e := verifySubtitleSnapshot(p, dir, len(p.Renders) > 0 && p.Renders[len(p.Renders)-1].Kind == "draft"); e != nil {
		return e
	}
	return atomicJSON(filepath.Join(dir, "snapshot.json"), p)
}
func (a *App) render(ctx context.Context, p Project, job Render) {
	if usesEditDSL(p) {
		a.renderEditDSL(ctx, p, job)
		return
	}
	dir := filepath.Join(a.dir(p.ID), "renders", job.ID)
	e := os.MkdirAll(dir, 0700)
	var logfile *os.File
	if e == nil {
		logfile, e = os.OpenFile(filepath.Join(dir, "render.log"), os.O_CREATE|os.O_WRONLY, 0600)
	}
	if e == nil {
		e = a.compose(p, dir)
	}
	if logfile != nil {
		defer logfile.Close()
	}
	run := func(name string, args ...string) error {
		fmt.Fprintln(logfile, name, strings.Join(args, " "))
		cmd := exec.CommandContext(ctx, name, args...)
		cmd.Env = rendererEnvironment()
		cmd.Dir = dir
		cmd.Stdout = logfile
		cmd.Stderr = logfile
		configureProcess(cmd)
		return cmd.Run()
	}
	if e == nil {
		e = run("node", a.hf, "check", dir)
	}
	if e == nil {
		output := "final.mp4"
		if job.HasAudio {
			output = "silent.mp4"
		}
		e = run("node", a.hf, "render", dir, "--quality", "draft", "--workers", "1", "--output", filepath.Join(dir, output))
	}
	if e == nil {
		e = mixAudio(ctx, p, dir, job.Duration, run)
	}
	if e == nil {
		cmd := exec.CommandContext(ctx, "ffprobe", "-v", "error", "-show_format", "-show_streams", "-of", "json", filepath.Join(dir, "final.mp4"))
		cmd.Env = rendererEnvironment()
		configureProcess(cmd)
		raw, err := cmd.Output()
		e = err
		if e == nil {
			var result struct {
				Format struct {
					Duration string `json:"duration"`
				} `json:"format"`
				Streams []struct {
					CodecType string `json:"codec_type"`
					Width     int    `json:"width"`
					Height    int    `json:"height"`
					FPS       string `json:"r_frame_rate"`
				} `json:"streams"`
			}
			if json.Unmarshal(raw, &result) != nil || len(result.Streams) == 0 {
				e = errors.New("ffprobe 无有效视频流")
			} else {
				found, foundAudio := false, false
				for _, s := range result.Streams {
					if s.CodecType == "audio" {
						foundAudio = true
					}
					if s.CodecType == "video" && s.Width == 1080 && s.Height == 1920 && s.FPS == "30/1" {
						found = true
					}
				}
				var seconds float64
				fmt.Sscan(result.Format.Duration, &seconds)
				if !found || (job.HasAudio && !foundAudio) || seconds < job.Duration-0.2 || seconds > job.Duration+0.2 {
					e = errors.New("成片尺寸、帧率、音轨或时长不符合快照")
				}
			}
			_ = os.WriteFile(filepath.Join(dir, "validation.json"), raw, 0600)
		}
	}
	if e != nil && logfile != nil {
		fmt.Fprintln(logfile, "FAILED:", e)
	}
	a.finishRender(ctx, p.ID, job.ID, e)
}
func (a *App) renderFile(w http.ResponseWriter, r *http.Request) {
	p, e := a.get(r.PathValue("id"))
	if e != nil {
		fail(w, 404, e)
		return
	}
	rid, file := r.PathValue("rid"), r.PathValue("file")
	found := false
	for _, job := range p.Renders {
		if job.ID == rid {
			found = true
			if file == "final.mp4" && job.State != "completed" {
				fail(w, 409, errors.New("成片尚未通过检查"))
				return
			}
		}
	}
	if !found || (file != "final.mp4" && file != "render.log" && file != "snapshot.json" && file != "validation.json" && file != "subtitles.srt" && file != "edit-ir.json" && file != "filter.ffscript" && file != "render-proof.json") {
		fail(w, 404, errors.New("文件不存在"))
		return
	}
	root, e := os.OpenRoot(filepath.Join(a.dir(p.ID), "renders", rid))
	if e != nil {
		fail(w, 404, e)
		return
	}
	defer root.Close()
	f, e := root.Open(file)
	if e != nil {
		fail(w, 404, e)
		return
	}
	defer f.Close()
	info, e := f.Stat()
	if e != nil {
		fail(w, 404, e)
		return
	}
	if file == "final.mp4" {
		w.Header().Set("Content-Type", "video/mp4")
	} else {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	}
	if r.URL.Query().Has("download") {
		w.Header().Set("Content-Disposition", "attachment; filename=\""+file+"\"")
	}
	http.ServeContent(w, r, file, info.ModTime(), f)
}
