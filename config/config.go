package config

import (
	"api-in-one/model"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync"
	"time"

	"gopkg.in/yaml.v3"
)

type ServerConfig struct {
	Port                         int               `json:"port"       yaml:"port"`
	AdminKey                     string            `json:"admin_key"  yaml:"admin_key"`
	AccessKeys                   []AccessKeyConfig `json:"access_keys" yaml:"access_keys"`
	ModelSystemPrompts           map[string]string `json:"model_system_prompts,omitempty" yaml:"model_system_prompts,omitempty"`
	KeyFailureThreshold          int               `json:"key_failure_threshold,omitempty" yaml:"key_failure_threshold,omitempty"`
	KeyFailureCooldownSeconds    int               `json:"key_failure_cooldown_seconds,omitempty" yaml:"key_failure_cooldown_seconds,omitempty"`
	ChannelModelFailureThreshold int               `json:"channel_model_failure_threshold,omitempty" yaml:"channel_model_failure_threshold,omitempty"`
}

type AccessKeyConfig struct {
	Key            string   `json:"key" yaml:"key"`
	AllowedModels  []string `json:"allowed_models,omitempty" yaml:"allowed_models,omitempty"`
	ExcludedModels []string `json:"excluded_models,omitempty" yaml:"excluded_models,omitempty"`
}

func (a *AccessKeyConfig) UnmarshalYAML(value *yaml.Node) error {
	if value.Kind == yaml.ScalarNode {
		a.Key = value.Value
		a.AllowedModels = nil
		a.ExcludedModels = nil
		return nil
	}
	type plain AccessKeyConfig
	var out plain
	if err := value.Decode(&out); err != nil {
		return err
	}
	*a = AccessKeyConfig(out)
	return nil
}

type ChannelConfig struct {
	Name              string              `json:"name"          yaml:"name"`
	Type              string              `json:"type"          yaml:"type"`              // openai | claude | gemini
	BaseURL           string              `json:"base_url"      yaml:"base_url"`          // default URL (openai)
	BaseURLClaude     string              `json:"base_url_claude" yaml:"base_url_claude"` // optional: Claude protocol URL
	BaseURLGemini     string              `json:"base_url_gemini" yaml:"base_url_gemini"` // optional: Gemini protocol URL
	SupportsResponses bool                `json:"supports_responses" yaml:"supports_responses"`
	DisableMiMoCompat bool                `json:"disable_mimo_compat,omitempty" yaml:"disable_mimo_compat,omitempty"`
	Keys              []string            `json:"keys"          yaml:"keys"`
	DisabledKeys      []string            `json:"disabled_keys,omitempty" yaml:"disabled_keys,omitempty"`
	DisabledModels    []string            `json:"disabled_models,omitempty" yaml:"disabled_models,omitempty"`
	KeyModels         map[string][]string `json:"key_models,omitempty" yaml:"key_models,omitempty"`
	Models            []string            `json:"models"        yaml:"models"`
	ModelMapping      map[string]string   `json:"model_mapping"  yaml:"model_mapping"`
	Priority          int                 `json:"priority"      yaml:"priority"`
	Weight            int                 `json:"weight"        yaml:"weight"`
	Enabled           *bool               `json:"enabled"       yaml:"enabled"`
}

type Config struct {
	Server   ServerConfig    `yaml:"server"`
	Channels []ChannelConfig `yaml:"channels"`
}

var (
	globalConfig   Config
	configMu       sync.RWMutex
	configPath     string
	fastAccessKeys map[string]AccessKeyConfig
)

func rebuildAccessKeyCacheLocked() {
	fastAccessKeys = make(map[string]AccessKeyConfig, len(globalConfig.Server.AccessKeys))
	for _, key := range globalConfig.Server.AccessKeys {
		fastAccessKeys[key.Key] = key
	}
}

func Load(path string) error {
	configPath = path
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return err
	}
	applyDefaults(&cfg)
	configMu.Lock()
	globalConfig = cfg
	rebuildAccessKeyCacheLocked()
	configMu.Unlock()
	slog.Info("config loaded", "channels", len(cfg.Channels))
	return nil
}

func Reload() error {
	return Load(configPath)
}

