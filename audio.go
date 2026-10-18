package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Times are seconds. Volume is linear gain, not a percentage.
type AudioTrack struct {
	AssetID     string  `json:"assetId"`
	Volume      float64 `json:"volume"`
	Start       float64 `json:"start"`
	SourceStart float64 `json:"sourceStart"`
	Loop        bool    `json:"loop"`
}
type AudioMix struct {
	Narration AudioTrack `json:"narration"`
	Music     AudioTrack `json:"music"`
}

func cloneAudio(m *AudioMix) *AudioMix {
	if m == nil {
		return nil
	}
	c := *m
	return &c
}
func audioTracks(m *AudioMix) []AudioTrack {
	if m == nil {
		return nil
	}
	tracks := []AudioTrack{}
	for _, t := range []AudioTrack{m.Narration, m.Music} {
		if t.AssetID != "" {
			tracks = append(tracks, t)
		}
	}
	return tracks
}
func finite(v float64) bool { return !math.IsNaN(v) && !math.IsInf(v, 0) }
func validateAudio(m *AudioMix, assets []Asset, duration float64) error {
	if m == nil {
		return nil
	}
	found := map[string]Asset{}
	for _, a := range assets {
		found[a.ID] = a
	}
	for name, t := range map[string]AudioTrack{"配音": m.Narration, "背景音乐": m.Music} {
		if t.AssetID == "" {
			continue
		}
		a, ok := found[t.AssetID]
		if !ok || !strings.HasPrefix(a.MIME, "audio/") {
			return fmt.Errorf("%s必须选择已上传的音频素材", name)
		}
		if !finite(t.Volume) || t.Volume < 0 || t.Volume > 2 {
			return fmt.Errorf("%s音量必须在 0–200%%", name)
		}
		if !finite(t.Start) || t.Start < 0 || t.Start > 600 || (duration > 0 && t.Start >= duration) {
			return fmt.Errorf("%s起点必须在视频时长以内", name)
		}
		if !finite(t.SourceStart) || t.SourceStart < 0 || t.SourceStart > 86400 {
			return fmt.Errorf("%s的素材截取起点无效", name)
		}
	}
	if m.Narration.AssetID != "" && m.Narration.Loop {
		return errors.New("配音不支持循环；请在背景音乐轨使用循环")
	}
	return nil
}
func snapshotAsset(pdir, dir string, asset Asset) error {
	root, e := os.OpenRoot(filepath.Join(pdir, "workspace", "assets"))
	if e != nil {
		return e
	}
	defer root.Close()
	in, e := root.Open(asset.File)
	if e != nil {
		return e
	}
	defer in.Close()
	out, e := os.OpenFile(filepath.Join(dir, "assets", asset.File), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
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
func (a *App) snapshotAudio(p Project, dir string) error {
	copied := map[string]bool{}
	for _, t := range audioTracks(p.Audio) {
		if copied[t.AssetID] {
			continue
		}
		found := false
		for _, asset := range p.Assets {
			if asset.ID == t.AssetID {
				if e := snapshotAsset(a.dir(p.ID), dir, asset); e != nil {
					return e
				}
				copied[t.AssetID] = true
				found = true
				break
			}
		}
		if !found {
			return errors.New("音频素材不存在")
		}
	}
	return nil
}
func rendererEnvironment() []string {
	env := []string{}
	for _, s := range os.Environ() {
		key, _, _ := strings.Cut(s, "=")
		switch key {
		case "VIDEO_APP_TOKEN", "RUNDESK_TOKEN", "VIDEO_TTS_API_KEY", "HYPERFRAMES_NO_TELEMETRY", "DO_NOT_TRACK":
			continue
		}
		env = append(env, s)
	}
	return append(env, "HYPERFRAMES_NO_TELEMETRY=1", "DO_NOT_TRACK=1")
}
func audioDuration(ctx context.Context, path string) (float64, error) {
	probeCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(probeCtx, "ffprobe", "-v", "error", "-protocol_whitelist", "file,pipe", "-select_streams", "a:0", "-show_entries", "stream=codec_type:format=duration", "-of", "json", path)
	cmd.Env = rendererEnvironment()
	configureProcess(cmd)
	raw, e := cmd.Output()
	if e != nil {
		return 0, fmt.Errorf("无法读取音频：%w", e)
	}
	var v struct {
		Streams []struct {
			CodecType string `json:"codec_type"`
		} `json:"streams"`
		Format struct {
			Duration string `json:"duration"`
		} `json:"format"`
	}
	if e = json.Unmarshal(raw, &v); e != nil || len(v.Streams) == 0 {
		return 0, errors.New("素材没有可解码的音轨")
	}
	d, e := strconv.ParseFloat(v.Format.Duration, 64)
	if e != nil || !finite(d) || d <= 0 {
		return 0, errors.New("无法确定音频时长")
	}
	return d, nil
}

type runCommand func(string, ...string) error

func mixAudio(ctx context.Context, p Project, dir string, duration float64, run runCommand) error {
	tracks := audioTracks(p.Audio)
	if len(tracks) == 0 {
		return nil
	}
	assets := map[string]Asset{}
	for _, a := range p.Assets {
		assets[a.ID] = a
	}
	args := []string{"-hide_banner", "-nostdin", "-y", "-i", filepath.Join(dir, "silent.mp4")}
	filters := []string{}
	inputs := ""
	for i, t := range tracks {
		asset, ok := assets[t.AssetID]
		if !ok {
			return errors.New("音频素材不存在")
		}
		source := filepath.Join(dir, "assets", asset.File)
		d, e := audioDuration(ctx, source)
		if e != nil {
			return fmt.Errorf("%s: %w", asset.Name, e)
		}
		if t.SourceStart >= d-0.001 {
			return fmt.Errorf("%s 的截取起点 %.3f 秒已超过素材时长 %.3f 秒", asset.Name, t.SourceStart, d)
		}
		// Decode the selected region first. Looping now repeats only that region.
		prepared := filepath.Join(dir, fmt.Sprintf("audio-%d.wav", i))
		span := math.Min(d-t.SourceStart, duration-t.Start)
		e = run("ffmpeg", "-hide_banner", "-nostdin", "-y", "-protocol_whitelist", "file,pipe", "-ss", fmt.Sprintf("%.6f", t.SourceStart), "-i", source, "-map", "0:a:0", "-vn", "-t", fmt.Sprintf("%.6f", span), "-ar", "48000", "-ac", "2", "-c:a", "pcm_s16le", prepared)
		if e != nil {
			return fmt.Errorf("解码音频 %s: %w", asset.Name, e)
		}
		if t.Loop {
			args = append(args, "-stream_loop", "-1")
		}
		args = append(args, "-i", prepared)
		delay := int64(math.Round(t.Start * 48000))
		filters = append(filters, fmt.Sprintf("[%d:a:0]asetpts=PTS-STARTPTS,volume=%.6f,adelay=%dS:all=1,apad,atrim=duration=%.6f[a%d]", i+1, t.Volume, delay, duration, i))
		inputs += fmt.Sprintf("[a%d]", i)
	}
	filters = append(filters, fmt.Sprintf("%samix=inputs=%d:normalize=0:duration=longest,alimiter=limit=0.95:level=0:latency=1[out]", inputs, len(tracks)))
	args = append(args, "-filter_complex", strings.Join(filters, ";"), "-map", "0:v:0", "-map", "[out]", "-c:v", "copy", "-c:a", "aac", "-b:a", "192k", "-ar", "48000", "-ac", "2", "-t", fmt.Sprintf("%.6f", duration), "-movflags", "+faststart", filepath.Join(dir, "final.mp4"))
	return run("ffmpeg", args...)
}
