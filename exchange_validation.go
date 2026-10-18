package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

const exchangeVideoUseCommit = "43cfc566833548093455c9d4a46bd69913b7e552"

type exchangeCanvas struct {
	Width  int     `json:"width"`
	Height int     `json:"height"`
	FPS    float64 `json:"fps"`
}

type exchangeEDL struct {
	SchemaVersion int               `json:"schemaVersion"`
	Canvas        exchangeCanvas    `json:"canvas"`
	Clips         []exchangeEDLClip `json:"clips"`
	Subtitles     struct {
		FontName string   `json:"fontName,omitempty"`
		FontSize *float64 `json:"fontSize,omitempty"`
		MarginV  *float64 `json:"marginV,omitempty"`
	} `json:"subtitles,omitempty"`
	Notes string `json:"notes,omitempty"`
}
type exchangeEDLClip struct {
	ID       string          `json:"id"`
	AssetID  string          `json:"assetId,omitempty"`
	Path     string          `json:"path"`
	Type     string          `json:"type"`
	Start    *float64        `json:"start"`
	Duration float64         `json:"duration"`
	SourceIn float64         `json:"sourceIn,omitempty"`
	Fit      string          `json:"fit,omitempty"`
	Note     string          `json:"note,omitempty"`
	TextRefs []TextReference `json:"textRefs,omitempty"`
}

func (v *exchangeEDLClip) UnmarshalJSON(raw []byte) error {
	type plain exchangeEDLClip
	var p plain
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if !utf8.Valid(raw) {
		return errors.New("EDL镜头必须使用有效UTF-8")
	}
	if err := d.Decode(&p); err != nil {
		return err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return err
	}
	if value, ok := fields["textRefs"]; ok && bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
		return errors.New("textRefs必须是引用数组，不能为null")
	}
	*v = exchangeEDLClip(p)
	return nil
}

