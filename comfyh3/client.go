// Package comfyh3 is a Go client for the verified MiniMax-H3 ComfyUI workflow.
package comfyh3

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"sort"
	"strings"
	"time"
)

const (
	maxJSONResponse    = 32 << 20
	maxWorkflowRequest = 8 << 20
	maxVideoResponse   = 128 << 20
)

var promptIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,127}$`)

// Configure Client before use. All requests prohibit redirects, including when
// a caller supplies HTTP. Credentials and remote bodies are never in errors.
type Client struct {
	BaseURL       string
	HTTP          *http.Client
	Authorization string // optional "Bearer <key>"
	PollEvery     time.Duration
	PollTimeout   time.Duration
}

func NewClient(baseURL string) *Client {
	return &Client{BaseURL: baseURL, HTTP: &http.Client{Timeout: 60 * time.Second}, PollEvery: 15 * time.Second, PollTimeout: 10 * time.Minute}
}
func (c *Client) endpoint(route string, query url.Values) (string, error) {
	base, err := url.Parse(c.BaseURL)
	if err != nil || base.Hostname() == "" || (base.Scheme != "http" && base.Scheme != "https") || base.User != nil || base.RawQuery != "" || base.ForceQuery || base.Fragment != "" || base.Opaque != "" || strings.TrimSpace(c.BaseURL) != c.BaseURL {
		return "", errors.New("invalid ComfyUI base URL")
	}
	if route == "" || !strings.HasPrefix(route, "/") || strings.ContainsAny(route, "?#\\\x00\r\n") {
		return "", errors.New("invalid ComfyUI API route")
	}
	base.Path = strings.TrimRight(base.Path, "/") + route
	base.RawPath = ""
	base.RawQuery = query.Encode()
	return base.String(), nil
}
func (c *Client) request(ctx context.Context, method, route string, query url.Values, body []byte) (*http.Response, error) {
	endpoint, err := c.endpoint(route, query)
	if err != nil {
		return nil, err
	}
	if c.Authorization != "" && (!strings.HasPrefix(c.Authorization, "Bearer ") || strings.TrimSpace(strings.TrimPrefix(c.Authorization, "Bearer ")) == "" || len(c.Authorization) > 4103 || strings.ContainsAny(c.Authorization, "\r\n\x00")) {
		return nil, errors.New("invalid ComfyUI authorization")
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, errors.New("cannot construct ComfyUI request")
	}
	if len(body) > 0 {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.Authorization != "" {
		req.Header.Set("Authorization", c.Authorization)
	}
	client := http.Client{Timeout: 60 * time.Second}
	if c.HTTP != nil {
		client = *c.HTTP
		if client.Timeout <= 0 {
			client.Timeout = 60 * time.Second
		}
	}
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if errors.Is(err, context.DeadlineExceeded) {
			return nil, fmt.Errorf("ComfyUI request timed out: %w", context.DeadlineExceeded)
		}
		return nil, errors.New("ComfyUI request failed")
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, fmt.Errorf("ComfyUI %s request returned HTTP %d", method, resp.StatusCode)
	}
	return resp, nil
}
func readBounded(resp *http.Response, limit int64) ([]byte, error) {
	defer resp.Body.Close()
	if resp.ContentLength > limit {
		return nil, errors.New("ComfyUI response exceeds size limit")
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		if resp.Request != nil && resp.Request.Context().Err() != nil {
			return nil, resp.Request.Context().Err()
		}
		return nil, errors.New("cannot read ComfyUI response")
	}
	if int64(len(raw)) > limit {
		return nil, errors.New("ComfyUI response exceeds size limit")
	}
	return raw, nil
}
func decodeResponse(raw []byte, out any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	if err := decoder.Decode(out); err != nil {
		return errors.New("invalid ComfyUI JSON response")
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return errors.New("invalid trailing ComfyUI JSON response")
	}
	return nil
}
func (c *Client) getJSON(ctx context.Context, route string, out any) error {
	resp, err := c.request(ctx, http.MethodGet, route, nil, nil)
	if err != nil {
		return err
	}
	raw, err := readBounded(resp, maxJSONResponse)
	if err != nil {
		return err
	}
	return decodeResponse(raw, out)
}

type SystemStats struct {
	System struct {
		ComfyUIVersion string `json:"comfyui_version"`
	} `json:"system"`
	Devices []struct {
		VRAMFree int64 `json:"vram_free"`
	} `json:"devices"`
}
type ObjectInfo map[string]struct {
	Input struct {
		Required map[string]any `json:"required"`
		Optional map[string]any `json:"optional"`
	} `json:"input"`
}

func (c *Client) SystemStats(ctx context.Context) (*SystemStats, error) {
	var out SystemStats
	if err := c.getJSON(ctx, "/system_stats", &out); err != nil {
		return nil, err
	}
	return &out, nil
}
func (c *Client) ObjectInfo(ctx context.Context) (ObjectInfo, error) {
	var out ObjectInfo
	if err := c.getJSON(ctx, "/object_info", &out); err != nil {
		return nil, err
	}
	if len(out) == 0 {
		return nil, errors.New("empty ComfyUI object_info response")
	}
	return out, nil
}

type QueueInfo struct {
	QueueRunning []any `json:"queue_running"`
	QueuePending []any `json:"queue_pending"`
}

func (c *Client) Queue(ctx context.Context) (*QueueInfo, error) {
	var out QueueInfo
	if err := c.getJSON(ctx, "/queue", &out); err != nil {
		return nil, err
	}
	return &out, nil
}
func (c *Client) Submit(ctx context.Context, wf map[string]any) (string, error) {
	body, err := json.Marshal(map[string]any{"prompt": wf})
	if err != nil || len(body) > maxWorkflowRequest {
		return "", errors.New("invalid or oversized ComfyUI workflow")
	}
	resp, err := c.request(ctx, http.MethodPost, "/prompt", nil, body)
	if err != nil {
		return "", err
	}
	raw, err := readBounded(resp, 1<<20)
	if err != nil {
		return "", err
	}
	var out struct {
		PromptID   string                     `json:"prompt_id"`
		NodeErrors map[string]json.RawMessage `json:"node_errors"`
	}
	if err := decodeResponse(raw, &out); err != nil {
		return "", err
	}
	if !promptIDPattern.MatchString(out.PromptID) || len(out.NodeErrors) > 0 {
		return "", errors.New("ComfyUI rejected workflow or omitted a valid prompt_id")
	}
	return out.PromptID, nil
}

type HistoryEntryStatus struct {
	Completed bool              `json:"completed"`
	StatusStr string            `json:"status_str"`
	Messages  []json.RawMessage `json:"messages"`
}
type HistoryEntry struct {
	Status HistoryEntryStatus `json:"status"`
}

// One context bounds both sleeps and in-flight HTTP. Auth, HTTP and JSON errors
// are reported immediately; execution errors precede completed=true.
func (c *Client) WaitForCompletion(ctx context.Context, promptID string) error {
	if !promptIDPattern.MatchString(promptID) {
		return errors.New("invalid ComfyUI prompt_id")
	}
	interval, timeout := c.PollEvery, c.PollTimeout
	if interval <= 0 {
		interval = 15 * time.Second
	}
	if timeout <= 0 {
		timeout = 10 * time.Minute
	}
	poll, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	for {
		if err := poll.Err(); err != nil {
			return err
		}
		var history map[string]HistoryEntry
		if err := c.getJSON(poll, "/history/"+promptID, &history); err != nil {
			return err
		}
		if entry, ok := history[promptID]; ok {
			if err := historyFailure(entry); err != nil {
				return err
			}
			if entry.Status.Completed {
				return nil
			}
		}
		timer := time.NewTimer(interval)
		select {
		case <-poll.Done():
			timer.Stop()
			return poll.Err()
		case <-timer.C:
		}
	}
}
func historyFailure(entry HistoryEntry) error {
	switch strings.ToLower(entry.Status.StatusStr) {
	case "error", "failed":
		return errors.New("ComfyUI execution failed")
	case "interrupted", "cancelled", "canceled":
		return errors.New("ComfyUI execution interrupted")
	}
	for _, message := range entry.Status.Messages {
		var pair []json.RawMessage
		if json.Unmarshal(message, &pair) != nil || len(pair) == 0 {
			continue
		}
		var kind string
		if json.Unmarshal(pair[0], &kind) != nil {
			continue
		}
		switch kind {
		case "execution_error":
			return errors.New("ComfyUI execution failed")
		case "execution_interrupted":
			return errors.New("ComfyUI execution interrupted")
		}
	}
	return nil
}

// CancelQueued removes only the named pending prompt. It never uses ComfyUI's
// global /interrupt; success does not prove an already-running prompt stopped.
func (c *Client) CancelQueued(ctx context.Context, promptID string) error {
	if !promptIDPattern.MatchString(promptID) {
		return errors.New("invalid ComfyUI prompt_id")
	}
	body, _ := json.Marshal(map[string]any{"delete": []string{promptID}})
	resp, err := c.request(ctx, http.MethodPost, "/queue", nil, body)
	if err != nil {
		return err
	}
	_, err = readBounded(resp, 1<<20)
	return err
}

type OutputItem struct{ Filename, Subfolder, Type string }

func validOutputItem(item OutputItem) bool {
	if item.Filename == "" || len(item.Filename) > 255 || item.Filename == "." || item.Filename == ".." || strings.ContainsAny(item.Filename, "/\\:\x00\r\n") {
		return false
	}
	if len(item.Subfolder) > 1024 || strings.ContainsAny(item.Subfolder, "\\\x00\r\n") || strings.HasPrefix(item.Subfolder, "/") {
		return false
	}
	if item.Subfolder != "" {
		if path.Clean(item.Subfolder) != item.Subfolder || item.Subfolder == "." {
			return false
		}
		for _, part := range strings.Split(item.Subfolder, "/") {
			if part == ".." || strings.Contains(part, ":") {
				return false
			}
		}
	}
	return item.Type == "" || item.Type == "output" || item.Type == "temp"
}

// ListOutputs preserves VHS nested output fields and returns a deterministic,
// deduplicated order. Invalid file metadata fails before /view is requested.
func (c *Client) ListOutputs(ctx context.Context, promptID string) ([]OutputItem, error) {
	if !promptIDPattern.MatchString(promptID) {
		return nil, errors.New("invalid ComfyUI prompt_id")
	}
	var history map[string]struct {
		Outputs map[string]any     `json:"outputs"`
		Status  HistoryEntryStatus `json:"status"`
	}
	if err := c.getJSON(ctx, "/history/"+promptID, &history); err != nil {
		return nil, err
	}
	entry, ok := history[promptID]
	if !ok {
		return nil, errors.New("ComfyUI prompt is not in history")
	}
	if err := historyFailure(HistoryEntry{Status: entry.Status}); err != nil {
		return nil, err
	}
	items := []OutputItem{}
	seen := map[OutputItem]bool{}
	var collect func(any, int) error
	collect = func(value any, depth int) error {
		if depth > 64 {
			return errors.New("ComfyUI output metadata exceeds nesting limit")
		}
		switch value := value.(type) {
		case map[string]any:
			if filename, ok := value["filename"].(string); ok {
				item := OutputItem{Filename: filename, Subfolder: str(value["subfolder"]), Type: str(value["type"])}
				if !validOutputItem(item) {
					return errors.New("invalid ComfyUI output file metadata")
				}
				if !seen[item] {
					items = append(items, item)
					seen[item] = true
				}
				if len(items) > 1000 {
					return errors.New("ComfyUI output file count exceeds limit")
				}
				return nil
			}
			for _, child := range value {
				if err := collect(child, depth+1); err != nil {
					return err
				}
			}
		case []any:
			for _, child := range value {
				if err := collect(child, depth+1); err != nil {
					return err
				}
			}
		}
		return nil
	}
	if err := collect(entry.Outputs, 0); err != nil {
		return nil, err
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].Subfolder != items[j].Subfolder {
			return items[i].Subfolder < items[j].Subfolder
		}
		if items[i].Filename != items[j].Filename {
			return items[i].Filename < items[j].Filename
		}
		return items[i].Type < items[j].Type
	})
	return items, nil
}

// Download admits only a bounded MP4 binary from this configured server.
func (c *Client) Download(ctx context.Context, item OutputItem) ([]byte, error) {
	if !validOutputItem(item) || !strings.EqualFold(path.Ext(item.Filename), ".mp4") {
		return nil, errors.New("invalid ComfyUI MP4 output metadata")
	}
	query := url.Values{"filename": {item.Filename}, "subfolder": {item.Subfolder}, "type": {item.Type}}
	if item.Type == "" {
		query.Set("type", "output")
	}
	resp, err := c.request(ctx, http.MethodGet, "/view", query, nil)
	if err != nil {
		return nil, err
	}
	data, err := readBounded(resp, maxVideoResponse)
	if err != nil {
		return nil, err
	}
	if len(data) < 12 || string(data[4:8]) != "ftyp" {
		return nil, errors.New("ComfyUI output is not an MP4 binary")
	}
	return data, nil
}
func str(value any) string { result, _ := value.(string); return result }
