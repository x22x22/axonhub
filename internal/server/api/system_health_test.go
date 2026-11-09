package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"

	"github.com/looplj/axonhub/internal/ent/enttest"
)

func newHealthTestHandlers(t *testing.T) *SystemHandlers {
	t.Helper()

	gin.SetMode(gin.TestMode)

	client := enttest.NewEntClient(t, "sqlite3", "file:ent?mode=memory&_fk=1")
	t.Cleanup(func() { _ = client.Close() })

	return &SystemHandlers{Client: client}
}

func doHealthRequest(t *testing.T, handler gin.HandlerFunc, path string) *httptest.ResponseRecorder {
	t.Helper()

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, path, nil)

	handler(c)

	return w
}

// TestHealthIncludesDatabaseCheck makes sure /health surfaces the dependency state.
//
// On 2026-11-09 the gateway database stayed locked for 30 minutes while /health
// kept returning 200, so the outage was invisible to monitoring. /health still
// returns 200 (a restart never fixes a database), but it now reports the check.
func TestHealthIncludesDatabaseCheck(t *testing.T) {
	h := newHealthTestHandlers(t)

	w := doHealthRequest(t, h.Health, "/health")
	require.Equal(t, http.StatusOK, w.Code)

	var resp HealthResponse

	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.Equal(t, "healthy", resp.Status)
	require.Equal(t, "ok", resp.Checks.Database)
}

// TestReadyOKWhenDatabaseIsReachable covers the happy path of the readiness probe.
func TestReadyOKWhenDatabaseIsReachable(t *testing.T) {
	h := newHealthTestHandlers(t)

	w := doHealthRequest(t, h.Ready, "/ready")
	require.Equal(t, http.StatusOK, w.Code)

	var resp ReadyResponse

	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.Equal(t, "ready", resp.Status)
	require.Equal(t, "ok", resp.Checks.Database)
}

// TestReadyReturns503WhenDatabaseIsDown is the behaviour that would have caught the
// 2026-11-09 outage: readiness must fail when the database cannot be queried.
func TestReadyReturns503WhenDatabaseIsDown(t *testing.T) {
	h := newHealthTestHandlers(t)

	require.NoError(t, h.Client.Close())

	w := doHealthRequest(t, h.Ready, "/ready")
	require.Equal(t, http.StatusServiceUnavailable, w.Code)

	var resp ReadyResponse

	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.Equal(t, "unavailable", resp.Status)
	require.Equal(t, "error", resp.Checks.Database)
}
