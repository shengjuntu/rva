package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

type hyperFramesMCPReply struct {
	ID     int `json:"id"`
	Result struct {
		IsError bool `json:"isError"`
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	} `json:"result"`
}

func TestHyperFramesMCPAdvertisesStrictFourToolContract(t *testing.T) {
	var out bytes.Buffer
	runMCP(strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`), &out, "http://unused", "", &http.Client{})
	var reply struct {
		Result struct {
			Tools []map[string]any `json:"tools"`
		} `json:"result"`
	}
	if err := json.Unmarshal(out.Bytes(), &reply); err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{"video_hyperframes_get": false, "video_hyperframes_update": true, "video_hyperframes_check": true, "video_review_timeline_get": false}
	seen := map[string]bool{}
	for _, tool := range reply.Result.Tools {
		name, _ := tool["name"].(string)
		write, relevant := want[name]
		if !relevant {
			continue
		}
		if seen[name] {
			t.Fatal("duplicate tool registration", name)
		}
		seen[name] = true
		schema := tool["inputSchema"].(map[string]any)
		annotations := tool["annotations"].(map[string]any)
		if schema["additionalProperties"] != false || annotations["readOnlyHint"] != !write || annotations["openWorldHint"] != (name == "video_hyperframes_check") {
			t.Fatal("incorrect permission/strictness hints", tool)
		}
		props := schema["properties"].(map[string]any)
		required := schema["required"].([]any)
		if len(required) == 0 || required[0] != "projectId" {
			t.Fatal("missing owned-project argument", name, required)
		}
		if write && (props["revision"] == nil || len(required) < 2 || required[1] != "revision") {
			t.Fatal("mutation lacks revision precondition", name)
		}
		if name == "video_hyperframes_update" {
			document := props["document"].(map[string]any)
			docProps := document["properties"].(map[string]any)
			canvas := docProps["canvas"].(map[string]any)["properties"].(map[string]any)
			for _, axis := range []string{"width", "height"} {
				bounds := canvas[axis].(map[string]any)
				if bounds["minimum"] != float64(16) || bounds["maximum"] != float64(4096) {
					t.Fatal("MCP canvas bounds differ from server contract", axis, bounds)
				}
			}
			if !strings.Contains(string(mustHyperFramesJSON(t, document["required"])), `"hasAudio"`) || document["additionalProperties"] != false {
				t.Fatal("document omits explicit audio or accepts unknown source fields", document)
			}
		}
	}
	if len(seen) != len(want) {
		t.Fatal("missing registered HyperFrames tool", seen)
	}
}

func mustHyperFramesJSON(t *testing.T, value any) []byte {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestHyperFramesMCPRejectsInvalidArgumentsBeforeHTTP(t *testing.T) {
	_, p, _, d := hyperFramesFixture(t)
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		io.WriteString(w, `{}`)
	}))
	defer server.Close()
	doc := string(mustHyperFramesJSON(t, d))
	for _, test := range []struct{ name, tool, args string }{
		{"unknown argument", "video_hyperframes_get", `{"projectId":"` + p.ID + `","path":"/tmp/other"}`},
		{"missing project", "video_hyperframes_get", `{}`},
		{"duplicate project", "video_hyperframes_get", `{"projectId":"` + p.ID + `","projectId":"` + uid() + `"}`},
		{"project traversal", "video_hyperframes_get", `{"projectId":"../other"}`},
		{"null arguments", "video_hyperframes_get", `null`},
		{"missing revision", "video_hyperframes_check", `{"projectId":"` + p.ID + `"}`},
		{"zero revision", "video_hyperframes_check", `{"projectId":"` + p.ID + `","revision":0}`},
		{"fractional revision", "video_hyperframes_check", `{"projectId":"` + p.ID + `","revision":1.5}`},
		{"null document", "video_hyperframes_update", `{"projectId":"` + p.ID + `","revision":1,"document":null}`},
		{"unknown nested field", "video_hyperframes_update", `{"projectId":"` + p.ID + `","revision":1,"document":` + strings.Replace(doc, `"canvas":{`, `"canvas":{"unsafe":true,`, 1) + `}`},
		{"duplicate nested field", "video_hyperframes_update", `{"projectId":"` + p.ID + `","revision":1,"document":` + strings.Replace(doc, `"schemaVersion":1`, `"schemaVersion":1,"schemaVersion":1`, 1) + `}`},
		{"implicit audio", "video_hyperframes_update", `{"projectId":"` + p.ID + `","revision":1,"document":` + strings.Replace(doc, `"hasAudio":false,`, ``, 1) + `}`},
		{"render traversal", "video_review_timeline_get", `{"projectId":"` + p.ID + `","renderId":"../other"}`},
		{"review write option", "video_review_timeline_get", `{"projectId":"` + p.ID + `","revision":1}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			params := json.RawMessage(`{"name":"` + test.tool + `","arguments":` + test.args + `}`)
			result, handled := callHyperFramesMCP(params, server.URL, "token", server.Client())
			if !handled || !strings.Contains(string(mustHyperFramesJSON(t, result)), `"isError":true`) {
				t.Fatal("invalid arguments accepted", handled, result)
			}
		})
	}
	if calls.Load() != 0 {
		t.Fatal("invalid arguments reached HTTP", calls.Load())
	}
	if _, handled := callHyperFramesMCP(json.RawMessage(`{"name":"other","arguments":{}}`), server.URL, "token", server.Client()); handled {
		t.Fatal("claimed unrelated MCP tool")
	}
}

