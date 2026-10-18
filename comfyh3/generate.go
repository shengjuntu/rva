package comfyh3

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"path"
	"strings"
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
	if err := validateGenerateParams(p); err != nil {
		return nil, err
	}
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
	if err := ValidateEnvironment(oi, ms, p); err != nil {
		return nil, err
	}

	wf := BuildGraph(oi, ms, p)
	start := time.Now()
	pid, err := c.Submit(ctx, wf)
	if err != nil {
		return nil, err
	}
	if err := c.WaitForCompletion(ctx, pid); err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			// Delete only our queued prompt. Running work may continue: do not
			// call the global interrupt endpoint or claim remote cancellation.
			cleanup, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			_ = c.CancelQueued(cleanup, pid)
			cancel()
		}
		return nil, err
	}
	items, err := c.ListOutputs(ctx, pid)
	if err != nil {
		return nil, err
	}
	var mp4 *OutputItem
	for i := range items {
		name := items[i].Filename
		if strings.EqualFold(path.Ext(name), ".mp4") {
			mp4 = &items[i]
			break
		}
	}
	if mp4 == nil {
		return nil, errors.New("ComfyUI completed without an MP4 output")
	}
	data, err := c.Download(ctx, *mp4)
	if err != nil {
		return nil, err
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

// ValidateEnvironment verifies the required node classes and advertised model
// choices without loading models or queuing GPU work. It is an API-schema
// preflight, not proof that the configured models can execute successfully.
func ValidateEnvironment(oi ObjectInfo, ms ModelSet, p GenerateParams) error {
	classes := []string{"UNETLoader", "CLIPLoader", "VAELoader", "MiniMaxH3ImageToVideo", "KSampler", "VAEDecode", "VHS_VideoCombine"}
	if ms.Lora != "" {
		classes = append(classes, "LoraLoader")
	}
	if p.Audio && ms.AudioVAE != "" {
		classes = append(classes, "VAEDecodeAudio")
	}
	for _, class := range classes {
		if _, ok := oi[class]; !ok {
			return fmt.Errorf("ComfyUI missing required node %s", class)
		}
	}
	models := []struct{ class, input, value string }{
		{"UNETLoader", "unet_name", ms.Diffusion},
		{"CLIPLoader", "clip_name", ms.Clip},
		{"CLIPLoader", "type", ms.ClipType},
		{"VAELoader", "vae_name", ms.VideoVAE},
	}
	if ms.Lora != "" {
		models = append(models, struct{ class, input, value string }{"LoraLoader", "lora_name", ms.Lora})
	}
	if p.Audio && ms.AudioVAE != "" {
		models = append(models, struct{ class, input, value string }{"VAELoader", "vae_name", ms.AudioVAE})
	}
	for _, model := range models {
		node := oi[model.class]
		input, ok := node.Input.Required[model.input]
		if !ok {
			input = node.Input.Optional[model.input]
		}
		spec, ok := input.([]any)
		if !ok || len(spec) == 0 {
			return fmt.Errorf("ComfyUI node %s has no supported model input schema", model.class)
		}
		choices, ok := spec[0].([]any)
		if !ok || len(choices) == 0 {
			return fmt.Errorf("ComfyUI node %s has no model choices", model.class)
		}
		found := false
		for _, choice := range choices {
			if name, ok := choice.(string); ok && name == model.value && name != "" {
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("ComfyUI configured model is unavailable for %s", model.class)
		}
	}
	return nil
}

func validateGenerateParams(p GenerateParams) error {
	if strings.TrimSpace(p.Prompt) == "" || len(p.Prompt) > 16000 || strings.ContainsRune(p.Prompt, 0) {
		return errors.New("invalid ComfyUI prompt")
	}
	if p.Width <= 0 || p.Height <= 0 || p.Width > 4096 || p.Height > 4096 || p.Length < 124 || p.Length > 362 || p.Seed < 0 || p.Steps < 0 || p.Steps > 1000 || math.IsNaN(p.Cfg) || math.IsInf(p.Cfg, 0) || p.Cfg < 0 || p.Cfg > 1000 {
		return errors.New("invalid MiniMax-H3 generation parameters")
	}
	if len(p.Prefix) > 200 || strings.ContainsAny(p.Prefix, "\\\x00\r\n") || strings.HasPrefix(p.Prefix, "/") || strings.Contains(p.Prefix, ":") || (p.Prefix != "" && path.Clean(p.Prefix) != p.Prefix) {
		return errors.New("invalid ComfyUI output prefix")
	}
	for _, component := range strings.Split(p.Prefix, "/") {
		if component == ".." || component == "." {
			return errors.New("invalid ComfyUI output prefix")
		}
	}
	return nil
}
