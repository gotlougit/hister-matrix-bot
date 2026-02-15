package extractor

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"

	"golang.org/x/net/html"
)

const defaultMaxBodyBytes int64 = 2 << 20

type Result struct {
	Title string
	Text  string
}

func makeHTTPRequest(ctx context.Context, client *http.Client, rawURL string, acceptHeader string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Accept", acceptHeader)
	req.Header.Set("User-Agent", "hister-element-bot/1.0")

	return client.Do(req)
}

func ExtractFromURL(ctx context.Context, httpClient *http.Client, rawURL string) (Result, error) {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return Result{}, fmt.Errorf("empty URL")
	}

	client := httpClient
	if client == nil {
		client = http.DefaultClient
	}

	resp, err := makeHTTPRequest(ctx, client, rawURL, "text/markdown")
	if err != nil || (resp != nil && (resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices)) {
		if resp != nil && resp.Body != nil {
			resp.Body.Close()
		}
		resp, err = makeHTTPRequest(ctx, client, rawURL, "text/html,application/xhtml+xml")
		if err != nil {
			return Result{}, fmt.Errorf("fetch URL: %w", err)
		}
	}
	defer resp.Body.Close()

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return Result{}, fmt.Errorf("fetch URL returned status %d", resp.StatusCode)
	}

	limited := io.LimitReader(resp.Body, defaultMaxBodyBytes+1)
	body, err := io.ReadAll(limited)
	if err != nil {
		return Result{}, fmt.Errorf("read response body: %w", err)
	}
	if int64(len(body)) > defaultMaxBodyBytes {
		return Result{}, fmt.Errorf("response body too large")
	}

	return ExtractFromReader(bytes.NewReader(body))
}

func ExtractFromReader(r io.Reader) (Result, error) {
	doc, err := html.Parse(r)
	if err != nil {
		return Result{}, fmt.Errorf("parse HTML: %w", err)
	}

	titleNode := findFirstElement(doc, "title")
	bodyNode := findFirstElement(doc, "body")

	var title string
	if titleNode != nil {
		title = normalizeWhitespace(nodeText(titleNode))
	}

	var bodyText string
	if bodyNode != nil {
		bodyText = normalizeWhitespace(bodyVisibleText(bodyNode))
	}

	return Result{
		Title: title,
		Text:  bodyText,
	}, nil
}

func findFirstElement(root *html.Node, tag string) *html.Node {
	if root == nil {
		return nil
	}

	var found *html.Node
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n == nil || found != nil {
			return
		}
		if n.Type == html.ElementNode && strings.EqualFold(n.Data, tag) {
			found = n
			return
		}
		for child := n.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
			if found != nil {
				return
			}
		}
	}

	walk(root)
	return found
}

func nodeText(root *html.Node) string {
	var b strings.Builder
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n == nil {
			return
		}
		if n.Type == html.TextNode {
			b.WriteString(n.Data)
			b.WriteByte(' ')
		}
		for child := n.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(root)
	return b.String()
}

func bodyVisibleText(root *html.Node) string {
	skip := map[string]struct{}{
		"script":   {},
		"style":    {},
		"noscript": {},
	}

	var b strings.Builder
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n == nil {
			return
		}
		if n.Type == html.ElementNode {
			if _, disallowed := skip[strings.ToLower(n.Data)]; disallowed {
				return
			}
		}
		if n.Type == html.TextNode {
			b.WriteString(n.Data)
			b.WriteByte(' ')
		}
		for child := n.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(root)
	return b.String()
}

func normalizeWhitespace(s string) string {
	return strings.Join(strings.Fields(s), " ")
}
