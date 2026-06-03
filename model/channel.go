package model

import (
	"sync"
	"sync/atomic"
	"time"
)

// Channel represents a configured upstream API provider.
type Channel struct {
	Name              string
	Type              string // openai | claude | gemini
	BaseURL           string
	BaseURLClaude     string // optional: Claude protocol URL
	BaseURLGemini     string // optional: Gemini protocol URL
	SupportsResponses bool   // whether upstream accepts /v1/responses natively
	DisableMiMoCompat bool
	Keys              []string
	DisabledKeys      map[string]bool
	KeyModels         map[string][]string
	Models            []string
	ModelMapping      map[string]string // alias → upstream model id
	Priority          int
	Weight            int
	Enabled           bool
	KeyStats          []KeyStats

	keyIndex  atomic.Uint64
	failCount atomic.Int32
	mu        sync.Mutex
}

type KeyStats struct {
	Index              int
	MaskedKey          string
	Disabled           bool
	TotalRequests      int64
	SuccessRequests    int64
	FailureRequests    int64
	ConsecutiveFailure int64
	LastStatus         int
	LastError          string
	LastUsedAt         string
	LastLatencyMs      int64
	SuspendedUntil     string
}

type ChannelRuntimeState struct {
	KeyIndex  uint64
	FailCount int32
	KeyStats  map[string]KeyStats
}

var (
	keyFailureCooldownSeconds atomic.Int64
	keyFailureThreshold       atomic.Int64
)

func init() {
	SetKeyFailurePolicy(3, 10*time.Minute)
}

func SetKeyFailurePolicy(threshold int, cooldown time.Duration) {
	if threshold < 1 {
		threshold = 3
	}
	if cooldown < time.Second {
		cooldown = 10 * time.Minute
	}
	keyFailureThreshold.Store(int64(threshold))
	keyFailureCooldownSeconds.Store(int64(cooldown.Seconds()))
}

func KeyFailurePolicy() (int, time.Duration) {
	threshold := int(keyFailureThreshold.Load())
	if threshold < 1 {
		threshold = 3
	}
	seconds := keyFailureCooldownSeconds.Load()
	if seconds < 1 {
		seconds = int64((10 * time.Minute).Seconds())
	}
	return threshold, time.Duration(seconds) * time.Second
}

// NewChannel creates a Channel from config.
func NewChannel(name, typ, baseURL, baseURLClaude, baseURLGemini string, supportsResponses bool, keys, models []string, modelMapping map[string]string, priority, weight int, keyModels ...map[string][]string) *Channel {
	if modelMapping == nil {
		modelMapping = make(map[string]string)
	}
	keyModelPolicy := map[string][]string(nil)
	if len(keyModels) > 0 {
		keyModelPolicy = keyModels[0]
	}
	ch := &Channel{
		Name:              name,
		Type:              typ,
		BaseURL:           baseURL,
		BaseURLClaude:     baseURLClaude,
		BaseURLGemini:     baseURLGemini,
		SupportsResponses: supportsResponses,
		Keys:              keys,
		KeyModels:         keyModelPolicy,
		Models:            models,
		ModelMapping:      modelMapping,
		Priority:          priority,
		Weight:            weight,
		Enabled:           true,
	}
	ch.initKeyStats()
	return ch
}

func (c *Channel) SetDisabledKeys(keys []string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.DisabledKeys = make(map[string]bool, len(keys))
	for _, key := range keys {
		c.DisabledKeys[key] = true
	}
	c.ensureKeyStatsLocked()
}

func (c *Channel) DisabledKeyList() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	result := make([]string, 0, len(c.DisabledKeys))
	for _, key := range c.Keys {
		if c.DisabledKeys[key] {
			result = append(result, key)
		}
	}
	return result
}

func (c *Channel) SupportsProtocol(protocol string) bool {
	if protocol == "" || protocol == c.Type {
		return true
	}
	switch protocol {
	case "claude":
		return c.BaseURLClaude != ""
	case "gemini":
		return c.BaseURLGemini != ""
	default:
		return false
	}
}

