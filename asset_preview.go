package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"image/png"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Give the RunDesk agent visual evidence rather than inferring contents from names.
func (a *App) assetPreview(w http.ResponseWriter, r *http.Request) {
	p, e := a.get(r.PathValue("id"))
	if e != nil {
		fail(w, 404, e)
		return
	}
	var asset *Asset
	for i := range p.Assets {
		if p.Assets[i].ID == r.PathValue("aid") {
			asset = &p.Assets[i]
			break
		}
	}
	if asset == nil {
		fail(w, 404, errors.New("素材不存在"))
		return
	}
	if !strings.HasPrefix(asset.MIME, "image/") && !strings.HasPrefix(asset.MIME, "video/") {
		fail(w, 400, errors.New("仅图片与视频支持视觉预览"))
		return
	}
	if filepath.Base(asset.File) != asset.File {
		fail(w, 400, errors.New("素材路径不合法"))
		return
	}
	input, e := a.assetInspectionCopy(p.ID, asset.File)
	if e != nil {
		fail(w, 409, e)
		return
	}
	defer os.Remove(input)
	format, e := inspectionMediaFormat(input, asset.MIME)
	if e != nil {
		fail(w, 409, e)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	args := []string{"-nostdin", "-v", "error", "-protocol_whitelist", "file,pipe"}
	args = append(args, inspectionFormatArgs(format)...)
	args = append(args, "-i", input, "-map", "0:v:0", "-frames:v", "1", "-vf", "scale=640:640:force_original_aspect_ratio=decrease", "-threads", "1", "-f", "image2pipe", "-c:v", "png", "pipe:1")
	cmd := exec.CommandContext(ctx, "ffmpeg", args...)
	cmd.Env = rendererEnvironment()
	configureProcess(cmd)
	var out renderInspectionOutput
	cmd.Stdout = &out
	cmd.Stderr = io.Discard
	if e = cmd.Run(); e != nil {
		fail(w, 409, errors.New("无法提取素材预览，请检查FFmpeg和文件格式"))
		return
	}
	config, e := png.DecodeConfig(bytes.NewReader(out.Bytes()))
	if e != nil || config.Width < 1 || config.Height < 1 || config.Width > 640 || config.Height > 640 {
		fail(w, 409, errors.New("预览图片大小异常"))
		return
	}
	label := "素材ID: " + asset.ID + "；名称: " + asset.Name
	if strings.HasPrefix(asset.MIME, "video/") {
		label += "。视频仅展示第一帧，不代表完整视频内容；可用video_asset_video_inspect抽查更多时间点。"
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, 200, map[string]any{"content": []any{map[string]string{"type": "text", "text": label}, map[string]string{"type": "image", "mimeType": "image/png", "data": base64.StdEncoding.EncodeToString(out.Bytes())}}})
}

// assetVideoInspect gives bounded samples of the actual video, with explicit
// source timestamps. These samples establish neither complete motion nor audio.
func (a *App) assetVideoInspect(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Times []*float64 `json:"times"`
	}
	if !decodeStrict(w, r, &request) {
		return
	}
	if len(request.Times) < 1 || len(request.Times) > 8 {
		fail(w, 422, errors.New("times必须包含1到8个递增时间点，单位秒"))
		return
	}
	for i, t := range request.Times {
		if t == nil || !finite(*t) || *t < 0 || (i > 0 && *t <= *request.Times[i-1]) {
			fail(w, 422, errors.New("times必须是递增且不重复的非负有限秒数"))
			return
		}
	}
	p, err := a.get(r.PathValue("id"))
	if err != nil {
		fail(w, 404, err)
		return
	}
	var asset *Asset
	for i := range p.Assets {
		if p.Assets[i].ID == r.PathValue("aid") {
			asset = &p.Assets[i]
			break
		}
	}
	if asset == nil {
		fail(w, 404, errors.New("素材不存在"))
		return
	}
	if !strings.HasPrefix(asset.MIME, "video/") || asset.File == "." || filepath.Base(asset.File) != asset.File {
		fail(w, 400, errors.New("只能抽查当前项目中合法的视频素材"))
		return
	}
	input, err := a.assetInspectionCopy(p.ID, asset.File)
	if err != nil {
		fail(w, 409, err)
		return
	}
	defer os.Remove(input)
	format, err := inspectionMediaFormat(input, asset.MIME)
	if err != nil {
		fail(w, 409, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	duration, err := inspectionMediaDuration(ctx, input, format)
	if err != nil || !finite(duration) || duration <= 0 {
		fail(w, 409, errors.New("无法确定视频素材实际时长"))
		return
	}
	if *request.Times[len(request.Times)-1] >= duration {
		fail(w, 422, errors.New("每个时间点都必须小于视频素材实际时长"))
		return
	}
	content := []any{map[string]string{"type": "text", "text": fmt.Sprintf("素材ID: %s；名称: %s；实际时长: %.3fs。以下仅为指定时间点的画面抽样，不代表看过完整视频，也没有检查音频或连续运动。", asset.ID, asset.Name, duration)}}
	total := 0
	for _, t := range request.Times {
		args := []string{"-nostdin", "-v", "error", "-protocol_whitelist", "file,pipe", "-ss", decimal(*t)}
		args = append(args, inspectionFormatArgs(format)...)
		args = append(args, "-i", input, "-map", "0:v:0", "-frames:v", "1", "-vf", "scale=640:640:force_original_aspect_ratio=decrease", "-threads", "1", "-f", "image2pipe", "-c:v", "png", "pipe:1")
		cmd := exec.CommandContext(ctx, "ffmpeg", args...)
		cmd.Env = rendererEnvironment()
		configureProcess(cmd)
		var out renderInspectionOutput
		cmd.Stdout = &out
		cmd.Stderr = io.Discard
		if err := cmd.Run(); err != nil {
			fail(w, 409, errors.New("无法提取视频画面；请检查FFmpeg和时间点"))
			return
		}
		config, err := png.DecodeConfig(bytes.NewReader(out.Bytes()))
		if err != nil || config.Width < 1 || config.Height < 1 || config.Width > 640 || config.Height > 640 {
			fail(w, 409, errors.New("视频抽样画面无效"))
			return
		}
		total += len(out.Bytes())
		if total > 8<<20 {
			fail(w, 409, errors.New("视频抽样总图片超过8MiB，请减少时间点"))
			return
		}
		content = append(content, map[string]string{"type": "text", "text": fmt.Sprintf("素材 %s，源视频时间点 %.3fs", asset.ID, *t)}, map[string]string{"type": "image", "mimeType": "image/png", "data": base64.StdEncoding.EncodeToString(out.Bytes())})
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, 200, map[string]any{"content": content, "duration": duration, "sampleCount": len(request.Times), "inspectionLimit": "sampled_frames_no_audio"})
}

// The rooted copy protects path traversal, but is insufficient if a file is a
// playlist: a demuxer could open paths named by its contents. Admit only uploaded
// binary media types, check their signatures, then force the specific demuxer.
func inspectionMediaFormat(path, mime string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", errors.New("无法检查素材格式")
	}
	defer f.Close()
	header := make([]byte, 16)
	n, err := io.ReadFull(f, header)
	if err != nil && err != io.ErrUnexpectedEOF {
		return "", errors.New("无法读取素材格式")
	}
	header = header[:n]
	var format string
	switch mime {
	case "image/png":
		if bytes.HasPrefix(header, []byte{137, 80, 78, 71, 13, 10, 26, 10}) {
			format = "png_pipe"
		}
	case "image/jpeg":
		if bytes.HasPrefix(header, []byte{255, 216, 255}) {
			format = "jpeg_pipe"
		}
	case "image/webp":
		if len(header) >= 12 && string(header[:4]) == "RIFF" && string(header[8:12]) == "WEBP" {
			format = "webp_pipe"
		}
	case "video/mp4":
		if len(header) >= 12 && string(header[4:8]) == "ftyp" {
			format = "mov"
		}
	case "video/webm":
		if bytes.HasPrefix(header, []byte{0x1a, 0x45, 0xdf, 0xa3}) {
			format = "matroska"
		}
	}
	if format == "" {
		return "", errors.New("素材实际内容与受支持的图片/视频格式不符；不允许播放列表或外部引用")
	}
	return format, nil
}

func inspectionFormatArgs(format string) []string {
	args := []string{"-format_whitelist", format, "-f", format}
	if format == "mov" {
		args = append(args, "-enable_drefs", "0", "-use_absolute_path", "0")
	}
	return args
}

func inspectionMediaDuration(ctx context.Context, path, format string) (float64, error) {
	if format != "mov" && format != "matroska" {
		return 0, errors.New("只支持探测MP4/WebM视频")
	}
	step, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	args := []string{"-v", "error", "-protocol_whitelist", "file,pipe"}
	args = append(args, inspectionFormatArgs(format)...)
	args = append(args, "-select_streams", "v:0", "-show_entries", "stream=codec_type,duration:format=duration", "-of", "json", path)
	cmd := exec.CommandContext(step, "ffprobe", args...)
	cmd.Env = rendererEnvironment()
	configureProcess(cmd)
	var out renderInspectionOutput
	cmd.Stdout = &out
	cmd.Stderr = io.Discard
	if err := cmd.Run(); err != nil {
		return 0, errors.New("视频格式或视频流无效")
	}
	var probe struct {
		Streams []struct {
			Type     string `json:"codec_type"`
			Duration string `json:"duration"`
		} `json:"streams"`
		Format struct {
			Duration string `json:"duration"`
		} `json:"format"`
	}
	if err := json.Unmarshal(out.Bytes(), &probe); err != nil || len(probe.Streams) != 1 || probe.Streams[0].Type != "video" {
		return 0, errors.New("视频缺少有效视频流")
	}
	duration, err := strconv.ParseFloat(probe.Streams[0].Duration, 64)
	if err != nil || !finite(duration) || duration <= 0 {
		duration, err = strconv.ParseFloat(probe.Format.Duration, 64)
	}
	if err != nil || !finite(duration) || duration <= 0 {
		return 0, errors.New("无法确定视频实际时长")
	}
	return duration, nil
}

// Read through os.Root and copy the opened regular file: FFmpeg must not reopen
// an attacker-replaced symlink after our validation. Bound disk use as uploads do.
func (a *App) assetInspectionCopy(pid, name string) (string, error) {
	root, err := os.OpenRoot(filepath.Join(a.dir(pid), "workspace", "assets"))
	if err != nil {
		return "", errors.New("素材目录不可用")
	}
	defer root.Close()
	f, err := root.Open(name)
	if err != nil {
		return "", errors.New("视频素材不可读取")
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > 256<<20 {
		return "", errors.New("视频素材必须是非空普通文件且不超过256MiB")
	}
	copy, err := os.CreateTemp("", "rundesk-video-inspect-*.media")
	if err != nil {
		return "", errors.New("无法创建视频检查临时文件")
	}
	name = copy.Name()
	n, copyErr := io.Copy(copy, io.LimitReader(f, (256<<20)+1))
	closeErr := copy.Close()
	if copyErr != nil || closeErr != nil || n == 0 || n > 256<<20 {
		os.Remove(name)
		return "", errors.New("无法准备视频素材检查")
	}
	return name, nil
}
