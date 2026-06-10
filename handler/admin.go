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
	Name              string            `json:"name"`
	Type              string            `json:"type"`
	BaseURL           string            `json:"base_url"`
	BaseURLClaude     string            `json:"base_url_claude,omitempty"`
	BaseURLGemini     string            `json:"base_url_gemini,omitempty"`
	SupportsResponses bool              `json:"supports_responses"`
	DisableMiMoCompat bool              `json:"disable_mimo_compat"`
	Temporary         bool              `json:"temporary"`
	KeyCount          int               `json:"key_count"`
	MaskedKeys        []string          `json:"masked_keys"`
	KeyStats          []KeyStatus       `json:"key_stats"`
	Models            []string          `json:"models"`
	DisabledModels    []string          `json:"disabled_models"`
	ModelStats        []ModelStatus     `json:"model_stats"`
	ModelMapping      map[string]string `json:"model_mapping"`
	Priority          int               `json:"priority"`
	Weight            int               `json:"weight"`
	Enabled           bool              `json:"enabled"`
	Healthy           bool              `json:"healthy"`
}

type ModelStatus struct {
	Model              string `json:"model"`
	ResolvedModel      string `json:"resolved_model,omitempty"`
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
	SuspendedUntil     string `json:"suspended_until,omitempty"`
	Healthy            bool   `json:"healthy"`
}

// ListChannels returns all configured channels and their status.
func (h *Admin) ListChannels(c *gin.Context) {
	channels := h.pool.GetChannels()
	var result []ChannelStatus
	for _, ch := range channels {
		result = append(result, ChannelStatus{
			Name:              ch.Name,
			Type:              ch.Type,
			BaseURL:           ch.BaseURL,
			BaseURLClaude:     ch.BaseURLClaude,
			BaseURLGemini:     ch.BaseURLGemini,
			SupportsResponses: ch.SupportsResponses,
			DisableMiMoCompat: ch.DisableMiMoCompat,
			Temporary:         ch.Temporary,
			KeyCount:          len(ch.Keys),
			MaskedKeys:        maskKeys(ch.Keys),
			KeyStats:          buildKeyStatus(ch.GetKeyStats()),
			Models:            ch.Models,
			DisabledModels:    ch.DisabledModelList(),
			ModelStats:        buildModelStatus(ch.GetModelStats()),
			ModelMapping:      ch.ModelMapping,
			Priority:          ch.Priority,
			Weight:            ch.Weight,
			Enabled:           ch.Enabled,
			Healthy:           ch.IsHealthy(),
		})
	}
	c.JSON(http.StatusOK, gin.H{
		"channels": result,
		"total":    len(result),
	})
}

func buildModelStatus(stats []model.ModelStats) []ModelStatus {
	result := make([]ModelStatus, 0, len(stats))
	threshold := config.Get().Server.ChannelModelFailureThreshold
	if threshold < 1 {
		threshold = 1
	}
	for _, stat := range stats {
		result = append(result, ModelStatus{
			Model:              stat.Model,
			ResolvedModel:      stat.ResolvedModel,
			Disabled:           stat.Disabled,
			TotalRequests:      stat.TotalRequests,
			SuccessRequests:    stat.SuccessRequests,
			FailureRequests:    stat.FailureRequests,
			ConsecutiveFailure: stat.ConsecutiveFailure,
			LastStatus:         stat.LastStatus,
			LastError:          stat.LastError,
			LastUsedAt:         stat.LastUsedAt,
			LastLatencyMs:      stat.LastLatencyMs,
			Healthy:            !stat.Disabled && stat.ConsecutiveFailure < int64(threshold),
		})
	}
	return result
}