func applyDefaults(cfg *Config) {
	if cfg.Server.Port == 0 {
		cfg.Server.Port = 3000
	}
	if cfg.Server.ModelSystemPrompts == nil {
		cfg.Server.ModelSystemPrompts = make(map[string]string)
	}
	applyKeyFailureDefaults(&cfg.Server)
	applyChannelModelFailureDefaults(&cfg.Server)
	normalizeAccessKeys(&cfg.Server.AccessKeys)
	normalizeModelSystemPrompts(cfg.Server.ModelSystemPrompts)
	for i := range cfg.Channels {
		if cfg.Channels[i].Priority == 0 {
			cfg.Channels[i].Priority = 10
		}
		if cfg.Channels[i].Weight == 0 {
			cfg.Channels[i].Weight = 100
		}
		if cfg.Channels[i].Enabled == nil {
			enabled := true
			cfg.Channels[i].Enabled = &enabled
		}
		if cfg.Channels[i].ModelMapping == nil {
			cfg.Channels[i].ModelMapping = make(map[string]string)
		}
		cfg.Channels[i].KeyModels = filterKeyModels(cfg.Channels[i].KeyModels, cfg.Channels[i].Keys, cfg.Channels[i].Models)
		cfg.Channels[i].DisabledModels = filterVisibleChannelModels(cfg.Channels[i].DisabledModels, cfg.Channels[i].Models, cfg.Channels[i].ModelMapping)
	}
	model.SetKeyFailurePolicy(cfg.Server.KeyFailureThreshold, time.Duration(cfg.Server.KeyFailureCooldownSeconds)*time.Second)
}

func applyChannelModelFailureDefaults(server *ServerConfig) {
	if server.ChannelModelFailureThreshold < 1 {
		server.ChannelModelFailureThreshold = 3
	}
}

func applyKeyFailureDefaults(server *ServerConfig) {
	if server.KeyFailureThreshold < 1 {
		server.KeyFailureThreshold = 3
	}
	if server.KeyFailureCooldownSeconds < 1 {
		server.KeyFailureCooldownSeconds = 600
	}
}

func normalizeModelSystemPrompts(prompts map[string]string) {
	for modelName, prompt := range prompts {
		cleanName := strings.TrimSpace(modelName)
		prompt = strings.TrimSpace(prompt)
		if cleanName == "" || prompt == "" {
			delete(prompts, modelName)
			continue
		}
		if cleanName != modelName {
			delete(prompts, modelName)
		}
		prompts[cleanName] = prompt
	}
}

func normalizeAccessKeys(keys *[]AccessKeyConfig) {
	cleaned := make([]AccessKeyConfig, 0, len(*keys))
	seen := make(map[string]bool, len(*keys))
	for _, key := range *keys {
		key.Key = strings.TrimSpace(key.Key)
		if key.Key == "" || seen[key.Key] {
			continue
		}
		key.AllowedModels = cleanStringList(key.AllowedModels)
		key.ExcludedModels = cleanStringList(key.ExcludedModels)
		cleaned = append(cleaned, key)
		seen[key.Key] = true
	}
	*keys = cleaned
}

func cleanStringList(values []string) []string {
	cleaned := make([]string, 0, len(values))
	seen := make(map[string]bool, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		cleaned = append(cleaned, value)
		seen[value] = true
	}
	return cleaned
}

// ---- Persistence ----

func saveToDisk() error {
	configMu.RLock()
	cfg := globalConfig
	configMu.RUnlock()
	return writeConfig(cfg)
}

func saveToDiskLocked() error {
	return writeConfig(globalConfig)
}

func writeConfig(cfg Config) error {
	data, err := yaml.Marshal(&cfg)
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}
	if err := os.WriteFile(configPath, data, 0644); err != nil {
		return fmt.Errorf("write config: %w", err)
	}
	slog.Info("config saved", "path", configPath, "channels", len(cfg.Channels), "bytes", len(data))
	return nil
}

// ---- Channel CRUD ----

func AddChannel(ch ChannelConfig) error {
	configMu.Lock()
	defer configMu.Unlock()
	for _, existing := range globalConfig.Channels {
		if existing.Name == ch.Name {
			return fmt.Errorf("channel %q already exists", ch.Name)
		}
	}
	applyChannelDefaults(&ch)
	globalConfig.Channels = append(globalConfig.Channels, ch)
	return saveToDiskLocked()
}

func UpdateChannel(name string, ch ChannelConfig) error {
	configMu.Lock()
	defer configMu.Unlock()
	for i, existing := range globalConfig.Channels {
		if existing.Name == name {
			ch.Name = name
			if ch.DisabledKeys == nil {
				ch.DisabledKeys = existing.DisabledKeys
			}
			if ch.DisabledModels == nil {
				ch.DisabledModels = existing.DisabledModels
			}
			if ch.KeyModels == nil {
				ch.KeyModels = existing.KeyModels
			}
			ch.DisabledKeys = filterExistingKeys(ch.DisabledKeys, ch.Keys)
			ch.DisabledModels = filterVisibleChannelModels(ch.DisabledModels, ch.Models, ch.ModelMapping)
			ch.KeyModels = filterKeyModels(ch.KeyModels, ch.Keys, ch.Models)
			applyChannelDefaults(&ch)
			globalConfig.Channels[i] = ch
			return saveToDiskLocked()
		}
	}
	return fmt.Errorf("channel %q not found", name)
}

