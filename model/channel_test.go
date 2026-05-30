package model

import (
	"errors"
	"testing"
	"time"
)

func TestNextKeySkipsUnhealthyKeys(t *testing.T) {
	ch := NewChannel("test", "openai", "https://example.com", "", "", []string{"key-a", "key-b"}, []string{"m"}, nil, 10, 100)
	for i := 0; i < 3; i++ {
		ch.RecordKeyResult("key-a", 401, time.Millisecond, errors.New("invalid key"))
	}

	for i := 0; i < 4; i++ {
		if got := ch.NextKey(); got != "key-b" {
			t.Fatalf("expected healthy key-b, got %q", got)
		}
	}
}

func TestNextKeySkipsDisabledKeys(t *testing.T) {
	ch := NewChannel("test", "openai", "https://example.com", "", "", []string{"key-a", "key-b"}, []string{"m"}, nil, 10, 100)
	ch.SetDisabledKeys([]string{"key-a"})

	for i := 0; i < 4; i++ {
		if got := ch.NextKey(); got != "key-b" {
			t.Fatalf("expected enabled key-b, got %q", got)
		}
	}
}

func TestRecordKeyResultResetsConsecutiveFailures(t *testing.T) {
	ch := NewChannel("test", "openai", "https://example.com", "", "", []string{"key-a"}, []string{"m"}, nil, 10, 100)
	ch.RecordKeyResult("key-a", 429, time.Millisecond, errors.New("rate limited"))
	ch.RecordKeyResult("key-a", 200, time.Millisecond, nil)

	stats := ch.GetKeyStats()
	if stats[0].ConsecutiveFailure != 0 {
		t.Fatalf("expected consecutive failures reset, got %d", stats[0].ConsecutiveFailure)
	}
	if stats[0].SuccessRequests != 1 || stats[0].FailureRequests != 1 {
		t.Fatalf("unexpected counters: %#v", stats[0])
	}
}
