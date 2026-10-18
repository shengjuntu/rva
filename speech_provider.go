package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Settings are local administrator configuration. Requests/MCP cannot supply a
// URL, key, arbitrary reference file, command or Python code.
type SpeechConfig struct {
	Backend        string `json:"backend"`
	URL            string `json:"url"`
	Model          string `json:"model"`
	Voice          string `json:"voice"`
	Language       string `json:"language"`
	TimeoutSeconds int    `json:"timeoutSeconds"`
	APIKey         string `json:"apiKey,omitempty"`
}

var speechVoiceID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.+\-]{0,127}$`)

func defaultSpeechConfig() SpeechConfig {
	return SpeechConfig{Backend: "espeak-ng", Model: "IndexTeam/IndexTTS-2.5", Language: "zh", TimeoutSeconds: 180}
}

func validSpeechLanguage(s string) bool {
	switch s {
	case "zh", "en", "ja", "es", "ar", "zhen":
		return true
	}
	return false
}

func validateSpeechConfig(c SpeechConfig) error {
	if c.Backend != "espeak-ng" && c.Backend != "indextts" {
		return errors.New("配音后端必须为 espeak-ng 或 indextts")
	}
	if c.TimeoutSeconds < 10 || c.TimeoutSeconds > 900 {
		return errors.New("单段合成超时需在 10–900 秒之间")
	}
	if !validSpeechLanguage(c.Language) {
		return errors.New("IndexTTS 语言需为 zh/en/ja/es/ar/zhen")
	}
	if len(c.APIKey) > 4096 || strings.ContainsAny(c.APIKey, "\r\n") {
		return errors.New("配音服务 API Key 格式无效")
	}
	if c.Voice != "" && !speechVoiceID.MatchString(c.Voice) {
		return errors.New("音色 ID 需为 1–128 位字母、数字、下划线、点或连字符")
	}
	if c.URL != "" || c.Backend == "indextts" {
		u, e := url.Parse(c.URL)
		if e != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || !strings.HasSuffix(u.Path, "/v1/audio/speech") {
			return errors.New("服务地址必须是完整 HTTP(S) 地址，并以 /v1/audio/speech 结尾")
		}
	}
	if c.Backend == "indextts" && (strings.TrimSpace(c.Model) == "" || len(c.Model) > 200 || c.Voice == "") {
		return errors.New("请填写 IndexTTS 模型名和已注册的音色 ID")
	}
	return nil
}

func (a *App) loadSpeechConfig() error {
	a.tts = defaultSpeechConfig()
	b, e := os.ReadFile(filepath.Join(a.data, "speech-config.json"))
	if os.IsNotExist(e) {
		return nil
	}
	if e != nil {
		return e
	}
	if e = json.Unmarshal(b, &a.tts); e != nil {
		return fmt.Errorf("配音配置无法读取: %w", e)
	}
	return validateSpeechConfig(a.tts)
}

func (a *App) speechEnvironment() error {
	for _, v := range []struct {
		name string
		dest *string
	}{
		{"VIDEO_TTS_BACKEND", &a.tts.Backend}, {"VIDEO_TTS_URL", &a.tts.URL}, {"VIDEO_TTS_MODEL", &a.tts.Model}, {"VIDEO_TTS_VOICE", &a.tts.Voice}, {"VIDEO_TTS_LANGUAGE", &a.tts.Language}, {"VIDEO_TTS_API_KEY", &a.tts.APIKey},
	} {
		if value := os.Getenv(v.name); value != "" {
			*v.dest = strings.TrimSpace(value)
			a.ttsManaged = true
		}
	}
	if v := os.Getenv("VIDEO_TTS_TIMEOUT_SECONDS"); v != "" {
		n, e := strconv.Atoi(v)
		if e != nil {
			return errors.New("VIDEO_TTS_TIMEOUT_SECONDS 必须为整数")
		}
		a.tts.TimeoutSeconds = n
		a.ttsManaged = true
	}
	return validateSpeechConfig(a.tts)
}

func (a *App) speechConfig() SpeechConfig { a.mu.Lock(); defer a.mu.Unlock(); return a.tts }
func (a *App) publicSpeechConfig() any {
	c := a.tts
	hasKey := c.APIKey != ""
	c.APIKey = ""
	return struct {
		SpeechConfig
		HasAPIKey bool `json:"hasApiKey"`
		Managed   bool `json:"managedByEnvironment"`
	}{c, hasKey, a.ttsManaged}
}
func (a *App) speechSettings(w http.ResponseWriter, r *http.Request) {
	a.mu.Lock()
	defer a.mu.Unlock()
	writeJSON(w, 200, a.publicSpeechConfig())
}
func (a *App) saveSpeechSettings(w http.ResponseWriter, r *http.Request) {
	var in struct {
		SpeechConfig
		ClearAPIKey bool `json:"clearApiKey"`
	}
	if !decode(w, r, &in) {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.ttsManaged {
		fail(w, 409, errors.New("配音服务由环境变量管理，请修改启动配置并重启"))
		return
	}
	if len(a.speechJobs) != 0 {
		fail(w, 409, errors.New("请先等待或取消运行中的配音，再修改服务"))
		return
	}
	c := in.SpeechConfig
	c.URL = strings.TrimSpace(c.URL)
	c.Model = strings.TrimSpace(c.Model)
	c.Voice = strings.TrimSpace(c.Voice)
	if c.APIKey == "" && !in.ClearAPIKey {
		if c.URL != a.tts.URL && a.tts.APIKey != "" {
			fail(w, 400, errors.New("更换服务地址时请重新填写 API Key 或勾选清除，防止旧密钥发送到新服务"))
			return
		}
		c.APIKey = a.tts.APIKey
	}
	if in.ClearAPIKey {
		c.APIKey = ""
	}
	if e := validateSpeechConfig(c); e != nil {
		fail(w, 400, e)
		return
	}
	if e := atomicJSON(filepath.Join(a.data, "speech-config.json"), c); e != nil {
		fail(w, 500, e)
		return
	}
	a.tts = c
	writeJSON(w, 200, a.publicSpeechConfig())
}

func speechBackend(s string) string {
	if s == "" {
		return "espeak-ng"
	}
	return s
}
func (in *SpeechRequest) normalize(c SpeechConfig) error {
	if in.Backend == "" {
		in.Backend = c.Backend
	}
	if !identifier.MatchString(in.RequestID) {
		return errors.New("需要 24 位十六进制 requestId")
	}
	switch in.Backend {
	case "espeak-ng":
		if in.Voice == "" {
			in.Voice = "cmn"
		}
		if in.Rate == 0 {
			in.Rate = 170
		}
		if !speechVoiceID.MatchString(in.Voice) || in.Rate < 80 || in.Rate > 300 || in.Speed != 0 || in.Language != "" {
			return errors.New("eSpeak 需要合法 voice、80–300 rate；不接受 speed/language")
		}
	case "indextts":
		if in.Voice == "" {
			in.Voice = c.Voice
		}
		if in.Language == "" {
			in.Language = c.Language
		}
		if in.Speed == 0 {
			in.Speed = 1
		}
		if !speechVoiceID.MatchString(in.Voice) || !validSpeechLanguage(in.Language) || math.IsNaN(in.Speed) || math.IsInf(in.Speed, 0) || in.Speed < .5 || in.Speed > 2 || in.Rate != 0 {
			return errors.New("IndexTTS 需要已注册音色、有效语言、0.5–2.0 倍 speed；不接受词/分钟 rate")
		}
	default:
		return errors.New("未知配音后端")
	}
	return nil
}

func speechHTTPClient(timeout time.Duration) *http.Client {
	return &http.Client{Timeout: timeout, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}

// Models is a non-synthesizing connectivity check, not an audio quality test.
func probeIndexTTS(ctx context.Context, c SpeechConfig) (string, error) {
	if c.URL == "" {
		return "", errors.New("请先配置 IndexTTS 服务")
	}
	endpoint := strings.TrimSuffix(c.URL, "/audio/speech") + "/models"
	req, e := http.NewRequestWithContext(ctx, "GET", endpoint, nil)
	if e != nil {
		return "", errors.New("IndexTTS 地址无效")
	}
	if c.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.APIKey)
	}
	resp, e := speechHTTPClient(8 * time.Second).Do(req)
	if e != nil {
		return "", errors.New("无法连接 IndexTTS：检查地址、端口、网络与 TLS 证书")
	}
	defer resp.Body.Close()
	if resp.StatusCode == 404 || resp.StatusCode == 405 {
		return "unknown", errors.New("服务未提供 /v1/models，无法自动确认；可生成短句验证 /v1/audio/speech")
	}
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("IndexTTS 环境检查 HTTP %d（检查认证和服务状态）", resp.StatusCode)
	}
	var models struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if e = json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&models); e != nil {
		return "", errors.New("服务没有返回有效模型列表；请勿填写 Gradio 页面地址")
	}
	for _, m := range models.Data {
		if m.ID == c.Model {
			return "ready", nil
		}
	}
	return "", errors.New("服务模型列表中没有配置的模型名，请核对 model")
}

func synthesizeIndexTTS(ctx context.Context, c SpeechConfig, job SpeechJob, text, output string) error {
	body, _ := json.Marshal(map[string]any{"model": job.Model, "input": text, "voice": job.Voice, "speed": job.Speed, "response_format": "wav", "extra_params": map[string]any{"lang": job.Language, "text_normalization": true}})
	step, cancel := context.WithTimeout(ctx, time.Duration(c.TimeoutSeconds)*time.Second)
	defer cancel()
	req, e := http.NewRequestWithContext(step, "POST", c.URL, bytes.NewReader(body))
	if e != nil {
		return errors.New("IndexTTS 地址无效")
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "audio/wav")
	if c.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.APIKey)
	}
	resp, e := speechHTTPClient(time.Duration(c.TimeoutSeconds) * time.Second).Do(req)
	if e != nil {
		if step.Err() != nil {
			return fmt.Errorf("IndexTTS 请求取消或超时: %w", step.Err())
		}
		return errors.New("IndexTTS 请求失败：检查服务地址、网络和 TLS 证书")
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		// Remote response bodies may echo secrets, text or filesystem paths.
		hint := "检查服务日志"
		switch resp.StatusCode {
		case 401, 403:
			hint = "检查 API Key"
		case 400, 422:
			hint = "检查模型名、已注册音色、语言和语音协议"
		case 404:
			hint = "检查 /v1/audio/speech 接口；Gradio 页面不能直接当作语音接口"
		case 429, 503:
			hint = "服务忙，请稍后手动重试"
		}
		return fmt.Errorf("IndexTTS HTTP %d：%s", resp.StatusCode, hint)
	}
	const maxAudioBytes = 32 << 20
	if resp.ContentLength > maxAudioBytes {
		return errors.New("IndexTTS 单段响应超过 32 MiB")
	}
	f, e := os.OpenFile(output, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if e != nil {
		return e
	}
	defer f.Close()
	var header [12]byte
	if _, e = io.ReadFull(resp.Body, header[:]); e != nil {
		return errors.New("IndexTTS 返回的 WAV 不完整")
	}
	if string(header[:4]) != "RIFF" || string(header[8:]) != "WAVE" {
		return errors.New("IndexTTS 必须直接返回 WAV 音频；不支持 JSON 文件路径、MP3 或 Gradio 响应")
	}
	if _, e = f.Write(header[:]); e != nil {
		return e
	}
	n, e := io.Copy(f, io.LimitReader(resp.Body, maxAudioBytes-12+1))
	if e != nil {
		return errors.New("IndexTTS 音频下载中断")
	}
	if n+12 > maxAudioBytes {
		return errors.New("IndexTTS 单段响应超过 32 MiB")
	}
	return f.Sync()
}
