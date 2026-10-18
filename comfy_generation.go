package main

import (
	"context"
	"errors"
	"net/url"
	"os"
	"os/exec"
	"time"

	"rundesk-video-app/comfyh3"
)

// fetchComfyGenerated is the built-in ComfyUI MiniMax-H3 backend.
// It is selected by configuring:
//
//	VIDEO_VIDEO_GEN_URL=comfy://127.0.0.1:19897
//	VIDEO_VIDEO_GEN_MODEL=minimax_h3_fl2va
//
// Instead of POSTing to an http_binary_v1 adapter, it drives ComfyUI
// directly through the embedded comfyh3 library (submit /prompt, poll
// /history, download /view) and returns the raw MP4 bytes, which then
// flow through the exact same validateGenerated/asset pipeline.
func fetchComfyGenerated(ctx context.Context, j GenerationJob, target GenerationTarget, references []generationReferenceImage) ([]byte, error) {
	if len(references) > 0 {
		return nil, errors.New("内置ComfyUI后端暂不支持参考图")
	}
	u, e := url.Parse(target.URL)
	if e != nil || u.Host == "" {
		return nil, errors.New("生成服务配置无效")
	}
	client := comfyh3.NewClient("http://" + u.Host)
	client.PollTimeout = 10 * time.Minute

	params := comfyh3.GenerateParams{
		Prompt: j.Prompt,
		Width:  768,
		Height: 432,
		Length: 124,
		Seed:   time.Now().UnixNano() % 1_000_000_000,
		Steps:  4,
		Cfg:    4.5,
		Prefix: "h3_" + requestPrefix(j.RequestID),
		Audio:  true,
	}
	// duration (request target in seconds) -> frames @24fps, clamped to the
	// H3 training range; actual duration of the artifact is authoritative.
	if j.Duration != nil && *j.Duration > 0 {
		frames := int(*j.Duration * 24)
		if frames < 124 {
			frames = 124
		}
		if frames > 362 {
			frames = 362
		}
		params.Length = frames
	}
	// aspectRatio -> verified resolution mapping.
	switch j.AspectRatio {
	case "9:16":
		params.Width, params.Height = 432, 768
	case "1:1":
		params.Width, params.Height = 512, 512
	case "4:3":
		params.Width, params.Height = 640, 480
	case "3:4":
		params.Width, params.Height = 480, 640
	}
	res, e := client.Generate(ctx, comfyh3.DefaultModelSet(), params)
	if e != nil {
		return nil, e
	}
	// H3 native audio is very quiet (mean ~-39 dB); normalize with ffmpeg.
	// Falls back to the raw bytes if ffmpeg is unavailable or fails.
	if data, e := normalizeAudio(ctx, res.Bytes); e == nil {
		return data, nil
	}
	return res.Bytes, nil
}

// normalizeAudio boosts quiet audio tracks with ffmpeg (+18 dB, copy video).
func normalizeAudio(ctx context.Context, mp4 []byte) ([]byte, error) {
	in, e := os.CreateTemp("", "h3-in-*.mp4")
	if e != nil {
		return nil, e
	}
	defer os.Remove(in.Name())
	if _, e := in.Write(mp4); e != nil {
		in.Close()
		return nil, e
	}
	in.Close()
	out, e := os.CreateTemp("", "h3-out-*.mp4")
	if e != nil {
		return nil, e
	}
	outName := out.Name()
	out.Close()
	defer os.Remove(outName)
	cmd := exec.CommandContext(ctx, "ffmpeg", "-y", "-i", in.Name(),
		"-af", "volume=18dB", "-c:v", "copy", "-movflags", "+faststart", outName)
	if b, e := cmd.CombinedOutput(); e != nil {
		return nil, errors.New("ffmpeg audio normalize failed: " + string(b))
	}
	data, e := os.ReadFile(outName)
	if e != nil || len(data) == 0 {
		return nil, errors.New("normalized audio output empty")
	}
	return data, nil
}

func requestPrefix(id string) string {
	if len(id) <= 8 {
		return id
	}
	return id[:8]
}
