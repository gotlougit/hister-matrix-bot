package matrix

import (
	"context"
	"fmt"
	"testing"
	"time"
)

func TestBucketMessagesByProximity_SplitsByGap(t *testing.T) {
	base := time.Now().UTC()
	msgs := []RoomMessage{
		{Sender: "@alice:test", Body: "first", Timestamp: base},
		{Sender: "@bob:test", Body: "second", Timestamp: base.Add(30 * time.Minute)},
		{Sender: "@carol:test", Body: "third", Timestamp: base.Add(2 * time.Hour)},
	}

	buckets := bucketMessagesByProximity(msgs, time.Hour, 30)
	if len(buckets) != 2 {
		t.Fatalf("expected 2 buckets, got %d", len(buckets))
	}
	if len(buckets[0]) != 2 || len(buckets[1]) != 1 {
		t.Fatalf("unexpected bucket sizes: %d and %d", len(buckets[0]), len(buckets[1]))
	}
}

func TestBucketMessagesByProximity_SplitsByMaxBucketSize(t *testing.T) {
	base := time.Now().UTC()
	msgs := make([]RoomMessage, 0, 31)
	for i := 0; i < 31; i++ {
		msgs = append(msgs, RoomMessage{
			Sender:    "@alice:test",
			Body:      fmt.Sprintf("msg %d", i),
			Timestamp: base.Add(time.Duration(i) * time.Minute),
		})
	}

	buckets := bucketMessagesByProximity(msgs, 24*time.Hour, 30)
	if len(buckets) != 2 {
		t.Fatalf("expected 2 buckets, got %d", len(buckets))
	}
	if len(buckets[0]) != 30 || len(buckets[1]) != 1 {
		t.Fatalf("unexpected bucket sizes: %d and %d", len(buckets[0]), len(buckets[1]))
	}
}

func TestBucketedSummarizer_SummarizeConcatenatesBucketOutputs(t *testing.T) {
	base := time.Now().UTC()
	msgs := []RoomMessage{
		{Sender: "@alice:test", Body: "hello", Timestamp: base},
		{Sender: "@bob:test", Body: "world", Timestamp: base.Add(10 * time.Minute)},
		{Sender: "@carol:test", Body: "later", Timestamp: base.Add(2 * time.Hour)},
	}

	calls := 0
	s := &BucketedSummarizer{
		extract: func(_ context.Context, transcript string) (string, error) {
			calls++
			if transcript == "" {
				t.Fatal("expected non-empty transcript")
			}
			if calls == 1 {
				return "- topic-one", nil
			}
			return "- topic-two", nil
		},
	}

	out, err := s.Summarize(context.Background(), msgs)
	if err != nil {
		t.Fatalf("Summarize failed: %v", err)
	}
	if calls != 2 {
		t.Fatalf("expected 2 extractor calls, got %d", calls)
	}
	if out != "- topic-one\n- topic-two" {
		t.Fatalf("unexpected summary output: %q", out)
	}
}
