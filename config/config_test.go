package config

import (
	"testing"
	"time"
)

func TestApplyDefaultsPreservesExplicitDisabled(t *testing.T) {
	disabled := false
	cfg := Config{
		Channels: []ChannelConfig{{
			Name:    "disabled",
			Enabled: &disabled,
		}},
	}

	applyDefaults(&cfg)

	if cfg.Channels[0].Enabled == nil {
		t.Fatal("expected enabled to be set")
	}
	if *cfg.Channels[0].Enabled {
		t.Fatal("expected explicit enabled=false to be preserved")
	}
}

func TestApplyDefaultsEnablesMissingValue(t *testing.T) {
	cfg := Config{
		Channels: []ChannelConfig{{
			Name: "defaulted",
		}},
	}

	applyDefaults(&cfg)

	if cfg.Channels[0].Enabled == nil {
		t.Fatal("expected enabled to be defaulted")
	}
	if !*cfg.Channels[0].Enabled {
		t.Fatal("expected missing enabled to default to true")
	}
}

func TestAccessKeyExpired(t *testing.T) {
	now := time.Date(2026, 6, 10, 12, 0, 0, 0, time.UTC)

	if AccessKeyExpired(AccessKeyConfig{Key: "k"}, now) {
		t.Fatal("empty expires_at should not expire")
	}
	if !AccessKeyExpired(AccessKeyConfig{Key: "k", ExpiresAt: "2026-06-10T12:00:00Z"}, now) {
		t.Fatal("expires_at equal to now should expire")
	}
	if AccessKeyExpired(AccessKeyConfig{Key: "k", ExpiresAt: "2026-06-10T12:00:01Z"}, now) {
		t.Fatal("future expires_at should not expire")
	}
	if !AccessKeyExpired(AccessKeyConfig{Key: "k", ExpiresAt: "not-a-time"}, now) {
		t.Fatal("invalid expires_at should fail closed")
	}
}
