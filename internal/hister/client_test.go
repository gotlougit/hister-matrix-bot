package hister

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

type fakeWSConn struct {
	written [][]byte
	readMsg []byte
	readErr error
}

func (f *fakeWSConn) WriteMessage(_ int, data []byte) error {
	copied := make([]byte, len(data))
	copy(copied, data)
	f.written = append(f.written, copied)
	return nil
}

func (f *fakeWSConn) ReadMessage() (int, []byte, error) {
	if f.readErr != nil {
		return 0, nil, f.readErr
	}
	return websocket.TextMessage, f.readMsg, nil
}

func (f *fakeWSConn) SetReadDeadline(time.Time) error  { return nil }
func (f *fakeWSConn) SetWriteDeadline(time.Time) error { return nil }
func (f *fakeWSConn) Close() error                     { return nil }

func TestClientIndexURLRetriesOnServerError(t *testing.T) {
	t.Parallel()

	var attempts atomic.Int32
	transport := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path != "/add" {
			return &http.Response{StatusCode: http.StatusNotFound, Body: io.NopCloser(bytes.NewReader(nil)), Header: make(http.Header)}, nil
		}
		if r.Method != http.MethodPost {
			return &http.Response{StatusCode: http.StatusMethodNotAllowed, Body: io.NopCloser(bytes.NewReader(nil)), Header: make(http.Header)}, nil
		}
		if got := r.Header.Get("Content-Type"); got != "application/x-www-form-urlencoded" {
			t.Fatalf("unexpected content-type: %q", got)
		}
		if got := r.Header.Get("User-Agent"); got != "Mozilla/5.0 (X11; Linux x86_64; rv:147.0) Gecko/20100101 Firefox/147.0" {
			t.Fatalf("unexpected user-agent: %q", got)
		}
		if got := r.Header.Get("Accept"); got != "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8" {
			t.Fatalf("unexpected accept: %q", got)
		}
		if err := r.ParseForm(); err != nil {
			t.Fatalf("ParseForm() error = %v", err)
		}
		if r.PostForm.Get("url") == "" {
			return &http.Response{StatusCode: http.StatusBadRequest, Body: io.NopCloser(bytes.NewReader(nil)), Header: make(http.Header)}, nil
		}
		if r.PostForm.Get("title") != "example" || r.PostForm.Get("text") != "example" {
			t.Fatalf("expected title/text to be domain label, got title=%q text=%q", r.PostForm.Get("title"), r.PostForm.Get("text"))
		}

		status := http.StatusInternalServerError
		if attempts.Add(1) >= 3 {
			status = http.StatusCreated
		}
		return &http.Response{StatusCode: status, Body: io.NopCloser(bytes.NewReader(nil)), Header: make(http.Header)}, nil
	})

	c, err := NewClient("https://hister.local", 2*time.Second)
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	c.HTTPClient = &http.Client{Transport: transport, Timeout: 2 * time.Second}
	c.RetryBackoff = 5 * time.Millisecond
	c.MaxRetryBackoff = 5 * time.Millisecond

	if err := c.IndexURL(context.Background(), "https://example.com/a"); err != nil {
		t.Fatalf("IndexURL() error = %v", err)
	}
	if got := attempts.Load(); got != 3 {
		t.Fatalf("IndexURL() attempts = %d, want 3", got)
	}
}

func TestClientIndexURLRequiresCreatedStatus(t *testing.T) {
	t.Parallel()

	transport := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path != "/add" {
			return &http.Response{StatusCode: http.StatusNotFound, Body: io.NopCloser(bytes.NewReader(nil)), Header: make(http.Header)}, nil
		}
		if r.Method != http.MethodPost {
			return &http.Response{StatusCode: http.StatusMethodNotAllowed, Body: io.NopCloser(bytes.NewReader(nil)), Header: make(http.Header)}, nil
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(bytes.NewReader(nil)), Header: make(http.Header)}, nil
	})

	c, err := NewClient("https://hister.local", 2*time.Second)
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	c.HTTPClient = &http.Client{Transport: transport, Timeout: 2 * time.Second}

	err = c.IndexURL(context.Background(), "https://example.com/a")
	if err == nil {
		t.Fatal("IndexURL() expected error, got nil")
	}
	if !strings.Contains(err.Error(), "status 200") {
		t.Fatalf("IndexURL() error = %v, want status 200 in message", err)
	}
}

