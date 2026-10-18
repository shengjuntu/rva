package main

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"rundesk-video-app/comfyh3"
)

func comfyModelSupported(model string) bool {
	return model == "minimax_h3_fl2va" || model == comfyh3.DefaultModelSet().Diffusion
}

// Keep the user's verified H3 workflow, but expose the effective request instead
// of silently treating a clamped frame count as the requested/actual duration.
func comfyGenerationParams(j GenerationJob, gainDB float64) (comfyh3.GenerateParams, *GenerationParameters, []string) {
	sum := sha256.Sum256([]byte(j.RequestID))
	p := comfyh3.GenerateParams{Prompt: j.Prompt, Width: 768, Height: 432, Length: 124,
		Seed: int64(binary.BigEndian.Uint64(sum[:8]) % 1_000_000_000), Steps: 4, Cfg: 4.5,
		Prefix: "h3_" + j.RequestID, Audio: true}
	var warnings []string
	if j.Duration != nil {
		p.Length = int(*j.Duration * 24)
		if p.Length < 124 {
			p.Length = 124
		}
		if p.Length > 362 {
			p.Length = 362
		}
		if math.Abs(float64(p.Length)/24-*j.Duration) > 0.000001 {
			warnings = append(warnings, fmt.Sprintf("MiniMax-H3时长目标%.6g秒映射为%d帧@24fps（%.6g秒）；帧数范围124–362，产物实际时长仍以媒体探测为准", *j.Duration, p.Length, float64(p.Length)/24))
		}
	}
	switch j.AspectRatio {
	case "9:16":
		p.Width, p.Height = 432, 768
	case "1:1":
		p.Width, p.Height = 512, 512
	case "4:3":
		p.Width, p.Height = 640, 480
	case "3:4":
		p.Width, p.Height = 480, 640
	}
	effective := &GenerationParameters{Width: p.Width, Height: p.Height, FrameCount: p.Length, FrameRate: 24,
		DurationSeconds: float64(p.Length) / 24, Seed: p.Seed, NativeAudio: true, AudioGainDB: gainDB}
	return p, effective, warnings
}

func newComfyGenerationClient(target GenerationTarget) (*comfyh3.Client, error) {
	u, err := url.Parse(target.URL)
	if err != nil || u.Scheme != "comfy" || u.Hostname() == "" || u.User != nil || u.Path != "" || u.RawPath != "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.Opaque != "" || !comfyModelSupported(target.Model) {
		return nil, errors.New("ComfyUI生成配置无效；地址必须为comfy://host:port，模型必须为minimax_h3_fl2va")
	}
	client := comfyh3.NewClient("http://" + u.Host)
	if target.Key != "" {
		client.Authorization = "Bearer " + target.Key
	}
	client.PollTimeout = 10 * time.Minute
	return client, nil
}

// Probe only reads API/node/model metadata. It does not load or execute models.
func probeComfyGeneration(ctx context.Context, target GenerationTarget) error {
	client, err := newComfyGenerationClient(target)
	if err != nil {
		return err
	}
	stats, err := client.SystemStats(ctx)
	if err != nil || stats.System.ComfyUIVersion == "" {
		return errors.New("ComfyUI系统信息不可读取或版本信息缺失")
	}
	oi, err := client.ObjectInfo(ctx)
	if err != nil {
		return errors.New("ComfyUI节点和模型清单不可读取")
	}
	if err := comfyh3.ValidateEnvironment(oi, comfyh3.DefaultModelSet(), comfyh3.GenerateParams{Audio: true}); err != nil {
		return fmt.Errorf("ComfyUI工作流依赖未就绪：%w", err)
	}
	return nil
}

// fetchComfyGenerated uses the verified text-to-video graph. Reference-image
// requests are rejected before submission, not silently ignored.
func fetchComfyGenerated(ctx context.Context, j GenerationJob, target GenerationTarget, references []generationReferenceImage) ([]byte, error) {
	if j.Type != "video_generation" || len(references) > 0 {
		return nil, errors.New("内置ComfyUI MiniMax-H3仅支持无参考图的文生视频")
	}
	client, err := newComfyGenerationClient(target)
	if err != nil {
		return nil, err
	}
	params, _, _ := comfyGenerationParams(j, target.AudioGainDB)
	res, err := client.Generate(ctx, comfyh3.DefaultModelSet(), params)
	if err != nil {
		return nil, err
	}
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	return applyComfyAudioGain(ctx, res.Bytes, target.AudioGainDB)
}

// The user's +18dB correction remains the default; zero preserves the original
// MP4 bytes. Gain is not loudness normalization. Limit after gain to avoid hard
// sample clipping; failures are explicit so a canceled/failed conversion cannot
// quietly return a different audio result.
func applyComfyAudioGain(ctx context.Context, mp4 []byte, gainDB float64) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !finite(gainDB) || gainDB < -30 || gainDB > 30 {
		return nil, errors.New("ComfyUI音轨增益必须在-30至30dB之间")
	}
	if len(mp4) < 12 || string(mp4[4:8]) != "ftyp" || len(mp4) > 128<<20 {
		return nil, errors.New("ComfyUI生成产物必须为非空MP4且不超过128MiB")
	}
	if gainDB == 0 {
		return mp4, nil
	}
	dir, err := os.MkdirTemp("", "h3-audio-*")
	if err != nil {
		return nil, errors.New("无法准备ComfyUI音轨处理")
	}
	defer os.RemoveAll(dir)
	input, output := filepath.Join(dir, "input.mp4"), filepath.Join(dir, "output.mp4")
	if err := os.WriteFile(input, mp4, 0600); err != nil {
		return nil, errors.New("无法准备ComfyUI音轨处理")
	}
	step, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	args := []string{"-nostdin", "-v", "error", "-y", "-protocol_whitelist", "file,pipe"}
	args = append(args, inspectionFormatArgs("mov")...)
	args = append(args, "-i", input, "-map", "0:v:0", "-map", "0:a:0", "-c:v", "copy", "-c:a", "aac",
		"-af", "volume="+decimal(gainDB)+"dB,alimiter=limit=0.95:level=false:latency=true", "-t", "600", "-fs", "134217728", "-movflags", "+faststart", output)
	cmd := exec.CommandContext(step, "ffmpeg", args...)
	cmd.Env = rendererEnvironment()
	configureProcess(cmd)
	cmd.Stdout, cmd.Stderr = io.Discard, io.Discard
	if err := cmd.Run(); err != nil {
		if step.Err() != nil {
			return nil, step.Err()
		}
		return nil, errors.New("ComfyUI原生音轨增益处理失败（需要有效视频和音频流及FFmpeg volume/alimiter过滤器）；未回退到未处理音频")
	}
	info, err := os.Stat(output)
	if err != nil || info.Size() <= 0 || info.Size() > 128<<20 {
		return nil, errors.New("ComfyUI音轨处理产物为空或超过128MiB")
	}
	data, err := os.ReadFile(output)
	if err != nil {
		return nil, errors.New("无法读取ComfyUI音轨处理产物")
	}
	return data, nil
}
