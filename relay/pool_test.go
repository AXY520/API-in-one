package relay

import (
	"api-in-one/model"
	"testing"
)

func TestSelectChannelUsesLowestPriority(t *testing.T) {
	high := model.NewChannel("high", "openai", "https://high.example", "", "", false, []string{"k"}, []string{"m"}, nil, 20, 100)
	low := model.NewChannel("low", "openai", "https://low.example", "", "", false, []string{"k"}, []string{"m"}, nil, 10, 100)
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
	one := model.NewChannel("one", "openai", "https://one.example", "", "", false, []string{"k"}, []string{"m"}, nil, 10, 1)
	three := model.NewChannel("three", "openai", "https://three.example", "", "", false, []string{"k"}, []string{"m"}, nil, 10, 3)
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

func TestSelectChannelAlternatesEqualWeightChannels(t *testing.T) {
	a := model.NewChannel("a", "openai", "https://a.example", "", "", false, []string{"k"}, []string{"m"}, nil, 10, 100)
	b := model.NewChannel("b", "openai", "https://b.example", "", "", false, []string{"k"}, []string{"m"}, nil, 10, 100)
	pool := NewPool([]*model.Channel{a, b})

	var got []string
	for i := 0; i < 4; i++ {
		ch, _, err := pool.SelectChannel("m")
		if err != nil {
			t.Fatalf("select channel: %v", err)
		}
		got = append(got, ch.Name)
	}

	want := []string{"a", "b", "a", "b"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("expected smooth equal-weight rotation %v, got %v", want, got)
		}
	}
}

func TestGetAvailableModelsHidesMappedUpstreamIDs(t *testing.T) {
	ch := model.NewChannel(
		"mapped",
		"openai",
		"https://mapped.example",
		"",
		"",
		false,
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
	openai := model.NewChannel("openai", "openai", "https://openai.example", "", "", false, []string{"k"}, []string{"m"}, nil, 10, 100)
	claude := model.NewChannel("claude", "claude", "https://claude.example", "", "", false, []string{"k"}, []string{"m"}, nil, 10, 100)
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

func TestPeekChannelResolvesMappingWithoutAdvancingRoundRobin(t *testing.T) {
	xiaomi := model.NewChannel("小米", "openai", "https://xiaomi.example", "", "", false, []string{"k"}, []string{"mimo-v2.5-pro"}, map[string]string{"codex-alias": "mimo-v2.5-pro"}, 10, 100)
	other := model.NewChannel("other", "openai", "https://other.example", "", "", false, []string{"k"}, []string{"codex-alias"}, nil, 10, 100)
	pool := NewPool([]*model.Channel{xiaomi, other})

	ch, resolved, err := pool.PeekChannel("codex-alias")
	if err != nil {
		t.Fatal(err)
	}
	if ch.Name != "小米" || resolved != "mimo-v2.5-pro" {
		t.Fatalf("unexpected peek route: %s %s", ch.Name, resolved)
	}

	first, _, err := pool.SelectChannel("codex-alias")
	if err != nil {
		t.Fatal(err)
	}
	if first.Name != "小米" {
		t.Fatalf("peek should not advance route state, got first select %s", first.Name)
	}
}

func TestSelectResponsesChannelRequiresSupportFlag(t *testing.T) {
	chatOnly := model.NewChannel("chat-only", "openai", "https://chat.example", "", "", false, []string{"k"}, []string{"m"}, nil, 10, 100)
	native := model.NewChannel("native", "openai", "https://native.example", "", "", true, []string{"k"}, []string{"m"}, nil, 10, 100)
	pool := NewPool([]*model.Channel{chatOnly, native})

	ch, _, err := pool.SelectResponsesChannel("m")
	if err != nil {
		t.Fatal(err)
	}
	if ch.Name != "native" {
		t.Fatalf("expected native responses channel, got %s", ch.Name)
	}
}
