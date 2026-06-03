package model

import (
	"errors"
	"testing"
	"time"
)

func TestNextKeySkipsUnhealthyKeys(t *testing.T) {
	ch := NewChannel("test", "openai", "https://example.com", "", "", false, []string{"key-a", "key-b"}, []string{"m"}, nil, 10, 100)
	for i := 0; i < 3; i++ {
		ch.RecordKeyResult("key-a", 401, time.Millisecond, errors.New("invalid key"))
	}

	for i := 0; i < 4; i++ {
		if got := ch.NextKey(); got != "key-b" {
			t.Fatalf("expected healthy key-b, got %q", got)
		}
	}
}

func TestNextKeyReturnsEmptyWhenAllKeysSuspended(t *testing.T) {
	ch := NewChannel("test", "openai", "https://example.com", "", "", false, []string{"key-a"}, []string{"m"}, nil, 10, 100)
	for i := 0; i < 3; i++ {
		ch.RecordKeyResult("key-a", 429, time.Millisecond, errors.New("rate limited"))
	}
	if got := ch.NextKey(); got != "" {
		t.Fatalf("expected no key while suspended, got %q", got)
	}
}

func TestNextKeySkipsDisabledKeys(t *testing.T) {
	ch := NewChannel("test", "openai", "https://example.com", "", "", false, []string{"key-a", "key-b"}, []string{"m"}, nil, 10, 100)
	ch.SetDisabledKeys([]string{"key-a"})

	for i := 0; i < 4; i++ {
		if got := ch.NextKey(); got != "key-b" {
			t.Fatalf("expected enabled key-b, got %q", got)
		}
	}
}

func TestNextKeyForModelUsesOnlyAllowedKeys(t *testing.T) {
	ch := NewChannel("test", "openai", "https://example.com", "", "", false,
		[]string{"key-a", "key-b"},
		[]string{"model-a", "model-b"},
		nil,
		10,
		100,
		map[string][]string{
			"key-a": {"model-a"},
			"key-b": {"model-b"},
		},
	)

	for i := 0; i < 4; i++ {
		if got := ch.NextKeyForModel("model-b"); got != "key-b" {
			t.Fatalf("expected key-b for model-b, got %q", got)
		}
	}
	if got := ch.NextKeyForModel("model-c"); got != "" {
		t.Fatalf("expected no key for unsupported model, got %q", got)
	}
}

func TestRecordKeyResultResetsConsecutiveFailures(t *testing.T) {
	ch := NewChannel("test", "openai", "https://example.com", "", "", false, []string{"key-a"}, []string{"m"}, nil, 10, 100)
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

func TestResetKeyFailureRestoresSuspendedKey(t *testing.T) {
	ch := NewChannel("test", "openai", "https://example.com", "", "", false, []string{"key-a", "key-b"}, []string{"m"}, nil, 10, 100)
	for i := 0; i < 3; i++ {
		ch.RecordKeyResult("key-a", 429, time.Millisecond, errors.New("rate limited"))
	}
	if ch.IsKeyHealthy(0) {
		t.Fatalf("expected key-a suspended before reset")
	}
	if !ch.ResetKeyFailure(0) {
		t.Fatalf("expected reset to succeed")
	}
	if !ch.IsKeyHealthy(0) {
		t.Fatalf("expected key-a healthy after reset")
	}
}

func TestChannelHealthUsesConfiguredFailureThreshold(t *testing.T) {
	oldThreshold, oldCooldown := KeyFailurePolicy()
	SetKeyFailurePolicy(2, oldCooldown)
	defer SetKeyFailurePolicy(oldThreshold, oldCooldown)

	ch := NewChannel("test", "openai", "https://example.com", "", "", false, []string{"key-a"}, []string{"m"}, nil, 10, 100)
	ch.RecordKeyResult("key-a", 502, time.Millisecond, errors.New("bad gateway"))
	ch.RecordFailure()
	if !ch.IsHealthy() {
		t.Fatalf("expected channel healthy before configured threshold")
	}
	ch.RecordKeyResult("key-a", 502, time.Millisecond, errors.New("bad gateway"))
	ch.RecordFailure()
	if ch.IsHealthy() {
		t.Fatalf("expected channel unhealthy at configured threshold")
	}
}

func TestResetKeyFailureRestoresChannelHealth(t *testing.T) {
	oldThreshold, oldCooldown := KeyFailurePolicy()
	SetKeyFailurePolicy(2, oldCooldown)
	defer SetKeyFailurePolicy(oldThreshold, oldCooldown)

	ch := NewChannel("test", "openai", "https://example.com", "", "", false, []string{"key-a"}, []string{"m"}, nil, 10, 100)
	for i := 0; i < 3; i++ {
		ch.RecordKeyResult("key-a", 429, time.Millisecond, errors.New("rate limited"))
	}
	ch.RecordFailure()
	ch.RecordFailure()
	if ch.IsHealthy() {
		t.Fatalf("expected channel unhealthy before reset")
	}
	if !ch.ResetKeyFailure(0) {
		t.Fatalf("expected reset to succeed")
	}
	if !ch.IsHealthy() {
		t.Fatalf("expected channel healthy after key reset")
	}
}

func TestSuspendedKeyRecoversAfterCooldown(t *testing.T) {
	oldThreshold, oldCooldown := KeyFailurePolicy()
	SetKeyFailurePolicy(3, time.Second)
	defer SetKeyFailurePolicy(oldThreshold, oldCooldown)

	ch := NewChannel("test", "openai", "https://example.com", "", "", false, []string{"key-a"}, []string{"m"}, nil, 10, 100)
	for i := 0; i < 3; i++ {
		ch.RecordKeyResult("key-a", 429, time.Millisecond, errors.New("rate limited"))
	}
	stats := ch.GetKeyStats()
	stats[0].SuspendedUntil = time.Now().Add(-time.Second).Format("2006-01-02 15:04:05")
	ch.RestoreRuntimeState(ChannelRuntimeState{KeyStats: map[string]KeyStats{"key-a": stats[0]}})
	if !ch.IsKeyHealthy(0) {
		t.Fatalf("expected key-a to recover after expired cooldown")
	}
	stats = ch.GetKeyStats()
	if stats[0].ConsecutiveFailure != 0 || stats[0].SuspendedUntil != "" {
		t.Fatalf("expected recovered stats, got %#v", stats[0])
	}
}