func TestClientIndexURLSendsDomainLabelForSubdomain(t *testing.T) {
	t.Parallel()

	inputURL := "https://x.y.z.gotlou.com/path?q=1"
	transport := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path != "/add" {
			return &http.Response{StatusCode: http.StatusNotFound, Body: io.NopCloser(bytes.NewReader(nil)), Header: make(http.Header)}, nil
		}

		if got := r.Header.Get("Content-Type"); got != "application/x-www-form-urlencoded" {
			t.Fatalf("unexpected content-type: %q", got)
		}
		if err := r.ParseForm(); err != nil {
			t.Fatalf("ParseForm() error = %v", err)
		}
		rawURL := r.PostForm.Get("url")
		title := r.PostForm.Get("title")
		text := r.PostForm.Get("text")

		if rawURL != inputURL {
			t.Fatalf("unexpected url payload: got %q want %q", rawURL, inputURL)
		}
		if title != "gotlou" || text != "gotlou" {
			t.Fatalf("expected title/text to equal registrable domain label, got title=%q text=%q", title, text)
		}
		return &http.Response{StatusCode: http.StatusCreated, Body: io.NopCloser(bytes.NewReader(nil)), Header: make(http.Header)}, nil
	})

	c, err := NewClient("https://hister.local", 2*time.Second)
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	c.HTTPClient = &http.Client{Transport: transport, Timeout: 2 * time.Second}

	if err := c.IndexURL(context.Background(), inputURL); err != nil {
		t.Fatalf("IndexURL() error = %v", err)
	}
}

func TestClientSearchReconnectsAndParsesDocuments(t *testing.T) {
	t.Parallel()

	var attempts atomic.Int32
	conn := &fakeWSConn{}
	resp := map[string]any{
		"documents": []map[string]string{
			{"title": "First", "url": "https://a.example", "text": "Snippet A"},
			{"title": "Second", "url": "https://b.example", "text": "Snippet B"},
		},
	}
	blob, _ := json.Marshal(resp)
	conn.readMsg = blob

	c, err := NewClient("https://hister.local", 2*time.Second)
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	c.DialWS = func(ctx context.Context, wsURL string) (wsConn, error) {
		_ = ctx
		if wsURL != "wss://hister.local/search" {
			t.Fatalf("unexpected ws url: %s", wsURL)
		}
		if attempts.Add(1) == 1 {
			return nil, errors.New("temporary dial failure")
		}
		return conn, nil
	}
	c.RetryBackoff = 5 * time.Millisecond
	c.MaxRetryBackoff = 5 * time.Millisecond

	results, err := c.Search(context.Background(), "golang", 1)
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	if got := attempts.Load(); got != 2 {
		t.Fatalf("Search() dial attempts = %d, want 2", got)
	}

	if len(conn.written) != 1 {
		t.Fatalf("expected one ws write, got %d", len(conn.written))
	}
	var q struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(conn.written[0], &q); err != nil {
		t.Fatalf("Search() write payload decode error: %v", err)
	}
	if q.Text != "golang" {
		t.Fatalf("Search() query payload = %q, want %q", q.Text, "golang")
	}

	if len(results) != 1 {
		t.Fatalf("Search() result length = %d, want 1", len(results))
	}
	if results[0].Title != "First" {
		t.Fatalf("Search() first title = %q, want %q", results[0].Title, "First")
	}
	if results[0].URL != "https://a.example" {
		t.Fatalf("Search() first URL = %q, want %q", results[0].URL, "https://a.example")
	}
	if results[0].Snippet != "Snippet A" {
		t.Fatalf("Search() first snippet = %q, want %q", results[0].Snippet, "Snippet A")
	}
}
