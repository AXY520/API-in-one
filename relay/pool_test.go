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