// NextKey returns the next API key using round-robin.
func (c *Channel) NextKey() string {
	return c.NextKeyForModel("")
}

func (c *Channel) NextKeyForModel(modelName string) string {
	if len(c.Keys) == 0 {
		return ""
	}
	for i := 0; i < len(c.Keys); i++ {
		idx := c.keyIndex.Add(1)
		keyIdx := int((idx - 1) % uint64(len(c.Keys)))
		if c.IsKeyHealthy(keyIdx) && c.KeyCanUseModel(keyIdx, modelName) {
			return c.Keys[keyIdx]
		}
	}
	return ""
}

func (c *Channel) KeyIndex(key string) int {
	for i, k := range c.Keys {
		if k == key {
			return i
		}
	}
	return -1
}

func (c *Channel) IsKeyHealthy(index int) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.ensureKeyStatsLocked()
	if index < 0 || index >= len(c.KeyStats) {
		return false
	}
	key := c.Keys[index]
	if c.DisabledKeys[key] {
		return false
	}
	return c.keyHealthyLocked(index, time.Now())
}

func (c *Channel) KeyCanUseModel(index int, modelName string) bool {
	if modelName == "" {
		return true
	}
	if index < 0 || index >= len(c.Keys) {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	models := c.KeyModels[c.Keys[index]]
	if len(models) == 0 {
		return true
	}
	for _, allowed := range models {
		if allowed == modelName {
			return true
		}
	}
	return false
}

// RecordSuccess resets the failure count.
func (c *Channel) RecordSuccess() {
	c.failCount.Store(0)
}

// RecordFailure increments the failure count.
func (c *Channel) RecordFailure() {
	c.failCount.Add(1)
}

func (c *Channel) RecordKeyResult(key string, status int, latency time.Duration, err error) {
	idx := c.KeyIndex(key)
	if idx < 0 {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.ensureKeyStatsLocked()
	ks := &c.KeyStats[idx]
	ks.TotalRequests++
	ks.LastStatus = status
	ks.LastUsedAt = time.Now().Format("2006-01-02 15:04:05")
	ks.LastLatencyMs = latency.Milliseconds()
	if err != nil || status < 200 || status >= 400 {
		ks.FailureRequests++
		ks.ConsecutiveFailure++
		threshold, cooldown := KeyFailurePolicy()
		if ks.ConsecutiveFailure >= int64(threshold) {
			ks.SuspendedUntil = time.Now().Add(cooldown).Format("2006-01-02 15:04:05")
		}
		if err != nil {
			ks.LastError = err.Error()
		}
	} else {
		ks.SuccessRequests++
		ks.ConsecutiveFailure = 0
		ks.LastError = ""
		ks.SuspendedUntil = ""
	}
}

func (c *Channel) ResetKeyFailure(index int) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.ensureKeyStatsLocked()
	if index < 0 || index >= len(c.KeyStats) {
		return false
	}
	c.KeyStats[index].ConsecutiveFailure = 0
	c.KeyStats[index].LastError = ""
	c.KeyStats[index].SuspendedUntil = ""
	c.failCount.Store(0)
	return true
}

func (c *Channel) keyHealthyLocked(index int, now time.Time) bool {
	ks := &c.KeyStats[index]
	threshold, cooldown := KeyFailurePolicy()
	if ks.ConsecutiveFailure < int64(threshold) {
		return true
	}
	if ks.SuspendedUntil == "" {
		ks.SuspendedUntil = now.Add(cooldown).Format("2006-01-02 15:04:05")
		return false
	}
	until, err := time.ParseInLocation("2006-01-02 15:04:05", ks.SuspendedUntil, time.Local)
	if err != nil || now.Before(until) {
		return false
	}
	ks.ConsecutiveFailure = 0
	ks.LastError = ""
	ks.SuspendedUntil = ""
	return true
}

func (c *Channel) GetKeyStats() []KeyStats {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.ensureKeyStatsLocked()
	now := time.Now()
	for i, key := range c.Keys {
		if !c.DisabledKeys[key] {
			c.keyHealthyLocked(i, now)
		}
	}
	result := make([]KeyStats, len(c.KeyStats))
	copy(result, c.KeyStats)
	return result
}

func (c *Channel) SnapshotRuntimeState() ChannelRuntimeState {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.ensureKeyStatsLocked()
	state := ChannelRuntimeState{
		KeyIndex:  c.keyIndex.Load(),
		FailCount: c.failCount.Load(),
		KeyStats:  make(map[string]KeyStats, len(c.Keys)),
	}
	for i, key := range c.Keys {
		state.KeyStats[key] = c.KeyStats[i]
	}
	return state
}

func (c *Channel) RestoreRuntimeState(state ChannelRuntimeState) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.ensureKeyStatsLocked()
	c.keyIndex.Store(state.KeyIndex)
	c.failCount.Store(state.FailCount)
	for i, key := range c.Keys {
		stat, ok := state.KeyStats[key]
		if !ok {
			continue
		}
		stat.Index = i
		stat.MaskedKey = maskKey(key)
		stat.Disabled = c.DisabledKeys[key]
		c.KeyStats[i] = stat
	}
}