// Validate independently of adapter status: successful JSON alone is not evidence
// that this run's declared inputs and archived edit were the ones rendered.
func validateProductionExchange(ctx context.Context, archive, inputs *os.Root, run ProductionRun, completion ExchangeCompletion, editHash string) error {
	if completion.Engine != "video_use" || completion.UpstreamCommit != exchangeVideoUseCommit {
		return errors.New("完成清单必须注明固定版本 video_use 适配器")
	}
	var manifest ExchangeManifest
	if err := exchangeJSON(inputs, "input/manifest.json", 4<<20, &manifest); err != nil {
		return errors.New("冻结输入清单无效")
	}
	if manifest.SchemaVersion != 1 || manifest.ProductionRunID != run.ID || manifest.ProjectID != completion.ProjectID || manifest.InputHash != run.InputHash || manifest.SourceHash != run.SourceHash || !finite(manifest.Narration.Duration) || math.Abs(manifest.Narration.Duration-run.Duration) > .001 {
		return errors.New("冻结输入清单与制作运行不一致")
	}
	var edl exchangeEDL
	if err := exchangeJSON(archive, "edit-ir.json", 4<<20, &edl); err != nil {
		return errors.New("EDL字段或结构无效")
	}
	source, err := readFrozenExchangeText(inputs, manifest, run.SourceHash)
	if err != nil {
		return err
	}
	anyTextRefs := false
	for _, clip := range edl.Clips {
		if err := validateTextReferences(source, clip.TextRefs); err != nil {
			return errors.New("EDL原文引用无效: " + err.Error())
		}
		if len(clip.TextRefs) > 0 {
			anyTextRefs = true
		}
	}
	c := edl.Canvas
	if edl.SchemaVersion != 1 || c.Width < 64 || c.Width > 3840 || c.Width%2 != 0 || c.Height < 64 || c.Height > 3840 || c.Height%2 != 0 || !finite(c.FPS) || c.FPS < 1 || c.FPS > 60 || len(edl.Clips) < 1 || len(edl.Clips) > 300 {
		return errors.New("EDL需要有效画布、帧率及1至300个镜头")
	}
	media := map[string]ExchangeMedia{}
	for _, item := range manifest.Media {
		if !exchangeRelative(item.Path) || media[item.Path].Path != "" || (item.Type != "image" && item.Type != "video") || item.AssetID == "" {
			return errors.New("冻结素材清单无效")
		}
		media[item.Path] = item
	}
	ids := map[string]bool{}
	durations := map[string]float64{}
	cursor := 0.0
	for _, clip := range edl.Clips {
		if len(clip.ID) < 1 || len(clip.ID) > 80 || strings.Trim(clip.ID, "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789_-") != "" || ids[clip.ID] {
			return errors.New("EDL镜头ID无效或重复")
		}
		ids[clip.ID] = true
		record, ok := media[clip.Path]
		if !ok || !exchangeRelative(clip.Path) || clip.Type != record.Type || (clip.AssetID != "" && clip.AssetID != record.AssetID) {
			return errors.New("EDL镜头必须引用冻结清单中匹配的素材")
		}
		if clip.Start == nil || !finite(*clip.Start) || *clip.Start < 0 || *clip.Start > 600 || !finite(clip.Duration) || clip.Duration < 1/c.FPS || clip.Duration > 600 || !finite(clip.SourceIn) || clip.SourceIn < 0 || clip.SourceIn > 36000 || math.Abs(*clip.Start-cursor) > .001 || (clip.Fit != "" && clip.Fit != "cover" && clip.Fit != "contain") || math.Round((cursor+clip.Duration)*c.FPS)-math.Round(cursor*c.FPS) < 1 {
			return errors.New("EDL镜头时序、源范围或布局无效")
		}
		if clip.Type == "image" && clip.SourceIn != 0 {
			return errors.New("EDL图片不能设置源入点")
		}
		if clip.Type == "video" {
			d, known := durations[clip.Path]
			if !known {
				var err error
				d, err = probeExchangeSourceDuration(ctx, inputs, record)
				if err != nil {
					return err
				}
				durations[clip.Path] = d
			}
			if clip.SourceIn+clip.Duration > d+1/c.FPS {
				return errors.New("EDL镜头超出真实视频长度")
			}
		}
		cursor += clip.Duration
	}
	if !finite(cursor) || math.Abs(cursor-run.Duration) > 1/c.FPS+.001 {
		return errors.New("EDL镜头必须连续覆盖完整实测配音")
	}
	style := edl.Subtitles
	if style.FontName != "" && (len(style.FontName) > 80 || strings.Trim(style.FontName, "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789 _-") != "") || style.FontSize != nil && (!finite(*style.FontSize) || *style.FontSize < 8 || *style.FontSize > 100) || style.MarginV != nil && (!finite(*style.MarginV) || *style.MarginV < 0 || *style.MarginV > 200) {
		return errors.New("EDL字幕样式无效")
	}
	var report map[string]any
	if err := exchangeJSON(archive, "validation.json", 4<<20, &report); err != nil || report == nil {
		return errors.New("适配器验证报告不是有效JSON对象")
	}
	for key, want := range map[string]any{"schemaVersion": float64(1), "engine": "video_use", "upstreamCommit": exchangeVideoUseCommit, "projectId": completion.ProjectID, "productionRunId": run.ID, "sourceHash": run.SourceHash, "inputHash": run.InputHash, "edlHash": editHash, "narrationSHA256": manifest.Narration.SHA256, "passed": true} {
		if !exchangeScalarEqual(report[key], want) {
			return errors.New("适配器验证报告未通过或未绑定当前输入与EDL")
		}
	}
	if anyTextRefs {
		version, ok := report["adapterVersion"].(float64)
		refs, refsOK := report["textReferences"].(map[string]any)
		if !ok || version < 2 || !refsOK || refs["sourceVerified"] != true {
			return errors.New("带原文引用的EDL需要第2版适配器及原文校验报告")
		}
	}
	checks, ok := report["checks"].([]any)
	if !ok || len(checks) == 0 {
		return errors.New("适配器验证报告缺少成功检查")
	}
	names := map[string]bool{}
	for _, item := range checks {
		row, ok := item.(map[string]any)
		if !ok {
			return errors.New("适配器验证检查无效")
		}
		name, _ := row["name"].(string)
		if name == "" || names[name] || row["ok"] != true {
			return errors.New("适配器验证检查失败、缺失或重复")
		}
		names[name] = true
	}
	for _, name := range []string{"dimensions", "videoCodec", "audioCodec", "duration", "fileNonempty", "frameRate"} {
		if !names[name] {
			return errors.New("适配器验证报告缺少必要检查")
		}
	}
	return nil
}

