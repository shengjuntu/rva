package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

func decimal(v float64) string { return strconv.FormatFloat(v, 'f', 6, 64) }
func (a *App) renderEditDSL(ctx context.Context, p Project, job Render) {
	dir := filepath.Join(a.dir(p.ID), "renders", job.ID)
	e := os.MkdirAll(filepath.Join(dir, "assets"), 0700)
	var log *os.File
	if e == nil {
		log, e = os.OpenFile(filepath.Join(dir, "render.log"), os.O_CREATE|os.O_WRONLY, 0600)
	}
	if log != nil {
		defer log.Close()
	}
	ir := EditIR{}
	if e == nil {
		ir, e = compileEdit(*p.EditDSL, p.Assets)
	}
	if e == nil {
		e = atomicJSON(filepath.Join(dir, "snapshot.json"), p)
	}
	if e == nil {
		e = atomicJSON(filepath.Join(dir, "edit-ir.json"), ir)
	}
	byID := map[string]Asset{}
	for _, asset := range p.Assets {
		byID[asset.ID] = asset
	}
	copied := map[string]bool{}
	if e == nil {
		for _, clip := range ir.Clips {
			if clip.Asset == "" || copied[clip.Asset] {
				continue
			}
			asset := byID[clip.Asset]
			if filepath.Base(asset.File) != asset.File {
				e = errors.New("素材路径无效")
				break
			}
			e = snapshotAsset(a.dir(p.ID), dir, asset)
			if e != nil {
				break
			}
			copied[clip.Asset] = true
		}
	}
	if e == nil && ir.HasText {
		e = copyFile(a.font, filepath.Join(dir, "font.ttf"))
	}
	// Probe actual media ranges after immutable snapshot creation, before encoding.
	durations := map[string]float64{}
	if e == nil {
		for _, clip := range ir.Clips {
			if clip.Kind == "text" || strings.HasPrefix(byID[clip.Asset].MIME, "image/") {
				continue
			}
			if _, ok := durations[clip.Asset]; !ok {
				var duration float64
				duration, e = editMediaDuration(ctx, filepath.Join(dir, "assets", byID[clip.Asset].File), clip.Kind)
				if e != nil {
					break
				}
				durations[clip.Asset] = duration
			}
			if clip.SourceOut > durations[clip.Asset]+.002 {
				e = fmt.Errorf("片段 %s 的素材出点 %.3f 超过实际时长 %.3f", clip.ID, clip.SourceOut, durations[clip.Asset])
				break
			}
		}
	}
	args := []string{"-nostdin", "-y", "-v", "warning", "-filter_complex_threads", "1", "-f", "lavfi", "-i", fmt.Sprintf("color=c=0x%s:s=%dx%d:r=%d:d=%s", strings.TrimPrefix(ir.Canvas.Background, "#"), ir.Canvas.Width, ir.Canvas.Height, ir.Canvas.FPS, decimal(ir.Duration))}
	var graph strings.Builder
	visuals := []EditIRClip{}
	audioLabels := []string{}
	inputs := 1
	if e == nil {
		for i, clip := range ir.Clips {
			label := fmt.Sprintf("clip%d", i)
			if clip.Kind == "text" {
				file := fmt.Sprintf("text_%d.txt", i)
				e = os.WriteFile(filepath.Join(dir, file), []byte(clip.Text), 0600)
				if e != nil {
					break
				}
				fmt.Fprintf(&graph, "color=c=black@0:s=%dx%d:r=%d:d=%s,format=rgba,drawtext=fontfile=font.ttf:textfile=%s:expansion=none:fontcolor=0x%s:fontsize=%d:x=0:y=0", clip.Layout.Width, clip.Layout.Height, ir.Canvas.FPS, decimal(clip.Duration), file, strings.TrimPrefix(clip.Color, "#"), clip.FontSize)
			} else {
				asset := byID[clip.Asset]
				source := filepath.Join("assets", asset.File)
				image := strings.HasPrefix(asset.MIME, "image/")
				if image {
					args = append(args, "-loop", "1", "-framerate", strconv.Itoa(ir.Canvas.FPS), "-t", decimal(clip.Duration), "-protocol_whitelist", "file,pipe", "-i", source)
				} else {
					args = append(args, "-ss", decimal(clip.SourceIn), "-t", decimal(clip.SourceOut-clip.SourceIn), "-protocol_whitelist", "file,pipe", "-i", source)
				}
				input := inputs
				inputs++
				if clip.Kind == "audio" {
					fmt.Fprintf(&graph, "[%d:a]asetpts=PTS-STARTPTS,atempo=%s,aresample=48000,aformat=sample_fmts=fltp:channel_layouts=stereo,asettb=1/48000,asetpts=N/SR/TB,atrim=duration=%s,volume=%s", input, decimal(clip.Speed), decimal(clip.Duration), decimal(clip.Volume))
					if clip.FadeIn > 0 {
						fmt.Fprintf(&graph, ",afade=t=in:st=0:d=%s", decimal(clip.FadeIn))
					}
					if clip.FadeOut > 0 {
						fmt.Fprintf(&graph, ",afade=t=out:st=%s:d=%s", decimal(clip.Duration-clip.FadeOut), decimal(clip.FadeOut))
					}
					delay := int64(math.Round(clip.Start * 1000))
					fmt.Fprintf(&graph, ",adelay=%d|%d,asetpts=N/SR/TB[%s];\n", delay, delay, label)
					audioLabels = append(audioLabels, "["+label+"]")
					continue
				}
				fmt.Fprintf(&graph, "[%d:v]setpts=(PTS-STARTPTS)/%s,fps=%d,format=rgba,", input, decimal(clip.Speed), ir.Canvas.FPS)
				if clip.Layout.Fit == "contain" {
					fmt.Fprintf(&graph, "scale=%d:%d:force_original_aspect_ratio=decrease,pad=%d:%d:(ow-iw)/2:(oh-ih)/2:color=black@0", clip.Layout.Width, clip.Layout.Height, clip.Layout.Width, clip.Layout.Height)
				} else {
					fmt.Fprintf(&graph, "scale=%d:%d:force_original_aspect_ratio=increase,crop=%d:%d", clip.Layout.Width, clip.Layout.Height, clip.Layout.Width, clip.Layout.Height)
				}
			}
			if clip.Layout.Opacity != nil {
				fmt.Fprintf(&graph, ",colorchannelmixer=aa=%s", decimal(*clip.Layout.Opacity))
			}
			if clip.FadeIn > 0 {
				fmt.Fprintf(&graph, ",fade=t=in:st=0:d=%s:alpha=1", decimal(clip.FadeIn))
			}
			if clip.FadeOut > 0 {
				fmt.Fprintf(&graph, ",fade=t=out:st=%s:d=%s:alpha=1", decimal(clip.Duration-clip.FadeOut), decimal(clip.FadeOut))
			}
			fmt.Fprintf(&graph, ",setpts=PTS-STARTPTS+%s/TB[%s];\n", decimal(clip.Start), label)
			visuals = append(visuals, clip)
		}
	}
	if e == nil {
		// Track order defines compositing order; clip declaration order resolves overlaps within a track.
		sort.SliceStable(visuals, func(i, j int) bool { return visuals[i].Layer < visuals[j].Layer })
		fmt.Fprint(&graph, "[0:v]format=rgba[base0];\n")
		base := "base0"
		for n, clip := range visuals {
			index := 0
			for i, c := range ir.Clips {
				if c.ID == clip.ID {
					index = i
					break
				}
			}
			next := fmt.Sprintf("base%d", n+1)
			fmt.Fprintf(&graph, "[%s][clip%d]overlay=x=%d:y=%d:eof_action=pass:repeatlast=0:enable='gte(t,%s)*lt(t,%s)'[%s];\n", base, index, clip.Layout.X, clip.Layout.Y, decimal(clip.Start), decimal(clip.End), next)
			base = next
		}
		fmt.Fprintf(&graph, "[%s]format=yuv420p[vout]", base)
		if len(audioLabels) > 0 {
			fmt.Fprintf(&graph, ";\n%samix=inputs=%d:duration=longest:normalize=0,apad=whole_dur=%s,atrim=duration=%s,asetpts=N/SR/TB[aout]", strings.Join(audioLabels, ""), len(audioLabels), decimal(ir.Duration), decimal(ir.Duration))
		}
		e = os.WriteFile(filepath.Join(dir, "filter.ffscript"), []byte(graph.String()), 0600)
		args = append(args, "-filter_complex_script", "filter.ffscript", "-map", "[vout]")
		if ir.HasAudio {
			args = append(args, "-map", "[aout]", "-c:a", "aac", "-ar", "48000", "-ac", "2")
		}
		args = append(args, "-c:v", "libx264", "-preset", "veryfast", "-threads", "2", "-crf", "23", "-r", strconv.Itoa(ir.Canvas.FPS), "-t", decimal(ir.Duration), "-movflags", "+faststart", "final.mp4")
	}
	if e == nil {
		fmt.Fprintln(log, "ffmpeg editing DSL", strings.Join(args, " "))
		cmd := exec.CommandContext(ctx, "ffmpeg", args...)
		cmd.Dir = dir
		cmd.Env = rendererEnvironment()
		configureProcess(cmd)
		cmd.Stdout = log
		cmd.Stderr = log
		e = cmd.Run()
	}
	if e == nil {
		e = verifyEditOutput(ctx, dir, ir)
	}
	if e != nil && log != nil {
		fmt.Fprintln(log, "FAILED:", e)
	}
	a.finishRender(ctx, p.ID, job.ID, e)
}
func verifyEditOutput(ctx context.Context, dir string, ir EditIR) error {
	cmd := exec.CommandContext(ctx, "ffprobe", "-v", "error", "-show_format", "-show_streams", "-of", "json", filepath.Join(dir, "final.mp4"))
	cmd.Env = rendererEnvironment()
	configureProcess(cmd)
	raw, e := cmd.Output()
	if e != nil {
		return e
	}
	if e = os.WriteFile(filepath.Join(dir, "validation.json"), raw, 0600); e != nil {
		return e
	}
	var result struct {
		Format struct {
			Duration string `json:"duration"`
		} `json:"format"`
		Streams []struct {
			Type   string `json:"codec_type"`
			Width  int    `json:"width"`
			Height int    `json:"height"`
			FPS    string `json:"r_frame_rate"`
		} `json:"streams"`
	}
	if e = json.Unmarshal(raw, &result); e != nil {
		return e
	}
	video, audio := false, false
	for _, s := range result.Streams {
		if s.Type == "audio" {
			audio = true
		}
		if s.Type == "video" && s.Width == ir.Canvas.Width && s.Height == ir.Canvas.Height && s.FPS == fmt.Sprintf("%d/1", ir.Canvas.FPS) {
			video = true
		}
	}
	duration, e := strconv.ParseFloat(result.Format.Duration, 64)
	if e != nil || !finite(duration) || !video || audio != ir.HasAudio || math.Abs(duration-ir.Duration) > .2 {
		return errors.New("DSL成片尺寸、帧率、音轨或时长与编译结果不符")
	}
	return nil
}
func (a *App) finishRender(ctx context.Context, pid, rid string, e error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	delete(a.jobs, rid)
	p := a.projects[pid]
	if p == nil {
		return
	}
	for i := range p.Renders {
		if p.Renders[i].ID == rid {
			j := &p.Renders[i]
			j.State = "completed"
			if e != nil {
				j.State = "failed"
				j.Error = e.Error()
			}
			if ctx.Err() != nil {
				j.State = "interrupted"
				j.Error = "渲染已取消"
			}
			break
		}
	}
	_ = a.save(p)
}

