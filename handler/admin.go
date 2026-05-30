package handler

import (
	"api-in-one/config"
	"api-in-one/model"
	"api-in-one/relay"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

// Admin handles admin API endpoints.
type Admin struct {
	pool *relay.Pool
}

func NewAdmin(pool *relay.Pool) *Admin {
	return &Admin{pool: pool}
}

// ChannelStatus is the JSON representation of a channel for admin API.
type ChannelStatus struct {
	Name          string            `json:"name"`
	Type          string            `json:"type"`
	BaseURL       string            `json:"base_url"`
	BaseURLClaude string            `json:"base_url_claude,omitempty"`
	BaseURLGemini string            `json:"base_url_gemini,omitempty"`
	KeyCount      int               `json:"key_count"`
	MaskedKeys    []string          `json:"masked_keys"`
	KeyStats      []KeyStatus       `json:"key_stats"`
	Models        []string          `json:"models"`
	ModelMapping  map[string]string `json:"model_mapping"`
	Priority      int               `json:"priority"`
	Weight        int               `json:"weight"`
	Enabled       bool              `json:"enabled"`
	Healthy       bool              `json:"healthy"`
}

type KeyStatus struct {
	Index              int    `json:"index"`
	MaskedKey          string `json:"masked_key"`
	TotalRequests      int64  `json:"total_requests"`
	SuccessRequests    int64  `json:"success_requests"`
	FailureRequests    int64  `json:"failure_requests"`
	ConsecutiveFailure int64  `json:"consecutive_failure"`
	LastStatus         int    `json:"last_status"`
	LastError          string `json:"last_error,omitempty"`
	LastUsedAt         string `json:"last_used_at,omitempty"`
	LastLatencyMs      int64  `json:"last_latency_ms"`
	Healthy            bool   `json:"healthy"`
}

// ListChannels returns all configured channels and their status.
func (h *Admin) ListChannels(c *gin.Context) {
	channels := h.pool.GetChannels()
	var result []ChannelStatus
	for _, ch := range channels {
		result = append(result, ChannelStatus{
			Name:          ch.Name,
			Type:          ch.Type,
			BaseURL:       ch.BaseURL,
			BaseURLClaude: ch.BaseURLClaude,
			BaseURLGemini: ch.BaseURLGemini,
			KeyCount:      len(ch.Keys),
			MaskedKeys:    maskKeys(ch.Keys),
			KeyStats:      buildKeyStatus(ch.GetKeyStats()),
			Models:        ch.Models,
			ModelMapping:  ch.ModelMapping,
			Priority:      ch.Priority,
			Weight:        ch.Weight,
			Enabled:       ch.Enabled,
			Healthy:       ch.IsHealthy(),
		})
	}
	c.JSON(http.StatusOK, gin.H{
		"channels": result,
		"total":    len(result),
	})
}

func buildKeyStatus(stats []model.KeyStats) []KeyStatus {
	result := make([]KeyStatus, 0, len(stats))
	for _, stat := range stats {
		result = append(result, KeyStatus{
			Index:              stat.Index,
			MaskedKey:          stat.MaskedKey,
			TotalRequests:      stat.TotalRequests,
			SuccessRequests:    stat.SuccessRequests,
			FailureRequests:    stat.FailureRequests,
			ConsecutiveFailure: stat.ConsecutiveFailure,
			LastStatus:         stat.LastStatus,
			LastError:          stat.LastError,
			LastUsedAt:         stat.LastUsedAt,
			LastLatencyMs:      stat.LastLatencyMs,
			Healthy:            stat.ConsecutiveFailure < 3,
		})
	}
	return result
}

// CreateChannel adds a new channel.
func (h *Admin) CreateChannel(c *gin.Context) {
	var ch config.ChannelConfig
	if err := c.ShouldBindJSON(&ch); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request: " + err.Error()})
		return
	}
	if ch.Name == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "name is required"})
		return
	}
	if ch.Type == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "type is required"})
		return
	}
	if ch.BaseURL == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "base_url is required"})
		return
	}
	if len(ch.Keys) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "at least one key is required"})
		return
	}

	if err := config.AddChannel(ch); err != nil {
		c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
		return
	}

	h.rebuildPool()
	c.JSON(http.StatusCreated, gin.H{"message": "channel created", "name": ch.Name})
}

