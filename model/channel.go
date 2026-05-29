package model

import (
	"sync"
	"sync/atomic"
)

// Channel represents a configured upstream API provider.
type Channel struct {
	Name          string
	Type          string // openai | claude | gemini
	BaseURL       string
	BaseURLClaude string // optional: Claude protocol URL
	BaseURLGemini string // optional: Gemini protocol URL
	Keys          []string
	Models        []string
	ModelMapping  map[string]string // alias → upstream model id
	Priority      int
	Weight        int
	Enabled       bool

	keyIndex  atomic.Uint64
	failCount atomic.Int32
	mu        sync.Mutex
}

// NewChannel creates a Channel from config.
func NewChannel(name, typ, baseURL, baseURLClaude, baseURLGemini string, keys, models []string, modelMapping map[string]string, priority, weight int) *Channel {
	if modelMapping == nil {
		modelMapping = make(map[string]string)
	}
	return &Channel{
		Name:          name,
		Type:          typ,
		BaseURL:       baseURL,
		BaseURLClaude: baseURLClaude,
		BaseURLGemini: baseURLGemini,
		Keys:          keys,
		Models:        models,
		ModelMapping:  modelMapping,
		Priority:      priority,
		Weight:        weight,
		Enabled:       true,
	}
}

// NextKey returns the next API key using round-robin.
func (c *Channel) NextKey() string {
	if len(c.Keys) == 0 {
		return ""
	}
	idx := c.keyIndex.Add(1)
	return c.Keys[(idx-1)%uint64(len(c.Keys))]
}

// RecordSuccess resets the failure count.
func (c *Channel) RecordSuccess() {
	c.failCount.Store(0)
}

// RecordFailure increments the failure count.
func (c *Channel) RecordFailure() {
	c.failCount.Add(1)
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
