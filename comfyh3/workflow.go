package comfyh3

import "fmt"

// Workflow builds the MiniMax-H3 text-to-video ComfyUI graph.
// The topology and model names below are the verified-productive set:
//   - diffusion: minimax_h3_fl2va_pruned_bf16.safetensors
//   - clip:      qwen3vl_32b_minimax_h3_int8_convrot.safetensors (5376-dim)
//   - video vae: minimax_h3_video_vae_fp16.safetensors
//   - audio vae: minimax_h3_audio_vae_fp32.safetensors
//   - lora:      minimax_h3_fl2v_turbo_4step_v1.2_768p_comfyui_bf16.safetensors
//   - KSampler:  steps=4 (with lora), cfg=4.5
//   - VHS_VideoCombine frame_rate=24, format=video/h264-mp4

type ModelSet struct {
	Diffusion string
	Clip      string
	ClipType  string // "minimax"
	VideoVAE  string
	AudioVAE  string // optional ("" disables audio)
	Lora      string // optional ("" disables lora)
}

type GenerateParams struct {
	Prompt string
	Width  int
	Height int
	Length int // frames @24fps; H3 trained range 124..362
	Seed   int64
	Steps  int
	Cfg    float64
	Prefix string
	Audio  bool
}

func DefaultModelSet() ModelSet {
	return ModelSet{
		Diffusion: "minimax_h3_fl2va_pruned_bf16.safetensors",
		Clip:      "qwen3vl_32b_minimax_h3_int8_convrot.safetensors",
		ClipType:  "minimax",
		VideoVAE:  "minimax_h3_video_vae_fp16.safetensors",
		AudioVAE:  "minimax_h3_audio_vae_fp32.safetensors",
		Lora:      "minimax_h3_fl2v_turbo_4step_v1.2_768p_comfyui_bf16.safetensors",
	}
}

// BuildGraph returns a ComfyUI workflow map for the given models and params.
// It fills every required/optional input default from the live object_info
// schema (same behavior as the proven Python build_inputs): caller-supplied
// values win, everything else gets the schema default (enum[0] when no default).
func BuildGraph(oi ObjectInfo, ms ModelSet, p GenerateParams) map[string]any {
	wf := map[string]any{}

	wf["1"] = node(oi, "UNETLoader", map[string]any{"unet_name": ms.Diffusion})
	modelRef := []any{"1", 0}

	wf["2"] = node(oi, "CLIPLoader", map[string]any{"clip_name": ms.Clip, "type": ms.ClipType})
	clipRef := []any{"2", 0}

	wf["3"] = node(oi, "VAELoader", map[string]any{"vae_name": ms.VideoVAE})
	videoVAERef := []any{"3", 0}

	nid := 4
	if ms.Lora != "" {
		wf[itoa(nid)] = node(oi, "LoraLoader", map[string]any{
			"model": modelRef, "clip": clipRef, "lora_name": ms.Lora})
		modelRef = []any{itoa(nid), 0}
		clipRef = []any{itoa(nid), 1}
		nid++
	}

	genNID := itoa(nid)
	wf[genNID] = node(oi, "MiniMaxH3ImageToVideo", map[string]any{
		"clip": clipRef, "vae": videoVAERef,
		"prompt": p.Prompt, "width": p.Width, "height": p.Height, "length": p.Length})
	genCond := []any{genNID, 0}
	genLat := []any{genNID, 1}
	nid++

	var audioVAERef []any
	if p.Audio && ms.AudioVAE != "" {
		audioNID := itoa(nid)
		wf[audioNID] = node(oi, "VAELoader", map[string]any{"vae_name": ms.AudioVAE})
		audioVAERef = []any{audioNID, 0}
		nid++
	}

	steps := p.Steps
	if steps <= 0 {
		if ms.Lora != "" {
			steps = 4
		} else {
			steps = 50
		}
	}
	cfg := p.Cfg
	if cfg <= 0 {
		cfg = 4.5
	}

	ksamplerNID := itoa(nid)
	wf[ksamplerNID] = node(oi, "KSampler", map[string]any{
		"model": modelRef, "positive": genCond, "negative": genCond, "latent_image": genLat,
		"seed": p.Seed, "steps": steps, "cfg": cfg})
	ksamplerRef := []any{ksamplerNID, 0}
	nid++

	vaeDecodeNID := itoa(nid)
	wf[vaeDecodeNID] = node(oi, "VAEDecode", map[string]any{
		"samples": ksamplerRef, "vae": videoVAERef})
	imgRef := []any{vaeDecodeNID, 0}
	nid++

	var audRef []any
	if p.Audio && ms.AudioVAE != "" {
		audNID := itoa(nid)
		wf[audNID] = node(oi, "VAEDecodeAudio", map[string]any{
			"samples": ksamplerRef, "vae": audioVAERef})
		audRef = []any{audNID, 0}
		nid++
	}

	prefix := p.Prefix
	if prefix == "" {
		prefix = "h3_video"
	}
	combineInputs := fillDefaults(oi, "VHS_VideoCombine", map[string]any{
		"images":          imgRef,
		"frame_rate":      24,
		"filename_prefix": prefix,
		"format":          "video/h264-mp4",
	})
	if audRef != nil {
		combineInputs["audio"] = audRef
	}
	wf[itoa(nid)] = map[string]any{"class_type": "VHS_VideoCombine", "inputs": combineInputs}
	return wf
}

// node wraps filled inputs into the ComfyUI API format:
// {"class_type": cls, "inputs": {...}}.
func node(oi ObjectInfo, cls string, inputs map[string]any) map[string]any {
	return map[string]any{"class_type": cls, "inputs": fillDefaults(oi, cls, inputs)}
}

// fillDefaults merges schema defaults into inputs for class cls.
func fillDefaults(oi ObjectInfo, cls string, inputs map[string]any) map[string]any {
	node, ok := oi[cls]
	if !ok {
		return inputs
	}
	out := map[string]any{}
	for k, v := range inputs {
		out[k] = v
	}
	for _, sec := range []map[string]any{node.Input.Required, node.Input.Optional} {
		for name, specAny := range sec {
			if _, set := out[name]; set {
				continue
			}
			spec, ok := specAny.([]any)
			if !ok || len(spec) == 0 {
				continue
			}
			s0 := spec[0]
			var def any
			if len(spec) > 1 {
				if m, ok := spec[1].(map[string]any); ok {
					def = m["default"]
				}
			}
			if enum, isEnum := s0.([]any); isEnum {
				if def != nil {
					if containsAny(enum, def) {
						out[name] = def
					} else if len(enum) > 0 {
						out[name] = enum[0]
					}
				} else if len(enum) > 0 {
					out[name] = enum[0]
				}
			} else {
				out[name] = def // scalar type: schema default (may be nil)
			}
		}
	}
	return out
}

func containsAny(list []any, v any) bool {
	for _, x := range list {
		if fmt.Sprint(x) == fmt.Sprint(v) {
			return true
		}
	}
	return false
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}