func probeExchangeSourceDuration(ctx context.Context, root *os.Root, record ExchangeMedia) (float64, error) {
	format, ext := "", ""
	switch record.MIMEType {
	case "video/mp4":
		format, ext = "mov", ".mp4"
	case "video/webm":
		format, ext = "matroska", ".webm"
	default:
		return 0, errors.New("EDL视频源必须为MP4或WebM")
	}
	source, err := exchangeOpen(root, record.Path, exchangeFileLimit)
	if err != nil {
		return 0, errors.New("EDL视频源不可读")
	}
	defer source.Close()
	// Media tools receive only private verified bytes. Never reopen a mutable
	// exchange path after its rooted descriptor has been checked.
	private, err := os.MkdirTemp("", "video-exchange-source-")
	if err != nil {
		return 0, err
	}
	defer os.RemoveAll(private)
	privateRoot, err := os.OpenRoot(private)
	if err != nil {
		return 0, err
	}
	defer privateRoot.Close()
	name := "source" + ext
	copied, err := exchangeCopy(source, privateRoot, name, exchangeFileLimit)
	if err != nil || copied.SHA256 != record.SHA256 || copied.Size != record.Size {
		return 0, errors.New("EDL视频源复制指纹与冻结输入不一致")
	}
	step, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(step, "ffprobe", "-v", "error", "-protocol_whitelist", "file,pipe", "-format_whitelist", format, "-f", format, "-enable_drefs", "0", "-select_streams", "v:0", "-show_entries", "stream=codec_type,duration:stream_tags=DURATION", "-of", "json", filepath.Join(private, name))
	cmd.Env = rendererEnvironment()
	configureProcess(cmd)
	b, err := cmd.Output()
	var info struct {
		Streams []struct {
			Type     string            `json:"codec_type"`
			Duration string            `json:"duration"`
			Tags     map[string]string `json:"tags"`
		} `json:"streams"`
	}
	if err != nil || json.Unmarshal(b, &info) != nil || len(info.Streams) != 1 || info.Streams[0].Type != "video" {
		return 0, errors.New("EDL视频源无法按声明格式探测")
	}
	stream := info.Streams[0]
	d, err := strconv.ParseFloat(stream.Duration, 64)
	if err != nil {
		parts := strings.Split(stream.Tags["DURATION"], ":")
		if len(parts) == 3 {
			hours, he := strconv.Atoi(parts[0])
			minutes, me := strconv.Atoi(parts[1])
			seconds, se := strconv.ParseFloat(parts[2], 64)
			if he == nil && me == nil && se == nil && hours >= 0 && minutes >= 0 && minutes < 60 && finite(seconds) && seconds >= 0 && seconds < 60 {
				d, err = float64(hours*3600+minutes*60)+seconds, nil
			}
		}
	}
	if err != nil || !finite(d) || d <= 0 || d > 36000 {
		return 0, errors.New("EDL视频源缺少可验证的画面轨时长")
	}
	return d, nil
}

func exchangeScalarEqual(got, want any) bool {
	switch value := want.(type) {
	case string:
		actual, ok := got.(string)
		return ok && actual == value
	case float64:
		actual, ok := got.(float64)
		return ok && actual == value
	case bool:
		actual, ok := got.(bool)
		return ok && actual == value
	}
	return false
}

func exchangeFrameRate(raw string) (float64, error) {
	parts := strings.Split(raw, "/")
	if len(parts) < 1 || len(parts) > 2 {
		return 0, errors.New("无效帧率")
	}
	numerator, err := strconv.ParseFloat(parts[0], 64)
	if err != nil || !finite(numerator) || numerator <= 0 {
		return 0, errors.New("无效帧率")
	}
	denominator := 1.0
	if len(parts) == 2 {
		denominator, err = strconv.ParseFloat(parts[1], 64)
		if err != nil || !finite(denominator) || denominator <= 0 {
			return 0, errors.New("无效帧率")
		}
	}
	return numerator / denominator, nil
}

// manifest.TextPath is already bound by the frozen manifest hash. Verify the
// exact narration bytes as well, before accepting any editorial quote metadata.
func readFrozenExchangeText(root *os.Root, manifest ExchangeManifest, sourceHash string) (string, error) {
	if !exchangeDigest.MatchString(sourceHash) || manifest.SourceHash != sourceHash || !exchangeRelative(manifest.TextPath) {
		return "", errors.New("冻结原文路径或指纹无效")
	}
	f, err := exchangeOpen(root, manifest.TextPath, 4<<20)
	if err != nil {
		return "", errors.New("冻结原文不可读")
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, (4<<20)+1))
	if err != nil || len(b) > 4<<20 || !utf8.Valid(b) || hashBytes(b) != sourceHash {
		return "", errors.New("冻结原文与主播稿指纹不一致")
	}
	return string(b), nil
}
