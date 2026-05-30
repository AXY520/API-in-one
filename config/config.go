package config

import (
	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync"

	"gopkg.in/yaml.v3"
)

type ServerConfig struct {
	Port       int               `json:"port"       yaml:"port"`
	AdminKey   string            `json:"admin_key"  yaml:"admin_key"`
	AccessKeys []AccessKeyConfig `json:"access_keys" yaml:"access_keys"`
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
	Name          string            `json:"name"          yaml:"name"`
	Type          string            `json:"type"          yaml:"type"`              // openai | claude | gemini
	BaseURL       string            `json:"base_url"      yaml:"base_url"`          // default URL (openai)
	BaseURLClaude string            `json:"base_url_claude" yaml:"base_url_claude"` // optional: Claude protocol URL
	BaseURLGemini string            `json:"base_url_gemini" yaml:"base_url_gemini"` // optional: Gemini protocol URL
	Keys          []string          `json:"keys"          yaml:"keys"`
	DisabledKeys  []string          `json:"disabled_keys,omitempty" yaml:"disabled_keys,omitempty"`
	Models        []string          `json:"models"        yaml:"models"`
	ModelMapping  map[string]string `json:"model_mapping"  yaml:"model_mapping"`
	Priority      int               `json:"priority"      yaml:"priority"`
	Weight        int               `json:"weight"        yaml:"weight"`
	Enabled       *bool             `json:"enabled"       yaml:"enabled"`
}

type Config struct {
	Server   ServerConfig    `yaml:"server"`
	Channels []ChannelConfig `yaml:"channels"`
}

var (
	globalConfig Config
	configMu     sync.RWMutex
	configPath   string
)

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
	normalizeAccessKeys(&cfg.Server.AccessKeys)
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
			ch.DisabledKeys = filterExistingKeys(ch.DisabledKeys, ch.Keys)
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
	return saveToDiskLocked()
}

func FindAccessKey(token string) (AccessKeyConfig, bool) {
	configMu.RLock()
	defer configMu.RUnlock()
	for _, key := range globalConfig.Server.AccessKeys {
		if key.Key == token {
			return key, true
		}
	}
	return AccessKeyConfig{}, false
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