func findChannelConfig(name string) *config.ChannelConfig {
	for _, ch := range config.GetChannels() {
		if ch.Name == name {
			return &ch
		}
	}
	return nil
}

func maskKeys(keys []string) []string {
	masked := make([]string, 0, len(keys))
	for _, key := range keys {
		masked = append(masked, maskKey(key))
	}
	return masked
}

func maskKey(key string) string {
	if len(key) <= 8 {
		return "****"
	}
	return key[:4] + "****" + key[len(key)-4:]
}

// UpdateChannel updates an existing channel by name.
func (h *Admin) UpdateChannel(c *gin.Context) {
	name := c.Param("name")
	var ch config.ChannelConfig
	if err := c.ShouldBindJSON(&ch); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request: " + err.Error()})
		return
	}
	if ch.BaseURL == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "base_url is required"})
		return
	}
	if len(ch.Keys) == 0 {
		existing := findChannelConfig(name)
		if existing == nil || len(existing.Keys) == 0 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "at least one key is required"})
			return
		}
		ch.Keys = existing.Keys
	}

	if err := config.UpdateChannel(name, ch); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}

	h.rebuildPool()
	c.JSON(http.StatusOK, gin.H{"message": "channel updated", "name": name})
}

// UpdateChannelKeys replaces only the upstream API keys for a channel.
func (h *Admin) UpdateChannelKeys(c *gin.Context) {
	name := c.Param("name")
	var req struct {
		Keys interface{} `json:"keys"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request: " + err.Error()})
		return
	}
	keys := parseKeys(req.Keys)
	if len(keys) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "at least one key is required"})
		return
	}
	if err := config.UpdateChannelKeys(name, keys); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}
	h.rebuildPool()
	c.JSON(http.StatusOK, gin.H{
		"message":     "channel keys updated",
		"name":        name,
		"key_count":   len(keys),
		"masked_keys": maskKeys(keys),
	})
}

func parseKeys(raw interface{}) []string {
	seen := map[string]bool{}
	var keys []string
	add := func(key string) {
		key = strings.TrimSpace(key)
		if key == "" || seen[key] {
			return
		}
		seen[key] = true
		keys = append(keys, key)
	}
	switch v := raw.(type) {
	case string:
		for _, part := range strings.FieldsFunc(v, func(r rune) bool {
			return r == '\n' || r == '\r' || r == ',' || r == ';'
		}) {
			add(part)
		}
	case []interface{}:
		for _, item := range v {
			if s, ok := item.(string); ok {
				add(s)
			}
		}
	case []string:
		for _, item := range v {
			add(item)
		}
	}
	return keys
}

// DeleteChannel removes a channel by name.
func (h *Admin) DeleteChannel(c *gin.Context) {
	name := c.Param("name")
	if err := config.DeleteChannel(name); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}

	h.rebuildPool()
	c.JSON(http.StatusOK, gin.H{"message": "channel deleted", "name": name})
}

// ReloadConfig hot-reloads the configuration file.
func (h *Admin) ReloadConfig(c *gin.Context) {
	if err := config.Reload(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to reload config: " + err.Error()})
		return
	}
	h.rebuildPool()
	c.JSON(http.StatusOK, gin.H{"message": "config reloaded successfully"})
}

// GetSettings returns the server settings (admin_key, access_keys).
func (h *Admin) GetSettings(c *gin.Context) {
	cfg := config.Get()
	c.JSON(http.StatusOK, gin.H{
		"port":        cfg.Server.Port,
		"admin_key":   cfg.Server.AdminKey,
		"access_keys": cfg.Server.AccessKeys,
	})
}

// FetchModels calls the upstream API to get available models.
func (h *Admin) FetchModels(c *gin.Context) {
	var req struct {
		Type    string `json:"type"`
		BaseURL string `json:"base_url"`
		Key     string `json:"key"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request: " + err.Error()})
		return
	}
	if req.BaseURL == "" || req.Key == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "base_url and key are required"})
		return
	}

	client := &http.Client{Timeout: 15 * time.Second}
	var models []string

	switch req.Type {
	case "claude":
		models = []string{
			"claude-opus-4-20250514",
			"claude-sonnet-4-20250514",
			"claude-haiku-4-20250514",
		}
	case "gemini":
		url := strings.TrimRight(req.BaseURL, "/") + "/v1beta/models?key=" + req.Key
		httpResp, err := client.Get(url)
		if err != nil {
			c.JSON(http.StatusBadGateway, gin.H{"error": "failed to fetch models: " + err.Error()})
			return
		}
		defer httpResp.Body.Close()
		body, _ := io.ReadAll(httpResp.Body)
		if httpResp.StatusCode != http.StatusOK {
			c.JSON(http.StatusBadGateway, gin.H{"error": fmt.Sprintf("upstream error (%d): %s", httpResp.StatusCode, string(body))})
			return
		}
		models = parseGeminiModels(body)
	default: // openai compatible
		url := strings.TrimRight(req.BaseURL, "/") + "/models"
		httpReq, _ := http.NewRequest("GET", url, nil)
		httpReq.Header.Set("Authorization", "Bearer "+req.Key)
		httpResp, err := client.Do(httpReq)
		if err != nil {
			c.JSON(http.StatusBadGateway, gin.H{"error": "failed to fetch models: " + err.Error()})
			return
		}
		defer httpResp.Body.Close()
		body, _ := io.ReadAll(httpResp.Body)
		if httpResp.StatusCode != http.StatusOK {
			c.JSON(http.StatusBadGateway, gin.H{"error": fmt.Sprintf("upstream error (%d): %s", httpResp.StatusCode, string(body))})
			return
		}
		models = parseOpenAIModels(body)
	}

	c.JSON(http.StatusOK, gin.H{"models": models})
}

