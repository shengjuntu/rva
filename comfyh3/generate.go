package comfyh3

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"time"
)

// Result is the outcome of one generation.
type Result struct {
	PromptID   string
	Duration   time.Duration
	OutputName string
	Bytes      []byte
	SHA256     string
	Size       int64
}

// Generate runs the whole pipeline: submit -> wait -> download the first MP4.
func (c *Client) Generate(ctx context.Context, ms ModelSet, p GenerateParams) (*Result, error) {
	stats, err := c.SystemStats(ctx)
	if err != nil {
		return nil, fmt.Errorf("comfyui unreachable: %w", err)
	}
	if stats.System.ComfyUIVersion == "" {
		return nil, fmt.Errorf("comfyui version unknown")
	}

	oi, err := c.ObjectInfo(ctx)
	if err != nil {
		return nil, fmt.Errorf("object_info: %w", err)
	}
	for _, cls := range []string{"UNETLoader", "CLIPLoader", "VAELoader", "MiniMaxH3ImageToVideo", "KSampler", "VAEDecode", "VHS_VideoCombine"} {
		if _, ok := oi[cls]; !ok {
			return nil, fmt.Errorf("comfyui missing node class %q", cls)
		}
	}
	if p.Audio && ms.AudioVAE != "" {
		if _, ok := oi["VAEDecodeAudio"]; !ok {
			return nil, fmt.Errorf("comfyui missing node class VAEDecodeAudio (audio requested)")
		}
	}

	wf := BuildGraph(oi, ms, p)
	start := time.Now()
	pid, err := c.Submit(ctx, wf)
	if err != nil {
		return nil, err
	}
	if err := c.WaitForCompletion(ctx, pid); err != nil {
		return nil, err
	}
	items, err := c.ListOutputs(ctx, pid)
	if err != nil {
		return nil, err
	}
	var mp4 *OutputItem
	for i := range items {
		name := items[i].Filename
		if len(name) >= 4 && name[len(name)-4:] == ".mp4" {
			mp4 = &items[i]
			break
		}
	}
	if mp4 == nil {
		return nil, fmt.Errorf("no mp4 output for prompt %s", pid)
	}
	data, err := c.Download(ctx, *mp4)
	if err != nil {
		return nil, err
	}
	if len(data) == 0 {
		return nil, fmt.Errorf("downloaded mp4 is empty for %s", mp4.Filename)
	}
	sum := sha256.Sum256(data)
	return &Result{
		PromptID:   pid,
		Duration:   time.Since(start),
		OutputName: mp4.Filename,
		Bytes:      data,
		SHA256:     hex.EncodeToString(sum[:]),
		Size:       int64(len(data)),
	}, nil
}
