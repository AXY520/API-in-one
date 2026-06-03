package handler

import (
	"api-in-one/relay"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

// RequestLog represents a single API request log entry.
type RequestLog struct {
	ID              int64              `json:"id"`
	Protocol        string             `json:"protocol"` // openai | claude-inbound | gemini-inbound | responses
	Mode            string             `json:"mode"`     // passthrough | converted
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
	SinceID   int64
	UntilID   int64
	Protocol  string
	Model     string
	Channel   string
	AccessKey string
	Status    string
	Query     string
}

// LogStore persists request logs in SQLite. List queries intentionally return
// metadata only; request bodies are loaded by the detail endpoint.
type LogStore struct {
	mu         sync.RWMutex
	writeMu    sync.Mutex
	db         *sql.DB
	path       string
	inserted   int64
	maxEntries int
}

var globalLogStore = &LogStore{}

const (
	defaultLogPath    = "data/request_logs.sqlite3"
	legacyJSONLogPath = "data/request_logs.json"
	defaultMaxLogRows = 20000
	pruneEveryRows    = 200
)

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
	if entry.Mode == "" {
		if entry.UpstreamRequest != nil {
			entry.Mode = "converted"
		} else {
			entry.Mode = "passthrough"
		}
	}
	globalLogStore.add(entry)
}

func beginRequestLog(entry RequestLog) int64 {
	if entry.Timestamp == "" {
		entry.Timestamp = time.Now().Format("2006-01-02 15:04:05")
	}
	if entry.Mode == "" {
		if entry.UpstreamRequest != nil {
			entry.Mode = "converted"
		} else {
			entry.Mode = "passthrough"
		}
	}
	if entry.Status == 0 {
		entry.Status = 102
	}
	return globalLogStore.insert(entry)
}

func finishRequestLog(id int64, entry RequestLog) {
	if id <= 0 {
		logRequestDetail(entry)
		return
	}
	if entry.Mode == "" {
		if entry.UpstreamRequest != nil {
			entry.Mode = "converted"
		} else {
			entry.Mode = "passthrough"
		}
	}
	globalLogStore.update(id, entry)
}

func errStr(err error) string {
	if err != nil {
		return err.Error()
	}
	return ""
}

var (
	logChan   = make(chan RequestLog, 4096)
	startOnce sync.Once
)

func startLogProcessor() {
	startOnce.Do(func() {
		go func() {
			for entry := range logChan {
				globalLogStore.save(entry)
			}
		}()
	})
}

func InitLogStore(path string) {
	if path == "" {
		path = defaultLogPath
	}
	legacyPath := legacyPathFor(path)
	if strings.HasSuffix(path, ".json") {
		path = strings.TrimSuffix(path, ".json") + ".sqlite3"
	}
	if err := globalLogStore.open(path); err != nil {
		slog.Warn("failed to initialize request log database", "path", path, "error", err)
		return
	}
	startLogProcessor()
	if err := globalLogStore.importLegacyJSON(legacyPath); err != nil {
		slog.Warn("failed to import legacy request logs", "error", err)
	}
}

