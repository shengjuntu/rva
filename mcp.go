package main

import (
	"bufio"
	"bytes"
	"encoding/base64"
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
			for _, t := range []struct{ Name, Description string }{{"rundesk_diagnostic_image", "Return a deterministic nonce-bound PNG for read-only MCP transport diagnostics. No model is invoked; this never proves model vision."}, {"video_runtime_diagnose", "Check the project's selected runtime, fixed image transport and optional shared-workspace roundtrip. Returns explicit failed/blocked/not_checked rows and remedies; does not run models or rebuild environments."}, {"video_edit_locate", "Locate stable clip IDs and resolved output times using exact original narration quotes and saved textRefs. Optional one-based occurrence disambiguates repeats; renderId selects an owned historical snapshot. Unmapped clips are not guessed."}, {"video_media_alignment_get", "Read persisted exact-text-to-media choices, currentness, unresolved coverage and actual asset provenance. This records Agent editorial decisions, not automatic visual confidence."}, {"video_media_alignment_update", "Save free editorial units linked to exact narration quotes and real assets. Requires revision/sourceHash; repeated quote requires one-based occurrence. Partial coverage is allowed and remains unresolved."}, {"video_asset_video_inspect", "Inspect 1 to 8 strictly increasing actual video frame times. Returns timestamped PNG images; sampled frames do not prove whole-video motion or audio."}, {"video_production_run_prepare", "Freeze current prepared narration, alignment and selected material into the configured shared workspace for video_use. Requires revision and fresh requestId; identical retry reuses snapshot. Returns relative paths and adapter CLI."}, {"video_production_run_get", "Read an owned video_use production run and its frozen input hashes, exchange paths and imported render."}, {"video_production_run_ingest", "Validate completion manifest, media bytes and provenance from an owned shared production run, copy artifacts privately and archive a completed video_use render. Requires current revision; late old inputs stay stale."}, {"video_production_board_get", "Read authored narration hash and actual stage artifacts: plan, speech, media mapping, saved edit, render, review and final. States derive from persisted evidence; review documents are Agent records, not human approval."}, {"video_production_checkpoint", "Persist a freeform plan, media coverage map or review record for the exact authored narration hash and current revision. No required scene template. Completed review must reference a current completed full render."}, {"video_render_inspect", "Inspect one actual rendered frame at output time in seconds from a completed project render. Returns bounded PNG visual evidence only, not audio listening or whole-video verification."}, {"video_final_verify", "Rehash current referenced assets/font and final output/snapshot to verify input currency and output integrity. This is more expensive than final_get."}, {"video_final_get", "Read final render designation and whether it still matches current DSL and narration."}, {"video_final_save", "Designate a completed full DSL render as final after explicit human review. Requires revision,renderId,reviewed=true. Rejects partial previews and changed narration. Captures output and project snapshot hashes."}, {"video_edit_redo", "Redo most recent undone patch only if current DSL and mode still match its prior state. Requires revision."}, {"video_edit_patch_preview", "Compile clip/track/anchor/canvas changes without saving. Returns before/after and affected compiled clip IDs including timing dependencies. fields merges top-level clip fields; nested fields replace whole objects."}, {"video_edit_patch_apply", "Apply revision checked clip changes atomically and record an undo snapshot. Preserves active renderer mode."}, {"video_edit_undo", "Undo most recent patch if current DSL and mode still match that patch. Requires revision. Redo is available through video_edit_redo."}, {"video_asset_describe", "Save user supplied or Agent inspected visual description and tags for an actual image/video asset. Requires revision. basis is attribution, not proof that App inspected pixels."}, {"video_media_align", "Rank visual assets using explicit descriptions and lexical overlap with free text units. Read-only revision checked candidates with matching terms. Not a visual semantic model, confidence probability or automatic editing."}, {"video_narration_update", "Save free narration text independently of shots and editing DSL. Requires revision. No automatic content rewriting."}, {"video_speech_materialize", "Import completed independent narration as an audio asset and return measured cues and editing anchors. Requires revision and unchanged narration. Does not overwrite shots or DSL; timings are synthesis chunk boundaries, not forced word alignment."}, {"video_edit_get", "Read stored editing DSL, activation mode, revision and compiled timeline IR. Content remains in referenced assets; no paragraph projection required."}, {"video_edit_validate", "Validate and compile a proposed editing DSL without saving or rendering. Returns stable clip IDs and resolved times; unknown fields and cyclic references are rejected."}, {"video_edit_update", "Save a version-checked editing DSL and activate it. Supports video/audio/text tracks, source in/out/speed, layout, fades and time references. Subsequent rendering uses FFmpeg editing runtime, not legacy shots/audio/subtitles."}, {"video_edit_activate", "Activate saved DSL or switch to the legacy shot renderer without deleting either representation. Requires current revision."}, {"video_capabilities_get", "Discover implemented capabilities, dependency readiness and configured providers. probe=true optionally checks the configured IndexTTS model list, never synthesizes. No secrets are returned."}, {"video_project_create", "Create a named project. Returns a project ID; do not infer that a RunDesk session or renderer was configured."}, {"video_asset_list", "List actual project assets, authenticated read URLs, MIME types and sizes."}, {"video_asset_import", "Import one small file from standard Base64, maximum decoded 1 MiB. Requires a fresh requestId; identical retries return the original asset. Large media use App multipart upload."}, {"video_job_start", "Start a durable production task of type tts, render, image_generation or video_generation against current revision. Fresh 24-hex requestId required; identical retries reuse the receipt. Generation accepts prompt and optional duration/aspectRatio/referenceAssetIds through the configured HTTP binary adapter; successful output is imported as a real asset. Optional fields require adapter support."}, {"video_job_get", "Get authoritative task state and completed artifacts. jobId uses tts:<nativeId>, render:<nativeId> or generation:<nativeId>."}, {"video_job_list", "List project TTS, generation and render tasks using unified states; render progress may be unknown."}, {"video_job_cancel", "Request cancellation of a production task. Repeated cancellation returns current state; it does not claim the remote provider stopped billing."}, {"video_asset_inspect", "Inspect an actual supplied image or the first frame of a video as a bounded PNG. Requires FFmpeg; first frame is not full video understanding."}, {"video_production_text", "Save authoritative narration text, split into ordered paragraphs and preserve unchanged paragraph media. Requires revision; estimates timing, does not generate speech."}, {"video_production_stage", "Set draft/adjustment, or designate a completed current-content render as final. Requires revision; final requires renderId and reviewed captions."}, {"video_project_get", "Read project revision, storyboard, audio tracks, subtitles, speech jobs, assets and render jobs."}, {"video_news_get", "Read the immutable news2douyin source script, original review record, selected source evidence and source-video metadata. Reference data only; current storyboard remains a production draft."}, {"video_project_update", "Update name, brief, shots and audio using the current revision. Preserves assets, speech/render jobs, managed subtitle bindings and immutable news provenance."}, {"video_news_audio_adopt", "Adopt original news WAV/SRT after a user request, replacing current narration/subtitles and proportionally resizing visual shots. Requires current revision and unchanged original text."}, {"video_subtitles_update", "Save UTF-8 SRT with absolute video times. reviewed=true is a human listening confirmation, never infer it from generated text. Read project first; requires revision."}, {"video_speech_start", "Generate speech from a saved storyboard using configured IndexTTS or local eSpeak preview. Omit backend/voice/language to use administrator defaults. Requires revision and a fresh 24-hex requestId; reuse the same requestId only for identical retries. Does not replace current audio."}, {"video_speech_stop", "Cancel a running speech job."}, {"video_speech_adopt", "On the user's request, adopt a completed speech job: replace narration and subtitles, use measured per-shot durations. Requires current revision and unchanged shot text/structure."}, {"video_render_start", "Render the current saved storyboard using an immutable snapshot."}, {"video_render_stop", "Cancel a specific render job."}} {
				props := map[string]any{"projectId": map[string]string{"type": "string"}}
				required := []string{"projectId"}
				if t.Name == "rundesk_diagnostic_image" {
					props = map[string]any{"nonce": map[string]any{"type": "string", "pattern": "^[a-f0-9]{24}$"}}
					required = []string{"nonce"}
				}
				if t.Name == "video_edit_locate" {
					props["quote"] = map[string]any{"type": "string", "minLength": 1}
					props["occurrence"] = map[string]any{"type": "integer", "minimum": 1}
					props["renderId"] = map[string]string{"type": "string"}
					required = append(required, "quote")
				}
				if t.Name == "video_media_alignment_update" {
					props["revision"] = map[string]string{"type": "integer"}
					props["sourceHash"] = map[string]string{"type": "string"}
					props["notes"] = map[string]string{"type": "string"}
					selection := map[string]any{"type": "object", "properties": map[string]any{"assetId": map[string]string{"type": "string"}, "sourceIn": map[string]any{"type": "number", "minimum": 0}, "sourceOut": map[string]any{"type": "number", "exclusiveMinimum": 0}}, "required": []string{"assetId", "sourceIn", "sourceOut"}, "additionalProperties": false}
					props["units"] = map[string]any{"type": "array", "maxItems": 200, "items": map[string]any{"type": "object", "properties": map[string]any{"id": map[string]string{"type": "string"}, "quote": map[string]string{"type": "string"}, "occurrence": map[string]any{"type": "integer", "minimum": 1}, "visualIntent": map[string]string{"type": "string"}, "assetIds": map[string]any{"type": "array", "items": map[string]string{"type": "string"}}, "selections": map[string]any{"type": "array", "items": selection}, "role": map[string]any{"type": "string", "enum": []string{"evidence", "illustration", "background", "textcard"}}, "reason": map[string]string{"type": "string"}, "status": map[string]any{"type": "string", "enum": []string{"selected", "missing", "planned"}}}, "required": []string{"id", "quote", "visualIntent", "role", "reason", "status"}, "additionalProperties": false}}
					required = append(required, "revision", "sourceHash", "units")
				}
				if t.Name == "video_asset_video_inspect" {
					props["assetId"] = map[string]string{"type": "string"}
					props["times"] = map[string]any{"type": "array", "minItems": 1, "maxItems": 8, "items": map[string]any{"type": "number", "minimum": 0}}
					required = append(required, "assetId", "times")
				}
				if t.Name == "video_production_run_prepare" {
					props["revision"] = map[string]string{"type": "integer"}
					props["requestId"] = map[string]string{"type": "string"}
					required = append(required, "revision", "requestId")
				}
				if t.Name == "video_production_run_get" || t.Name == "video_production_run_ingest" {
					props["productionRunId"] = map[string]string{"type": "string"}
					required = append(required, "productionRunId")
					if t.Name == "video_production_run_ingest" {
						props["revision"] = map[string]string{"type": "integer"}
						required = append(required, "revision")
					}
				}
				if t.Name == "video_production_checkpoint" {
					for _, field := range []string{"sourceHash", "summary", "document"} {
						props[field] = map[string]string{"type": "string"}
					}
					props["revision"] = map[string]string{"type": "integer"}
					props["stage"] = map[string]any{"type": "string", "enum": []string{"plan", "media", "review"}}
					props["status"] = map[string]any{"type": "string", "enum": []string{"working", "completed", "blocked"}}
					props["renderId"] = map[string]string{"type": "string"}
					required = append(required, "revision", "sourceHash", "stage", "status", "summary", "document")
				}
				if t.Name == "video_render_inspect" {
					props["renderId"] = map[string]string{"type": "string"}
					props["time"] = map[string]any{"type": "number", "minimum": 0}
					required = append(required, "renderId", "time")
				}
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
					props["type"] = map[string]any{"type": "string", "enum": []string{"tts", "render", "image_generation", "video_generation"}}
					props["revision"] = map[string]string{"type": "integer"}
					props["requestId"] = map[string]string{"type": "string"}
					props["options"] = map[string]any{"type": "object", "properties": map[string]any{"range": map[string]any{"type": "object", "properties": map[string]any{"start": map[string]string{"type": "number"}, "end": map[string]string{"type": "number"}}, "required": []string{"start", "end"}, "additionalProperties": false}, "prompt": map[string]string{"type": "string"}, "duration": map[string]any{"type": "number", "exclusiveMinimum": 0, "maximum": 60}, "aspectRatio": map[string]any{"type": "string", "enum": []string{"16:9", "9:16", "1:1", "4:3", "3:4"}}, "referenceAssetIds": map[string]any{"type": "array", "maxItems": 4, "items": map[string]string{"type": "string"}}, "source": map[string]any{"type": "string", "enum": []string{"narration", "shots"}}, "backend": map[string]any{"type": "string", "enum": []string{"indextts", "espeak-ng"}}, "voice": map[string]string{"type": "string"}, "language": map[string]string{"type": "string"}, "speed": map[string]string{"type": "number"}, "rate": map[string]string{"type": "integer"}, "kind": map[string]any{"type": "string", "enum": []string{"draft"}}}, "additionalProperties": false}
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
				if t.Name == "video_asset_describe" || t.Name == "video_media_align" {
					props["revision"] = map[string]string{"type": "integer"}
					required = append(required, "revision")
					if t.Name == "video_asset_describe" {
						props["assetId"] = map[string]string{"type": "string"}
						props["description"] = map[string]any{"type": "object", "properties": map[string]any{"text": map[string]string{"type": "string"}, "tags": map[string]any{"type": "array", "items": map[string]string{"type": "string"}}, "basis": map[string]any{"type": "string", "enum": []string{"user", "agent_inspection"}}}, "required": []string{"text", "basis"}, "additionalProperties": false}
						required = append(required, "assetId", "description")
					} else {
						props["units"] = map[string]any{"type": "array", "items": map[string]any{"type": "object", "properties": map[string]any{"id": map[string]string{"type": "string"}, "text": map[string]string{"type": "string"}}, "required": []string{"id", "text"}, "additionalProperties": false}}
						props["limit"] = map[string]any{"type": "integer", "minimum": 1, "maximum": 20}
						required = append(required, "units")
					}
				}
				if t.Name == "video_edit_patch_preview" || t.Name == "video_edit_patch_apply" || t.Name == "video_edit_undo" || t.Name == "video_edit_redo" {
					props["revision"] = map[string]string{"type": "integer"}
					required = append(required, "revision")
					if t.Name != "video_edit_undo" && t.Name != "video_edit_redo" {
						props["textSourceHash"] = map[string]string{"type": "string"}
						props["changes"] = map[string]any{"type": "array", "items": map[string]any{"type": "object", "properties": map[string]any{"target": map[string]any{"type": "string", "enum": []string{"clip", "track", "anchor", "canvas"}}, "id": map[string]string{"type": "string"}, "op": map[string]any{"type": "string", "enum": []string{"add", "update", "delete"}}, "fields": map[string]string{"type": "object"}}, "required": []string{"id", "op"}, "additionalProperties": false}}
						required = append(required, "changes")
					}
				}
				if t.Name == "video_final_save" {
					props["revision"] = map[string]string{"type": "integer"}
					props["renderId"] = map[string]string{"type": "string"}
					props["reviewed"] = map[string]string{"type": "boolean"}
					required = append(required, "revision", "renderId", "reviewed")
				}
				if t.Name == "video_render_start" {
					props["range"] = map[string]any{"type": "object", "properties": map[string]any{"start": map[string]string{"type": "number"}, "end": map[string]string{"type": "number"}}, "required": []string{"start", "end"}, "additionalProperties": false}
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
				tool := map[string]any{"name": t.Name, "description": t.Description, "inputSchema": map[string]any{"type": "object", "properties": props, "required": required, "additionalProperties": false}}
				if t.Name == "rundesk_diagnostic_image" || t.Name == "video_edit_locate" {
					tool["annotations"] = map[string]bool{"readOnlyHint": true, "destructiveHint": false, "idempotentHint": true, "openWorldHint": false}
				}
				tools = append(tools, tool)
			}
			send(msg.ID, map[string]any{"tools": tools})
		case "tools/call":
			var p struct {
				Name      string `json:"name"`
				Arguments struct {
					Nonce           string           `json:"nonce"`
					Quote           string           `json:"quote"`
					Occurrence      int              `json:"occurrence"`
					TextSourceHash  *string          `json:"textSourceHash"`
					SourceHash      string           `json:"sourceHash"`
					Summary         string           `json:"summary"`
					Document        string           `json:"document"`
					Status          string           `json:"status"`
					Time            *float64         `json:"time"`
					Range           *RenderRange     `json:"range"`
					Changes         []EditChange     `json:"changes"`
					Description     AssetDescription `json:"description"`
					Units           json.RawMessage  `json:"units"`
					Notes           string           `json:"notes"`
					Times           json.RawMessage  `json:"times"`
					ProductionRunID string           `json:"productionRunId"`
					Limit           int              `json:"limit"`
					Source          string           `json:"source"`
					DSL             *EditDocument    `json:"dsl"`
					Active          bool             `json:"active"`
					Name            string           `json:"name"`
					Data            string           `json:"data"`
					Probe           bool             `json:"probe"`
					Type            string           `json:"type"`
					JobID           string           `json:"jobId"`
					Options         JobOptions       `json:"options"`
					AssetID         string           `json:"assetId"`
					Text            string           `json:"text"`
					Stage           string           `json:"stage"`
					Kind            string           `json:"kind"`
					ProjectID       string           `json:"projectId"`
					Revision        int              `json:"revision"`
					RequestID       string           `json:"requestId"`
					Backend         string           `json:"backend"`
					Language        string           `json:"language"`
					Speed           float64          `json:"speed"`
					Voice           string           `json:"voice"`
					Rate            int              `json:"rate"`
					SpeechID        string           `json:"speechId"`
					SRT             string           `json:"srt"`
					Enabled         bool             `json:"enabled"`
					Reviewed        bool             `json:"reviewed"`
					RenderID        string           `json:"renderId"`
					Project         json.RawMessage  `json:"project"`
				} `json:"arguments"`
			}
			e := json.Unmarshal(msg.Params, &p)
			if e == nil {
				e = validateFoundationArguments(p.Name, msg.Params)
			}
			path := "/api/projects/" + p.Arguments.ProjectID
			method := "GET"
			var body []byte
			if p.Name != "video_capabilities_get" && p.Name != "video_project_create" && p.Name != "rundesk_diagnostic_image" && !identifier.MatchString(p.Arguments.ProjectID) {
				e = fmt.Errorf("invalid projectId")
			}
			switch p.Name {
			case "rundesk_diagnostic_image":
				path = "/api/diagnostics/image"
				method = "POST"
				body, _ = json.Marshal(map[string]string{"nonce": p.Arguments.Nonce})
			case "video_runtime_diagnose":
				path += "/diagnostics"
				method = "POST"
				body = []byte("{}")
			case "video_edit_locate":
				path += "/edit/locate"
				method = "POST"
				body, _ = json.Marshal(EditLocateRequest{Quote: p.Arguments.Quote, Occurrence: p.Arguments.Occurrence, RenderID: p.Arguments.RenderID})
			case "video_media_alignment_get":
				path += "/media-alignment"
			case "video_media_alignment_update":
				method = "PUT"
				path += "/media-alignment"
				body, _ = json.Marshal(map[string]any{"revision": p.Arguments.Revision, "sourceHash": p.Arguments.SourceHash, "notes": p.Arguments.Notes, "units": p.Arguments.Units})
			case "video_asset_video_inspect":
				if !identifier.MatchString(p.Arguments.AssetID) {
					e = fmt.Errorf("invalid assetId")
				}
				method = "POST"
				path += "/assets/" + p.Arguments.AssetID + "/inspect-video"
				body, _ = json.Marshal(map[string]any{"times": p.Arguments.Times})
			case "video_production_run_prepare":
				method = "POST"
				path += "/production-runs/prepare"
				body, _ = json.Marshal(map[string]any{"revision": p.Arguments.Revision, "requestId": p.Arguments.RequestID})
			case "video_production_run_get", "video_production_run_ingest":
				if !identifier.MatchString(p.Arguments.ProductionRunID) {
					e = fmt.Errorf("invalid productionRunId")
				}
				path += "/production-runs/" + p.Arguments.ProductionRunID
				if p.Name == "video_production_run_ingest" {
					method = "POST"
					path += "/ingest"
					body, _ = json.Marshal(map[string]any{"revision": p.Arguments.Revision})
				}
			case "video_production_board_get":
				path += "/production-board"
			case "video_production_checkpoint":
				method = "POST"
				path += "/production-checkpoints"
				body, _ = json.Marshal(CheckpointRequest{Revision: p.Arguments.Revision, SourceHash: p.Arguments.SourceHash, Stage: p.Arguments.Stage, Status: p.Arguments.Status, Summary: p.Arguments.Summary, Document: p.Arguments.Document, RenderID: p.Arguments.RenderID})
			case "video_render_inspect":
				method = "POST"
				if !identifier.MatchString(p.Arguments.RenderID) || p.Arguments.Time == nil {
					e = fmt.Errorf("renderId and numeric time are required")
				}
				path += "/renders/" + p.Arguments.RenderID + "/inspect"
				body, _ = json.Marshal(map[string]any{"time": p.Arguments.Time})
			case "video_final_verify":
				path += "/final/verify"
			case "video_final_get":
				path += "/final"
			case "video_final_save":
				method = "POST"
				path += "/final"
				body, _ = json.Marshal(map[string]any{"revision": p.Arguments.Revision, "renderId": p.Arguments.RenderID, "reviewed": p.Arguments.Reviewed})
			case "video_edit_patch_preview", "video_edit_patch_apply":
				method = "POST"
				path += "/edit/patch/preview"
				if p.Name == "video_edit_patch_apply" {
					path = "/api/projects/" + p.Arguments.ProjectID + "/edit/patch/apply"
				}
				body, _ = json.Marshal(EditPatchRequest{Revision: p.Arguments.Revision, Changes: p.Arguments.Changes, TextSourceHash: p.Arguments.TextSourceHash})
			case "video_edit_redo":
				method = "POST"
				path += "/edit/redo"
				body, _ = json.Marshal(map[string]any{"revision": p.Arguments.Revision})
			case "video_edit_undo":
				method = "POST"
				path += "/edit/undo"
				body, _ = json.Marshal(map[string]any{"revision": p.Arguments.Revision})
			case "video_asset_describe":
				method = "PUT"
				path += "/assets/" + p.Arguments.AssetID + "/description"
				body, _ = json.Marshal(map[string]any{"revision": p.Arguments.Revision, "description": p.Arguments.Description})
			case "video_media_align":
				method = "POST"
				path += "/media/align"
				body, _ = json.Marshal(map[string]any{"revision": p.Arguments.Revision, "units": p.Arguments.Units, "limit": p.Arguments.Limit})
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
				body, _ = json.Marshal(RenderRequest{Range: p.Arguments.Range, Kind: p.Arguments.Kind, Revision: p.Arguments.Revision, RequestID: p.Arguments.RequestID})
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
			} else if p.Name == "video_render_inspect" {
				if len(raw) > 4<<20 || !bytes.HasPrefix(raw, []byte{137, 80, 78, 71, 13, 10, 26, 10}) {
					send(msg.ID, map[string]any{"isError": true, "content": []any{map[string]string{"type": "text", "text": "Invalid rendered PNG response"}}})
				} else {
					send(msg.ID, map[string]any{"content": []any{map[string]string{"type": "text", "text": fmt.Sprintf("One rendered frame at %.3fs; single_frame visual evidence, no audio listening or full-video verification.", *p.Arguments.Time)}, map[string]string{"type": "image", "mimeType": "image/png", "data": base64.StdEncoding.EncodeToString(raw)}}})
				}
			} else if p.Name == "video_asset_inspect" || p.Name == "video_asset_video_inspect" || p.Name == "rundesk_diagnostic_image" {
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