// rebuildPool rebuilds the channel pool from current config.
func (h *Admin) rebuildPool() {
	cfg := config.Get()
	channels := h.buildChannels(cfg)
	h.pool.UpdateChannels(channels)
}

func (h *Admin) buildChannels(cfg config.Config) []*relay.Channel {
	var channels []*relay.Channel
	for _, cc := range cfg.Channels {
		channels = append(channels, relay.NewChannelFromConfig(cc))
	}
	return channels
}

func parseOpenAIModels(body []byte) []string {
	var resp struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil
	}
	var models []string
	for _, m := range resp.Data {
		models = append(models, m.ID)
	}
	return models
}

func parseGeminiModels(body []byte) []string {
	var resp struct {
		Models []struct {
			Name string `json:"name"`
		} `json:"models"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil
	}
	var models []string
	for _, m := range resp.Models {
		models = append(models, strings.TrimPrefix(m.Name, "models/"))
	}
	return models
}

// GetStats returns request statistics.
func (h *Admin) GetStats(c *gin.Context) {
	c.JSON(http.StatusOK, globalLogStore.stats())
}

// GetLogs returns recent request logs.
func (h *Admin) GetLogs(c *gin.Context) {
	n := 100
	if v := c.Query("limit"); v != "" {
		if parsed, err := strconv.Atoi(v); err == nil && parsed > 0 {
			n = parsed
		}
	}
	c.JSON(http.StatusOK, gin.H{
		"logs":  globalLogStore.recent(n),
		"total": globalLogStore.total,
	})
}
