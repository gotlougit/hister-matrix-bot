package hister

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gorilla/websocket"
	"github.com/gotlou/hister-element-bot/bot/internal/extractor"
)

const (
	defaultAddPath         = "/add"
	defaultSearchPath      = "/search"
	defaultTimeout         = 10 * time.Second
	defaultRetryBackoff    = 100 * time.Millisecond
	defaultMaxRetryBackoff = 1 * time.Second
	defaultAddRetries      = 3
	defaultSearchRetries   = 3
)

type SearchResult struct {
	Title   string
	URL     string
	Snippet string
}

type SearchBackend interface {
	IndexURL(ctx context.Context, rawURL string) error
	Search(ctx context.Context, query string, limit int) ([]SearchResult, error)
}

type ClientOption func(*Client)

type Client struct {
	BaseURL string

	AddPath    string
	SearchPath string
	Timeout    time.Duration

	AddRetries    int
	SearchRetries int

	RetryBackoff    time.Duration
	MaxRetryBackoff time.Duration

	HTTPClient *http.Client
	Dialer     *websocket.Dialer
	DialWS     func(ctx context.Context, wsURL string) (wsConn, error)
	Extract    func(ctx context.Context, rawURL string) (extractor.Result, error)
}

type wsConn interface {
	WriteMessage(messageType int, data []byte) error
	ReadMessage() (messageType int, p []byte, err error)
	SetReadDeadline(t time.Time) error
	SetWriteDeadline(t time.Time) error
	Close() error
}

func NewClient(baseURL string, timeout time.Duration, opts ...ClientOption) (*Client, error) {
	c := &Client{
		BaseURL: baseURL,
		Timeout: timeout,
	}
	for _, opt := range opts {
		opt(c)
	}
	if err := c.validate(); err != nil {
		return nil, err
	}
	c.applyDefaults()
	return c, nil
}

func (c *Client) IndexURL(ctx context.Context, rawURL string) error {
	if err := c.prepare(); err != nil {
		return err
	}

	endpoint, err := c.endpoint(c.AddPath, false)
	if err != nil {
		return err
	}

	content, err := c.Extract(ctx, rawURL)
	if err != nil {
		return fmt.Errorf("extract URL content: %w", err)
	}

	return c.addDocument(ctx, endpoint, addRequest{
		URL:   rawURL,
		Title: content.Title,
		Text:  content.Text,
	})
}

type addRequest struct {
	URL   string `json:"url"`
	Title string `json:"title,omitempty"`
	Text  string `json:"text,omitempty"`
}

type addStatusError struct {
	StatusCode int
	Body       string
}

func (e *addStatusError) Error() string {
	if strings.TrimSpace(e.Body) == "" {
		return fmt.Sprintf("add request failed with status %d (expected %d)", e.StatusCode, http.StatusCreated)
	}
	return fmt.Sprintf("add request failed with status %d (expected %d): %s", e.StatusCode, http.StatusCreated, e.Body)
}

func (c *Client) addDocument(ctx context.Context, endpoint string, payload addRequest) error {
	form := url.Values{}
	form.Set("url", payload.URL)
	if strings.TrimSpace(payload.Title) != "" {
		form.Set("title", payload.Title)
	}
	if strings.TrimSpace(payload.Text) != "" {
		form.Set("text", payload.Text)
	}
	body := form.Encode()

	for attempt := 0; ; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(body))
		if err != nil {
			return fmt.Errorf("create add request: %w", err)
		}
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("User-Agent", "Mozilla/5.0 (X11; Linux x86_64; rv:147.0) Gecko/20100101 Firefox/147.0")
		req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")

		resp, err := c.HTTPClient.Do(req)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if attempt < c.AddRetries {
				if err := sleepWithContext(ctx, c.retryDelay(attempt)); err != nil {
					return err
				}
				continue
			}
			return fmt.Errorf("add request failed after %d attempts: %w", attempt+1, err)
		}

		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<10))
		_ = resp.Body.Close()

		if resp.StatusCode >= 500 {
			if attempt < c.AddRetries {
				if err := sleepWithContext(ctx, c.retryDelay(attempt)); err != nil {
					return err
				}
				continue
			}
			return fmt.Errorf("add request failed with status %d", resp.StatusCode)
		}

		if resp.StatusCode != http.StatusCreated {
			return &addStatusError{
				StatusCode: resp.StatusCode,
				Body:       strings.TrimSpace(string(respBody)),
			}
		}
		return nil
	}
}

