package relay

import (
	"api-in-one/config"
	"api-in-one/model"
	"api-in-one/relay/adaptor"
	"log/slog"
	"sync"
	"sync/atomic"
)

// Channel is an alias for model.Channel for convenience.
type Channel = model.Channel

// NewChannelFromConfig creates a Channel from a config.ChannelConfig.
func NewChannelFromConfig(cc config.ChannelConfig) *Channel {
	ch := model.NewChannel(cc.Name, cc.Type, cc.BaseURL, cc.BaseURLClaude, cc.BaseURLGemini, cc.Keys, cc.Models, cc.ModelMapping, cc.Priority, cc.Weight)
	if cc.Enabled != nil {
		ch.Enabled = *cc.Enabled
	}
	ch.SetDisabledKeys(cc.DisabledKeys)
	return ch
}

// Pool manages channels and provides model-based routing with round-robin.
type Pool struct {
	channels []*model.Channel
	adaptors map[string]adaptor.Adaptor // type -> adaptor
	mu       sync.RWMutex
	rrIndex  atomic.Uint64
}

// NewPool creates a Pool from config channels.
func NewPool(channels []*model.Channel) *Pool {
	return &Pool{
		channels: channels,
		adaptors: map[string]adaptor.Adaptor{
			"openai": &adaptor.OpenAIAdaptor{},
			"claude": &adaptor.ClaudeAdaptor{},
			"gemini": &adaptor.GeminiAdaptor{},
		},
	}
}

// GetAdaptor returns the adaptor for the given channel type.
func (p *Pool) GetAdaptor(typ string) adaptor.Adaptor {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.adaptors[typ]
}

// SelectChannel finds the best channel for the given model.
// Returns the channel, resolved upstream model name, and an error if none available.
func (p *Pool) SelectChannel(requestedModel string) (*model.Channel, string, error) {
	return p.selectChannel(requestedModel, "")
}

func (p *Pool) SelectChannelForProtocol(requestedModel string, protocol string) (*model.Channel, string, error) {
	return p.selectChannel(requestedModel, protocol)
}

func (p *Pool) selectChannel(requestedModel string, protocol string) (*model.Channel, string, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()

	type candidate struct {
		channel *model.Channel
		model   string // resolved upstream model id
	}
	var candidates []candidate

	for _, ch := range p.channels {
		if !ch.IsHealthy() {
			continue
		}
		if protocol != "" && ch.Type != protocol {
			continue
		}
		if ch.HasModel(requestedModel) {
			resolved := ch.ResolveModel(requestedModel)
			candidates = append(candidates, candidate{ch, resolved})
		}
	}

	if len(candidates) == 0 {
		return nil, "", ErrNoAvailableChannel
	}

	lowestPriority := candidates[0].channel.Priority
	for _, c := range candidates[1:] {
		if c.channel.Priority < lowestPriority {
			lowestPriority = c.channel.Priority
		}
	}

	var weighted []candidate
	for _, c := range candidates {
		if c.channel.Priority == lowestPriority {
			weighted = append(weighted, c)
		}
	}

	totalWeight := 0
	for _, c := range weighted {
		weight := c.channel.Weight
		if weight <= 0 {
			weight = 1
		}
		totalWeight += weight
	}
	if totalWeight <= 0 {
		totalWeight = len(weighted)
	}

	idx := int((p.rrIndex.Add(1) - 1) % uint64(totalWeight))
	selected := weighted[0]
	for _, c := range weighted {
		weight := c.channel.Weight
		if weight <= 0 {
			weight = 1
		}
		if idx < weight {
			selected = c
			break
		}
		idx -= weight
	}
	return selected.channel, selected.model, nil
}

// UpdateChannels replaces the channel list (for hot reload).
func (p *Pool) UpdateChannels(channels []*model.Channel) {
	p.mu.Lock()
	defer p.mu.Unlock()
	// Reset health for all new channels so previously unhealthy ones get a fresh start
	for _, ch := range channels {
		ch.ResetHealth()
	}
	p.channels = channels
	slog.Info("pool updated", "channels", len(channels))
}

// GetChannels returns all channels (for admin API).
func (p *Pool) GetChannels() []*model.Channel {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.channels
}

// GetAvailableModels returns client-visible model names across healthy channels.
func (p *Pool) GetAvailableModels() []model.ModelObject {
	p.mu.RLock()
	defer p.mu.RUnlock()
	seen := make(map[string]bool)
	var models []model.ModelObject

	for _, ch := range p.channels {
		if !ch.IsHealthy() {
			continue
		}
		mappedUpstream := make(map[string]bool)
		for _, upstream := range ch.ModelMapping {
			mappedUpstream[upstream] = true
		}
		for _, m := range ch.Models {
			if mappedUpstream[m] {
				continue
			}
			if !seen[m] {
				seen[m] = true
				models = append(models, model.ModelObject{
					ID:      m,
					Object:  "model",
					Created: 1700000000,
					OwnedBy: ch.Name,
				})
			}
		}
		for alias := range ch.ModelMapping {
			if !seen[alias] {
				seen[alias] = true
				models = append(models, model.ModelObject{
					ID:      alias,
					Object:  "model",
					Created: 1700000000,
					OwnedBy: ch.Name + " (alias)",
				})
			}
		}
	}
	return models
}
