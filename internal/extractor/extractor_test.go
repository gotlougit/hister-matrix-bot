package extractor

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestExtractFromURLExtractsTitleAndBodyText(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(`<!doctype html>
<html>
  <head><title>  Example   Title </title></head>
  <body>
    Alpha <b>Beta</b>
    <script>ignored script content</script>
    <style>.ignored { color: red; }</style>
    <noscript>ignored noscript content</noscript>
    <p>Gamma</p>
  </body>
</html>`))
	}))
	defer srv.Close()

	got, err := ExtractFromURL(context.Background(), srv.Client(), srv.URL)
	if err != nil {
		t.Fatalf("ExtractFromURL() error = %v", err)
	}

	if got.Title != "Example Title" {
		t.Fatalf("ExtractFromURL() title = %q, want %q", got.Title, "Example Title")
	}
	if got.Text != "Alpha Beta Gamma" {
		t.Fatalf("ExtractFromURL() text = %q, want %q", got.Text, "Alpha Beta Gamma")
	}
}

func TestExtractFromURLReturnsHTTPError(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r
		http.Error(w, "boom", http.StatusBadGateway)
	}))
	defer srv.Close()

	_, err := ExtractFromURL(context.Background(), srv.Client(), srv.URL)
	if err == nil {
		t.Fatal("ExtractFromURL() expected error, got nil")
	}
	if !strings.Contains(err.Error(), "status 502") {
		t.Fatalf("ExtractFromURL() error = %v, want status 502", err)
	}
}