func (c *Client) Search(ctx context.Context, query string, limit int) ([]SearchResult, error) {
	if err := c.prepare(); err != nil {
		return nil, err
	}

	wsURL, err := c.endpoint(c.SearchPath, true)
	if err != nil {
		return nil, err
	}

	reqBody, err := json.Marshal(struct {
		Text string `json:"text"`
	}{Text: query})
	if err != nil {
		return nil, fmt.Errorf("marshal search request: %w", err)
	}

	for attempt := 0; ; attempt++ {
		conn, err := c.DialWS(ctx, wsURL)
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			if attempt < c.SearchRetries {
				if err := sleepWithContext(ctx, c.retryDelay(attempt)); err != nil {
					return nil, err
				}
				continue
			}
			return nil, fmt.Errorf("search dial failed after %d attempts: %w", attempt+1, err)
		}

		res, err := c.searchOnce(ctx, conn, reqBody, limit)
		_ = conn.Close()
		if err == nil {
			return res, nil
		}

		var nonRetryable *nonRetryableError
		if errors.As(err, &nonRetryable) {
			return nil, nonRetryable.err
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if attempt >= c.SearchRetries {
			return nil, err
		}
		if !isRetryableWSError(err) {
			return nil, err
		}
		if err := sleepWithContext(ctx, c.retryDelay(attempt)); err != nil {
			return nil, err
		}
	}
}

func (c *Client) searchOnce(ctx context.Context, conn wsConn, reqBody []byte, limit int) ([]SearchResult, error) {
	if deadline, ok := combinedDeadline(ctx, c.Timeout); ok {
		_ = conn.SetWriteDeadline(deadline)
	}
	if err := conn.WriteMessage(websocket.TextMessage, reqBody); err != nil {
		return nil, fmt.Errorf("write search request: %w", err)
	}

	msg, err := readMessageWithContext(ctx, conn, c.Timeout)
	if err != nil {
		return nil, err
	}

	results, err := parseSearchResults(msg, limit)
	if err != nil {
		return nil, &nonRetryableError{err: err}
	}
	return results, nil
}

func parseSearchResults(body []byte, limit int) ([]SearchResult, error) {
	type doc struct {
		Title       string `json:"title"`
		URL         string `json:"url"`
		Text        string `json:"text"`
		Snippet     string `json:"snippet"`
		Description string `json:"description"`
	}
	type response struct {
		Documents []doc `json:"documents"`
		Results   struct {
			Documents []doc `json:"documents"`
		} `json:"results"`
	}

	var parsed response
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("decode search response: %w", err)
	}

	documents := parsed.Documents
	if len(documents) == 0 {
		documents = parsed.Results.Documents
	}

	out := make([]SearchResult, 0, len(documents))
	for _, d := range documents {
		snippet := d.Snippet
		if snippet == "" {
			snippet = d.Text
		}
		if snippet == "" {
			snippet = d.Description
		}
		out = append(out, SearchResult{
			Title:   d.Title,
			URL:     d.URL,
			Snippet: snippet,
		})
	}

	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func readMessageWithContext(ctx context.Context, conn wsConn, timeout time.Duration) ([]byte, error) {
	type readResult struct {
		msg []byte
		err error
	}
	resultCh := make(chan readResult, 1)

	if deadline, ok := combinedDeadline(ctx, timeout); ok {
		_ = conn.SetReadDeadline(deadline)
	}

	go func() {
		_, msg, err := conn.ReadMessage()
		resultCh <- readResult{msg: msg, err: err}
	}()

	select {
	case <-ctx.Done():
		_ = conn.Close()
		return nil, ctx.Err()
	case res := <-resultCh:
		if res.err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			return nil, fmt.Errorf("read search response: %w", res.err)
		}
		return res.msg, nil
	}
}

func (c *Client) endpoint(path string, websocketURL bool) (string, error) {
	u, err := url.Parse(c.BaseURL)
	if err != nil {
		return "", fmt.Errorf("parse base URL: %w", err)
	}

	p := strings.TrimSpace(path)
	if p == "" {
		p = "/"
	}
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}

	switch u.Scheme {
	case "http", "https", "ws", "wss":
	default:
		return "", fmt.Errorf("unsupported URL scheme %q", u.Scheme)
	}

	if websocketURL {
		switch u.Scheme {
		case "http":
			u.Scheme = "ws"
		case "https":
			u.Scheme = "wss"
		}
	} else {
		switch u.Scheme {
		case "ws":
			u.Scheme = "http"
		case "wss":
			u.Scheme = "https"
		}
	}

	u.RawQuery = ""
	u.Fragment = ""
	u.Path = joinURLPath(u.Path, p)
	return u.String(), nil
}

