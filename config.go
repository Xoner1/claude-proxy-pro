package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// DefaultConfigPath returns the default config path in the user's home directory.
func DefaultConfigPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "config.yaml"
	}
	dir := filepath.Join(home, ".claude-proxy-pro")
	return filepath.Join(dir, "config.yaml")
}

// Provider represents an upstream API provider.
type Provider struct {
	Name       string `json:"name"`
	URL        string `json:"url"`
	Key        string `json:"key,omitempty"`
	Model      string `json:"model"`
	Priority   int    `json:"priority"`
	Status     string `json:"status"` // online, offline, degraded
	LastCheck  string `json:"last_check"`
	Requests   int    `json:"requests_today"`
	Latency    int    `json:"latency_ms"`
	SuccessCnt int    `json:"success_count"`
	FailCnt    int    `json:"fail_count"`
}

// ModelInfo represents a discovered model from a provider.
type ModelInfo struct {
	ID          string `json:"id"`
	Provider    string `json:"provider"`
	ContextSize int    `json:"context_size"`
	Pricing     string `json:"pricing"` // free, paid, unknown
	Latency     int    `json:"latency_ms"`
	Object      string `json:"object"`
	DisplayName string `json:"display_name"`
	Created     int64  `json:"created,omitempty"`
	OwnedBy     string `json:"owned_by,omitempty"`
}

// AppConfig is the full application configuration.
type AppConfig struct {
	Providers     []Provider `json:"providers"`
	Port          string     `json:"port"`
	ActiveIdx     int        `json:"active_idx"`
	AutoRetry     bool       `json:"auto_retry"`
	RetryMax      int        `json:"retry_max"`
	Failover      bool       `json:"failover"`
	CheckInterval int        `json:"check_interval_seconds"`
}

// DefaultConfig returns a sensible default configuration.
func DefaultConfig() AppConfig {
	return AppConfig{
		Providers:     []Provider{},
		Port:          "8082",
		ActiveIdx:     0,
		AutoRetry:     true,
		RetryMax:      3,
		Failover:      true,
		CheckInterval: 60,
	}
}

// ConfigStore manages the application configuration with thread-safe access.
type ConfigStore struct {
	mu     sync.RWMutex
	cfg    AppConfig
	path   string
	models []ModelInfo
}

// NewConfigStore creates a new ConfigStore and loads config from disk.
func NewConfigStore(path string) (*ConfigStore, error) {
	cs := &ConfigStore{
		path: path,
		cfg:  DefaultConfig(),
	}

	// Try to load from disk; if missing, save defaults.
	if _, err := os.Stat(path); err == nil {
		if err := cs.load(); err != nil {
			return nil, fmt.Errorf("load config: %w", err)
		}
	} else {
		// Ensure directory exists
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			return nil, fmt.Errorf("create config dir: %w", err)
		}
		if err := cs.save(); err != nil {
			return nil, fmt.Errorf("save default config: %w", err)
		}
	}

	return cs, nil
}

// ── YAML parser/writer (stdlib only, handles our specific config format) ──

// load reads config.yaml from disk using a simple YAML parser.
func (cs *ConfigStore) load() error {
	data, err := os.ReadFile(cs.path)
	if err != nil {
		return err
	}

	cfg := DefaultConfig()
	lines := strings.Split(string(data), "\n")
	var currentProvider *Provider
	inProviders := false

	for _, rawLine := range lines {
		line := strings.TrimSpace(rawLine)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		// Top-level keys (no indentation)
		if !strings.HasPrefix(rawLine, " ") && !strings.HasPrefix(rawLine, "\t") {
			inProviders = false
			if strings.HasPrefix(line, "port:") {
				cfg.Port = cleanYAMLVal(strings.TrimPrefix(line, "port:"))
			} else if strings.HasPrefix(line, "active_idx:") {
				v, _ := strconv.Atoi(cleanYAMLVal(strings.TrimPrefix(line, "active_idx:")))
				cfg.ActiveIdx = v
			} else if strings.HasPrefix(line, "auto_retry:") {
				cfg.AutoRetry = cleanYAMLVal(strings.TrimPrefix(line, "auto_retry:")) == "true"
			} else if strings.HasPrefix(line, "retry_max:") {
				v, _ := strconv.Atoi(cleanYAMLVal(strings.TrimPrefix(line, "retry_max:")))
				cfg.RetryMax = v
			} else if strings.HasPrefix(line, "failover:") {
				cfg.Failover = cleanYAMLVal(strings.TrimPrefix(line, "failover:")) == "true"
			} else if strings.HasPrefix(line, "check_interval_seconds:") {
				v, _ := strconv.Atoi(cleanYAMLVal(strings.TrimPrefix(line, "check_interval_seconds:")))
				cfg.CheckInterval = v
			} else if strings.HasPrefix(line, "providers:") {
				inProviders = true
			}
			continue
		}

		// Provider entries
		if inProviders {
			trimmed := strings.TrimLeft(rawLine, " \t")
			if strings.HasPrefix(trimmed, "- ") {
				// New provider entry
				if currentProvider != nil {
					cfg.Providers = append(cfg.Providers, *currentProvider)
				}
				currentProvider = &Provider{Status: "unknown"}
				// Check if the first field is on the same line as "- "
				rest := strings.TrimSpace(strings.TrimPrefix(trimmed, "- "))
				if strings.Contains(rest, ":") {
					setProviderField(currentProvider, rest)
				}
			} else if currentProvider != nil && strings.Contains(trimmed, ":") {
				setProviderField(currentProvider, trimmed)
			}
		}
	}

	// Don't forget the last provider
	if currentProvider != nil {
		cfg.Providers = append(cfg.Providers, *currentProvider)
	}

	cs.mu.Lock()
	cs.cfg = cfg
	cs.mu.Unlock()
	return nil
}

