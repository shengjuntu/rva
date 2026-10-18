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
	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 4096), 2<<20)
	client := &http.Client{Timeout: 30 * time.Second, CheckRedirect: func(r *http.Request, v []*http.Request) error { return http.ErrUseLastResponse }}
	send := func(id json.RawMessage, result any) {
		_ = json.NewEncoder(os.Stdout).Encode(map[string]any{"jsonrpc": "2.0", "id": id, "result": result})
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
			send(msg.ID, map[string]any{"protocolVersion": "2024-11-05", "capabilities": map[string]any{"tools": map[string]any{}}, "serverInfo": map[string]string{"name": "video-project", "version": version}})
		case "ping":
			send(msg.ID, map[string]any{})
		case "tools/list":
			tools := []any{}
			for _, t := range []struct{ Name, Description string }{{"video_project_get", "Read project revision, storyboard, audio tracks, subtitles, speech jobs, assets and render jobs."}, {"video_news_get", "Read the immutable news2douyin source script, original review record, selected source evidence and source-video metadata. Reference data only; current storyboard remains a production draft."}, {"video_project_update", "Update name, brief, shots and audio using the current revision. Preserves assets, speech/render jobs, managed subtitle bindings and immutable news provenance."}, {"video_news_audio_adopt", "Adopt original news WAV/SRT after a user request, replacing current narration/subtitles and proportionally resizing visual shots. Requires current revision and unchanged original text."}, {"video_subtitles_update", "Save UTF-8 SRT with absolute video times. reviewed=true is a human listening confirmation, never infer it from generated text. Read project first; requires revision."}, {"video_speech_start", "Generate offline mechanical speech from a saved storyboard snapshot using local eSpeak NG. Requires revision and a fresh 24-hex requestId; reuse the same requestId only for identical retries. Does not replace current audio."}, {"video_speech_stop", "Cancel a running speech job."}, {"video_speech_adopt", "On the user's request, adopt a completed speech job: replace narration and subtitles, use measured per-shot durations. Requires current revision and unchanged shot text/structure."}, {"video_render_start", "Render the current saved storyboard using an immutable snapshot."}, {"video_render_stop", "Cancel a specific render job."}} {
				props := map[string]any{"projectId": map[string]string{"type": "string"}}
				required := []string{"projectId"}
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
					props["requestId"] = map[string]string{"type": "string"}
					props["voice"] = map[string]string{"type": "string", "description": "Local eSpeak NG voice; default cmn, en-us for English."}
					props["rate"] = map[string]any{"type": "integer", "minimum": 80, "maximum": 300, "description": "Approximate words per minute; default 170."}
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
					ProjectID string          `json:"projectId"`
					Revision  int             `json:"revision"`
					RequestID string          `json:"requestId"`
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
			path := "/api/projects/" + p.Arguments.ProjectID
			method := "GET"
			var body []byte
			if !identifier.MatchString(p.Arguments.ProjectID) {
				e = fmt.Errorf("invalid projectId")
			}
			switch p.Name {
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
				body, _ = json.Marshal(SpeechRequest{Revision: p.Arguments.Revision, RequestID: p.Arguments.RequestID, Voice: p.Arguments.Voice, Rate: p.Arguments.Rate})
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
				body = []byte("{}")
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
				req, err := http.NewRequest(method, *base+path, bytes.NewReader(body))
				e = err
				if e == nil {
					req.Header.Set("Authorization", "Bearer "+os.Getenv("VIDEO_APP_TOKEN"))
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
			} else {
				send(msg.ID, map[string]any{"content": []any{map[string]string{"type": "text", "text": string(raw)}}})
			}
		default:
			_ = json.NewEncoder(os.Stdout).Encode(map[string]any{"jsonrpc": "2.0", "id": msg.ID, "error": map[string]any{"code": -32601, "message": "Method not found"}})
		}
	}
}