func joinURLPath(basePath, path string) string {
	if basePath == "" || basePath == "/" {
		return path
	}
	return strings.TrimRight(basePath, "/") + path
}

func (c *Client) retryDelay(attempt int) time.Duration {
	delay := c.RetryBackoff
	for i := 0; i < attempt; i++ {
		if delay >= c.MaxRetryBackoff {
			return c.MaxRetryBackoff
		}
		delay *= 2
	}
	if delay > c.MaxRetryBackoff {
		return c.MaxRetryBackoff
	}
	return delay
}

func (c *Client) prepare() error {
	if err := c.validate(); err != nil {
		return err
	}
	c.applyDefaults()
	return nil
}

func (c *Client) validate() error {
	if strings.TrimSpace(c.BaseURL) == "" {
		return errors.New("base URL is required")
	}
	parsed, err := url.Parse(c.BaseURL)
	if err != nil {
		return fmt.Errorf("invalid base URL: %w", err)
	}
	if parsed.Scheme == "" || parsed.Host == "" {
		return errors.New("base URL must include scheme and host")
	}
	return nil
}

func (c *Client) applyDefaults() {
	if c.AddPath == "" {
		c.AddPath = defaultAddPath
	}
	if c.SearchPath == "" {
		c.SearchPath = defaultSearchPath
	}
	if c.Timeout <= 0 {
		c.Timeout = defaultTimeout
	}
	if c.AddRetries < 0 {
		c.AddRetries = 0
	}
	if c.SearchRetries < 0 {
		c.SearchRetries = 0
	}
	if c.AddRetries == 0 {
		c.AddRetries = defaultAddRetries
	}
	if c.SearchRetries == 0 {
		c.SearchRetries = defaultSearchRetries
	}
	if c.RetryBackoff <= 0 {
		c.RetryBackoff = defaultRetryBackoff
	}
	if c.MaxRetryBackoff <= 0 {
		c.MaxRetryBackoff = defaultMaxRetryBackoff
	}
	if c.MaxRetryBackoff < c.RetryBackoff {
		c.MaxRetryBackoff = c.RetryBackoff
	}

	if c.HTTPClient == nil {
		c.HTTPClient = &http.Client{Timeout: c.Timeout}
	} else if c.HTTPClient.Timeout == 0 {
		c.HTTPClient.Timeout = c.Timeout
	}
	if c.Extract == nil {
		c.Extract = func(ctx context.Context, rawURL string) (extractor.Result, error) {
			return extractor.ExtractFromURL(ctx, c.HTTPClient, rawURL)
		}
	}

	if c.Dialer == nil {
		c.Dialer = &websocket.Dialer{HandshakeTimeout: c.Timeout}
	} else if c.Dialer.HandshakeTimeout == 0 {
		c.Dialer.HandshakeTimeout = c.Timeout
	}
	if c.DialWS == nil {
		c.DialWS = func(ctx context.Context, wsURL string) (wsConn, error) {
			conn, _, err := c.Dialer.DialContext(ctx, wsURL, nil)
			return conn, err
		}
	}
}

func combinedDeadline(ctx context.Context, timeout time.Duration) (time.Time, bool) {
	var deadline time.Time
	hasDeadline := false
	if timeout > 0 {
		deadline = time.Now().Add(timeout)
		hasDeadline = true
	}
	if ctxDeadline, ok := ctx.Deadline(); ok {
		if !hasDeadline || ctxDeadline.Before(deadline) {
			deadline = ctxDeadline
			hasDeadline = true
		}
	}
	return deadline, hasDeadline
}

func sleepWithContext(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return nil
	}
	t := time.NewTimer(d)
	defer t.Stop()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

func isRetryableWSError(err error) bool {
	if err == nil {
		return false
	}

	var closeErr *websocket.CloseError
	if errors.As(err, &closeErr) {
		switch closeErr.Code {
		case websocket.CloseNormalClosure:
			return false
		default:
			return true
		}
	}

	var netErr net.Error
	if errors.As(err, &netErr) {
		return true
	}

	return true
}

type nonRetryableError struct {
	err error
}

func (e *nonRetryableError) Error() string {
	return e.err.Error()
}

func (e *nonRetryableError) Unwrap() error {
	return e.err
}