func (s *LogStore) open(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return err
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	if _, err := db.Exec(`PRAGMA journal_mode=WAL; PRAGMA synchronous=NORMAL; PRAGMA busy_timeout=5000;`); err != nil {
		_ = db.Close()
		return err
	}
	if _, err := db.Exec(`
CREATE TABLE IF NOT EXISTS request_logs (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  protocol TEXT NOT NULL,
  mode TEXT NOT NULL DEFAULT 'passthrough',
  model TEXT NOT NULL,
  resolved_model TEXT,
  channel TEXT,
  access_key TEXT,
  status INTEGER NOT NULL,
  duration INTEGER NOT NULL,
  stream INTEGER NOT NULL,
  error TEXT,
  attempts_json TEXT,
  request_json TEXT,
  upstream_request_json TEXT,
  timestamp TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_request_logs_timestamp ON request_logs(id DESC);
CREATE INDEX IF NOT EXISTS idx_request_logs_protocol ON request_logs(protocol);
CREATE INDEX IF NOT EXISTS idx_request_logs_model ON request_logs(model);
CREATE INDEX IF NOT EXISTS idx_request_logs_resolved_model ON request_logs(resolved_model);
CREATE INDEX IF NOT EXISTS idx_request_logs_channel ON request_logs(channel);
CREATE INDEX IF NOT EXISTS idx_request_logs_access_key ON request_logs(access_key);
CREATE INDEX IF NOT EXISTS idx_request_logs_status ON request_logs(status);
CREATE INDEX IF NOT EXISTS idx_request_logs_mode ON request_logs(mode);
`); err != nil {
		_ = db.Close()
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db != nil {
		_ = s.db.Close()
	}
	s.db = db
	s.path = path
	s.maxEntries = defaultMaxLogRows
	return nil
}

func legacyPathFor(path string) string {
	if strings.HasSuffix(path, ".json") {
		return path
	}
	return filepath.Join(filepath.Dir(path), "request_logs.json")
}

func (s *LogStore) add(entry RequestLog) {
	select {
	case logChan <- entry:
	default:
		slog.Warn("request log queue is full, dropping log entry", "model", entry.Model)
	}
}

func (s *LogStore) insert(entry RequestLog) int64 {
	s.mu.RLock()
	db := s.db
	s.mu.RUnlock()
	if db == nil {
		return 0
	}
	attemptsJSON := mustJSON(entry.Attempts)
	requestJSON := mustJSON(entry.Request)
	upstreamJSON := mustJSON(entry.UpstreamRequest)
	stream := 0
	if entry.Stream {
		stream = 1
	}
	s.writeMu.Lock()
	res, err := db.Exec(`
	INSERT INTO request_logs
	(protocol, mode, model, resolved_model, channel, access_key, status, duration, stream, error, attempts_json, request_json, upstream_request_json, timestamp)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		entry.Protocol, entry.Mode, entry.Model, entry.ResolvedModel, entry.Channel, entry.AccessKey, entry.Status, entry.Duration,
		stream, entry.Error, attemptsJSON, requestJSON, upstreamJSON, entry.Timestamp,
	)
	s.writeMu.Unlock()
	if err != nil {
		slog.Warn("failed to insert request log", "error", err)
		return 0
	}
	id, _ := res.LastInsertId()
	s.afterInsert()
	return id
}

func (s *LogStore) save(entry RequestLog) {
	s.mu.RLock()
	db := s.db
	s.mu.RUnlock()
	if db == nil {
		return
	}
	attemptsJSON := mustJSON(entry.Attempts)
	requestJSON := mustJSON(entry.Request)
	upstreamJSON := mustJSON(entry.UpstreamRequest)
	stream := 0
	if entry.Stream {
		stream = 1
	}
	s.writeMu.Lock()
	if _, err := db.Exec(`
	INSERT INTO request_logs
	(protocol, mode, model, resolved_model, channel, access_key, status, duration, stream, error, attempts_json, request_json, upstream_request_json, timestamp)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		entry.Protocol, entry.Mode, entry.Model, entry.ResolvedModel, entry.Channel, entry.AccessKey, entry.Status, entry.Duration,
		stream, entry.Error, attemptsJSON, requestJSON, upstreamJSON, entry.Timestamp,
	); err != nil {
		s.writeMu.Unlock()
		slog.Warn("failed to insert request log", "error", err)
		return
	}
	s.writeMu.Unlock()
	s.afterInsert()
}

func (s *LogStore) afterInsert() {
	s.mu.Lock()
	s.inserted++
	shouldPrune := s.maxEntries > 0 && s.inserted%pruneEveryRows == 0
	s.mu.Unlock()
	if shouldPrune {
		s.prune()
	}
}

func (s *LogStore) update(id int64, entry RequestLog) {
	s.mu.RLock()
	db := s.db
	s.mu.RUnlock()
	if db == nil {
		return
	}
	attemptsJSON := mustJSON(entry.Attempts)
	requestJSON := mustJSON(entry.Request)
	upstreamJSON := mustJSON(entry.UpstreamRequest)
	stream := 0
	if entry.Stream {
		stream = 1
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if _, err := db.Exec(`
	UPDATE request_logs
	SET protocol = ?, mode = ?, model = ?, resolved_model = ?, channel = ?, access_key = ?, status = ?, duration = ?, stream = ?, error = ?, attempts_json = ?, request_json = ?, upstream_request_json = ?
WHERE id = ?`,
		entry.Protocol, entry.Mode, entry.Model, entry.ResolvedModel, entry.Channel, entry.AccessKey, entry.Status, entry.Duration,
		stream, entry.Error, attemptsJSON, requestJSON, upstreamJSON, id,
	); err != nil {
		slog.Warn("failed to update request log", "id", id, "error", err)
	}
}

func (s *LogStore) clear() error {
	db := s.database()
	if db == nil {
		return nil
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if _, err := db.Exec(`DELETE FROM request_logs`); err != nil {
		return err
	}
	if _, err := db.Exec(`VACUUM`); err != nil {
		return err
	}
	s.mu.Lock()
	s.inserted = 0
	s.mu.Unlock()
	return nil
}

func (s *LogStore) prune() {
	db := s.database()
	if db == nil {
		return
	}
	s.mu.RLock()
	maxEntries := s.maxEntries
	s.mu.RUnlock()
	if maxEntries <= 0 {
		return
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if _, err := db.Exec(`DELETE FROM request_logs WHERE id NOT IN (SELECT id FROM request_logs ORDER BY id DESC LIMIT ?)`, maxEntries); err != nil {
		slog.Warn("failed to prune request logs", "max_entries", maxEntries, "error", err)
	}
}

func (s *LogStore) search(filter LogFilter) []RequestLog {
	db := s.database()
	if db == nil {
		return nil
	}
	filter = normalizeLogFilter(filter)
	where, args := buildLogWhere(filter)
	args = append(args, filter.Limit, filter.Offset)
	rows, err := db.Query(`
SELECT id, protocol, mode, model, resolved_model, channel, access_key, status, duration, stream, error, attempts_json, timestamp
FROM request_logs `+where+`
ORDER BY id DESC
LIMIT ? OFFSET ?`, args...)
	if err != nil {
		slog.Warn("failed to query request logs", "error", err)
		return nil
	}
	defer rows.Close()

	result := make([]RequestLog, 0, filter.Limit)
	for rows.Next() {
		var log RequestLog
		var stream int
		var attemptsJSON string
		if err := rows.Scan(&log.ID, &log.Protocol, &log.Mode, &log.Model, &log.ResolvedModel, &log.Channel, &log.AccessKey, &log.Status, &log.Duration, &stream, &log.Error, &attemptsJSON, &log.Timestamp); err != nil {
			slog.Warn("failed to scan request log", "error", err)
			continue
		}
		log.Stream = stream == 1
		_ = json.Unmarshal([]byte(attemptsJSON), &log.Attempts)
		result = append(result, log)
	}
	return result
}

func (s *LogStore) export(filter LogFilter) []RequestLog {
	db := s.database()
	if db == nil {
		return nil
	}
	filter = normalizeLogExportFilter(filter)
	where, args := buildLogWhere(filter)
	args = append(args, filter.Limit, filter.Offset)
	rows, err := db.Query(`
SELECT id, protocol, mode, model, resolved_model, channel, access_key, status, duration, stream, error, attempts_json, request_json, upstream_request_json, timestamp
FROM request_logs `+where+`
ORDER BY id DESC
LIMIT ? OFFSET ?`, args...)
	if err != nil {
		slog.Warn("failed to export request logs", "error", err)
		return nil
	}
	defer rows.Close()

	result := make([]RequestLog, 0, filter.Limit)
	for rows.Next() {
		log, err := scanFullRequestLog(rows)
		if err != nil {
			slog.Warn("failed to scan exported request log", "error", err)
			continue
		}
		result = append(result, log)
	}
	return result
}

func (s *LogStore) count(filter LogFilter) int {
	db := s.database()
	if db == nil {
		return 0
	}
	where, args := buildLogWhere(filter)
	var total int
	if err := db.QueryRow(`SELECT COUNT(*) FROM request_logs `+where, args...).Scan(&total); err != nil {
		slog.Warn("failed to count request logs", "error", err)
		return 0
	}
	return total
}

func (s *LogStore) total() int64 {
	db := s.database()
	if db == nil {
		return 0
	}
	var total int64
	if err := db.QueryRow(`SELECT COUNT(*) FROM request_logs`).Scan(&total); err != nil {
		return 0
	}
	return total
}

func (s *LogStore) get(id int64) (RequestLog, bool) {
	db := s.database()
	if db == nil {
		return RequestLog{}, false
	}
	var log RequestLog
	var stream int
	var attemptsJSON, requestJSON, upstreamJSON string
	err := db.QueryRow(`
SELECT id, protocol, mode, model, resolved_model, channel, access_key, status, duration, stream, error, attempts_json, request_json, upstream_request_json, timestamp
FROM request_logs WHERE id = ?`, id).
		Scan(&log.ID, &log.Protocol, &log.Mode, &log.Model, &log.ResolvedModel, &log.Channel, &log.AccessKey, &log.Status, &log.Duration, &stream, &log.Error, &attemptsJSON, &requestJSON, &upstreamJSON, &log.Timestamp)
	if errors.Is(err, sql.ErrNoRows) {
		return RequestLog{}, false
	}
	if err != nil {
		slog.Warn("failed to get request log", "id", id, "error", err)
		return RequestLog{}, false
	}
	log.Stream = stream == 1
	_ = json.Unmarshal([]byte(attemptsJSON), &log.Attempts)
	log.Request = decodeJSONValue(requestJSON)
	log.UpstreamRequest = decodeJSONValue(upstreamJSON)
	return log, true
}

type fullLogScanner interface {
	Scan(dest ...interface{}) error
}

func scanFullRequestLog(scanner fullLogScanner) (RequestLog, error) {
	var log RequestLog
	var stream int
	var attemptsJSON, requestJSON, upstreamJSON string
	if err := scanner.Scan(&log.ID, &log.Protocol, &log.Mode, &log.Model, &log.ResolvedModel, &log.Channel, &log.AccessKey, &log.Status, &log.Duration, &stream, &log.Error, &attemptsJSON, &requestJSON, &upstreamJSON, &log.Timestamp); err != nil {
		return RequestLog{}, err
	}
	log.Stream = stream == 1
	_ = json.Unmarshal([]byte(attemptsJSON), &log.Attempts)
	log.Request = decodeJSONValue(requestJSON)
	log.UpstreamRequest = decodeJSONValue(upstreamJSON)
	return log, nil
}

func (s *LogStore) stats() map[string]interface{} {
	db := s.database()
	if db == nil {
		return emptyStats()
	}
	var total, successCount, errorCount, avgDuration int64
	_ = db.QueryRow(`SELECT COUNT(*), COALESCE(CAST(AVG(duration) AS INTEGER), 0) FROM request_logs`).Scan(&total, &avgDuration)
	_ = db.QueryRow(`SELECT COUNT(*) FROM request_logs WHERE status >= 200 AND status < 400`).Scan(&successCount)
	_ = db.QueryRow(`SELECT COUNT(*) FROM request_logs WHERE status < 200 OR status >= 400`).Scan(&errorCount)
	return map[string]interface{}{
		"total":           total,
		"success":         successCount,
		"error":           errorCount,
		"avg_duration_ms": avgDuration,
		"models":          groupedCounts(db, "model"),
		"protocols":       groupedCounts(db, "protocol"),
		"modes":           groupedCounts(db, "mode"),
		"recent_count":    total,
	}
}

func (s *LogStore) database() *sql.DB {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.db
}

func normalizeLogFilter(filter LogFilter) LogFilter {
	if filter.Limit <= 0 {
		filter.Limit = 50
	}
	if filter.Limit > 200 {
		filter.Limit = 200
	}
	if filter.Offset < 0 {
		filter.Offset = 0
	}
	return filter
}

func normalizeLogExportFilter(filter LogFilter) LogFilter {
	if filter.Limit <= 0 {
		filter.Limit = 100
	}
	if filter.Limit > 1000 {
		filter.Limit = 1000
	}
	if filter.Offset < 0 {
		filter.Offset = 0
	}
	return filter
}

func buildLogWhere(filter LogFilter) (string, []interface{}) {
	clauses := make([]string, 0, 8)
	args := make([]interface{}, 0, 8)
	if filter.SinceID > 0 {
		clauses = append(clauses, "id > ?")
		args = append(args, filter.SinceID)
	}
	if filter.UntilID > 0 {
		clauses = append(clauses, "id <= ?")
		args = append(args, filter.UntilID)
	}
	if filter.Protocol != "" {
		clauses = append(clauses, "protocol = ?")
		args = append(args, filter.Protocol)
	}
	if filter.Model != "" {
		clauses = append(clauses, "(model = ? OR resolved_model = ?)")
		args = append(args, filter.Model, filter.Model)
	}
	if filter.Channel != "" {
		clauses = append(clauses, "(channel = ? OR attempts_json LIKE ?)")
		args = append(args, filter.Channel, "%\"channel\":\""+escapeLike(filter.Channel)+"\"%")
	}
	if filter.AccessKey != "" {
		clauses = append(clauses, "access_key = ?")
		args = append(args, filter.AccessKey)
	}
	switch filter.Status {
	case "success":
		clauses = append(clauses, "status >= 200 AND status < 400")
	case "error":
		clauses = append(clauses, "(status < 200 OR status >= 400)")
	default:
		if filter.Status != "" {
			if status, err := strconv.Atoi(filter.Status); err == nil {
				clauses = append(clauses, "status = ?")
				args = append(args, status)
			}
		}
	}
	if filter.Query != "" {
		q := "%" + escapeLike(filter.Query) + "%"
		clauses = append(clauses, "(model LIKE ? ESCAPE '\\' OR resolved_model LIKE ? ESCAPE '\\' OR channel LIKE ? ESCAPE '\\' OR access_key LIKE ? ESCAPE '\\' OR error LIKE ? ESCAPE '\\' OR CAST(id AS TEXT) LIKE ? ESCAPE '\\')")
		args = append(args, q, q, q, q, q, q)
	}
	if len(clauses) == 0 {
		return "", args
	}
	return "WHERE " + strings.Join(clauses, " AND "), args
}

func escapeLike(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `%`, `\%`)
	s = strings.ReplaceAll(s, `_`, `\_`)
	return s
}

func mustJSON(v interface{}) string {
	if v == nil {
		return ""
	}
	data, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	return string(data)
}

func decodeJSONValue(raw string) interface{} {
	if raw == "" {
		return nil
	}
	var v interface{}
	if err := json.Unmarshal([]byte(raw), &v); err != nil {
		return nil
	}
	return v
}

func groupedCounts(db *sql.DB, column string) map[string]int64 {
	if column != "model" && column != "protocol" && column != "mode" {
		return map[string]int64{}
	}
	rows, err := db.Query(fmt.Sprintf(`SELECT %s, COUNT(*) FROM request_logs GROUP BY %s ORDER BY COUNT(*) DESC LIMIT 50`, column, column))
	if err != nil {
		return map[string]int64{}
	}
	defer rows.Close()
	result := map[string]int64{}
	for rows.Next() {
		var key string
		var count int64
		if err := rows.Scan(&key, &count); err == nil {
			result[key] = count
		}
	}
	return result
}

func emptyStats() map[string]interface{} {
	return map[string]interface{}{
		"total":           int64(0),
		"success":         int64(0),
		"error":           int64(0),
		"avg_duration_ms": int64(0),
		"models":          map[string]int64{},
		"protocols":       map[string]int64{},
		"modes":           map[string]int64{},
		"recent_count":    int64(0),
	}
}

func (s *LogStore) importLegacyJSON(path string) error {
	db := s.database()
	if db == nil {
		return nil
	}
	var existing int
	if err := db.QueryRow(`SELECT COUNT(*) FROM request_logs`).Scan(&existing); err != nil {
		return err
	}
	if existing > 0 {
		return nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	var snapshot struct {
		Logs []RequestLog `json:"logs"`
	}
	if err := json.Unmarshal(data, &snapshot); err != nil {
		return err
	}
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	stmt, err := tx.Prepare(`
INSERT INTO request_logs
(id, protocol, mode, model, resolved_model, channel, access_key, status, duration, stream, error, attempts_json, request_json, upstream_request_json, timestamp)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		_ = tx.Rollback()
		return err
	}
	defer stmt.Close()
	for _, entry := range snapshot.Logs {
		if entry.Mode == "" {
			if entry.UpstreamRequest != nil {
				entry.Mode = "converted"
			} else {
				entry.Mode = "passthrough"
			}
		}
		stream := 0
		if entry.Stream {
			stream = 1
		}
		if _, err := stmt.Exec(entry.ID, entry.Protocol, entry.Mode, entry.Model, entry.ResolvedModel, entry.Channel, entry.AccessKey, entry.Status, entry.Duration, stream, entry.Error, mustJSON(entry.Attempts), mustJSON(entry.Request), mustJSON(entry.UpstreamRequest), entry.Timestamp); err != nil {
			_ = tx.Rollback()
			return err
		}
	}
	return tx.Commit()
}
