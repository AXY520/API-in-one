package handler

import (
	"api-in-one/config"
	"api-in-one/relay"
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestChannelByNameQueryHandlesSlashNames(t *testing.T) {
	gin.SetMode(gin.TestMode)
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	cfg := []byte(`server:
  port: 3000
  admin_key: admin
  access_keys:
    - client
channels:
  - name: https://linux.do/t/topic/2338340
    type: openai
    base_url: https://sub.100xlabs.space/v1
    keys:
      - sk-test
    models:
      - test-model
    model_mapping: {}
    priority: 10
    weight: 100
    enabled: true
`)
	if err := os.WriteFile(cfgPath, cfg, 0600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	if err := config.Load(cfgPath); err != nil {
		t.Fatalf("load config: %v", err)
	}
	pool := relay.NewPool([]*relay.Channel{
		relay.NewChannelFromConfig(config.GetChannels()[0]),
	})
	admin := NewAdmin(pool)

	r := gin.New()
	r.GET("/channels/by-name/keys", admin.GetChannelKeys)
	r.PUT("/channels/by-name/state", admin.UpdateChannelState)

	name := "https://linux.do/t/topic/2338340"
	req := httptest.NewRequest(http.MethodGet, "/channels/by-name/keys?name="+url.QueryEscape(name), nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected keys request to succeed, got %d: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), name) {
		t.Fatalf("response did not include channel name: %s", w.Body.String())
	}

	req = httptest.NewRequest(http.MethodPut, "/channels/by-name/state?name="+url.QueryEscape(name), strings.NewReader(`{"enabled":false}`))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected state request to succeed, got %d: %s", w.Code, w.Body.String())
	}
}

func TestProbeClaudeOnlyChannelUsesClaudeProtocol(t *testing.T) {
	gin.SetMode(gin.TestMode)
	var gotPath string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":"msg_test","type":"message","role":"assistant","content":[{"type":"text","text":"ok"}],"model":"claude-sonnet-4-20250514","stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1}}`))
	}))
	defer upstream.Close()

	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	cfg := []byte(`server:
  port: 3000
  admin_key: admin
  access_keys:
    - client
channels:
  - name: claude-only
    type: openai
    base_url: ""
    base_url_claude: "` + upstream.URL + `"
    keys:
      - sk-test
    models:
      - claude-sonnet-4-20250514
    model_mapping: {}
    priority: 10
    weight: 100
    enabled: true
`)
	if err := os.WriteFile(cfgPath, cfg, 0600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	if err := config.Load(cfgPath); err != nil {
		t.Fatalf("load config: %v", err)
	}
	pool := relay.NewPool([]*relay.Channel{
		relay.NewChannelFromConfig(config.GetChannels()[0]),
	})
	admin := NewAdmin(pool)

	r := gin.New()
	r.POST("/channels/by-name/probe", admin.ProbeChannelKeys)

	req := httptest.NewRequest(http.MethodPost, "/channels/by-name/probe?name=claude-only&model=claude-sonnet-4-20250514", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected probe to succeed, got %d: %s", w.Code, w.Body.String())
	}
	if gotPath != "/v1/messages" {
		t.Fatalf("expected Claude probe path /v1/messages, got %q", gotPath)
	}
	var resp struct {
		Protocol string `json:"protocol"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode probe response: %v", err)
	}
	if resp.Protocol != "claude" {
		t.Fatalf("expected probe protocol claude, got %q", resp.Protocol)
	}
}

func TestChannelCanUseClaudeURLWithoutStandardBaseURL(t *testing.T) {
	gin.SetMode(gin.TestMode)
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	cfg := []byte(`server:
  port: 3000
  admin_key: admin
  access_keys:
    - client
channels: []
`)
	if err := os.WriteFile(cfgPath, cfg, 0600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	if err := config.Load(cfgPath); err != nil {
		t.Fatalf("load config: %v", err)
	}
	pool := relay.NewPool(nil)
	admin := NewAdmin(pool)

	r := gin.New()
	r.POST("/channels", admin.CreateChannel)
	r.PUT("/channels/by-name", admin.UpdateChannel)

	body := []byte(`{
		"name":"claude-only",
		"type":"claude",
		"base_url":"",
		"base_url_claude":"https://api.anthropic.com/v1",
		"keys":["sk-test"],
		"models":["claude-sonnet-4-20250514"],
		"model_mapping":{},
		"priority":10,
		"weight":100,
		"enabled":true
	}`)
	req := httptest.NewRequest(http.MethodPost, "/channels", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("expected claude-only create to succeed, got %d: %s", w.Code, w.Body.String())
	}

	updateBody := []byte(`{
		"name":"claude-only",
		"type":"claude",
		"base_url":"",
		"base_url_claude":"https://api.anthropic.com/v1",
		"keys":["sk-test"],
		"models":["claude-sonnet-4-20250514"],
		"model_mapping":{},
		"priority":10,
		"weight":100,
		"enabled":true
	}`)
	req = httptest.NewRequest(http.MethodPut, "/channels/by-name?name=claude-only", bytes.NewReader(updateBody))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected claude-only update to succeed, got %d: %s", w.Code, w.Body.String())
	}
}
