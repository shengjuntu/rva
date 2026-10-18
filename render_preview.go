package main

import (
	"bytes"
	"context"
	"errors"
	"image/png"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

// renderInspect supplies evidence of one rendered frame. It does not assert that
// the whole video, its audio, or even a complete cut boundary has been reviewed.
func (a *App) renderInspect(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Time *float64 `json:"time"`
	}
	if !decodeStrict(w, r, &request) {
		return
	}
	if request.Time == nil || !finite(*request.Time) || *request.Time < 0 {
		fail(w, 422, errors.New("time必须是非负有限秒数"))
		return
	}
	p, err := a.get(r.PathValue("id"))
	if err != nil {
		fail(w, 404, err)
		return
	}
	var found *Render
	for i := range p.Renders {
		if p.Renders[i].ID == r.PathValue("rid") {
			found = &p.Renders[i]
			break
		}
	}
	if found == nil || !identifier.MatchString(found.ID) {
		fail(w, 404, errors.New("渲染任务不存在"))
		return
	}
	if found.State != "completed" {
		fail(w, 409, errors.New("只能检查已完成的渲染产物"))
		return
	}
	if !finite(found.Duration) || found.Duration <= 0 || *request.Time >= found.Duration {
		fail(w, 422, errors.New("time必须位于渲染产物的时长以内；局部预览从0秒计时"))
		return
	}
	root, err := os.OpenRoot(filepath.Join(a.dir(p.ID), "renders", found.ID))
	if err != nil {
		fail(w, 409, errors.New("渲染产物不可用"))
		return
	}
	defer root.Close()
	source, err := root.Open("final.mp4")
	if err != nil {
		fail(w, 409, errors.New("无法读取渲染产物"))
		return
	}
	defer source.Close()
	info, err := source.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() == 0 {
		fail(w, 409, errors.New("渲染产物不是有效文件"))
		return
	}
	// FFmpeg receives our private copy of the already-open file, rather than
	// reopening a potentially replaced project path outside os.Root protection.
	input, err := os.CreateTemp("", "rundesk-render-inspect-*.mp4")
	if err != nil {
		fail(w, 500, errors.New("无法创建检查临时文件"))
		return
	}
	defer os.Remove(input.Name())
	_, copyErr := io.Copy(input, source)
	closeErr := input.Close()
	if copyErr != nil || closeErr != nil {
		fail(w, 409, errors.New("无法准备渲染产物检查"))
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "ffmpeg", "-nostdin", "-v", "error", "-protocol_whitelist", "file,pipe", "-ss", decimal(*request.Time), "-i", input.Name(), "-map", "0:v:0", "-frames:v", "1", "-vf", "scale=960:960:force_original_aspect_ratio=decrease", "-threads", "1", "-f", "image2pipe", "-c:v", "png", "pipe:1")
	cmd.Env = rendererEnvironment()
	var out renderInspectionOutput
	cmd.Stdout = &out
	cmd.Stderr = io.Discard
	if err := cmd.Run(); err != nil {
		fail(w, 409, errors.New("无法提取成片画面，请检查FFmpeg和渲染产物"))
		return
	}
	config, err := png.DecodeConfig(bytes.NewReader(out.Bytes()))
	if err != nil || config.Width < 1 || config.Height < 1 || config.Width > 960 || config.Height > 960 {
		fail(w, 409, errors.New("成片检查图片无效"))
		return
	}
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Inspection-Limit", "single_frame")
	w.Header().Set("X-Inspection-Time", decimal(*request.Time))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(out.Bytes())
}

// Bound memory even if the external process produces unexpected output.
type renderInspectionOutput struct{ buffer bytes.Buffer }

func (b *renderInspectionOutput) Bytes() []byte { return b.buffer.Bytes() }

func (b *renderInspectionOutput) Write(p []byte) (int, error) {
	if len(p) > (4<<20)-b.buffer.Len() {
		return 0, errors.New("成片检查图片超过4MiB")
	}
	return b.buffer.Write(p)
}
