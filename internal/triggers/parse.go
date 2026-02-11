package triggers

import (
	"net/url"
	"regexp"
	"strings"
)

const defaultSearchCommand = "/search"

var (
	urlPattern          = regexp.MustCompile(`https?://[^\s<>"']+`)
	trailingPunctuation = "\"'.,!?;:"
)

// Parser implements extraction of search triggers and URLs from message bodies.
type Parser struct {
	searchCommand string
	commandRegex  *regexp.Regexp
}

// NewParser creates a parser. If searchCommand is empty, /search is used.
func NewParser(searchCommand ...string) *Parser {
	command := defaultSearchCommand
	if len(searchCommand) > 0 && strings.TrimSpace(searchCommand[0]) != "" {
		command = strings.TrimSpace(searchCommand[0])
	}

	return &Parser{
		searchCommand: command,
		commandRegex:  regexp.MustCompile(`(?i)^\s*` + regexp.QuoteMeta(command) + `\s+(.+?)\s*$`),
	}
}

func (p *Parser) ExtractSearchQuery(msg, botDisplayName string) (query string, ok bool) {
	if p == nil {
		p = NewParser()
	}

	if match := p.commandRegex.FindStringSubmatch(msg); len(match) == 2 {
		q := strings.TrimSpace(match[1])
		if q != "" {
			return q, true
		}
	}

	name := normalizeDisplayName(botDisplayName)
	if name == "" {
		return "", false
	}

	prefixPattern := regexp.MustCompile(`(?i)^\s*@` + regexp.QuoteMeta(name) + `[:,]?\s+(.+?)\s*$`)
	if match := prefixPattern.FindStringSubmatch(msg); len(match) == 2 {
		q := strings.TrimSpace(match[1])
		if q != "" {
			return q, true
		}
	}

	suffixPattern := regexp.MustCompile(`(?i)^\s*(.+?)\s+@` + regexp.QuoteMeta(name) + `\s*$`)
	if match := suffixPattern.FindStringSubmatch(msg); len(match) == 2 {
		q := strings.TrimSpace(match[1])
		if q != "" {
			return q, true
		}
	}

	return "", false
}

func (Parser) ExtractURLs(msg string) []string {
	matches := urlPattern.FindAllString(msg, -1)
	if len(matches) == 0 {
		return nil
	}

	urls := make([]string, 0, len(matches))
	for _, raw := range matches {
		cleaned := normalizeMatchedURL(raw)
		if cleaned == "" {
			continue
		}
		u, err := url.Parse(cleaned)
		if err != nil {
			continue
		}
		if (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			continue
		}
		urls = append(urls, cleaned)
	}

	if len(urls) == 0 {
		return nil
	}
	return urls
}

func normalizeDisplayName(s string) string {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "@")
	return strings.TrimSpace(s)
}

func normalizeMatchedURL(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}

	for {
		trimmed := strings.TrimRight(raw, trailingPunctuation)
		if trimmed == raw {
			break
		}
		raw = trimmed
	}

	for strings.HasSuffix(raw, ")") {
		if strings.Count(raw, "(") >= strings.Count(raw, ")") {
			break
		}
		raw = strings.TrimSuffix(raw, ")")
	}

	return raw
}
