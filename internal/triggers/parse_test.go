package triggers

import "testing"

func TestExtractSearchQuery_Precedence(t *testing.T) {
	p := NewParser("/search")

	q, ok := p.ExtractSearchQuery("/search golang @bot", "bot")
	if !ok || q != "golang @bot" {
		t.Fatalf("command precedence failed: ok=%v q=%q", ok, q)
	}
}

func TestExtractSearchQuery_Mentions(t *testing.T) {
	p := NewParser()

	q, ok := p.ExtractSearchQuery("@bot, golang", "bot")
	if !ok || q != "golang" {
		t.Fatalf("prefix mention failed: ok=%v q=%q", ok, q)
	}

	q, ok = p.ExtractSearchQuery("golang @bot", "bot")
	if !ok || q != "golang" {
		t.Fatalf("suffix mention failed: ok=%v q=%q", ok, q)
	}
}

func TestExtractURLs_Cleanup(t *testing.T) {
	p := NewParser()
	urls := p.ExtractURLs("see https://example.org/a), and https://example.org/b.")
	if len(urls) != 2 {
		t.Fatalf("unexpected url count: %d", len(urls))
	}
	if urls[0] != "https://example.org/a" {
		t.Fatalf("unexpected first URL: %q", urls[0])
	}
	if urls[1] != "https://example.org/b" {
		t.Fatalf("unexpected second URL: %q", urls[1])
	}
}