// ResetHealth resets the failure count to zero.
func (c *Channel) ResetHealth() {
	c.failCount.Store(0)
}

// IsHealthy returns true if the channel hasn't exceeded failure threshold.
func (c *Channel) IsHealthy() bool {
	if !c.Enabled {
		return false
	}
	threshold, _ := KeyFailurePolicy()
	if c.failCount.Load() < int32(threshold) {
		return true
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.ensureKeyStatsLocked()
	now := time.Now()
	for i, key := range c.Keys {
		if !c.DisabledKeys[key] && c.keyHealthyLocked(i, now) {
			c.failCount.Store(0)
			return true
		}
	}
	return false
}

// HasModel checks if this channel can serve the given model (directly or via mapping).
func (c *Channel) HasModel(model string) bool {
	// Check if it's directly in the models list
	for _, m := range c.Models {
		if m == model {
			return true
		}
	}
	// Check if it's an alias that maps to a model in the list
	if upstream, ok := c.ModelMapping[model]; ok {
		for _, m := range c.Models {
			if m == upstream {
				return true
			}
		}
	}
	return false
}

// ResolveModel resolves a model name through this channel's mapping.
// Returns the upstream model id to actually request.
func (c *Channel) ResolveModel(model string) string {
	if upstream, ok := c.ModelMapping[model]; ok {
		return upstream
	}
	return model
}

// GetBaseURL returns the appropriate base URL for the given protocol.
// Falls back to BaseURL if the protocol-specific URL is not configured.
func (c *Channel) GetBaseURL(protocol string) string {
	switch protocol {
	case "claude":
		if c.BaseURLClaude != "" {
			return c.BaseURLClaude
		}
	case "gemini":
		if c.BaseURLGemini != "" {
			return c.BaseURLGemini
		}
	}
	return c.BaseURL
}

func (c *Channel) initKeyStats() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.ensureKeyStatsLocked()
}

func (c *Channel) ensureKeyStatsLocked() {
	if c.DisabledKeys == nil {
		c.DisabledKeys = make(map[string]bool)
	}
	if len(c.KeyStats) == len(c.Keys) {
		for i := range c.KeyStats {
			c.KeyStats[i].Index = i
			c.KeyStats[i].MaskedKey = maskKey(c.Keys[i])
			c.KeyStats[i].Disabled = c.DisabledKeys[c.Keys[i]]
		}
		return
	}
	oldByKey := make(map[string]KeyStats)
	for _, stat := range c.KeyStats {
		oldByKey[stat.MaskedKey] = stat
	}
	c.KeyStats = make([]KeyStats, len(c.Keys))
	for i, key := range c.Keys {
		masked := maskKey(key)
		stat := oldByKey[masked]
		stat.Index = i
		stat.MaskedKey = masked
		stat.Disabled = c.DisabledKeys[key]
		c.KeyStats[i] = stat
	}
}

func maskKey(key string) string {
	if len(key) <= 8 {
		return "****"
	}
	return key[:4] + "****" + key[len(key)-4:]
}
