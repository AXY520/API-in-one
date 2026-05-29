package config

import "testing"

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
