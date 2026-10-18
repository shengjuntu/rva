package comfyh3

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestPollingReportsFailureBeforeCompletedAndSanitizesDetails(t *testing.T) {
	for _, body := range []string{
		`{"owned":{"status":{"completed":true,"status_str":"error"}}}`,
		`{"owned":{"status":{"completed":true,"status_str":"success","messages":[["execution_error",{"exception_message":"private-token backend-path"}]]}}}`,
		`{"owned":{"status":{"completed":false,"messages":[["execution_interrupted"]]}}}`,
	} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(body)) }))
		c := NewClient(server.URL)
		c.PollEvery = time.Millisecond
		c.PollTimeout = time.Second
		err := c.WaitForCompletion(context.Background(), "owned")
		server.Close()
		if err == nil || strings.Contains(err.Error(), "private-token") || strings.Contains(err.Error(), "backend-path") {
			t.Fatal("execution failure hidden or leaked", err)
		}
	}
}

func TestPollingHTTPAndInvalidJSONErrorsAreNotHiddenAsTimeout(t *testing.T) {
	for _, status := range []int{401, 429, 503} {
		var calls atomic.Int32
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			calls.Add(1)
			w.WriteHeader(status)
			w.Write([]byte("secret diagnostic response"))
		}))
		c := NewClient(server.URL)
		c.PollEvery = time.Millisecond
		c.PollTimeout = time.Second
		err := c.WaitForCompletion(context.Background(), "owned")
		server.Close()
		if err == nil || calls.Load() != 1 || strings.Contains(err.Error(), "secret") {
			t.Fatal(status, calls.Load(), err)
		}
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(`{} {"secret":"private"}`)) }))
	defer server.Close()
	c := NewClient(server.URL)
	if err := c.WaitForCompletion(context.Background(), "owned"); err == nil {
		t.Fatal("trailing JSON accepted")
	}
}

