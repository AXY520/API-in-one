package handler

import (
	"api-in-one/config"
	"api-in-one/model"
	"api-in-one/relay"
	"api-in-one/relay/adaptor"
	"context"
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

type KeyProbeResult struct {
	Index     int    `json:"index"`
	MaskedKey string `json:"masked_key"`
	Disabled  bool   `json:"disabled"`
	OK        bool   `json:"ok"`
	Status    int    `json:"status"`
	LatencyMs int64  `json:"latency_ms"`
	Error     string `json:"error,omitempty"`
	TestedAt  string `json:"tested_at"`
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
	Disabled           bool   `json:"disabled"`
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
			Disabled:           stat.Disabled,
			TotalRequests:      stat.TotalRequests,
			SuccessRequests:    stat.SuccessRequests,
			FailureRequests:    stat.FailureRequests,
			ConsecutiveFailure: stat.ConsecutiveFailure,
			LastStatus:         stat.LastStatus,
			LastError:          stat.LastError,
			LastUsedAt:         stat.LastUsedAt,
			LastLatencyMs:      stat.LastLatencyMs,
			Healthy:            !stat.Disabled && stat.ConsecutiveFailure < 3,
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

// GetChannelKeys returns the raw upstream API keys for a channel.
func (h *Admin) GetChannelKeys(c *gin.Context) {
	name := c.Param("name")
	ch := findChannelConfig(name)
	if ch == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": fmt.Sprintf("channel %q not found", name)})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"name":          name,
		"keys":          ch.Keys,
		"key_count":     len(ch.Keys),
		"masked_keys":   maskKeys(ch.Keys),
		"disabled_keys": maskKeys(ch.DisabledKeys),
	})
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

// UpdateChannelKeyState enables or disables one upstream key by index.
func (h *Admin) UpdateChannelKeyState(c *gin.Context) {
	name := c.Param("name")
	index, err := strconv.Atoi(c.Param("index"))
	if err != nil || index < 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid key index"})
		return
	}
	var req struct {
		Disabled bool `json:"disabled"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request: " + err.Error()})
		return
	}
	ch := findChannelConfig(name)
	if ch == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": fmt.Sprintf("channel %q not found", name)})
		return
	}
	if index >= len(ch.Keys) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "key index out of range"})
		return
	}
	disabled := make(map[string]bool, len(ch.DisabledKeys)+1)
	for _, key := range ch.DisabledKeys {
		disabled[key] = true
	}
	key := ch.Keys[index]
	if req.Disabled {
		disabled[key] = true
	} else {
		delete(disabled, key)
	}
	disabledKeys := make([]string, 0, len(ch.Keys))
	for _, key := range ch.Keys {
		if disabled[key] {
			disabledKeys = append(disabledKeys, key)
		}
	}
	if err := config.UpdateChannelDisabledKeys(name, disabledKeys); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}
	h.rebuildPool()
	c.JSON(http.StatusOK, gin.H{
		"message":       "channel key state updated",
		"name":          name,
		"key_index":     index,
		"disabled":      req.Disabled,
		"disabled_keys": maskKeys(disabledKeys),
	})
}

// ProbeChannelKeys sends a tiny non-stream request with every key in a channel.
func (h *Admin) ProbeChannelKeys(c *gin.Context) {
	name := c.Param("name")
	ch := findChannelConfig(name)
	if ch == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": fmt.Sprintf("channel %q not found", name)})
		return
	}
	if len(ch.Models) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "channel has no models"})
		return
	}
	protocol := strings.TrimSpace(c.Query("protocol"))
	if protocol == "" {
		protocol = ch.Type
	}
	modelName := strings.TrimSpace(c.Query("model"))
	if modelName == "" {
		modelName = ch.Models[0]
	}
	indexFilter := -1
	if v := c.Query("index"); v != "" {
		parsed, err := strconv.Atoi(v)
		if err != nil || parsed < 0 || parsed >= len(ch.Keys) {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid key index"})
			return
		}
		indexFilter = parsed
	}

	disabled := make(map[string]bool, len(ch.DisabledKeys))
	for _, key := range ch.DisabledKeys {
		disabled[key] = true
	}

	ad := h.pool.GetAdaptor(protocol)
	if ad == nil && protocol == "responses" {
		ad = h.pool.GetAdaptor("openai")
	}
	if ad == nil {
		ad = h.pool.GetAdaptor(ch.Type)
	}
	if ad == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "no adaptor for protocol"})
		return
	}

	results := make([]KeyProbeResult, 0, len(ch.Keys))
	for i, key := range ch.Keys {
		if indexFilter >= 0 && i != indexFilter {
			continue
		}
		results = append(results, probeOneKey(*ch, ad, protocol, modelName, i, key, disabled[key]))
	}
	c.JSON(http.StatusOK, gin.H{
		"name":      name,
		"model":     modelName,
		"protocol":  protocol,
		"key_count": len(results),
		"results":   results,
	})
}

func probeOneKey(ch config.ChannelConfig, ad adaptor.Adaptor, protocol, modelName string, index int, key string, disabled bool) KeyProbeResult {
	result := KeyProbeResult{
		Index:     index,
		MaskedKey: maskKey(key),
		Disabled:  disabled,
		TestedAt:  time.Now().Format("2006-01-02 15:04:05"),
	}
	req := &model.ChatCompletionRequest{
		Model: modelName,
		Messages: []model.Message{{
			Role:    "user",
			Content: "ping",
		}},
		MaxTokens: intPtr(1),
	}
	baseURL := channelBaseURL(ch, protocol)
	httpReq, err := ad.BuildHTTPRequest(baseURL, key, req)
	if err != nil {
		result.Error = err.Error()
		return result
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	httpReq = httpReq.WithContext(ctx)
	start := time.Now()
	httpResp, err := http.DefaultClient.Do(httpReq)
	result.LatencyMs = time.Since(start).Milliseconds()
	if err != nil {
		result.Error = err.Error()
		return result
	}
	defer httpResp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(httpResp.Body, 4096))
	result.Status = httpResp.StatusCode
	result.OK = httpResp.StatusCode >= 200 && httpResp.StatusCode < 400
	if !result.OK {
		result.Error = strings.TrimSpace(string(body))
	}
	return result
}

func intPtr(v int) *int {
	return &v
}

func channelBaseURL(ch config.ChannelConfig, protocol string) string {
	switch protocol {
	case "claude":
		if ch.BaseURLClaude != "" {
			return ch.BaseURLClaude
		}
	case "gemini":
		if ch.BaseURLGemini != "" {
			return ch.BaseURLGemini
		}
	}
	return ch.BaseURL
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

// UpdateAccessKeys updates client API keys and their model policies.
func (h *Admin) UpdateAccessKeys(c *gin.Context) {
	var req struct {
		AccessKeys []config.AccessKeyConfig `json:"access_keys"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request: " + err.Error()})
		return
	}
	if err := config.UpdateAccessKeys(req.AccessKeys); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to update access keys: " + err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true, "access_keys": config.GetAccessKeyConfigs()})
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
	page := 1
	if v := c.Query("page"); v != "" {
		if parsed, err := strconv.Atoi(v); err == nil && parsed > 0 {
			page = parsed
		}
	}
	filter := LogFilter{
		Limit:     n,
		Offset:    (page - 1) * n,
		Protocol:  strings.TrimSpace(c.Query("protocol")),
		Model:     strings.TrimSpace(c.Query("model")),
		Channel:   strings.TrimSpace(c.Query("channel")),
		AccessKey: strings.TrimSpace(c.Query("access_key")),
		Status:    strings.TrimSpace(c.Query("status")),
		Query:     strings.TrimSpace(c.Query("q")),
	}
	filteredTotal := globalLogStore.count(filter)
	c.JSON(http.StatusOK, gin.H{
		"logs":           globalLogStore.search(filter),
		"total":          globalLogStore.total(),
		"filtered_total": filteredTotal,
		"page":           page,
		"limit":          n,
	})
}

// GetLog returns one request log by id.
func (h *Admin) GetLog(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid log id"})
		return
	}
	log, ok := globalLogStore.get(id)
	if !ok {
		c.JSON(http.StatusNotFound, gin.H{"error": "log not found"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"log": log})
}

// ClearLogs removes all persisted request logs.
func (h *Admin) ClearLogs(c *gin.Context) {
	if err := globalLogStore.clear(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to clear request logs: " + err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}
