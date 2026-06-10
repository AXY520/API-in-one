package relay

import (
	"api-in-one/config"
	"api-in-one/model"
	"api-in-one/relay/adaptor"
	"log/slog"
	"strconv"
	"strings"
	"sync"
)

// Channel is an alias for model.Channel for convenience.
type Channel = model.Channel

// NewChannelFromConfig creates a Channel from a config.ChannelConfig.
func NewChannelFromConfig(cc config.ChannelConfig) *Channel {
	ch := model.NewChannel(cc.Name, cc.Type, cc.BaseURL, cc.BaseURLClaude, cc.BaseURLGemini, cc.SupportsResponses, cc.Keys, cc.Models, cc.ModelMapping, cc.Priority, cc.Weight, cc.KeyModels)
	ch.DisableMiMoCompat = cc.DisableMiMoCompat
	ch.Temporary = cc.Temporary
	if cc.Enabled != nil {
		ch.Enabled = *cc.Enabled
	}
	ch.SetDisabledKeys(cc.DisabledKeys)
	ch.SetDisabledModels(cc.DisabledModels)
	return ch
}

// Pool manages channels and provides model-based routing with round-robin.
type Pool struct {
	channels    []*model.Channel
	adaptors    map[string]adaptor.Adaptor // type -> adaptor
	mu          sync.RWMutex
	routeStates map[string]map[string]int
}

type routeCandidate struct {
	channel *model.Channel
	model   string // resolved upstream model id
	weight  int
}

// NewPool creates a Pool from config channels.
func NewPool(channels []*model.Channel) *Pool {
	return &Pool{
		channels:    channels,
		routeStates: make(map[string]map[string]int),
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

func (p *Pool) PeekChannel(requestedModel string) (*model.Channel, string, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.bestCandidateLocked(requestedModel, "")
}

func (p *Pool) SelectChannelForProtocol(requestedModel string, protocol string) (*model.Channel, string, error) {
	return p.selectChannel(requestedModel, protocol)
}

func (p *Pool) SelectResponsesChannel(requestedModel string) (*model.Channel, string, error) {
	return p.selectChannel(requestedModel, "responses")
}

func (p *Pool) selectChannel(requestedModel string, protocol string) (*model.Channel, string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	weighted, err := p.weightedCandidatesLocked(requestedModel, protocol)
	if err != nil {
		return nil, "", err
	}
	totalWeight := 0
	for i := range weighted {
		weight := weighted[i].channel.Weight
		if weight <= 0 {
			weight = 1
		}
		weighted[i].weight = weight
		totalWeight += weight
	}
	stateKey := routeStateKey(requestedModel, protocol, weighted)
	state := p.routeStates[stateKey]
	if state == nil {
		state = make(map[string]int, len(weighted))
		p.routeStates[stateKey] = state
	}
	selected := weighted[0]
	bestScore := 0
	for i, c := range weighted {
		state[c.channel.Name] += c.weight
		score := state[c.channel.Name]
		if i == 0 || score > bestScore {
			selected = c
			bestScore = score
		}
	}
	state[selected.channel.Name] -= totalWeight
	return selected.channel, selected.model, nil
}

func (p *Pool) bestCandidateLocked(requestedModel string, protocol string) (*model.Channel, string, error) {
	weighted, err := p.weightedCandidatesLocked(requestedModel, protocol)
	if err != nil {
		return nil, "", err
	}
	return weighted[0].channel, weighted[0].model, nil
}

func (p *Pool) weightedCandidatesLocked(requestedModel string, protocol string) ([]routeCandidate, error) {
	var candidates []routeCandidate

	for _, ch := range p.channels {
		if !ch.IsHealthy() {
			continue
		}
		if protocol == "responses" {
			if ch.Type != "openai" || !ch.SupportsResponses {
				continue
			}
		} else if protocol != "" && !ch.SupportsProtocol(protocol) {
			continue
		}
		if ch.HasModel(requestedModel) {
			resolved := ch.ResolveModel(requestedModel)
			candidates = append(candidates, routeCandidate{channel: ch, model: resolved})
		}
	}

	if len(candidates) == 0 {
		return nil, ErrNoAvailableChannel
	}

	hasTemporary := false
	for _, c := range candidates {
		if c.channel.Temporary {
			hasTemporary = true
			break
		}
	}
	if hasTemporary {
		filtered := candidates[:0]
		for _, c := range candidates {
			if c.channel.Temporary {
				filtered = append(filtered, c)
			}
		}
		candidates = filtered
	}

	lowestPriority := candidates[0].channel.Priority
	for _, c := range candidates[1:] {
		if c.channel.Priority < lowestPriority {
			lowestPriority = c.channel.Priority
		}
	}

	var weighted []routeCandidate
	for _, c := range candidates {
		if c.channel.Priority == lowestPriority {
			weighted = append(weighted, c)
		}
	}
	return weighted, nil
}

func routeStateKey(requestedModel string, protocol string, candidates []routeCandidate) string {
	var b strings.Builder
	b.WriteString(protocol)
	b.WriteByte('|')
	b.WriteString(requestedModel)
	for _, c := range candidates {
		b.WriteByte('|')
		b.WriteString(c.channel.Name)
		b.WriteByte(':')
		b.WriteString(strconv.Itoa(c.weight))
	}
	return b.String()
}

// UpdateChannels replaces the channel list (for hot reload).
func (p *Pool) UpdateChannels(channels []*model.Channel) {
	p.mu.Lock()
	defer p.mu.Unlock()
	oldStates := make(map[string]model.ChannelRuntimeState, len(p.channels))
	for _, ch := range p.channels {
		oldStates[ch.Name] = ch.SnapshotRuntimeState()
	}
	for _, ch := range channels {
		if state, ok := oldStates[ch.Name]; ok {
			ch.RestoreRuntimeState(state)
		}
	}
	p.channels = channels
	p.routeStates = make(map[string]map[string]int)
	slog.Info("pool updated", "channels", len(channels))
}

// GetChannels returns all channels (for admin API).
func (p *Pool) GetChannels() []*model.Channel {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.channels
}

func (p *Pool) ResetChannelKeyFailure(channelName string, keyIndex int) bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	for _, ch := range p.channels {
		if ch.Name == channelName {
			return ch.ResetKeyFailure(keyIndex)
		}
	}
	return false
}

func (p *Pool) ResetChannelModelFailure(channelName string, modelName string) bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	for _, ch := range p.channels {
		if ch.Name == channelName {
			return ch.ResetModelFailure(modelName)
		}
	}
	return false
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
			if !ch.HasModel(m) {
				continue
			}
			if !seen[m] {
				seen[m] = true
				models = append(models, model.ModelObject{
					ID:      m,
					Object:  "model",
					Created: 1700000000,
					OwnedBy: modelProvider(m),
				})
			}
		}
		for alias, upstream := range ch.ModelMapping {
			if !ch.HasModel(alias) {
				continue
			}
			if !seen[alias] {
				seen[alias] = true
				models = append(models, model.ModelObject{
					ID:      alias,
					Object:  "model",
					Created: 1700000000,
					OwnedBy: modelProvider(upstream),
				})
			}
		}
	}
	return models
}