func TestPollingDeadlineCancelsInFlightHeadersAndBody(t *testing.T) {
	for _, flushBody := range []bool{false, true} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if flushBody {
				w.Write([]byte(`{`))
				w.(http.Flusher).Flush()
			}
			<-r.Context().Done()
		}))
		c := NewClient(server.URL)
		c.PollEvery = 0
		c.PollTimeout = 40 * time.Millisecond
		start := time.Now()
		err := c.WaitForCompletion(context.Background(), "owned")
		server.Close()
		if !errors.Is(err, context.DeadlineExceeded) || time.Since(start) > time.Second {
			t.Fatal("poll timeout did not bound HTTP", flushBody, err, time.Since(start))
		}
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"owned":{"status":{"completed":true,"status_str":"success"}}}`))
	}))
	defer server.Close()
	c := NewClient(server.URL)
	c.PollEvery = -time.Second
	c.PollTimeout = 0
	if err := c.WaitForCompletion(context.Background(), "owned"); err != nil {
		t.Fatal("zero-value timings must not panic", err)
	}
}

func TestURLPrefixAuthorizationAndRedirectIsolation(t *testing.T) {
	var redirected atomic.Int32
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { redirected.Add(1) }))
	defer other.Close()
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.URL.Path != "/comfy/system_stats" || r.Header.Get("Authorization") != "Bearer private-token" {
			t.Error("prefix/auth lost", r.URL.Path, r.Header)
		}
		http.Redirect(w, r, other.URL+"/steal", http.StatusFound)
	}))
	defer server.Close()
	c := NewClient(server.URL + "/comfy/")
	c.Authorization = "Bearer private-token"
	c.HTTP.CheckRedirect = func(*http.Request, []*http.Request) error { return nil }
	_, err := c.SystemStats(context.Background())
	if err == nil || redirected.Load() != 0 || calls.Load() != 1 || strings.Contains(err.Error(), "private-token") || strings.Contains(err.Error(), other.URL) {
		t.Fatal("redirect escaped configured server", redirected.Load(), err)
	}
	for _, base := range []string{"file:///private", "http://user:secret@localhost", "http://localhost/?key=secret", "http://localhost/#secret", " http://localhost"} {
		_, err := NewClient(base).SystemStats(context.Background())
		if err == nil || strings.Contains(err.Error(), "secret") {
			t.Fatal("invalid URL accepted/leaked", err)
		}
	}
	c = NewClient(server.URL)
	c.Authorization = "Bearer x\r\nX-Leak: secret"
	if _, err := c.SystemStats(context.Background()); err == nil || strings.Contains(err.Error(), "secret") {
		t.Fatal("header injection accepted/leaked", err)
	}
}

func TestDownloadUsesEncodedQueryAndRejectsRedirectNonBinaryAndOversize(t *testing.T) {
	mp4 := append([]byte{0, 0, 0, 16, 'f', 't', 'y', 'p', 'i', 's', 'o', 'm'}, []byte("fixture")...)
	item := OutputItem{Filename: "片段 & %.MP4", Subfolder: "nested folder", Type: "output"}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/prefix/view" || r.URL.Query().Get("filename") != item.Filename || r.URL.Query().Get("subfolder") != item.Subfolder || r.URL.Query().Get("type") != "output" {
			t.Error("bad query", r.URL)
		}
		w.Write(mp4)
	}))
	c := NewClient(server.URL + "/prefix")
	got, err := c.Download(context.Background(), item)
	server.Close()
	if err != nil || !bytes.Equal(got, mp4) {
		t.Fatal(err)
	}
	for _, body := range []string{"", "<html>secret diagnostic</html>", `{"url":"https://unrelated/video.mp4"}`} {
		server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(body)) }))
		_, err = NewClient(server.URL).Download(context.Background(), OutputItem{Filename: "clip.mp4"})
		server.Close()
		if err == nil || strings.Contains(err.Error(), "secret") || strings.Contains(err.Error(), "unrelated") {
			t.Fatal("nonbinary output accepted/leaked", err)
		}
	}
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "134217729")
		w.WriteHeader(200)
	}))
	defer server.Close()
	if _, err := NewClient(server.URL).Download(context.Background(), OutputItem{Filename: "clip.mp4"}); err == nil {
		t.Fatal("oversize binary accepted")
	}
	for _, item := range []OutputItem{{Filename: "../clip.mp4"}, {Filename: "clip.mp4", Subfolder: "../private"}, {Filename: "clip.mp4", Subfolder: "/private"}, {Filename: "C:secret.mp4"}, {Filename: "clip.mp4", Type: "input"}} {
		if _, err := NewClient(server.URL).Download(context.Background(), item); err == nil {
			t.Fatal("unsafe output accepted", item)
		}
	}
}

type countBody struct {
	io.Reader
	reads  int
	closed bool
}

func (b *countBody) Read(p []byte) (int, error) { b.reads++; return b.Reader.Read(p) }
func (b *countBody) Close() error               { b.closed = true; return nil }
func TestBoundedReadsRejectInsteadOfSilentlyTruncating(t *testing.T) {
	body := &countBody{Reader: strings.NewReader(strings.Repeat("x", 40))}
	if _, err := readBounded(&http.Response{Body: body, ContentLength: -1}, 32); err == nil || !body.closed {
		t.Fatal("chunked oversize response accepted")
	}
	body = &countBody{Reader: strings.NewReader("ignored")}
	if _, err := readBounded(&http.Response{Body: body, ContentLength: maxJSONResponse + 1}, maxJSONResponse); err == nil || body.reads != 0 || !body.closed {
		t.Fatal("advertised oversized response was read")
	}
}

func TestOutputOrderingAndScopedQueueDelete(t *testing.T) {
	var queueDeletes atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/history/owned":
			w.Write([]byte(`{"owned":{"status":{"completed":true,"status_str":"success"},"outputs":{"9":{"gifs":[{"filename":"z.mp4","subfolder":"","type":"output"}]},"2":{"gifs":[{"filename":"a.mp4","type":"output"},{"filename":"a.mp4","type":"output"}]}}}}`))
		case "/queue":
			if r.Method != "POST" {
				t.Error(r.Method)
			}
			var payload map[string]any
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Error(err)
			}
			if !reflect.DeepEqual(payload, map[string]any{"delete": []any{"owned"}}) {
				t.Error("queue cancellation not scoped", payload)
			}
			queueDeletes.Add(1)
			w.Write([]byte(`{}`))
		default:
			t.Error("unexpected/global operation", r.URL.Path)
			w.WriteHeader(500)
		}
	}))
	defer server.Close()
	c := NewClient(server.URL)
	for i := 0; i < 10; i++ {
		items, err := c.ListOutputs(context.Background(), "owned")
		if err != nil || len(items) != 2 || items[0].Filename != "a.mp4" || items[1].Filename != "z.mp4" {
			t.Fatal("nondeterministic outputs", items, err)
		}
	}
	if err := c.CancelQueued(context.Background(), "owned"); err != nil || queueDeletes.Load() != 1 {
		t.Fatal(err)
	}
	for _, id := range []string{"../private", "id?query=x", "id/child", ""} {
		if err := c.CancelQueued(context.Background(), id); err == nil {
			t.Fatal("unsafe prompt id accepted", id)
		}
	}
	if queueDeletes.Load() != 1 {
		t.Fatal("invalid prompt ids reached queue")
	}
}

func TestSubmitRejectsInvalidAndOversizedResponse(t *testing.T) {
	for _, body := range []string{`{}`, `{"prompt_id":"../private"}`, `{"prompt_id":"owned","node_errors":{"5":{"secret":"private"}}}`, `{"prompt_id":"owned"} {}`} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(body)) }))
		_, err := NewClient(server.URL).Submit(context.Background(), map[string]any{})
		server.Close()
		if err == nil || strings.Contains(err.Error(), "private") {
			t.Fatal("bad submit response accepted/leaked", err)
		}
	}
}
