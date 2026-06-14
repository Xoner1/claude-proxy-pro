package main

import (
	"fmt"
	"math"
	"math/rand"
	"net/http"
	"sync"
	"time"
)

// StabilityStatus tracks the overall system stability.
type StabilityStatus struct {
	IsHealthy       bool      `json:"is_healthy"`
	Uptime          string    `json:"uptime"`
	StartTime       time.Time `json:"start_time"`
	TotalRequests   int64     `json:"total_requests"`
	TotalErrors     int64     `json:"total_errors"`
	TotalRetries    int64     `json:"total_retries"`
	TotalFailovers  int64     `json:"total_failovers"`
	ConsecutiveErrs int       `json:"consecutive_errors"`
	LastErrAt       time.Time `json:"last_error_at"`
	LastRetryAt     time.Time `json:"last_retry_at"`
	LastFailoverAt  time.Time `json:"last_failover_at"`
}

// StabilityManager handles error recovery, retries, and failover logic.
type StabilityManager struct {
	mu     sync.RWMutex
	status StabilityStatus
	cfg    *ConfigStore
	rng    *rand.Rand
}

// NewStabilityManager creates a new stability manager.
func NewStabilityManager(cfg *ConfigStore) *StabilityManager {
	return &StabilityManager{
		cfg: cfg,
		rng: rand.New(rand.NewSource(time.Now().UnixNano())),
		status: StabilityStatus{
			IsHealthy: true,
			StartTime: time.Now(),
		},
	}
}

// RecordRequest records a request outcome.
func (sm *StabilityManager) RecordRequest(success bool) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	sm.status.TotalRequests++
	if success {
		sm.status.ConsecutiveErrs = 0
		sm.status.IsHealthy = true
	} else {
		sm.status.TotalErrors++
		sm.status.ConsecutiveErrs++
		sm.status.LastErrAt = time.Now()
		// If 5+ consecutive errors, mark as degraded
		if sm.status.ConsecutiveErrs >= 5 {
			sm.status.IsHealthy = false
		}
	}
}

// RecordRetry records a retry attempt.
func (sm *StabilityManager) RecordRetry() {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	sm.status.TotalRetries++
	sm.status.LastRetryAt = time.Now()
}

// RecordFailover records a failover event.
func (sm *StabilityManager) RecordFailover() {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	sm.status.TotalFailovers++
	sm.status.LastFailoverAt = time.Now()
}

// GetStatus returns a copy of the current stability status.
func (sm *StabilityManager) GetStatus() StabilityStatus {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	s := sm.status
	s.Uptime = time.Since(s.StartTime).Truncate(time.Second).String()
	return s
}

// RetryWithBackoff executes a function with exponential backoff retry logic.
// Returns the HTTP response and the provider index that succeeded.
func (sm *StabilityManager) RetryWithBackoff(fn func(providerIdx int) (*http.Response, error)) (*http.Response, int, error) {
	appCfg := sm.cfg.Get()
	if !appCfg.AutoRetry {
		// No retry - just try active provider
		_, ok := sm.cfg.GetActiveProvider()
		if !ok {
			return nil, 0, fmt.Errorf("no providers configured")
		}
		idx := appCfg.ActiveIdx
		resp, err := fn(idx)
		if err == nil {
			sm.RecordRequest(true)
		} else {
			sm.RecordRequest(false)
		}
		return resp, idx, err
	}

	maxRetries := appCfg.RetryMax
	if maxRetries <= 0 {
		maxRetries = 3
	}

	// Try active provider first, then retry with backoff
	for attempt := 0; attempt < maxRetries; attempt++ {
		// Read active index fresh each attempt (may have changed due to failover)
		currentCfg := sm.cfg.Get()
		if len(currentCfg.Providers) == 0 {
			return nil, 0, fmt.Errorf("no providers configured")
		}
		idx := currentCfg.ActiveIdx
		if idx < 0 || idx >= len(currentCfg.Providers) {
			idx = 0
		}

		resp, err := fn(idx)
		if err == nil {
			if resp.StatusCode < 500 {
				sm.RecordRequest(resp.StatusCode < 400)
				return resp, idx, nil
			}
			// Close the body of failed attempts (5xx status codes) to prevent connection leak
			resp.Body.Close()
			sm.RecordRequest(false)
		} else {
			sm.RecordRequest(false)
		}

		// Calculate backoff with jitter
		if attempt < maxRetries-1 {
			backoff := sm.calcBackoff(attempt)
			sm.RecordRetry()
			time.Sleep(backoff)
		}
	}

	// All retries exhausted — try failover if enabled
	if appCfg.Failover {
		return sm.tryFailover(fn)
	}

	return nil, 0, fmt.Errorf("all retries exhausted for provider %s", appCfg.Providers[appCfg.ActiveIdx].Name)
}

// tryFailover attempts to use alternative providers.
func (sm *StabilityManager) tryFailover(fn func(providerIdx int) (*http.Response, error)) (*http.Response, int, error) {
	appCfg := sm.cfg.Get()
	activeIdx := appCfg.ActiveIdx

	// Sort providers by priority, skip active
	type provIdx struct {
		Provider
		idx int
	}
	var alternatives []provIdx
	for i, p := range appCfg.Providers {
		if i != activeIdx && p.Status != "offline" {
			alternatives = append(alternatives, provIdx{p, i})
		}
	}

	// Sort by priority
	for i := 0; i < len(alternatives); i++ {
		for j := i + 1; j < len(alternatives); j++ {
			if alternatives[j].Priority < alternatives[i].Priority {
				alternatives[i], alternatives[j] = alternatives[j], alternatives[i]
			}
		}
	}

	for _, alt := range alternatives {
		resp, err := fn(alt.idx)
		if err == nil && resp.StatusCode < 500 {
			// Failover succeeded — switch active provider
			sm.cfg.SetActiveProvider(alt.idx)
			sm.RecordFailover()
			sm.RecordRequest(true)
			return resp, alt.idx, nil
		}
		sm.RecordRequest(false)
	}

	return nil, 0, fmt.Errorf("all providers failed including failover")
}

// calcBackoff calculates exponential backoff with jitter.
func (sm *StabilityManager) calcBackoff(attempt int) time.Duration {
	// Base: 1s, then 2s, 4s, 8s... with jitter
	base := time.Duration(math.Pow(2, float64(attempt))) * time.Second
	jitter := time.Duration(sm.rng.Int63n(int64(base / 2)))
	return base + jitter
}

// ShouldRetry checks if an error is retryable.
func ShouldRetry(statusCode int) bool {
	switch statusCode {
	case http.StatusTooManyRequests, // 429
		http.StatusInternalServerError, // 500
		http.StatusBadGateway,          // 502
		http.StatusServiceUnavailable,  // 503
		http.StatusGatewayTimeout:      // 504
		return true
	default:
		return false
	}
}

// Uptime returns the time since the server started.
func (sm *StabilityManager) Uptime() time.Duration {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	return time.Since(sm.status.StartTime)
}

// IsHealthy checks if the system is currently healthy.
func (sm *StabilityManager) IsHealthy() bool {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	return sm.status.IsHealthy
}

// ErrorCount returns total error count.
func (sm *StabilityManager) ErrorCount() int64 {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	return sm.status.TotalErrors
}
