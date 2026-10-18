package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"
)

func mcpMain(args []string) {
	flags := flag.NewFlagSet("mcp", flag.ExitOnError)
	base := flags.String("url", "http://127.0.0.1:3220", "video app URL")
	_ = flags.Parse(args)
	client := &http.Client{Timeout: 30 * time.Second, CheckRedirect: func(r *http.Request, v []*http.Request) error { return http.ErrUseLastResponse }}
	runMCP(os.Stdin, os.Stdout, *base, os.Getenv("VIDEO_APP_TOKEN"), client)
}
func runMCP(input io.Reader, output io.Writer, base, token string, client *http.Client) {
	scanner := bufio.NewScanner(input)
	scanner.Buffer(make([]byte, 4096), 2<<20)
	send := func(id json.RawMessage, result any) {
		_ = json.NewEncoder(output).Encode(map[string]any{"jsonrpc": "2.0", "id": id, "result": result})
	}
	for scanner.Scan() {
		var msg struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		if json.Unmarshal(scanner.Bytes(), &msg) != nil {
			continue
		}
		if len(msg.ID) == 0 {
			continue
		}
		switch msg.Method {
		case "initialize":
			send(msg.ID, map[string]any{"protocolVersion": mcpProtocol(msg.Params), "capabilities": map[string]any{"tools": map[string]any{}}, "serverInfo": map[string]string{"name": "video-project", "version": version}})
		case "ping":
			send(msg.ID, map[string]any{})
		case "tools/list":
			tools := []any{}
			for _, t := range []struct{ Name, Description string }{{"video_narration_update", "Save free narration text independently of shots and editing DSL. Requires revision. No automatic content rewriting."}, {"video_speech_materialize", "Import completed independent narration as an audio asset and return measured cues and editing anchors. Requires revision and unchanged narration. Does not overwrite shots or DSL; timings are synthesis chunk boundaries, not forced word alignment."}, {"video_edit_get", "Read stored editing DSL, activation mode, revision and compiled timeline IR. Content remains in referenced assets; no paragraph projection required."}, {"video_edit_validate", "Validate and compile a proposed editing DSL without saving or rendering. Returns stable clip IDs and resolved times; unknown fields and cyclic references are rejected."}, {"video_edit_update", "Save a version-checked editing DSL and activate it. Supports video/audio/text tracks, source in/out/speed, layout, fades and time references. Subsequent rendering uses FFmpeg editing runtime, not legacy shots/audio/subtitles."}, {"video_edit_activate", "Activate saved DSL or switch to the legacy shot renderer without deleting either representation. Requires current revision."}, {"video_capabilities_get", "Discover implemented capabilities, dependency readiness and configured providers. probe=true optionally checks the configured IndexTTS model list, never synthesizes. No secrets are returned."}, {"video_project_create", "Create a named project. Returns a project ID; do not infer that a RunDesk session or renderer was configured."}, {"video_asset_list", "List actual project assets, authenticated read URLs, MIME types and sizes."}, {"video_asset_import", "Import one small file from standard Base64, maximum decoded 1 MiB. Requires a fresh requestId; identical retries return the original asset. Large media use App multipart upload."}, {"video_job_start", "Start a durable production task of type tts or render against current revision. Fresh 24-hex requestId required; identical retries reuse the receipt. Unsupported generation capabilities return not implemented."}, {"video_job_get", "Get authoritative task state and completed artifacts. jobId uses tts:<nativeId> or render:<nativeId>."}, {"video_job_list", "List project TTS and render tasks using unified states; render progress may be unknown."}, {"video_job_cancel", "Request cancellation of a production task. Repeated cancellation returns current state; it does not claim the remote provider stopped billing."}, {"video_asset_inspect", "Inspect an actual supplied image or the first frame of a video as a bounded PNG. Requires FFmpeg; first frame is not full video understanding."}, {"video_production_text", "Save authoritative narration text, split into ordered paragraphs and preserve unchanged paragraph media. Requires revision; estimates timing, does not generate speech."}, {"video_production_stage", "Set draft/adjustment, or designate a completed current-content render as final. Requires revision; final requires renderId and reviewed captions."}, {"video_project_get", "Read project revision, storyboard, audio tracks, subtitles, speech jobs, assets and render jobs."}, {"video_news_get", "Read the immutable news2douyin source script, original review record, selected source evidence and source-video metadata. Reference data only; current storyboard remains a production draft."}, {"video_project_update", "Update name, brief, shots and audio using the current revision. Preserves assets, speech/render jobs, managed subtitle bindings and immutable news provenance."}, {"video_news_audio_adopt", "Adopt original news WAV/SRT after a user request, replacing current narration/subtitles and proportionally resizing visual shots. Requires current revision and unchanged original text."}, {"video_subtitles_update", "Save UTF-8 SRT with absolute video times. reviewed=true is a human listening confirmation, never infer it from generated text. Read project first; requires revision."}, {"video_speech_start", "Generate speech from a saved storyboard using configured IndexTTS or local eSpeak preview. Omit backend/voice/language to use administrator defaults. Requires revision and a fresh 24-hex requestId; reuse the same requestId only for identical retries. Does not replace current audio."}, {"video_speech_stop", "Cancel a running speech job."}, {"video_speech_adopt", "On the user's request, adopt a completed speech job: replace narration and subtitles, use measured per-shot durations. Requires current revision and unchanged shot text/structure."}, {"video_render_start", "Render the current saved storyboard using an immutable snapshot."}, {"video_render_stop", "Cancel a specific render job."}} {
				props := map[string]any{"projectId": map[string]string{"type": "string"}}
				required := []string{"projectId"}
				if t.Name == "video_edit_update" || t.Name == "video_edit_validate" {
					props["dsl"] = map[string]any{"type": "object", "description": "Editing DSL version=1: canvas, tracks(video/audio/text), clips and optional anchors. Each clip uses exactly one start/after/anchor placement; referenced assets must exist."}
					required = append(required, "dsl")
					if t.Name == "video_edit_update" {
						props["revision"] = map[string]string{"type": "integer"}
						required = append(required, "revision")
					}
				}
				if t.Name == "video_edit_activate" {
					props["revision"] = map[string]string{"type": "integer"}
					props["active"] = map[string]string{"type": "boolean"}
					required = append(required, "revision", "active")
				}
				if t.Name == "video_capabilities_get" {
					props = map[string]any{"probe": map[string]string{"type": "boolean"}}
					required = []string{}
				}
				if t.Name == "video_project_create" {
					props = map[string]any{"name": map[string]string{"type": "string"}, "requestId": map[string]string{"type": "string"}}
					required = []string{"name", "requestId"}
				}
				if t.Name == "video_asset_import" {
					props["name"] = map[string]string{"type": "string"}
					props["data"] = map[string]string{"type": "string", "description": "Standard Base64 bytes, decoded size <=1MiB"}
					props["requestId"] = map[string]string{"type": "string"}
					required = append(required, "name", "data", "requestId")
				}
				if t.Name == "video_job_start" {
					props["type"] = map[string]any{"type": "string", "enum": []string{"tts", "render"}}
					props["revision"] = map[string]string{"type": "integer"}
					props["requestId"] = map[string]string{"type": "string"}
					props["options"] = map[string]any{"type": "object", "properties": map[string]any{"backend": map[string]any{"type": "string", "enum": []string{"indextts", "espeak-ng"}}, "voice": map[string]string{"type": "string"}, "language": map[string]string{"type": "string"}, "speed": map[string]string{"type": "number"}, "rate": map[string]string{"type": "integer"}, "kind": map[string]any{"type": "string", "enum": []string{"draft"}}}, "additionalProperties": false}
					required = append(required, "type", "revision", "requestId")
				}
				if t.Name == "video_job_get" || t.Name == "video_job_cancel" {
					props["jobId"] = map[string]string{"type": "string"}
					required = append(required, "jobId")
				}
				if t.Name == "video_asset_inspect" {
					props["assetId"] = map[string]string{"type": "string"}
					required = append(required, "assetId")
				}
				if t.Name == "video_production_text" || t.Name == "video_production_stage" {
					props["revision"] = map[string]string{"type": "integer"}
					required = append(required, "revision")
					if t.Name == "video_production_text" {
						props["text"] = map[string]string{"type": "string"}
						required = append(required, "text")
					} else {
						props["stage"] = map[string]any{"type": "string", "enum": []string{"draft", "adjustment", "final"}}
						props["renderId"] = map[string]string{"type": "string"}
						required = append(required, "stage")
					}
				}
				if t.Name == "video_narration_update" || t.Name == "video_speech_materialize" {
					props["revision"] = map[string]string{"type": "integer"}
					required = append(required, "revision")
					if t.Name == "video_narration_update" {
						props["text"] = map[string]string{"type": "string"}
						required = append(required, "text")
					} else {
						props["speechId"] = map[string]string{"type": "string"}
						required = append(required, "speechId")
					}
				}
				if t.Name == "video_render_start" {
					props["revision"] = map[string]string{"type": "integer"}
					props["requestId"] = map[string]string{"type": "string"}
					props["kind"] = map[string]any{"type": "string", "enum": []string{"draft"}, "description": "draft permits unreviewed caption previews but never stale bindings; omitted requires ready captions."}
				}
				if t.Name == "video_project_update" {
					props["project"] = map[string]any{"type": "object", "description": "Current project with revision, name, brief, shots, optional audio. audio.narration and audio.music: {assetId, volume:0..2, start:seconds, sourceStart:seconds, loop:boolean}. Only music supports loop. Omitted audio preserves current audio; audio:{} clears it. Read before updating."}
					required = append(required, "project")
				}
				if t.Name == "video_news_audio_adopt" || t.Name == "video_subtitles_update" || t.Name == "video_speech_start" || t.Name == "video_speech_adopt" {
					props["revision"] = map[string]string{"type": "integer"}
					required = append(required, "revision")
				}
				if t.Name == "video_subtitles_update" {
					props["srt"] = map[string]string{"type": "string"}
					props["enabled"] = map[string]string{"type": "boolean"}
					props["reviewed"] = map[string]string{"type": "boolean", "description": "True only when the user has explicitly confirmed listening to this narration and checking this exact subtitle text/timing. False for generated drafts."}
					required = append(required, "srt", "enabled", "reviewed")
				}
				if t.Name == "video_speech_start" {
					props["source"] = map[string]any{"type": "string", "enum": []string{"narration", "shots"}}
					props["requestId"] = map[string]string{"type": "string"}
					props["voice"] = map[string]string{"type": "string", "description": "Registered IndexTTS voice ID or eSpeak voice name. Omit to use administrator default."}
					props["rate"] = map[string]any{"type": "integer", "minimum": 80, "maximum": 300, "description": "eSpeak only: approximate words per minute; default 170. Omit for IndexTTS."}
					props["backend"] = map[string]any{"type": "string", "enum": []string{"indextts", "espeak-ng"}, "description": "Omit for configured default."}
					props["language"] = map[string]any{"type": "string", "enum": []string{"zh", "en", "ja", "es", "ar", "zhen"}, "description": "IndexTTS only: script language, not translation."}
					props["speed"] = map[string]any{"type": "number", "minimum": 0.5, "maximum": 2, "description": "IndexTTS only: 1 = normal, greater = faster."}
					required = append(required, "requestId")
				}
				if t.Name == "video_speech_stop" || t.Name == "video_speech_adopt" {
					props["speechId"] = map[string]string{"type": "string"}
					required = append(required, "speechId")
				}
				if t.Name == "video_render_stop" {
					props["renderId"] = map[string]string{"type": "string"}
					required = append(required, "renderId")
				}
				tools = append(tools, map[string]any{"name": t.Name, "description": t.Description, "inputSchema": map[string]any{"type": "object", "properties": props, "required": required, "additionalProperties": false}})
			}
			send(msg.ID, map[string]any{"tools": tools})
		case "tools/call":
			var p struct {
				Name      string `json:"name"`
				Arguments struct {
					Source    string          `json:"source"`
					DSL       *EditDocument   `json:"dsl"`
					Active    bool            `json:"active"`
					Name      string          `json:"name"`
					Data      string          `json:"data"`
					Probe     bool            `json:"probe"`
					Type      string          `json:"type"`
					JobID     string          `json:"jobId"`
					Options   JobOptions      `json:"options"`
					AssetID   string          `json:"assetId"`
					Text      string          `json:"text"`
					Stage     string          `json:"stage"`
					Kind      string          `json:"kind"`
					ProjectID string          `json:"projectId"`
					Revision  int             `json:"revision"`
					RequestID string          `json:"requestId"`
					Backend   string          `json:"backend"`
					Language  string          `json:"language"`
					Speed     float64         `json:"speed"`
					Voice     string          `json:"voice"`
					Rate      int             `json:"rate"`
					SpeechID  string          `json:"speechId"`
					SRT       string          `json:"srt"`
					Enabled   bool            `json:"enabled"`
					Reviewed  bool            `json:"reviewed"`
					RenderID  string          `json:"renderId"`
					Project   json.RawMessage `json:"project"`
				} `json:"arguments"`
			}
			e := json.Unmarshal(msg.Params, &p)
			if e == nil {
				e = validateFoundationArguments(p.Name, msg.Params)
			}
			path := "/api/projects/" + p.Arguments.ProjectID
			method := "GET"
			var body []byte
			if p.Name != "video_capabilities_get" && p.Name != "video_project_create" && !identifier.MatchString(p.Arguments.ProjectID) {
				e = fmt.Errorf("invalid projectId")
			}
			switch p.Name {
			case "video_narration_update":
				method = "PUT"
				path += "/narration"
				body, _ = json.Marshal(map[string]any{"revision": p.Arguments.Revision, "text": p.Arguments.Text})
			case "video_speech_materialize":
				method = "POST"
				path += "/speech/" + p.Arguments.SpeechID + "/materialize"
				body, _ = json.Marshal(map[string]any{"revision": p.Arguments.Revision})
			case "video_edit_get":
				path += "/edit"
			case "video_edit_update", "video_edit_validate":
				method = "PUT"
				path += "/edit"
				if p.Name == "video_edit_validate" {
					method = "POST"
					path += "/validate"
				}
				body, _ = json.Marshal(EditRequest{Revision: p.Arguments.Revision, DSL: p.Arguments.DSL})
			case "video_edit_activate":
				method = "POST"
				path += "/edit/active"
				body, _ = json.Marshal(map[string]any{"revision": p.Arguments.Revision, "active": p.Arguments.Active})
			case "video_capabilities_get":
				path = "/api/capabilities"
				if p.Arguments.Probe {
					path += "?probe=1"
				}
			case "video_project_create":
				method = "POST"
				path = "/api/projects"
				body, _ = json.Marshal(map[string]string{"name": p.Arguments.Name, "requestId": p.Arguments.RequestID})
			case "video_asset_list":
				path += "/assets"
			case "video_asset_import":
				method = "POST"
				path += "/assets/import"
				body, _ = json.Marshal(AssetImportRequest{Name: p.Arguments.Name, Data: p.Arguments.Data, RequestID: p.Arguments.RequestID})
			case "video_job_list":
				path += "/jobs"
			case "video_job_start":
				method = "POST"
				path += "/jobs"
				body, _ = json.Marshal(JobStartRequest{Type: p.Arguments.Type, Revision: p.Arguments.Revision, RequestID: p.Arguments.RequestID, Options: p.Arguments.Options})
			case "video_job_get", "video_job_cancel":
				if !validJobID(p.Arguments.JobID) {
					e = fmt.Errorf("invalid jobId")
				}
				path += "/jobs/" + p.Arguments.JobID
				if p.Name == "video_job_cancel" {
					method = "POST"
					path += "/cancel"
					body = []byte("{}")
				}
			case "video_asset_inspect":
				if !identifier.MatchString(p.Arguments.AssetID) {
					e = fmt.Errorf("invalid assetId")
				}
				path += "/assets/" + p.Arguments.AssetID + "/preview"
			case "video_production_text":
				method = "PUT"
				path += "/production/text"
				body, _ = json.Marshal(ProductionRequest{Revision: p.Arguments.Revision, Text: p.Arguments.Text})
			case "video_production_stage":
				method = "POST"
				path += "/production/stage"
				body, _ = json.Marshal(ProductionRequest{Revision: p.Arguments.Revision, Stage: p.Arguments.Stage, RenderID: p.Arguments.RenderID})
			case "video_project_get":
			case "video_news_get":
				path += "/news"
			case "video_project_update":
				method = "PUT"
				body = p.Arguments.Project
			case "video_news_audio_adopt":
				method = "POST"
				path += "/news/audio"
				body, _ = json.Marshal(map[string]any{"revision": p.Arguments.Revision})
			case "video_subtitles_update":
				method = "PUT"
				path += "/subtitles"
				body, _ = json.Marshal(map[string]any{"revision": p.Arguments.Revision, "srt": p.Arguments.SRT, "enabled": p.Arguments.Enabled, "reviewed": p.Arguments.Reviewed})
			case "video_speech_start":
				method = "POST"
				path += "/speech"
				body, _ = json.Marshal(SpeechRequest{Source: p.Arguments.Source, Backend: p.Arguments.Backend, Language: p.Arguments.Language, Speed: p.Arguments.Speed, Revision: p.Arguments.Revision, RequestID: p.Arguments.RequestID, Voice: p.Arguments.Voice, Rate: p.Arguments.Rate})
			case "video_speech_stop", "video_speech_adopt":
				method = "POST"
				if !identifier.MatchString(p.Arguments.SpeechID) {
					e = fmt.Errorf("invalid speechId")
				}
				action := "stop"
				if p.Name == "video_speech_adopt" {
					action = "adopt"
				}
				path += "/speech/" + p.Arguments.SpeechID + "/" + action
				body, _ = json.Marshal(map[string]any{"revision": p.Arguments.Revision})
			case "video_render_start":
				method = "POST"
				path += "/render"
				body, _ = json.Marshal(RenderRequest{Kind: p.Arguments.Kind, Revision: p.Arguments.Revision, RequestID: p.Arguments.RequestID})
			case "video_render_stop":
				method = "POST"
				if !identifier.MatchString(p.Arguments.RenderID) {
					e = fmt.Errorf("invalid renderId")
				}
				path += "/renders/" + p.Arguments.RenderID + "/stop"
				body = []byte("{}")
			default:
				e = fmt.Errorf("unknown tool")
			}
			var raw []byte
			if e == nil {
				req, err := http.NewRequest(method, base+path, bytes.NewReader(body))
				e = err
				if e == nil {
					req.Header.Set("Authorization", "Bearer "+token)
					req.Header.Set("Content-Type", "application/json")
					resp, err := client.Do(req)
					e = err
					if e == nil {
						raw, e = io.ReadAll(io.LimitReader(resp.Body, (16<<20)+1))
						resp.Body.Close()
						if len(raw) > 16<<20 {
							e = fmt.Errorf("tool response exceeds 16 MiB; inspect source files in the App")
						}
						if resp.StatusCode >= 300 {
							e = fmt.Errorf("%s", raw)
						}
					}
				}
			}
			if e != nil {
				send(msg.ID, map[string]any{"isError": true, "content": []any{map[string]string{"type": "text", "text": e.Error()}}})
			} else if p.Name == "video_asset_inspect" {
				var preview map[string]any
				if err := json.Unmarshal(raw, &preview); err != nil {
					send(msg.ID, map[string]any{"isError": true, "content": []any{map[string]string{"type": "text", "text": "Invalid preview response"}}})
				} else {
					send(msg.ID, preview)
				}
			} else {
				send(msg.ID, map[string]any{"content": []any{map[string]string{"type": "text", "text": string(raw)}}})
			}
		default:
			_ = json.NewEncoder(output).Encode(map[string]any{"jsonrpc": "2.0", "id": msg.ID, "error": map[string]any{"code": -32601, "message": "Method not found"}})
		}
	}
}

func mcpProtocol(raw json.RawMessage) string {
	var v struct {
		Version string `json:"protocolVersion"`
	}
	_ = json.Unmarshal(raw, &v)
	switch v.Version {
	case "2024-11-05", "2025-03-26", "2025-06-18", "2025-11-25":
		return v.Version
	}
	return "2025-03-26"
}
