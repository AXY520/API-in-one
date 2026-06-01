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
	Keys              []string
	DisabledKeys      map[string]bool
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
}

// NewChannel creates a Channel from config.
func NewChannel(name, typ, baseURL, baseURLClaude, baseURLGemini string, supportsResponses bool, keys, models []string, modelMapping map[string]string, priority, weight int) *Channel {
	if modelMapping == nil {
		modelMapping = make(map[string]string)
	}
	ch := &Channel{
		Name:              name,
		Type:              typ,
		BaseURL:           baseURL,
		BaseURLClaude:     baseURLClaude,
		BaseURLGemini:     baseURLGemini,
		SupportsResponses: supportsResponses,
		Keys:              keys,
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

// NextKey returns the next API key using round-robin.
func (c *Channel) NextKey() string {
	if len(c.Keys) == 0 {
		return ""
	}
	for i := 0; i < len(c.Keys); i++ {
		idx := c.keyIndex.Add(1)
		keyIdx := int((idx - 1) % uint64(len(c.Keys)))
		if c.IsKeyHealthy(keyIdx) {
			return c.Keys[keyIdx]
		}
	}
	idx := c.keyIndex.Add(1)
	return c.Keys[(idx-1)%uint64(len(c.Keys))]
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
	return !c.DisabledKeys[key] && c.KeyStats[index].ConsecutiveFailure < 3
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
		if err != nil {
			ks.LastError = err.Error()
		}
	} else {
		ks.SuccessRequests++
		ks.ConsecutiveFailure = 0
		ks.LastError = ""
	}
}

func (c *Channel) GetKeyStats() []KeyStats {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.ensureKeyStatsLocked()
	result := make([]KeyStats, len(c.KeyStats))
	copy(result, c.KeyStats)
	return result
}

// ResetHealth resets the failure count to zero.
func (c *Channel) ResetHealth() {
	c.failCount.Store(0)
}

// IsHealthy returns true if the channel hasn't exceeded failure threshold.
func (c *Channel) IsHealthy() bool {
	return c.Enabled && c.failCount.Load() < 5
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
