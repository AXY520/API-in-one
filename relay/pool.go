package relay

import (
	"api-in-one/config"
	"api-in-one/model"
	"api-in-one/relay/adaptor"
	"log/slog"
	"sort"
	"strconv"
	"strings"
	"sync"
)

// Channel is an alias for model.Channel for convenience.
type Channel = model.Channel

// NewChannelFromConfig creates a Channel from a config.ChannelConfig.
func NewChannelFromConfig(cc config.ChannelConfig) *Channel {
	ch := model.NewChannel(cc.Name, cc.Type, cc.BaseURL, cc.BaseURLClaude, cc.BaseURLGemini, cc.SupportsResponses, cc.Keys, cc.Models, cc.ModelMapping, cc.Priority, cc.Weight, cc.KeyModels)
	ch.ResponsesOnly = cc.ResponsesOnly
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
	channels        []*model.Channel
	channelsByModel map[string][]*model.Channel
	adaptors        map[string]adaptor.Adaptor // type -> adaptor
	mu              sync.RWMutex
	routeStates     map[string]routeState
}

type routeCandidate struct {
	channel *model.Channel
	model   string // resolved upstream model id
	weight  int
}

type routeState struct {
	Cursor      int
	LastChannel string
	Signature   string
}

// NewPool creates a Pool from config channels.
func NewPool(channels []*model.Channel) *Pool {
	p := &Pool{
		channels:    channels,
		routeStates: make(map[string]routeState),
		adaptors: map[string]adaptor.Adaptor{
			"openai": &adaptor.OpenAIAdaptor{},
			"claude": &adaptor.ClaudeAdaptor{},
			"gemini": &adaptor.GeminiAdaptor{},
		},
	}
	p.rebuildModelIndexLocked()
	return p
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
	return p.selectChannel(requestedModel, "", nil)
}

func (p *Pool) SelectChannelExcluding(requestedModel string, excluded map[string]bool) (*model.Channel, string, error) {
	return p.selectChannel(requestedModel, "", excluded)
}

func (p *Pool) PeekChannel(requestedModel string) (*model.Channel, string, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.bestCandidateLocked(requestedModel, "")
}

func (p *Pool) PeekChannelForInboundProtocol(requestedModel string, protocol string) (*model.Channel, string, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.bestCandidateLocked(requestedModel, protocol+"-inbound")
}

func (p *Pool) SelectChannelForProtocol(requestedModel string, protocol string) (*model.Channel, string, error) {
	return p.selectChannel(requestedModel, protocol, nil)
}

func (p *Pool) SelectChannelForProtocolExcluding(requestedModel string, protocol string, excluded map[string]bool) (*model.Channel, string, error) {
	return p.selectChannel(requestedModel, protocol, excluded)
}

func (p *Pool) SelectChannelForInboundProtocol(requestedModel string, protocol string) (*model.Channel, string, error) {
	return p.selectChannel(requestedModel, protocol+"-inbound", nil)
}

func (p *Pool) SelectChannelForInboundProtocolExcluding(requestedModel string, protocol string, excluded map[string]bool) (*model.Channel, string, error) {
	return p.selectChannel(requestedModel, protocol+"-inbound", excluded)
}

func (p *Pool) SelectResponsesChannel(requestedModel string) (*model.Channel, string, error) {
	return p.selectChannel(requestedModel, "responses", nil)
}

func (p *Pool) SelectResponsesChannelExcluding(requestedModel string, excluded map[string]bool) (*model.Channel, string, error) {
	return p.selectChannel(requestedModel, "responses", excluded)
}

func (p *Pool) SelectAnyChannelExcluding(requestedModel string, excluded map[string]bool) (*model.Channel, string, error) {
	return p.selectChannel(requestedModel, "any-inbound", excluded)
}

func (p *Pool) SelectAnyChannelForInboundExcluding(requestedModel string, inboundProtocol string, excluded map[string]bool) (*model.Channel, string, error) {
	return p.selectChannel(requestedModel, "any:"+inboundProtocol+"-inbound", excluded)
}

