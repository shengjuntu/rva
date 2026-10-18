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
			for _, t := range []struct{ Name, Description string }{{"video_project_get", "Read project revision, storyboard, audio tracks, assets and render jobs."}, {"video_project_update", "Update name, brief, shots and audio using the current revision. Preserves assets and jobs."}, {"video_render_start", "Render the current saved storyboard using an immutable snapshot."}, {"video_render_stop", "Cancel a specific render job."}} {
				props := map[string]any{"projectId": map[string]string{"type": "string"}}
				required := []string{"projectId"}
				if t.Name == "video_project_update" {
					props["project"] = map[string]any{"type": "object", "description": "Current project with revision, name, brief, shots, optional audio. audio.narration and audio.music: {assetId, volume:0..2, start:seconds, sourceStart:seconds, loop:boolean}. Only music supports loop. Omitted audio preserves current audio; audio:{} clears it. Read before updating."}
					required = append(required, "project")
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
			case "video_project_update":
				method = "PUT"
				body = p.Arguments.Project
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
						raw, e = io.ReadAll(io.LimitReader(resp.Body, 4<<20))
						resp.Body.Close()
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