func modelProvider(modelID string) string {
	id := strings.ToLower(modelID)
	switch {
	case strings.HasPrefix(id, "gpt-"), strings.HasPrefix(id, "o1"), strings.HasPrefix(id, "o3"), strings.HasPrefix(id, "o4"), strings.Contains(id, "openai"):
		return "OpenAI"
	case strings.HasPrefix(id, "claude"), strings.Contains(id, "anthropic"):
		return "Anthropic"
	case strings.HasPrefix(id, "gemini"), strings.HasPrefix(id, "models/gemini"), strings.Contains(id, "google"):
		return "Google"
	case strings.Contains(id, "deepseek"):
		return "DeepSeek"
	case strings.Contains(id, "qwen"), strings.Contains(id, "tongyi"):
		return "Alibaba"
	case strings.Contains(id, "mistral"), strings.Contains(id, "mixtral"), strings.Contains(id, "codestral"):
		return "Mistral"
	case strings.Contains(id, "llama"), strings.Contains(id, "meta-"):
		return "Meta"
	case strings.Contains(id, "mimo"), strings.Contains(id, "xiaomi"):
		return "Xiaomi"
	case strings.Contains(id, "doubao"), strings.Contains(id, "volc"):
		return "ByteDance"
	case strings.Contains(id, "ernie"), strings.Contains(id, "wenxin"):
		return "Baidu"
	case strings.Contains(id, "glm"), strings.Contains(id, "chatglm"):
		return "Zhipu"
	default:
		return "Other"
	}
}