func (p *Pool) selectChannel(requestedModel string, protocol string, excluded map[string]bool) (*model.Channel, string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	weighted, err := p.weightedCandidatesLocked(requestedModel, protocol, nil)
	if err != nil {
		return nil, "", err
	}

	weighted = normalizeCandidateWeights(weighted)
	schedule := expandRouteSchedule(weighted)
	if len(schedule) == 0 {
		return nil, "", ErrNoAvailableChannel
	}

	stateKey := routeStateKey(requestedModel, protocol)
	state := p.routeStates[stateKey]
	signature := routeScheduleSignature(schedule)
	start := state.Cursor
	if state.Signature != signature {
		start = p.startAfterLastChannel(schedule, state.LastChannel)
	}

	selected, cursor, ok := selectFromSchedule(schedule, start, excluded)
	if !ok && len(excluded) > 0 {
		selected, cursor, ok = selectFromSchedule(schedule, start, nil)
	}
	if !ok {
		return nil, "", ErrNoAvailableChannel
	}
	p.routeStates[stateKey] = routeState{
		Cursor:      cursor,
		LastChannel: selected.channel.Name,
		Signature:   signature,
	}
	return selected.channel, selected.model, nil
}

func (p *Pool) bestCandidateLocked(requestedModel string, protocol string) (*model.Channel, string, error) {
	weighted, err := p.weightedCandidatesLocked(requestedModel, protocol, nil)
	if err != nil {
		return nil, "", err
	}
	return weighted[0].channel, weighted[0].model, nil
}

