// Package comfyh3 is a Go client for generating MiniMax-H3 videos through
// the ComfyUI HTTP API. It mirrors the proven Python workflow
// (comfy_h3_video.py v6): UNETLoader + CLIPLoader(qwen3vl-32b) + VAELoader
// (video+audio) + LoraLoader(4step turbo) + MiniMaxH3ImageToVideo + KSampler
// + VAEDecode(+Audio) + VHS_VideoCombine.
package comfyh3

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

// Client talks to the ComfyUI REST API.
type Client struct {
	BaseURL    string // e.g. http://127.0.0.1:19897
	HTTP       *http.Client
	PollEvery  time.Duration
	PollTimeout time.Duration
}

func NewClient(baseURL string) *Client {
	return &Client{
		BaseURL:     baseURL,
		HTTP:        &http.Client{Timeout: 60 * time.Second},
		PollEvery:   15 * time.Second,
		PollTimeout: 10 * time.Minute,
	}
}

func (c *Client) getJSON(ctx context.Context, path string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+path, nil)
	if err != nil {
		return err
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("GET %s -> %d: %s", path, resp.StatusCode, truncate(body))
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

// SystemStats returns the ComfyUI version and first-device VRAM (bytes).
type SystemStats struct {
	System struct {
		ComfyUIVersion string `json:"comfyui_version"`
	} `json:"system"`
	Devices []struct {
		VRAMFree int64 `json:"vram_free"`
	} `json:"devices"`
}

// ObjectInfo is the minimal shape of GET /object_info we need.
type ObjectInfo map[string]struct {
	Input struct {
		Required map[string]any `json:"required"`
		Optional map[string]any `json:"optional"`
	} `json:"input"`
}

func (c *Client) SystemStats(ctx context.Context) (*SystemStats, error) {
	var s SystemStats
	if err := c.getJSON(ctx, "/system_stats", &s); err != nil {
		return nil, err
	}
	return &s, nil
}

func (c *Client) ObjectInfo(ctx context.Context) (ObjectInfo, error) {
	var oi ObjectInfo
	if err := c.getJSON(ctx, "/object_info", &oi); err != nil {
		return nil, err
	}
	return oi, nil
}

// QueueInfo is the minimal shape of GET /queue.
type QueueInfo struct {
	QueueRunning []any `json:"queue_running"`
	QueuePending []any `json:"queue_pending"`
}

func (c *Client) Queue(ctx context.Context) (*QueueInfo, error) {
	var q QueueInfo
	if err := c.getJSON(ctx, "/queue", &q); err != nil {
		return nil, err
	}
	return &q, nil
}

// Submit posts a workflow to /prompt and returns the prompt_id.
func (c *Client) Submit(ctx context.Context, wf map[string]any) (string, error) {
	payload := map[string]any{"prompt": wf}
	body, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/prompt", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 65536))
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("POST /prompt -> %d: %s", resp.StatusCode, truncate(raw))
	}
	var out struct {
		PromptID string `json:"prompt_id"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return "", fmt.Errorf("bad submit response: %s", truncate(raw))
	}
	if out.PromptID == "" {
		return "", fmt.Errorf("no prompt_id in response: %s", truncate(raw))
	}
	return out.PromptID, nil
}

// HistoryEntry is the minimal shape of /history/{id} (status part only;
// outputs are parsed separately via ListOutputs).
type HistoryEntry struct {
	Status struct {
		Completed bool              `json:"completed"`
		StatusStr string            `json:"status_str"`
		Messages  []json.RawMessage `json:"messages"`
	} `json:"status"`
}

// WaitForCompletion polls /history/{id} until completed or errored.
// Returns nil error when completed; the caller then fetches outputs.
func (c *Client) WaitForCompletion(ctx context.Context, promptID string) error {
	deadline := time.Now().Add(c.PollTimeout)
	ticker := time.NewTicker(c.PollEvery)
	defer ticker.Stop()
	path := "/history/" + url.PathEscape(promptID)
	for {
		var hist map[string]HistoryEntry
		if err := c.getJSON(ctx, path, &hist); err == nil {
			if e, ok := hist[promptID]; ok {
				if e.Status.Completed {
					return nil
				}
				for _, m := range e.Status.Messages {
					var pair []json.RawMessage
					if json.Unmarshal(m, &pair) == nil && len(pair) == 2 {
						var kind string
						if json.Unmarshal(pair[0], &kind) == nil && (kind == "execution_error" || kind == "execution_interrupted") {
							var detail any
							if json.Unmarshal(pair[1], &detail) == nil {
								b, _ := json.Marshal(detail)
								return fmt.Errorf("execution %s: %s", kind, truncate(b))
							}
							return fmt.Errorf("execution %s", kind)
						}
					}
				}
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if time.Now().After(deadline) {
				return fmt.Errorf("timeout after %v waiting for prompt %s", c.PollTimeout, promptID)
			}
		}
	}
}

// OutputItem is one file produced by a workflow.
type OutputItem struct {
	Filename  string
	Subfolder string
	Type      string
}

// ListOutputs returns the produced files of a finished prompt.
func (c *Client) ListOutputs(ctx context.Context, promptID string) ([]OutputItem, error) {
	var hist map[string]struct {
		Outputs map[string]any `json:"outputs"`
	}
	if err := c.getJSON(ctx, "/history/"+url.PathEscape(promptID), &hist); err != nil {
		return nil, err
	}
	e, ok := hist[promptID]
	if !ok {
		return nil, fmt.Errorf("prompt %s not in history", promptID)
	}
	var items []OutputItem
	var collect func(any)
	collect = func(v any) {
		switch t := v.(type) {
		case map[string]any:
			if fn, ok := t["filename"].(string); ok {
				items = append(items, OutputItem{
					Filename:  fn,
					Subfolder: str(t["subfolder"]),
					Type:      str(t["type"]),
				})
				return
			}
			for _, it := range t {
				collect(it)
			}
		case []any:
			for _, it := range t {
				collect(it)
			}
		}
	}
	for _, outs := range e.Outputs {
		collect(outs)
	}
	return items, nil
}

// Download fetches the raw bytes of an output file via /view.
func (c *Client) Download(ctx context.Context, it OutputItem) ([]byte, error) {
	q := url.Values{}
	q.Set("filename", it.Filename)
	q.Set("subfolder", it.Subfolder)
	if it.Type == "" {
		it.Type = "output"
	}
	q.Set("type", it.Type)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+"/view?"+q.Encode(), nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("GET /view -> %d: %s", resp.StatusCode, truncate(body))
	}
	return io.ReadAll(resp.Body)
}

func str(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

func truncate(b []byte) string {
	const max = 1500
	if len(b) > max {
		return string(b[:max]) + "..."
	}
	return string(b)
}
