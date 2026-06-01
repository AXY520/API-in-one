package middleware

import (
	"api-in-one/config"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestAPIAuthRejectsAdminKeyAndAcceptsAccessKey(t *testing.T) {
	gin.SetMode(gin.TestMode)
	configPath := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(configPath, []byte(`
server:
  admin_key: admin-secret
  access_keys:
    - key: client-secret
`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := config.Load(configPath); err != nil {
		t.Fatal(err)
	}

	router := gin.New()
	router.GET("/v1/models", APIAuth(), func(c *gin.Context) {
		c.Status(http.StatusNoContent)
	})

	for _, test := range []struct {
		name       string
		token      string
		wantStatus int
	}{
		{name: "admin key", token: "admin-secret", wantStatus: http.StatusUnauthorized},
		{name: "access key", token: "client-secret", wantStatus: http.StatusNoContent},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
			request.Header.Set("Authorization", "Bearer "+test.token)
			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)
			if response.Code != test.wantStatus {
				t.Fatalf("status = %d, want %d", response.Code, test.wantStatus)
			}
		})
	}
}
