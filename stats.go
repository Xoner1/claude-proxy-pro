package main

import (
	"encoding/json"
	"sync"
	"time"
)

// RequestLog stores a single request's details.
type RequestLog struct {
	ID         int       `json:"id"`
	Timestamp  time.Time `json:"timestamp"`
	Provider   string    `json:"provider"`
	Model      string    `json:"model"`
	Stream     bool      `json:"stream"`
	Status     int       `json:"status"`
	Latency    int       `json:"latency_ms"`
	InputTok   int       `json:"input_tokens"`
	OutputTok  int       `json:"output_tokens"`
	Error      string    `json:"error,omitempty"`
}

// StatsManager tracks request statistics and logs.
type StatsManager struct {
	mu         sync.RWMutex
	requests   int64
	tokensIn   int64
	tokensOut  int64
	avgLatency int64 // cumulative for running average
	logs       []RequestLog
	logIdx     int
	maxLogs    int
	start      time.Time
}

// NewStatsManager creates a new stats manager.
func NewStatsManager() *StatsManager {
	return &StatsManager{
		maxLogs: 50,
		logs:    make([]RequestLog, 0, 50),
		start:   time.Now(),
	}
}

// RecordRequest records a completed request.
func (sm *StatsManager) RecordRequest(req RequestLog) {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	sm.requests++
	sm.tokensIn += int64(req.InputTok)
	sm.tokensOut += int64(req.OutputTok)
	sm.avgLatency = (sm.avgLatency*(sm.requests-1) + int64(req.Latency)) / sm.requests

	// Append log (circular buffer behavior)
	req.ID = int(sm.requests)
	if len(sm.logs) >= sm.maxLogs {
		// Replace oldest entry
		sm.logs[sm.logIdx] = req
		sm.logIdx = (sm.logIdx + 1) % sm.maxLogs
	} else {
		sm.logs = append(sm.logs, req)
	}
}

// GetStats returns current statistics.
func (sm *StatsManager) GetStats() map[string]interface{} {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	uptime := time.Since(sm.start).Truncate(time.Second)
	rps := float64(0)
	if uptime.Seconds() > 0 {
		rps = float64(sm.requests) / uptime.Seconds()
	}

	return map[string]interface{}{
		"total_requests":      sm.requests,
		"total_input_tokens":  sm.tokensIn,
		"total_output_tokens": sm.tokensOut,
		"avg_latency_ms":      sm.avgLatency,
		"requests_per_second": rps,
		"uptime":              uptime.String(),
		"start_time":          sm.start.Format(time.RFC3339),
		"log_count":           len(sm.logs),
	}
}

// GetLogs returns the request logs, newest first.
func (sm *StatsManager) GetLogs() []RequestLog {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	// Return sorted newest first
	out := make([]RequestLog, len(sm.logs))
	copy(out, sm.logs)

	// Sort by ID descending
	for i := 0; i < len(out)/2; i++ {
		j := len(out) - 1 - i
		out[i], out[j] = out[j], out[i]
	}

	return out
}

// StatsJSON returns stats as JSON.
func (sm *StatsManager) StatsJSON() ([]byte, error) {
	return json.Marshal(sm.GetStats())
}

// LogsJSON returns logs as JSON.
func (sm *StatsManager) LogsJSON() ([]byte, error) {
	return json.Marshal(sm.GetLogs())
}