func (p *Pool) weightedCandidatesLocked(requestedModel string, protocol string, excluded map[string]bool) ([]routeCandidate, error) {
	var candidates []routeCandidate

	candidateChannels := p.channelsByModel[requestedModel]
	for _, ch := range candidateChannels {
		if excluded != nil && excluded[ch.Name] {
			continue
		}
		if !ch.IsHealthy() {
			continue
		}
		selectionProtocol := protocol
		if strings.HasSuffix(selectionProtocol, "-inbound") {
			selectionProtocol = strings.TrimSuffix(selectionProtocol, "-inbound")
		}
		if selectionProtocol == "any" || strings.HasPrefix(selectionProtocol, "any:") {
			if !(ch.SupportsProtocol("openai") || ch.SupportsResponses || ch.SupportsProtocol("claude") || ch.SupportsProtocol("gemini") || ch.ResponsesOnly) {
				continue
			}
		} else if selectionProtocol == "responses" {
			if ch.Type != "openai" || !ch.SupportsResponses {
				continue
			}
		} else if selectionProtocol != "" && !ch.SupportsProtocol(selectionProtocol) && !ch.ResponsesOnly {
			continue
		}
		if protocol != "responses" && !strings.HasSuffix(protocol, "-inbound") && ch.ResponsesOnly {
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

	// Protocol Priority Routing: within the same priority, prefer channels that natively support the inbound protocol
	inboundProto := extractInboundProtocol(protocol)
	if inboundProto != "" && inboundProto != "any" {
		var nativeWeighted []routeCandidate
		for _, c := range weighted {
			isNative := false
			if inboundProto == "openai" {
				isNative = c.channel.SupportsProtocol("openai") && !c.channel.ResponsesOnly
			} else if inboundProto == "responses" {
				isNative = c.channel.SupportsResponses
			} else {
				isNative = c.channel.SupportsProtocol(inboundProto)
			}
			if c.channel.ResponsesOnly && inboundProto == "responses" {
				isNative = true
			}
			if isNative {
				nativeWeighted = append(nativeWeighted, c)
			}
		}
		if len(nativeWeighted) > 0 {
			weighted = nativeWeighted
		}
	}

	return weighted, nil
}

func extractInboundProtocol(protocol string) string {
	if protocol == "openai" {
		return "openai"
	}
	if protocol == "responses" {
		return "responses"
	}
	if protocol == "any-inbound" {
		return "openai"
	}
	if strings.HasPrefix(protocol, "any:") {
		p := strings.TrimPrefix(protocol, "any:")
		p = strings.TrimSuffix(p, "-inbound")
		return p
	}
	if strings.HasSuffix(protocol, "-inbound") {
		return strings.TrimSuffix(protocol, "-inbound")
	}
	return protocol
}

func normalizeCandidateWeights(candidates []routeCandidate) []routeCandidate {
	gcd := 0
	for i := range candidates {
		weight := candidates[i].channel.Weight
		if weight <= 0 {
			weight = 1
		}
		candidates[i].weight = weight
		if gcd == 0 {
			gcd = weight
		} else {
			gcd = intGCD(gcd, weight)
		}
	}
	if gcd <= 0 {
		gcd = 1
	}
	for i := range candidates {
		candidates[i].weight = candidates[i].weight / gcd
		if candidates[i].weight <= 0 {
			candidates[i].weight = 1
		}
	}
	return candidates
}

func expandRouteSchedule(candidates []routeCandidate) []routeCandidate {
	var schedule []routeCandidate
	for _, candidate := range candidates {
		weight := candidate.weight
		if weight <= 0 {
			weight = 1
		}
		for i := 0; i < weight; i++ {
			schedule = append(schedule, candidate)
		}
	}
	sort.SliceStable(schedule, func(i, j int) bool {
		if schedule[i].weight == schedule[j].weight {
			return false
		}
		return schedule[i].weight > schedule[j].weight
	})
	return schedule
}

func selectFromSchedule(schedule []routeCandidate, start int, excluded map[string]bool) (routeCandidate, int, bool) {
	if len(schedule) == 0 {
		return routeCandidate{}, 0, false
	}
	if start < 0 {
		start = 0
	}
	start = start % len(schedule)
	for offset := 0; offset < len(schedule); offset++ {
		index := (start + offset) % len(schedule)
		candidate := schedule[index]
		if excluded != nil && excluded[candidate.channel.Name] {
			continue
		}
		return candidate, (index + 1) % len(schedule), true
	}
	return routeCandidate{}, start, false
}

func (p *Pool) startAfterLastChannel(schedule []routeCandidate, lastChannel string) int {
	if len(schedule) == 0 || lastChannel == "" {
		return 0
	}
	lastOrder := -1
	for i, ch := range p.channels {
		if ch.Name == lastChannel {
			lastOrder = i
			break
		}
	}
	if lastOrder < 0 {
		return 0
	}
	bestIndex := -1
	bestOrder := len(p.channels) + 1
	for i, candidate := range schedule {
		order := p.channelOrder(candidate.channel.Name)
		if order > lastOrder && order < bestOrder {
			bestIndex = i
			bestOrder = order
		}
	}
	if bestIndex >= 0 {
		return bestIndex
	}
	bestOrder = len(p.channels) + 1
	for i, candidate := range schedule {
		order := p.channelOrder(candidate.channel.Name)
		if order >= 0 && order < bestOrder {
			bestIndex = i
			bestOrder = order
		}
	}
	if bestIndex >= 0 {
		return bestIndex
	}
	return 0
}

func (p *Pool) channelOrder(name string) int {
	for i, ch := range p.channels {
		if ch.Name == name {
			return i
		}
	}
	return -1
}

func routeScheduleSignature(schedule []routeCandidate) string {
	var b strings.Builder
	for _, c := range schedule {
		b.WriteByte('|')
		b.WriteString(c.channel.Name)
		b.WriteByte(':')
		b.WriteString(strconv.Itoa(c.weight))
	}
	return b.String()
}

func routeStateKey(requestedModel string, protocol string) string {
	var b strings.Builder
	b.WriteString(protocol)
	b.WriteByte('|')
	b.WriteString(requestedModel)
	return b.String()
}

func intGCD(a, b int) int {
	if a < 0 {
		a = -a
	}
	if b < 0 {
		b = -b
	}
	for b != 0 {
		a, b = b, a%b
	}
	if a == 0 {
		return 1
	}
	return a
}

func (p *Pool) rebuildModelIndexLocked() {
	p.channelsByModel = make(map[string][]*model.Channel)
	for _, ch := range p.channels {
		seen := make(map[string]bool)
		for _, m := range ch.Models {
			if !seen[m] {
				seen[m] = true
				p.channelsByModel[m] = append(p.channelsByModel[m], ch)
			}
		}
		for mappingSrc := range ch.ModelMapping {
			if !seen[mappingSrc] {
				seen[mappingSrc] = true
				p.channelsByModel[mappingSrc] = append(p.channelsByModel[mappingSrc], ch)
			}
		}
	}
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
	p.rebuildModelIndexLocked()
	p.routeStates = make(map[string]routeState)
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
	case strings.HasPrefix(id, "gpt-"), strings.HasPrefix(id, "o1"), strings.HasPrefix(id, "o3"), strings.HasPrefix(id, "o4"), strings.HasPrefix(id, "chatgpt-"), strings.Contains(id, "openai"):
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
	case strings.Contains(id, "glm"), strings.Contains(id, "chatglm"), strings.Contains(id, "codegeex"):
		return "Zhipu"
	case strings.Contains(id, "grok"):
		return "xAI"
	case strings.Contains(id, "command"), strings.Contains(id, "cohere"):
		return "Cohere"
	default:
		return "Other"
	}
}