// cleanYAMLVal strips inline comments, trims space, and trims quotes.
func cleanYAMLVal(val string) string {
	if idx := strings.Index(val, "#"); idx >= 0 {
		val = val[:idx]
	}
	val = strings.TrimSpace(val)
	return strings.Trim(val, `"'`)
}

// setProviderField parses a "key: value" line and sets it on the provider.
func setProviderField(p *Provider, line string) {
	line = strings.TrimSpace(line)
	parts := strings.SplitN(line, ":", 2)
	if len(parts) != 2 {
		return
	}
	key := strings.TrimSpace(parts[0])
	val := cleanYAMLVal(parts[1])

	switch key {
	case "name":
		p.Name = val
	case "url":
		p.URL = val
	case "key":
		p.Key = val
	case "model":
		p.Model = val
	case "priority":
		p.Priority, _ = strconv.Atoi(val)
	case "status":
		p.Status = val
	case "last_check":
		p.LastCheck = val
	case "requests_today":
		p.Requests, _ = strconv.Atoi(val)
	case "latency_ms":
		p.Latency, _ = strconv.Atoi(val)
	case "success_count":
		p.SuccessCnt, _ = strconv.Atoi(val)
	case "fail_count":
		p.FailCnt, _ = strconv.Atoi(val)
	}
}

// save writes config.yaml to disk using a simple YAML writer.
func (cs *ConfigStore) save() error {
	cs.mu.RLock()
	defer cs.mu.RUnlock()
	return cs.saveLocked()
}

// saveLocked writes config.yaml. Caller must hold read lock.
func (cs *ConfigStore) saveLocked() error {
	dir := filepath.Dir(cs.path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}

	var b strings.Builder
	b.WriteString("# Claude Proxy Pro - Configuration\n")
	b.WriteString("# Auto-generated. Do not edit manually.\n\n")

	b.WriteString(fmt.Sprintf("port: %s\n", cs.cfg.Port))
	b.WriteString(fmt.Sprintf("active_idx: %d\n", cs.cfg.ActiveIdx))
	b.WriteString(fmt.Sprintf("auto_retry: %t\n", cs.cfg.AutoRetry))
	b.WriteString(fmt.Sprintf("retry_max: %d\n", cs.cfg.RetryMax))
	b.WriteString(fmt.Sprintf("failover: %t\n", cs.cfg.Failover))
	b.WriteString(fmt.Sprintf("check_interval_seconds: %d\n", cs.cfg.CheckInterval))
	b.WriteString("\n")

	b.WriteString("providers:\n")
	if len(cs.cfg.Providers) == 0 {
		b.WriteString("  []\n")
	} else {
		for _, p := range cs.cfg.Providers {
			b.WriteString(fmt.Sprintf("  - name: %s\n", p.Name))
			b.WriteString(fmt.Sprintf("    url: %s\n", p.URL))
			b.WriteString(fmt.Sprintf("    key: %s\n", p.Key))
			b.WriteString(fmt.Sprintf("    model: %s\n", p.Model))
			b.WriteString(fmt.Sprintf("    priority: %d\n", p.Priority))
			b.WriteString(fmt.Sprintf("    status: %s\n", p.Status))
			b.WriteString(fmt.Sprintf("    last_check: %s\n", p.LastCheck))
			b.WriteString(fmt.Sprintf("    requests_today: %d\n", p.Requests))
			b.WriteString(fmt.Sprintf("    latency_ms: %d\n", p.Latency))
			b.WriteString(fmt.Sprintf("    success_count: %d\n", p.SuccessCnt))
			b.WriteString(fmt.Sprintf("    fail_count: %d\n", p.FailCnt))
			b.WriteString("\n")
		}
	}

	return os.WriteFile(cs.path, []byte(b.String()), 0644)
}