func UpdateChannelKeys(name string, keys []string) error {
	configMu.Lock()
	defer configMu.Unlock()
	for i, existing := range globalConfig.Channels {
		if existing.Name == name {
			if len(keys) == 0 {
				return fmt.Errorf("at least one key is required")
			}
			existing.Keys = keys
			existing.DisabledKeys = filterExistingKeys(existing.DisabledKeys, keys)
			existing.DisabledModels = filterVisibleChannelModels(existing.DisabledModels, existing.Models, existing.ModelMapping)
			existing.KeyModels = filterKeyModels(existing.KeyModels, keys, existing.Models)
			applyChannelDefaults(&existing)
			globalConfig.Channels[i] = existing
			return saveToDiskLocked()
		}
	}
	return fmt.Errorf("channel %q not found", name)
}

func UpdateChannelKeyModels(name string, keyModels map[string][]string) error {
	configMu.Lock()
	defer configMu.Unlock()
	for i, existing := range globalConfig.Channels {
		if existing.Name == name {
			existing.KeyModels = filterKeyModels(keyModels, existing.Keys, existing.Models)
			applyChannelDefaults(&existing)
			globalConfig.Channels[i] = existing
			return saveToDiskLocked()
		}
	}
	return fmt.Errorf("channel %q not found", name)
}

func filterExistingKeys(values []string, allowed []string) []string {
	allowedSet := make(map[string]bool, len(allowed))
	for _, key := range allowed {
		allowedSet[key] = true
	}
	result := make([]string, 0, len(values))
	for _, key := range values {
		if allowedSet[key] {
			result = append(result, key)
		}
	}
	return result
}

func filterKeyModels(values map[string][]string, keys []string, models []string) map[string][]string {
	if len(values) == 0 {
		return nil
	}
	keySet := make(map[string]bool, len(keys))
	for _, key := range keys {
		keySet[key] = true
	}
	modelSet := make(map[string]bool, len(models))
	for _, model := range models {
		modelSet[model] = true
	}
	result := make(map[string][]string)
	for key, list := range values {
		if !keySet[key] {
			continue
		}
		cleaned := cleanStringList(list)
		if len(modelSet) > 0 {
			filtered := cleaned[:0]
			for _, model := range cleaned {
				if modelSet[model] {
					filtered = append(filtered, model)
				}
			}
			cleaned = filtered
		}
		if len(cleaned) > 0 {
			result[key] = cleaned
		}
	}
	if len(result) == 0 {
		return nil
	}
	return result
}

func filterVisibleChannelModels(values []string, models []string, mapping map[string]string) []string {
	allowed := make(map[string]bool, len(models)+len(mapping)*2)
	for _, modelName := range models {
		allowed[modelName] = true
	}
	for alias, upstream := range mapping {
		allowed[alias] = true
		allowed[upstream] = true
	}
	result := make([]string, 0, len(values))
	seen := make(map[string]bool, len(values))
	for _, modelName := range values {
		modelName = strings.TrimSpace(modelName)
		if modelName == "" || seen[modelName] || !allowed[modelName] {
			continue
		}
		seen[modelName] = true
		result = append(result, modelName)
	}
	return result
}

func UpdateChannelDisabledKeys(name string, disabledKeys []string) error {
	configMu.Lock()
	defer configMu.Unlock()
	for i, existing := range globalConfig.Channels {
		if existing.Name == name {
			existing.DisabledKeys = disabledKeys
			applyChannelDefaults(&existing)
			globalConfig.Channels[i] = existing
			return saveToDiskLocked()
		}
	}
	return fmt.Errorf("channel %q not found", name)
}

func UpdateChannelDisabledModels(name string, disabledModels []string) error {
	configMu.Lock()
	defer configMu.Unlock()
	for i, existing := range globalConfig.Channels {
		if existing.Name == name {
			existing.DisabledModels = filterVisibleChannelModels(disabledModels, existing.Models, existing.ModelMapping)
			applyChannelDefaults(&existing)
			globalConfig.Channels[i] = existing
			return saveToDiskLocked()
		}
	}
	return fmt.Errorf("channel %q not found", name)
}

