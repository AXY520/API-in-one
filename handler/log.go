package handler

import (
	"api-in-one/relay"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// RequestLog represents a single API request log entry.
type RequestLog struct {
	ID              int64              `json:"id"`
	Protocol        string             `json:"protocol"` // openai | claude-inbound | gemini-inbound
	Model           string             `json:"model"`
	ResolvedModel   string             `json:"resolved_model,omitempty"`
	Channel         string             `json:"channel,omitempty"`
	AccessKey       string             `json:"access_key,omitempty"`
	Status          int                `json:"status"`   // HTTP status code
	Duration        int64              `json:"duration"` // milliseconds
	Stream          bool               `json:"stream"`
	Error           string             `json:"error,omitempty"`
	Attempts        []relay.AttemptLog `json:"attempts,omitempty"`
	Request         interface{}        `json:"request,omitempty"`
	UpstreamRequest interface{}        `json:"upstream_request,omitempty"`
	Timestamp       string             `json:"timestamp"`
}

type LogFilter struct {
	Limit     int
	Offset    int
	Protocol  string
	Model     string
	Channel   string
	AccessKey string
	Status    string
	Query     string
}

// LogStore is a ring buffer for request logs.
type LogStore struct {
	mu           sync.RWMutex
	logs         []RequestLog
	cap          int
	nextID       int64
	total        int64
	persistMu    sync.Mutex
	persistTimer *time.Timer
}

var globalLogStore = &LogStore{
	logs: make([]RequestLog, 0, 5000),
	cap:  5000,
}

const defaultLogPath = "data/request_logs.json"

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
		Protocol:        entry.Protocol,
		Model:           entry.Model,
		ResolvedModel:   entry.ResolvedModel,
		Channel:         entry.Channel,
		AccessKey:       entry.AccessKey,
		Status:          entry.Status,
		Duration:        entry.Duration,
		Stream:          entry.Stream,
		Error:           entry.Error,
		Attempts:        entry.Attempts,
		Request:         entry.Request,
		UpstreamRequest: entry.UpstreamRequest,
		Timestamp:       entry.Timestamp,
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
	s.scheduleSave(defaultLogPath)
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

func (s *LogStore) search(filter LogFilter) []RequestLog {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if filter.Limit <= 0 || filter.Limit > len(s.logs) {
		filter.Limit = len(s.logs)
	}
	result := make([]RequestLog, 0, filter.Limit)
	skipped := 0
	for i := len(s.logs) - 1; i >= 0 && len(result) < filter.Limit; i-- {
		log := s.logs[i]
		if !matchesLog(log, filter) {
			continue
		}
		if skipped < filter.Offset {
			skipped++
			continue
		}
		result = append(result, log)
	}
	return result
}

func (s *LogStore) count(filter LogFilter) int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	total := 0
	filter.Offset = 0
	filter.Limit = len(s.logs)
	for _, log := range s.logs {
		if matchesLog(log, filter) {
			total++
		}
	}
	return total
}

func (s *LogStore) get(id int64) (RequestLog, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for i := len(s.logs) - 1; i >= 0; i-- {
		if s.logs[i].ID == id {
			return s.logs[i], true
		}
	}
	return RequestLog{}, false
}

func matchesLog(log RequestLog, filter LogFilter) bool {
	if filter.Protocol != "" && log.Protocol != filter.Protocol {
		return false
	}
	if filter.Model != "" && log.Model != filter.Model && log.ResolvedModel != filter.Model {
		return false
	}
	if filter.Channel != "" && log.Channel != filter.Channel {
		found := false
		for _, attempt := range log.Attempts {
			if attempt.Channel == filter.Channel {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	if filter.AccessKey != "" && log.AccessKey != filter.AccessKey {
		return false
	}
	switch filter.Status {
	case "success":
		if log.Status < 200 || log.Status >= 400 {
			return false
		}
	case "error":
		if log.Status >= 200 && log.Status < 400 {
			return false
		}
	default:
		if filter.Status != "" {
			matched := false
			statusText := strconv.Itoa(log.Status)
			if statusText == filter.Status {
				matched = true
			}
			if !matched {
				return false
			}
		}
	}
	if filter.Query != "" {
		q := filter.Query
		if !contains(log.Model, q) && !contains(log.ResolvedModel, q) && !contains(log.Channel, q) && !contains(log.AccessKey, q) && !contains(log.Error, q) && !contains(strconv.FormatInt(log.ID, 10), q) {
			return false
		}
	}
	return true
}

func InitLogStore(path string) {
	if path == "" {
		path = defaultLogPath
	}
	if err := globalLogStore.load(path); err != nil {
		slog.Warn("failed to load request logs", "path", path, "error", err)
	}
}

func (s *LogStore) load(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	var snapshot struct {
		Logs   []RequestLog `json:"logs"`
		NextID int64        `json:"next_id"`
		Total  int64        `json:"total"`
	}
	if err := json.Unmarshal(data, &snapshot); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(snapshot.Logs) > s.cap {
		snapshot.Logs = snapshot.Logs[len(snapshot.Logs)-s.cap:]
	}
	s.logs = snapshot.Logs
	s.nextID = snapshot.NextID
	s.total = snapshot.Total
	if s.total < int64(len(s.logs)) {
		s.total = int64(len(s.logs))
	}
	for _, log := range s.logs {
		if log.ID > s.nextID {
			s.nextID = log.ID
		}
	}
	return nil
}

func (s *LogStore) save(path string) {
	s.mu.RLock()
	snapshot := struct {
		Logs   []RequestLog `json:"logs"`
		NextID int64        `json:"next_id"`
		Total  int64        `json:"total"`
	}{
		Logs:   append([]RequestLog(nil), s.logs...),
		NextID: s.nextID,
		Total:  s.total,
	}
	s.mu.RUnlock()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		slog.Warn("failed to create log dir", "path", path, "error", err)
		return
	}
	tmp := path + ".tmp"
	data, err := json.MarshalIndent(snapshot, "", "  ")
	if err != nil {
		slog.Warn("failed to marshal request logs", "error", err)
		return
	}
	if err := os.WriteFile(tmp, data, 0644); err != nil {
		slog.Warn("failed to write request logs", "path", tmp, "error", err)
		return
	}
	if err := os.Rename(tmp, path); err != nil {
		slog.Warn("failed to replace request logs", "path", path, "error", err)
	}
}

func (s *LogStore) scheduleSave(path string) {
	s.persistMu.Lock()
	defer s.persistMu.Unlock()
	if s.persistTimer != nil {
		s.persistTimer.Stop()
	}
	s.persistTimer = time.AfterFunc(500*time.Millisecond, func() {
		s.save(path)
	})
}

func contains(s, sub string) bool {
	return strings.Contains(strings.ToLower(s), strings.ToLower(sub))
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