func buildKeyStatus(stats []model.KeyStats) []KeyStatus {
	result := make([]KeyStatus, 0, len(stats))
	threshold, _ := model.KeyFailurePolicy()
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
			SuspendedUntil:     stat.SuspendedUntil,
			Healthy:            !stat.Disabled && stat.ConsecutiveFailure < int64(threshold),
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

func channelNameFromRequest(c *gin.Context) string {
	if name := strings.TrimSpace(c.Query("name")); name != "" {
		return name
	}
	return c.Param("name")
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
	name := channelNameFromRequest(c)
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
	name := channelNameFromRequest(c)
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
		"key_models":    maskKeyModels(ch.Keys, ch.KeyModels),
	})
}

// UpdateChannelKeys replaces only the upstream API keys for a channel.
func (h *Admin) UpdateChannelKeys(c *gin.Context) {
	name := channelNameFromRequest(c)
	var req struct {
		Keys            interface{}         `json:"keys"`
		DisabledKeys    []string            `json:"disabled_keys"`
		KeyModels       map[string][]string `json:"key_models"`
		KeyModelByIndex map[string][]string `json:"key_model_by_index"`
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
	if req.DisabledKeys != nil {
		if err := config.UpdateChannelDisabledKeys(name, req.DisabledKeys); err != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
			return
		}
	}
	keyModels := unmaskKeyModels(keys, req.KeyModels, req.KeyModelByIndex)
	if err := config.UpdateChannelKeyModels(name, keyModels); err != nil {
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

func maskKeyModels(keys []string, keyModels map[string][]string) map[string][]string {
	result := make(map[string][]string, len(keys))
	for i, key := range keys {
		models := keyModels[key]
		if len(models) == 0 {
			continue
		}
		result[strconv.Itoa(i)] = append([]string(nil), models...)
	}
	return result
}

func unmaskKeyModels(keys []string, byKey map[string][]string, byIndex map[string][]string) map[string][]string {
	result := make(map[string][]string)
	for key, models := range byKey {
		if models != nil {
			result[key] = models
		}
	}
	for rawIndex, models := range byIndex {
		index, err := strconv.Atoi(rawIndex)
		if err != nil || index < 0 || index >= len(keys) || len(models) == 0 {
			continue
		}
		result[keys[index]] = models
	}
	return result
}

// UpdateChannelKeyState enables or disables one upstream key by index.
func (h *Admin) UpdateChannelKeyState(c *gin.Context) {
	name := channelNameFromRequest(c)
	index, err := strconv.Atoi(c.Param("index"))
	if err != nil || index < 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid key index"})
		return
	}
	var req struct {
		Disabled bool `json:"disabled"`
		Recover  bool `json:"recover"`
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
	recovered := false
	if req.Recover || !req.Disabled {
		recovered = h.pool.ResetChannelKeyFailure(name, index)
	}
	c.JSON(http.StatusOK, gin.H{
		"message":       "channel key state updated",
		"name":          name,
		"key_index":     index,
		"disabled":      req.Disabled,
		"recovered":     recovered,
		"disabled_keys": maskKeys(disabledKeys),
	})
}

// UpdateChannelModelState enables or disables one model on one channel.
func (h *Admin) UpdateChannelModelState(c *gin.Context) {
	name := channelNameFromRequest(c)
	var req struct {
		Model    string `json:"model"`
		Disabled bool   `json:"disabled"`
		Recover  bool   `json:"recover"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request: " + err.Error()})
		return
	}
	req.Model = strings.TrimSpace(req.Model)
	if req.Model == "" {
		req.Model = strings.TrimSpace(c.Query("model"))
	}
	if req.Model == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "model is required"})
		return
	}
	ch := findChannelConfig(name)
	if ch == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": fmt.Sprintf("channel %q not found", name)})
		return
	}
	disabled := make(map[string]bool, len(ch.DisabledModels)+1)
	for _, modelName := range ch.DisabledModels {
		disabled[modelName] = true
	}
	if req.Disabled {
		disabled[req.Model] = true
	} else {
		delete(disabled, req.Model)
		if upstream, ok := ch.ModelMapping[req.Model]; ok {
			delete(disabled, upstream)
		}
	}
	disabledModels := make([]string, 0, len(disabled))
	for modelName := range disabled {
		disabledModels = append(disabledModels, modelName)
	}
	if err := config.UpdateChannelDisabledModels(name, disabledModels); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}
	h.rebuildPool()
	recovered := false
	if req.Recover || !req.Disabled {
		recovered = h.pool.ResetChannelModelFailure(name, req.Model)
	}
	c.JSON(http.StatusOK, gin.H{
		"message":         "channel model state updated",
		"name":            name,
		"model":           req.Model,
		"disabled":        req.Disabled,
		"recovered":       recovered,
		"disabled_models": disabledModels,
	})
}

// UpdateChannelState enables or disables a whole channel.
func (h *Admin) UpdateChannelState(c *gin.Context) {
	name := channelNameFromRequest(c)
	var req struct {
		Enabled bool `json:"enabled"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request: " + err.Error()})
		return
	}
	if err := config.UpdateChannelEnabled(name, req.Enabled); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}
	h.rebuildPool()
	c.JSON(http.StatusOK, gin.H{
		"message": "channel state updated",
		"name":    name,
		"enabled": req.Enabled,
	})
}

// ProbeChannelKeys sends a tiny non-stream request with every key in a channel.
func (h *Admin) ProbeChannelKeys(c *gin.Context) {
	name := channelNameFromRequest(c)
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
	name := channelNameFromRequest(c)
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
		"port":                 cfg.Server.Port,
		"admin_key":            cfg.Server.AdminKey,
		"access_keys":          cfg.Server.AccessKeys,
		"models":               h.pool.GetAvailableModels(),
		"model_system_prompts": cfg.Server.ModelSystemPrompts,
		"key_failure_policy": gin.H{
			"threshold":        cfg.Server.KeyFailureThreshold,
			"cooldown_seconds": cfg.Server.KeyFailureCooldownSeconds,
		},
		"channel_model_failure_policy": gin.H{
			"threshold": cfg.Server.ChannelModelFailureThreshold,
		},
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
	req.AccessKeys = h.normalizeAccessKeyModelPolicies(req.AccessKeys)
	if err := validateAccessKeyExpirations(req.AccessKeys); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := config.UpdateAccessKeys(req.AccessKeys); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to update access keys: " + err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true, "access_keys": config.GetAccessKeyConfigs()})
}

func (h *Admin) UpdateChannelModelFailurePolicy(c *gin.Context) {
	var req struct {
		Threshold int `json:"threshold"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request: " + err.Error()})
		return
	}
	if req.Threshold < 1 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "threshold must be greater than 0"})
		return
	}
	if err := config.UpdateChannelModelFailureThreshold(req.Threshold); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	cfg := config.Get()
	c.JSON(http.StatusOK, gin.H{
		"channel_model_failure_policy": gin.H{
			"threshold": cfg.Server.ChannelModelFailureThreshold,
		},
	})
}

func (h *Admin) UpdateModelSystemPrompts(c *gin.Context) {
	var req struct {
		Prompts map[string]string `json:"model_system_prompts"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request: " + err.Error()})
		return
	}
	req.Prompts = h.normalizeModelSystemPrompts(req.Prompts)
	if err := config.UpdateModelSystemPrompts(req.Prompts); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to update model system prompts: " + err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true, "model_system_prompts": config.GetModelSystemPrompts()})
}

func (h *Admin) UpdateKeyFailurePolicy(c *gin.Context) {
	var req struct {
		Threshold       int `json:"threshold"`
		CooldownSeconds int `json:"cooldown_seconds"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request: " + err.Error()})
		return
	}
	if err := config.UpdateKeyFailurePolicy(req.Threshold, req.CooldownSeconds); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to update key failure policy: " + err.Error()})
		return
	}
	cfg := config.Get()
	c.JSON(http.StatusOK, gin.H{
		"ok": true,
		"key_failure_policy": gin.H{
			"threshold":        cfg.Server.KeyFailureThreshold,
			"cooldown_seconds": cfg.Server.KeyFailureCooldownSeconds,
		},
	})
}

func (h *Admin) normalizeModelSystemPrompts(prompts map[string]string) map[string]string {
	visible := make(map[string]bool)
	for _, m := range h.pool.GetAvailableModels() {
		visible[m.ID] = true
	}
	result := make(map[string]string, len(prompts))
	for modelName, prompt := range prompts {
		modelName = strings.TrimSpace(modelName)
		prompt = strings.TrimSpace(prompt)
		if modelName == "" || prompt == "" || !visible[modelName] {
			continue
		}
		result[modelName] = prompt
	}
	return result
}

func (h *Admin) normalizeAccessKeyModelPolicies(keys []config.AccessKeyConfig) []config.AccessKeyConfig {
	visible := make(map[string]bool)
	for _, m := range h.pool.GetAvailableModels() {
		visible[m.ID] = true
	}
	for i := range keys {
		keys[i].AllowedModels = filterVisibleModels(keys[i].AllowedModels, visible)
		keys[i].ExcludedModels = filterVisibleModels(keys[i].ExcludedModels, visible)
	}
	return keys
}

func validateAccessKeyExpirations(keys []config.AccessKeyConfig) error {
	for _, key := range keys {
		expiresAt := strings.TrimSpace(key.ExpiresAt)
		if expiresAt == "" {
			continue
		}
		if _, err := time.Parse(time.RFC3339, expiresAt); err != nil {
			return fmt.Errorf("invalid expires_at for key %s: expected RFC3339", maskKey(key.Key))
		}
	}
	return nil
}

func filterVisibleModels(models []string, visible map[string]bool) []string {
	result := make([]string, 0, len(models))
	seen := make(map[string]bool, len(models))
	for _, modelName := range models {
		modelName = strings.TrimSpace(modelName)
		if modelName == "" || seen[modelName] || !visible[modelName] {
			continue
		}
		result = append(result, modelName)
		seen[modelName] = true
	}
	return result
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
		httpReq, _ := http.NewRequest("GET", url, nil)
		adaptor.SetUpstreamHeaders(httpReq)
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
		models = parseGeminiModels(body)
	default: // openai compatible
		url := strings.TrimRight(req.BaseURL, "/") + "/models"
		httpReq, _ := http.NewRequest("GET", url, nil)
		adaptor.SetUpstreamHeaders(httpReq)
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

// ExportLogs returns full request logs for external troubleshooting.
func (h *Admin) ExportLogs(c *gin.Context) {
	n := 100
	if v := c.Query("limit"); v != "" {
		if parsed, err := strconv.Atoi(v); err == nil && parsed > 0 {
			n = parsed
		}
	}
	if n > 1000 {
		n = 1000
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
		SinceID:   parseInt64Query(c, "since_id"),
		UntilID:   parseInt64Query(c, "until_id"),
		Protocol:  strings.TrimSpace(c.Query("protocol")),
		Model:     strings.TrimSpace(c.Query("model")),
		Channel:   strings.TrimSpace(c.Query("channel")),
		AccessKey: strings.TrimSpace(c.Query("access_key")),
		Status:    strings.TrimSpace(c.Query("status")),
		Query:     strings.TrimSpace(c.Query("q")),
	}
	logs := globalLogStore.export(filter)
	if strings.EqualFold(strings.TrimSpace(c.Query("format")), "jsonl") {
		c.Header("Content-Type", "application/x-ndjson; charset=utf-8")
		c.Status(http.StatusOK)
		encoder := json.NewEncoder(c.Writer)
		for _, entry := range logs {
			_ = encoder.Encode(entry)
		}
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"logs":           logs,
		"total":          globalLogStore.total(),
		"filtered_total": globalLogStore.count(filter),
		"page":           page,
		"limit":          n,
		"generated_at":   time.Now().Format(time.RFC3339),
	})
}

func parseInt64Query(c *gin.Context, name string) int64 {
	value := strings.TrimSpace(c.Query(name))
	if value == "" {
		return 0
	}
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil || parsed <= 0 {
		return 0
	}
	return parsed
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
