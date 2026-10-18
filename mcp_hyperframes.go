package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
)

func hyperFramesDocumentSchema() map[string]any {
	str := map[string]any{"type": "string"}
	number := map[string]any{"type": "number"}
	object := func(properties map[string]any, required ...string) map[string]any {
		return map[string]any{"type": "object", "properties": properties, "required": required, "additionalProperties": false}
	}
	array := func(items any) map[string]any { return map[string]any{"type": "array", "items": items} }
	ref := object(map[string]any{"quote": str, "occurrence": map[string]any{"type": "integer", "minimum": 1}}, "quote")
	canvas := object(map[string]any{"width": map[string]any{"type": "integer", "minimum": 16, "maximum": 4096}, "height": map[string]any{"type": "integer", "minimum": 16, "maximum": 4096}, "fps": map[string]any{"type": "integer", "minimum": 1, "maximum": 60}, "duration": number}, "width", "height", "fps", "duration")
	files := array(object(map[string]any{"path": str, "content": str}, "path", "content"))
	assets := array(object(map[string]any{"assetId": str, "path": str, "sha256": str, "size": map[string]any{"type": "integer", "minimum": 0}}, "assetId", "path"))
	clips := array(object(map[string]any{"id": str, "track": str, "kind": str, "label": str, "start": number, "duration": number, "assetId": str, "textRefs": array(ref)}, "id", "track", "start", "duration"))
	return object(map[string]any{"schemaVersion": map[string]any{"type": "integer", "enum": []int{1}}, "entrypoint": map[string]any{"type": "string", "enum": []string{"index.html"}}, "canvas": canvas, "hasAudio": map[string]any{"type": "boolean"}, "files": files, "assets": assets, "textSourceHash": str, "timeline": clips}, "schemaVersion", "entrypoint", "canvas", "hasAudio", "files", "assets", "textSourceHash")
}
func hyperFramesMCPTools() []any {
	tools := []any{}
	for _, t := range []struct {
		name, description string
		write             bool
	}{
		{"video_hyperframes_get", "Read the saved free HTML/CSS/JavaScript HyperFrames composition, asset bindings, source-text hash and currentness. Never executes composition source.", false},
		{"video_hyperframes_update", "Save a revision-checked, freely authored HyperFrames project. Supply complete local source files, real asset bindings, explicit hasAudio and exact narration textSourceHash. Optional timeline is Agent-authored review metadata, not measured video evidence. Does not render or change legacy edits.", true},
		{"video_hyperframes_check", "Freeze and check the current HyperFrames composition using the configured renderer. Requires revision. Returns actual check outcome and limitations; successful check is not a rendered video. Render through video_job_start(type=render) or video_render_start.", true},
		{"video_review_timeline_get", "Read a readonly multitrack review timeline and exact saved narration. Optional renderId reads that owned completed render's integrity-checked historical snapshot; no current-text substitution. Source marks authored annotations versus compiled timing.", false},
	} {
		props := map[string]any{"projectId": map[string]any{"type": "string"}}
		required := []string{"projectId"}
		if t.write {
			props["revision"] = map[string]any{"type": "integer", "minimum": 1}
			required = append(required, "revision")
		}
		if t.name == "video_hyperframes_update" {
			props["document"] = hyperFramesDocumentSchema()
			required = append(required, "document")
		}
		if t.name == "video_review_timeline_get" {
			props["renderId"] = map[string]any{"type": "string"}
		}
		tools = append(tools, map[string]any{"name": t.name, "description": t.description, "inputSchema": map[string]any{"type": "object", "properties": props, "required": required, "additionalProperties": false}, "annotations": map[string]bool{"readOnlyHint": !t.write, "destructiveHint": false, "idempotentHint": !t.write, "openWorldHint": t.name == "video_hyperframes_check"}})
	}
	return tools
}
func callHyperFramesMCP(params json.RawMessage, base, token string, client *http.Client) (any, bool) {
	var envelope struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	}
	if json.Unmarshal(params, &envelope) != nil {
		return nil, false
	}
	switch envelope.Name {
	case "video_hyperframes_get", "video_hyperframes_update", "video_hyperframes_check", "video_review_timeline_get":
	default:
		return nil, false
	}
	failed := func(err error) (any, bool) {
		return map[string]any{"isError": true, "content": []any{map[string]string{"type": "text", "text": err.Error()}}}, true
	}
	if err := validateFoundationArguments(envelope.Name, params); err != nil {
		return failed(err)
	}
	var args struct {
		ProjectID string               `json:"projectId"`
		Revision  int                  `json:"revision"`
		Document  *HyperFramesDocument `json:"document"`
		RenderID  string               `json:"renderId"`
	}
	if err := hyperFramesDecode(envelope.Arguments, &args); err != nil {
		return failed(err)
	}
	if !identifier.MatchString(args.ProjectID) {
		return failed(errors.New("invalid projectId"))
	}
	path := "/api/projects/" + args.ProjectID + "/hyperframes"
	method := "GET"
	var body []byte
	switch envelope.Name {
	case "video_hyperframes_update":
		if args.Revision < 1 || args.Document == nil {
			return failed(errors.New("revision and document are required"))
		}
		method = "PUT"
		body, _ = json.Marshal(HyperFramesRequest{Revision: args.Revision, Document: args.Document})
	case "video_hyperframes_check":
		if args.Revision < 1 {
			return failed(errors.New("revision is required"))
		}
		method = "POST"
		path += "/check"
		body, _ = json.Marshal(map[string]int{"revision": args.Revision})
	case "video_review_timeline_get":
		path = "/api/projects/" + args.ProjectID + "/review-timeline"
		if args.RenderID != "" {
			if !identifier.MatchString(args.RenderID) {
				return failed(errors.New("invalid renderId"))
			}
			path += "?renderId=" + args.RenderID
		}
	}
	req, err := http.NewRequest(method, base+path, bytes.NewReader(body))
	if err != nil {
		return failed(err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return failed(err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, (4<<20)+1))
	if err != nil {
		return failed(err)
	}
	if len(raw) > 4<<20 {
		return failed(errors.New("HyperFrames response exceeds 4 MiB"))
	}
	if resp.StatusCode >= 300 {
		return failed(fmt.Errorf("%s", raw))
	}
	return map[string]any{"content": []any{map[string]string{"type": "text", "text": string(raw)}}}, true
}
