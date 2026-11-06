package middleware

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

func scopeRouter(a *AuthMiddleware) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	api := r.Group("/api")
	api.Use(a.RequireAuth())
	ok := func(c *gin.Context) { c.String(http.StatusOK, "ok") }
	api.GET("/jobs", ok)
	api.GET("/jobs/:id", ok)
	api.PUT("/settings/password", ok)
	api.POST("/webhooks", ok)
	api.POST("/ai/api-key", ok)
	api.DELETE("/printers/:id", ok)
	api.GET("/filter/status", ok)
	return r
}

func machineCall(r *gin.Engine, method, path string, at time.Time) int {
	body := []byte(`{}`)
	req := httptest.NewRequest(method, path, bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+SignORRN("node-secret", method, path, body, at))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w.Code
}

// A valid machine token is refused (403) on every route outside the machine
// scope; the admin JWT still reaches them.
func TestMachineTokenScope(t *testing.T) {
	a := &AuthMiddleware{secret: []byte("jwt-secret-for-tests")}
	a.SetORRNVerifier(NewORRNVerifier("node-secret"))
	r := scopeRouter(a)
	now := time.Now()

	cases := []struct {
		method, path string
		want         int
	}{
		{http.MethodGet, "/api/jobs/7", http.StatusOK},
		{http.MethodGet, "/api/jobs", http.StatusForbidden},
		{http.MethodPut, "/api/settings/password", http.StatusForbidden},
		{http.MethodPost, "/api/webhooks", http.StatusForbidden},
		{http.MethodPost, "/api/ai/api-key", http.StatusForbidden},
		{http.MethodDelete, "/api/printers/1", http.StatusForbidden},
		{http.MethodGet, "/api/filter/status", http.StatusForbidden},
	}
	for i, tc := range cases {
		if got := machineCall(r, tc.method, tc.path, now.Add(time.Duration(i)*time.Millisecond)); got != tc.want {
			t.Errorf("machine %s %s: %d, want %d", tc.method, tc.path, got, tc.want)
		}
	}

	jwtToken, err := a.generateToken()
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPut, "/api/settings/password", nil)
	req.Header.Set("Authorization", "Bearer "+jwtToken)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("admin JWT on an admin route: %d", w.Code)
	}
}

// A deployment can add its own machine routes without widening the defaults.
func TestAllowMachineRoutes(t *testing.T) {
	a := &AuthMiddleware{secret: []byte("jwt-secret-for-tests")}
	a.SetORRNVerifier(NewORRNVerifier("node-secret"))
	a.AllowMachineRoutes("GET /api/filter/status")
	r := scopeRouter(a)
	now := time.Now()
	if got := machineCall(r, http.MethodGet, "/api/filter/status", now); got != http.StatusOK {
		t.Fatalf("added route: %d, want 200", got)
	}
	if got := machineCall(r, http.MethodGet, "/api/jobs/1", now.Add(time.Millisecond)); got != http.StatusOK {
		t.Fatalf("default route after AllowMachineRoutes: %d, want 200", got)
	}
	if got := machineCall(r, http.MethodPut, "/api/settings/password", now.Add(2*time.Millisecond)); got != http.StatusForbidden {
		t.Fatalf("admin route after AllowMachineRoutes: %d, want 403", got)
	}
	if defaultMachineRouteSet["GET /api/filter/status"] {
		t.Fatal("AllowMachineRoutes changed the package defaults")
	}
}