// Reload re-reads config from disk.
func (cs *ConfigStore) Reload() error {
	return cs.load()
}

// Get returns a copy of the current config.
func (cs *ConfigStore) Get() AppConfig {
	cs.mu.RLock()
	defer cs.mu.RUnlock()
	return cs.cfg
}

// GetActiveProvider returns the currently active provider.
func (cs *ConfigStore) GetActiveProvider() (Provider, bool) {
	cs.mu.RLock()
	defer cs.mu.RUnlock()
	if len(cs.cfg.Providers) == 0 {
		return Provider{}, false
	}
	idx := cs.cfg.ActiveIdx
	if idx < 0 || idx >= len(cs.cfg.Providers) {
		idx = 0
	}
	return cs.cfg.Providers[idx], true
}

// GetProviderByIndex returns a provider by index.
func (cs *ConfigStore) GetProviderByIndex(idx int) (Provider, bool) {
	cs.mu.RLock()
	defer cs.mu.RUnlock()
	if idx < 0 || idx >= len(cs.cfg.Providers) {
		return Provider{}, false
	}
	return cs.cfg.Providers[idx], true
}

// SetActiveProvider switches the active provider by index.
func (cs *ConfigStore) SetActiveProvider(idx int) error {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	if idx < 0 || idx >= len(cs.cfg.Providers) {
		return fmt.Errorf("provider index %d out of range", idx)
	}
	cs.cfg.ActiveIdx = idx
	return cs.saveLocked()
}

// AddProvider adds a new provider to the config.
func (cs *ConfigStore) AddProvider(p Provider) error {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	cs.cfg.Providers = append(cs.cfg.Providers, p)
	return cs.saveLocked()
}

// UpdateProvider updates a provider at the given index.
func (cs *ConfigStore) UpdateProvider(idx int, p Provider) error {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	if idx < 0 || idx >= len(cs.cfg.Providers) {
		return fmt.Errorf("provider index %d out of range", idx)
	}
	cs.cfg.Providers[idx] = p
	return cs.saveLocked()
}

// RemoveProvider removes a provider at the given index.
func (cs *ConfigStore) RemoveProvider(idx int) error {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	if idx < 0 || idx >= len(cs.cfg.Providers) {
		return fmt.Errorf("provider index %d out of range", idx)
	}
	cs.cfg.Providers = append(cs.cfg.Providers[:idx], cs.cfg.Providers[idx+1:]...)
	if cs.cfg.ActiveIdx >= len(cs.cfg.Providers) {
		cs.cfg.ActiveIdx = 0
	}
	return cs.saveLocked()
}

// UpdateProviderStatus updates the status of a provider.
func (cs *ConfigStore) UpdateProviderStatus(idx int, status string, latency int) {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	if idx < 0 || idx >= len(cs.cfg.Providers) {
		return
	}
	cs.cfg.Providers[idx].Status = status
	cs.cfg.Providers[idx].LastCheck = time.Now().UTC().Format(time.RFC3339)
	cs.cfg.Providers[idx].Latency = latency
	// Save after status update
	cs.saveLocked()
}

// IncrementRequests increments the request counter for a provider.
func (cs *ConfigStore) IncrementRequests(idx int, success bool) {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	if idx < 0 || idx >= len(cs.cfg.Providers) {
		return
	}
	cs.cfg.Providers[idx].Requests++
	if success {
		cs.cfg.Providers[idx].SuccessCnt++
	} else {
		cs.cfg.Providers[idx].FailCnt++
	}
	cs.saveLocked()
}

// SetModels updates the discovered models list.
func (cs *ConfigStore) SetModels(models []ModelInfo) {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	cs.models = models
}

// GetModels returns a copy of the discovered models.
func (cs *ConfigStore) GetModels() []ModelInfo {
	cs.mu.RLock()
	defer cs.mu.RUnlock()
	out := make([]ModelInfo, len(cs.models))
	copy(out, cs.models)
	return out
}

// ProviderJSON returns the providers list as JSON for the API.
func (cs *ConfigStore) ProviderJSON() ([]byte, error) {
	cs.mu.RLock()
	defer cs.mu.RUnlock()
	return json.Marshal(cs.cfg.Providers)
}

// ModelsJSON returns the models list as JSON for the API.
func (cs *ConfigStore) ModelsJSON() ([]byte, error) {
	cs.mu.RLock()
	defer cs.mu.RUnlock()
	return json.Marshal(cs.models)
}

// ConfigJSON returns the full config as JSON for the API.
func (cs *ConfigStore) ConfigJSON() ([]byte, error) {
	cs.mu.RLock()
	defer cs.mu.RUnlock()
	return json.Marshal(cs.cfg)
}