func TestHyperFramesMCPHTTPAndStdioDispatch(t *testing.T) {
	for _, transport := range []string{"stdio over HTTP", "HTTP MCP"} {
		t.Run(transport, func(t *testing.T) {
			a, p, _, d := hyperFramesFixture(t)
			server := httptest.NewServer(a.routes())
			defer server.Close()
			invoke := func(id int, tool string, arguments any) hyperFramesMCPReply {
				t.Helper()
				raw := mustHyperFramesJSON(t, map[string]any{"jsonrpc": "2.0", "id": id, "method": "tools/call", "params": map[string]any{"name": tool, "arguments": arguments}})
				var output []byte
				if transport == "stdio over HTTP" {
					var out bytes.Buffer
					runMCP(bytes.NewReader(append(raw, '\n')), &out, server.URL, a.token, server.Client())
					output = out.Bytes()
				} else {
					req, err := http.NewRequest(http.MethodPost, server.URL+"/api/mcp", bytes.NewReader(raw))
					if err != nil {
						t.Fatal(err)
					}
					req.Header.Set("Authorization", "Bearer "+a.token)
					req.Header.Set("Content-Type", "application/json")
					resp, err := server.Client().Do(req)
					if err != nil {
						t.Fatal(err)
					}
					output, err = io.ReadAll(resp.Body)
					resp.Body.Close()
					if err != nil || resp.StatusCode != 200 {
						t.Fatal(resp.StatusCode, err, string(output))
					}
				}
				var reply hyperFramesMCPReply
				if err := json.Unmarshal(output, &reply); err != nil || reply.ID != id || len(reply.Result.Content) != 1 || reply.Result.Content[0].Type != "text" {
					t.Fatal("malformed MCP reply", err, string(output))
				}
				return reply
			}
			updated := invoke(1, "video_hyperframes_update", map[string]any{"projectId": p.ID, "revision": p.Revision, "document": d})
			var view HyperFramesView
			if updated.Result.IsError || json.Unmarshal([]byte(updated.Result.Content[0].Text), &view) != nil || !view.Current || view.Revision != p.Revision+1 || view.Document.Assets[0].SHA256 != hashBytes([]byte("picture")) {
				t.Fatal("update not dispatched to revision-bound persistence", updated)
			}
			get := invoke(2, "video_hyperframes_get", map[string]any{"projectId": p.ID})
			if get.Result.IsError || json.Unmarshal([]byte(get.Result.Content[0].Text), &view) != nil || view.Document.Files[1].Content != d.Files[1].Content {
				t.Fatal("get lost freely authored source", get)
			}
			review := invoke(3, "video_review_timeline_get", map[string]any{"projectId": p.ID})
			var timeline ReviewTimeline
			if review.Result.IsError || json.Unmarshal([]byte(review.Result.Content[0].Text), &timeline) != nil || timeline.Source != "authored" || !timeline.Current || timeline.Text != p.NarrationText {
				t.Fatal("review not dispatched", review)
			}
			// The old revision reaches the real check handler and fails before
			// browser execution. Saving/checking cannot create a rendered artifact.
			check := invoke(4, "video_hyperframes_check", map[string]any{"projectId": p.ID, "revision": p.Revision})
			if !check.Result.IsError || !strings.Contains(check.Result.Content[0].Text, "版本冲突") {
				t.Fatal("check dropped the revision or HTTP failure", check)
			}
			q, _ := a.get(p.ID)
			if len(q.Renders) != 0 || len(a.jobs) != 0 || q.Final != nil || q.Revision != p.Revision+1 {
				t.Fatal("read/check changed production state", q)
			}
		})
	}
}

func TestHyperFramesMCPCheckForwardsExactExecutionRequest(t *testing.T) {
	projectID := uid()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/projects/"+projectID+"/hyperframes/check" || r.URL.RawQuery != "" || r.Header.Get("Authorization") != "Bearer scoped-token" || r.Header.Get("Content-Type") != "application/json" {
			t.Error("wrong scoped check request", r.Method, r.URL, r.Header)
		}
		raw, _ := io.ReadAll(r.Body)
		if string(raw) != `{"revision":7}` {
			t.Error("wrong check payload", string(raw))
		}
		io.WriteString(w, `{"status":"passed","rendered":false,"revision":7}`)
	}))
	defer server.Close()
	params := mustHyperFramesJSON(t, map[string]any{"name": "video_hyperframes_check", "arguments": map[string]any{"projectId": projectID, "revision": 7}})
	result, handled := callHyperFramesMCP(params, server.URL, "scoped-token", server.Client())
	raw := string(mustHyperFramesJSON(t, result))
	if !handled || strings.Contains(raw, `"isError":true`) || !strings.Contains(raw, `\"rendered\":false`) {
		t.Fatal("check result discarded/misrepresented", result)
	}
}
