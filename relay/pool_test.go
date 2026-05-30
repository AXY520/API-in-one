package relay

import (
	"api-in-one/model"
	"testing"
)

func TestSelectChannelUsesLowestPriority(t *testing.T) {
	high := model.NewChannel("high", "openai", "https://high.example", "", "", []string{"k"}, []string{"m"}, nil, 20, 100)
	low := model.NewChannel("low", "openai", "https://low.example", "", "", []string{"k"}, []string{"m"}, nil, 10, 100)
	pool := NewPool([]*model.Channel{high, low})

	for i := 0; i < 5; i++ {
		ch, _, err := pool.SelectChannel("m")
		if err != nil {
			t.Fatalf("select channel: %v", err)
		}
		if ch.Name != "low" {
			t.Fatalf("expected low priority channel, got %s", ch.Name)
		}
	}
}

func TestSelectChannelUsesWeightWithinSamePriority(t *testing.T) {
	one := model.NewChannel("one", "openai", "https://one.example", "", "", []string{"k"}, []string{"m"}, nil, 10, 1)
	three := model.NewChannel("three", "openai", "https://three.example", "", "", []string{"k"}, []string{"m"}, nil, 10, 3)
	pool := NewPool([]*model.Channel{one, three})

	counts := map[string]int{}
	for i := 0; i < 8; i++ {
		ch, _, err := pool.SelectChannel("m")
		if err != nil {
			t.Fatalf("select channel: %v", err)
		}
		counts[ch.Name]++
	}

	if counts["one"] != 2 || counts["three"] != 6 {
		t.Fatalf("expected 1:3 weighted selection over 8 requests, got %#v", counts)
	}
}

func TestGetAvailableModelsHidesMappedUpstreamIDs(t *testing.T) {
	ch := model.NewChannel(
		"mapped",
		"openai",
		"https://mapped.example",
		"",
		"",
		[]string{"k"},
		[]string{"upstream-model", "direct-model"},
		map[string]string{"public-model": "upstream-model"},
		10,
		100,
	)
	pool := NewPool([]*model.Channel{ch})

	models := pool.GetAvailableModels()
	seen := map[string]bool{}
	for _, m := range models {
		seen[m.ID] = true
	}

	if seen["upstream-model"] {
		t.Fatalf("mapped upstream model should not be visible: %#v", seen)
	}
	if !seen["public-model"] || !seen["direct-model"] {
		t.Fatalf("expected alias and unmapped direct model to be visible: %#v", seen)
	}
}

func TestSelectChannelForProtocolRequiresChannelType(t *testing.T) {
	openai := model.NewChannel("openai", "openai", "https://openai.example", "", "", []string{"k"}, []string{"m"}, nil, 10, 100)
	claude := model.NewChannel("claude", "claude", "https://claude.example", "", "", []string{"k"}, []string{"m"}, nil, 10, 100)
	pool := NewPool([]*model.Channel{openai, claude})

	ch, _, err := pool.SelectChannelForProtocol("m", "claude")
	if err != nil {
		t.Fatalf("select claude channel: %v", err)
	}
	if ch.Name != "claude" {
		t.Fatalf("expected claude channel, got %s", ch.Name)
	}

	_, _, err = NewPool([]*model.Channel{openai}).SelectChannelForProtocol("m", "claude")
	if err != ErrNoAvailableChannel {
		t.Fatalf("expected no channel for missing protocol, got %v", err)
	}
}
