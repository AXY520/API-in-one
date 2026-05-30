package handler

import (
	"api-in-one/relay"
	"sync"
	"time"
)

// RequestLog represents a single API request log entry.
type RequestLog struct {
	ID            int64              `json:"id"`
	Protocol      string             `json:"protocol"` // openai | claude-inbound | gemini-inbound
	Model         string             `json:"model"`
	ResolvedModel string             `json:"resolved_model,omitempty"`
	Channel       string             `json:"channel,omitempty"`
	Status        int                `json:"status"`   // HTTP status code
	Duration      int64              `json:"duration"` // milliseconds
	Stream        bool               `json:"stream"`
	Error         string             `json:"error,omitempty"`
	Attempts      []relay.AttemptLog `json:"attempts,omitempty"`
	Timestamp     string             `json:"timestamp"`
}

// LogStore is a ring buffer for request logs.
type LogStore struct {
	mu     sync.RWMutex
	logs   []RequestLog
	cap    int
	nextID int64
	total  int64
}

var globalLogStore = &LogStore{
	logs: make([]RequestLog, 0, 500),
	cap:  500,
}

func logRequest(protocol, model string, status int, duration time.Duration, err error) {
	logRequestDetail(RequestLog{
		Protocol: protocol,
		Model:    model,
		Status:   status,
		Duration: duration.Milliseconds(),
		Error:    errStr(err),
	})
}

func logRequestDetail(entry RequestLog) {
	if entry.Timestamp == "" {
		entry.Timestamp = time.Now().Format("2006-01-02 15:04:05")
	}
	globalLogStore.add(RequestLog{
		Protocol:      entry.Protocol,
		Model:         entry.Model,
		ResolvedModel: entry.ResolvedModel,
		Channel:       entry.Channel,
		Status:        entry.Status,
		Duration:      entry.Duration,
		Stream:        entry.Stream,
		Error:         entry.Error,
		Attempts:      entry.Attempts,
		Timestamp:     entry.Timestamp,
	})
}

func errStr(err error) string {
	if err != nil {
		return err.Error()
	}
	return ""
}

func (s *LogStore) add(entry RequestLog) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.nextID++
	entry.ID = s.nextID
	s.total++

	if len(s.logs) >= s.cap {
		// Ring buffer: drop oldest
		s.logs = append(s.logs[1:], entry)
	} else {
		s.logs = append(s.logs, entry)
	}
}

func (s *LogStore) recent(n int) []RequestLog {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if n <= 0 || n > len(s.logs) {
		n = len(s.logs)
	}
	start := len(s.logs) - n
	result := make([]RequestLog, n)
	copy(result, s.logs[start:])
	// Reverse to newest first
	for i, j := 0, len(result)-1; i < j; i, j = i+1, j-1 {
		result[i], result[j] = result[j], result[i]
	}
	return result
}

func (s *LogStore) stats() map[string]interface{} {
	s.mu.RLock()
	defer s.mu.RUnlock()

	total := s.total
	var successCount, errorCount int64
	var totalDuration int64
	modelCounts := map[string]int64{}
	protocolCounts := map[string]int64{}

	for _, l := range s.logs {
		if l.Status >= 200 && l.Status < 400 {
			successCount++
		} else {
			errorCount++
		}
		totalDuration += l.Duration
		modelCounts[l.Model]++
		protocolCounts[l.Protocol]++
	}

	var avgDuration int64
	if len(s.logs) > 0 {
		avgDuration = totalDuration / int64(len(s.logs))
	}

	return map[string]interface{}{
		"total":           total,
		"success":         successCount,
		"error":           errorCount,
		"avg_duration_ms": avgDuration,
		"models":          modelCounts,
		"protocols":       protocolCounts,
		"recent_count":    len(s.logs),
	}
}