func UpdateChannelEnabled(name string, enabled bool) error {
	configMu.Lock()
	defer configMu.Unlock()
	for i, existing := range globalConfig.Channels {
		if existing.Name == name {
			existing.Enabled = &enabled
			applyChannelDefaults(&existing)
			globalConfig.Channels[i] = existing
			return saveToDiskLocked()
		}
	}
	return fmt.Errorf("channel %q not found", name)
}

func DeleteChannel(name string) error {
	configMu.Lock()
	defer configMu.Unlock()
	for i, existing := range globalConfig.Channels {
		if existing.Name == name {
			globalConfig.Channels = append(globalConfig.Channels[:i], globalConfig.Channels[i+1:]...)
			return saveToDiskLocked()
		}
	}
	return fmt.Errorf("channel %q not found", name)
}

func applyChannelDefaults(ch *ChannelConfig) {
	if ch.Priority == 0 {
		ch.Priority = 10
	}
	if ch.Weight == 0 {
		ch.Weight = 100
	}
	if ch.ModelMapping == nil {
		ch.ModelMapping = make(map[string]string)
	}
	if ch.Enabled == nil {
		enabled := true
		ch.Enabled = &enabled
	}
}

// ---- Getters ----

func Get() Config {
	configMu.RLock()
	defer configMu.RUnlock()
	return globalConfig
}

func GetChannels() []ChannelConfig {
	configMu.RLock()
	defer configMu.RUnlock()
	return globalConfig.Channels
}

func GetAdminKey() string {
	configMu.RLock()
	defer configMu.RUnlock()
	return globalConfig.Server.AdminKey
}

func GetAccessKeys() []string {
	configMu.RLock()
	defer configMu.RUnlock()
	keys := make([]string, 0, len(globalConfig.Server.AccessKeys))
	for _, accessKey := range globalConfig.Server.AccessKeys {
		keys = append(keys, accessKey.Key)
	}
	return keys
}

func GetAccessKeyConfigs() []AccessKeyConfig {
	configMu.RLock()
	defer configMu.RUnlock()
	keys := make([]AccessKeyConfig, len(globalConfig.Server.AccessKeys))
	copy(keys, globalConfig.Server.AccessKeys)
	return keys
}

func UpdateAccessKeys(keys []AccessKeyConfig) error {
	configMu.Lock()
	defer configMu.Unlock()
	normalizeAccessKeys(&keys)
	globalConfig.Server.AccessKeys = keys
	rebuildAccessKeyCacheLocked()
	return saveToDiskLocked()
}

func GetModelSystemPrompts() map[string]string {
	configMu.RLock()
	defer configMu.RUnlock()
	prompts := make(map[string]string, len(globalConfig.Server.ModelSystemPrompts))
	for modelName, prompt := range globalConfig.Server.ModelSystemPrompts {
		prompts[modelName] = prompt
	}
	return prompts
}

func UpdateModelSystemPrompts(prompts map[string]string) error {
	configMu.Lock()
	defer configMu.Unlock()
	if prompts == nil {
		prompts = make(map[string]string)
	}
	normalizeModelSystemPrompts(prompts)
	globalConfig.Server.ModelSystemPrompts = prompts
	return saveToDiskLocked()
}

func UpdateKeyFailurePolicy(threshold, cooldownSeconds int) error {
	configMu.Lock()
	defer configMu.Unlock()
	globalConfig.Server.KeyFailureThreshold = threshold
	globalConfig.Server.KeyFailureCooldownSeconds = cooldownSeconds
	applyKeyFailureDefaults(&globalConfig.Server)
	model.SetKeyFailurePolicy(globalConfig.Server.KeyFailureThreshold, time.Duration(globalConfig.Server.KeyFailureCooldownSeconds)*time.Second)
	return saveToDiskLocked()
}

func UpdateChannelModelFailureThreshold(threshold int) error {
	configMu.Lock()
	defer configMu.Unlock()
	globalConfig.Server.ChannelModelFailureThreshold = threshold
	applyChannelModelFailureDefaults(&globalConfig.Server)
	return saveToDiskLocked()
}

func FindAccessKey(token string) (AccessKeyConfig, bool) {
	configMu.RLock()
	defer configMu.RUnlock()
	key, ok := fastAccessKeys[token]
	return key, ok
}

func AccessKeyCanUseModel(accessKey AccessKeyConfig, model string) bool {
	if model == "" {
		return true
	}
	for _, excluded := range accessKey.ExcludedModels {
		if excluded == model {
			return false
		}
	}
	if len(accessKey.AllowedModels) == 0 {
		return true
	}
	for _, allowed := range accessKey.AllowedModels {
		if allowed == model {
			return true
		}
	}
	return false
}

func GetPort() int {
	configMu.RLock()
	defer configMu.RUnlock()
	return globalConfig.Server.Port
}
