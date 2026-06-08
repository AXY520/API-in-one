package handler

import (
	"api-in-one/config"
	"api-in-one/relay"
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
