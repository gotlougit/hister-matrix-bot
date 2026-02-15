package matrix

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/gotlou/hister-element-bot/bot/internal/llm"
	openai "github.com/openai/openai-go/v2"
	"maunium.net/go/mautrix"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"
)

const (
	// Split conversation buckets when two neighboring messages are farther apart than this.
	summaryBucketGap = time.Hour
	// Cap each bucket to bound prompt size and per-call output.
	summaryBucketMaxMessages = 30
)

type RoomMessage struct {
	Sender    id.UserID
	Body      string
	Timestamp time.Time
}

type BucketedSummarizer struct {
	extract func(ctx context.Context, transcript string) (string, error)
}

func NewBucketedSummarizer(client openai.Client) *BucketedSummarizer {
	return &BucketedSummarizer{
		extract: func(ctx context.Context, transcript string) (string, error) {
			return llm.ExtractTopicsFromChatsWithError(transcript, client, ctx)
		},
	}
}

func (s *BucketedSummarizer) Summarize(ctx context.Context, messages []RoomMessage) (string, error) {
	if s == nil || s.extract == nil {
		return "", errors.New("summarizer is not initialized")
	}
	if len(messages) == 0 {
		return "", nil
	}

	buckets := bucketMessagesByProximity(messages, summaryBucketGap, summaryBucketMaxMessages)
	parts := make([]string, 0, len(buckets))

	for _, bucket := range buckets {
		transcript := formatMessagesForSummary(bucket)
		if strings.TrimSpace(transcript) == "" {
			continue
		}
		topics, err := s.extract(ctx, transcript)
		if err != nil {
			return "", err
		}
		topics = strings.TrimSpace(topics)
		if topics != "" {
			parts = append(parts, topics)
		}
	}

	return strings.TrimSpace(strings.Join(parts, "\n")), nil
}

func (c *Client) GetRecentTextMessages(ctx context.Context, roomID id.RoomID, since time.Time, max int) ([]RoomMessage, error) {
	if max <= 0 {
		return nil, errors.New("max must be greater than zero")
	}
	out := make([]RoomMessage, 0, max)
	// Matrix /messages expects a concrete pagination token. For backward
	// pagination, "END" starts from the live end of the room timeline.
	from := "END"
	pageSize := max
	if pageSize > 100 {
		pageSize = 100
	}

	for len(out) < max {
		resp, err := c.api.Messages(ctx, roomID, from, "", mautrix.DirectionBackward, nil, pageSize)
		if err != nil {
			return nil, fmt.Errorf("fetch room messages: %w", err)
		}
		if resp == nil || len(resp.Chunk) == 0 {
			break
		}

		reachedBeforeSince := false
		for _, ev := range resp.Chunk {
			parsed, ok := c.parseHistoryTextEvent(ctx, ev)
			if !ok {
				continue
			}

			ts := time.UnixMilli(parsed.Timestamp)
			if ts.Before(since) {
				// Backward pagination is newest -> oldest. Once we're past the cutoff,
				// further events are older and won't match either.
				reachedBeforeSince = true
				break
			}

			msg := parsed.Content.AsMessage()
			if msg == nil {
				continue
			}
			body := strings.TrimSpace(msg.Body)
			if body == "" {
				continue
			}
			out = append(out, RoomMessage{
				Sender:    parsed.Sender,
				Body:      body,
				Timestamp: ts,
			})
			if len(out) >= max {
				break
			}
		}

		if len(out) >= max || reachedBeforeSince {
			break
		}

		if resp.End == "" || resp.End == from {
			break
		}
		from = resp.End
	}
	return out, nil
}

func (c *Client) parseHistoryTextEvent(ctx context.Context, ev *event.Event) (*event.Event, bool) {
	if ev == nil {
		return nil, false
	}

	parsed := ev
	if parsed.Type == event.EventEncrypted {
		if parsed.Content.Parsed == nil {
			if err := parsed.Content.ParseRaw(parsed.Type); err != nil && !errors.Is(err, event.ErrContentAlreadyParsed) {
				c.logf("history parse failed room=%s event=%s err=%v", parsed.RoomID, parsed.ID, err)
				return nil, false
			}
		}
		if c.crypto == nil {
			return nil, false
		}
		decrypted, err := c.crypto.Decrypt(ctx, parsed)
		if err != nil {
			c.logf("history decrypt failed room=%s event=%s err=%v", parsed.RoomID, parsed.ID, err)
			return nil, false
		}
		parsed = decrypted
	}
	if parsed == nil || parsed.Type != event.EventMessage {
		return nil, false
	}
	if parsed.Content.Parsed == nil {
		if err := parsed.Content.ParseRaw(parsed.Type); err != nil && !errors.Is(err, event.ErrContentAlreadyParsed) {
			c.logf("history parse failed room=%s event=%s err=%v", parsed.RoomID, parsed.ID, err)
			return nil, false
		}
	}
	msg := parsed.Content.AsMessage()
	if msg == nil || !msg.MsgType.IsText() {
		return nil, false
	}
	return parsed, true
}

func bucketMessagesByProximity(messages []RoomMessage, maxGap time.Duration, maxBucketSize int) [][]RoomMessage {
	if len(messages) == 0 || maxBucketSize <= 0 {
		return nil
	}

	sorted := append([]RoomMessage(nil), messages...)
	sort.SliceStable(sorted, func(i, j int) bool {
		return sorted[i].Timestamp.Before(sorted[j].Timestamp)
	})

	buckets := make([][]RoomMessage, 0, len(sorted))
	current := make([]RoomMessage, 0, min(maxBucketSize, len(sorted)))

	for _, msg := range sorted {
		if len(current) == 0 {
			current = append(current, msg)
			continue
		}

		prev := current[len(current)-1]
		startNewBucket := len(current) >= maxBucketSize
		if !startNewBucket && !prev.Timestamp.IsZero() && !msg.Timestamp.IsZero() {
			startNewBucket = msg.Timestamp.Sub(prev.Timestamp) > maxGap
		}

		if startNewBucket {
			buckets = append(buckets, current)
			current = make([]RoomMessage, 0, min(maxBucketSize, len(sorted)))
		}
		current = append(current, msg)
	}

	if len(current) > 0 {
		buckets = append(buckets, current)
	}
	return buckets
}

func formatMessagesForSummary(messages []RoomMessage) string {
	lines := make([]string, 0, len(messages))
	for _, msg := range messages {
		if msg.Sender == "" || strings.TrimSpace(msg.Body) == "" {
			continue
		}
		lines = append(lines, fmt.Sprintf("%s: %s", msg.Sender, msg.Body))
	}
	return strings.Join(lines, "\n")
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