func editMediaDuration(ctx context.Context, path, kind string) (float64, error) {
	step, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	stream := "v:0"
	if kind == "audio" {
		stream = "a:0"
	}
	cmd := exec.CommandContext(step, "ffprobe", "-v", "error", "-protocol_whitelist", "file,pipe", "-select_streams", stream, "-show_entries", "stream=codec_type,duration:format=duration", "-of", "json", path)
	cmd.Env = rendererEnvironment()
	configureProcess(cmd)
	raw, e := cmd.Output()
	if e != nil {
		return 0, e
	}
	var info struct {
		Streams []struct {
			Type     string `json:"codec_type"`
			Duration string `json:"duration"`
		} `json:"streams"`
		Format struct {
			Duration string `json:"duration"`
		} `json:"format"`
	}
	if e = json.Unmarshal(raw, &info); e != nil || len(info.Streams) == 0 {
		return 0, errors.New("素材缺少所需的可解码媒体流")
	}
	duration, e := strconv.ParseFloat(info.Streams[0].Duration, 64)
	if e != nil || !finite(duration) || duration <= 0 {
		duration, e = strconv.ParseFloat(info.Format.Duration, 64)
	}
	if e != nil || !finite(duration) || duration <= 0 {
		return 0, errors.New("无法确定素材实际时长")
	}
	return duration, nil
}
