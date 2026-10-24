package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"src.solsynth.dev/sosys/filesystem/internal/config"
	"src.solsynth.dev/sosys/filesystem/internal/database"
	"src.solsynth.dev/sosys/filesystem/internal/service"
)

const handoffTestSecret = "handoff-client-secret-value"

// newHandoffIssuer stands in for Stargate's POST /api/auth/handoff/redeem.
// It records the wire request so the test can prove the drive redeems as its
// own confidential client.
func newHandoffIssuer(t *testing.T, respond func(w http.ResponseWriter, r *http.Request)) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/auth/handoff/redeem" {
			http.NotFound(w, r)
			return
		}
		respond(w, r)
	}))
	t.Cleanup(server.Close)
	return server
}

func handoffTestRouter(t *testing.T, issuer string) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	files := service.NewFileService(&database.DB{}, nil)
	tasks := service.NewTaskService(&database.DB{})
	quota := service.NewQuotaService(&database.DB{})
	RegisterRoutes(r, Deps{
		Config: &config.Config{Handoff: config.HandoffConfig{
			Issuer:       issuer,
			ClientID:     "app.solian.snfs",
			ClientSecret: handoffTestSecret,
		}},
		Files: files,
		Tasks: tasks,
		Quota: quota,
	})
	return r
}

// An unconfigured deployment must mount no new route: the redeem path falls
// through to the engine's JSON 404, exactly like any unknown API path.
func TestHandoffRouteNotMountedWhenUnconfigured(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	files := service.NewFileService(&database.DB{}, nil)
	tasks := service.NewTaskService(&database.DB{})
	quota := service.NewQuotaService(&database.DB{})
	RegisterRoutes(r, Deps{Config: &config.Config{}, Files: files, Tasks: tasks, Quota: quota})

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/picker/handoff/redeem", strings.NewReader(`{"code":"x"}`)))
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; the route must not be mounted when handoff is unconfigured", w.Code)
	}
}

func TestPickerHandoffRedeemReturnsTokenPair(t *testing.T) {
	var gotClientID, gotSecret, gotCode string
	issuer := newHandoffIssuer(t, func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			ClientID     string `json:"client_id"`
			Code         string `json:"code"`
			ClientSecret string `json:"client_secret"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("issuer body decode: %v", err)
		}
		gotClientID, gotSecret, gotCode = body.ClientID, body.ClientSecret, body.Code
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"token":"tok-123","refresh_token":"ref-456","expires_in":900,"refresh_expires_in":86400}`))
	})
	r := handoffTestRouter(t, issuer.URL)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/picker/handoff/redeem", strings.NewReader(`{"code":"handoff-code-1"}`)))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	if gotClientID != "app.solian.snfs" {
		t.Errorf("client_id = %q, want app.solian.snfs", gotClientID)
	}
	if gotSecret != handoffTestSecret {
		t.Errorf("client_secret = %q, want the configured secret", gotSecret)
	}
	if gotCode != "handoff-code-1" {
		t.Errorf("code = %q, want handoff-code-1", gotCode)
	}

	var res struct {
		Token            string `json:"token"`
		RefreshToken     string `json:"refresh_token"`
		ExpiresIn        int64  `json:"expires_in"`
		RefreshExpiresIn int64  `json:"refresh_expires_in"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &res); err != nil {
		t.Fatalf("response decode: %v", err)
	}
	if res.Token != "tok-123" || res.RefreshToken != "ref-456" || res.ExpiresIn != 900 || res.RefreshExpiresIn != 86400 {
		t.Errorf("token pair = %+v, want the issuer's pair", res)
	}
	// The whole point of the route: no cookie, the token travels in the body.
	if cookies := w.Result().Cookies(); len(cookies) != 0 {
		t.Errorf("Set-Cookie = %v, want none; a cross-site frame cannot receive a cookie", cookies)
	}
	if strings.Contains(w.Body.String(), handoffTestSecret) {
		t.Error("response body leaks the client secret")
	}
}

func TestPickerHandoffRedeemRejectsBlankCode(t *testing.T) {
	issuer := newHandoffIssuer(t, func(w http.ResponseWriter, _ *http.Request) {
		t.Error("issuer must not be called without a code")
	})
	r := handoffTestRouter(t, issuer.URL)

	for _, body := range []string{`{"code":""}`, `{"code":"   "}`, `{}`, `not-json`} {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/picker/handoff/redeem", strings.NewReader(body)))
		if w.Code != http.StatusBadRequest {
			t.Errorf("body %s: status = %d, want 400", body, w.Code)
		}
	}
}

func TestPickerHandoffRedeemRefusalIsGeneric(t *testing.T) {
	issuer := newHandoffIssuer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"invalid_grant","error_description":"code already consumed at the issuer"}`))
	})
	r := handoffTestRouter(t, issuer.URL)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/picker/handoff/redeem", strings.NewReader(`{"code":"used-code"}`)))
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", w.Code)
	}
	body := w.Body.String()
	if strings.Contains(body, "already consumed") {
		t.Errorf("body echoes the issuer's raw error: %s", body)
	}
	if strings.Contains(body, handoffTestSecret) {
		t.Errorf("body leaks the client secret: %s", body)
	}
	if !strings.Contains(body, "error") {
		t.Errorf("body %s does not use the error envelope", body)
	}
}

// The route must never echo the client secret, even when the issuer blames it.
func TestPickerHandoffRedeemSecretUnreachableInResponses(t *testing.T) {
	issuer := newHandoffIssuer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		// A hostile or broken issuer must not turn the secret into an echo.
		_, _ = w.Write([]byte(`{"error":"invalid_client","error_description":"` + handoffTestSecret + `"}`))
	})
	r := handoffTestRouter(t, issuer.URL)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/picker/handoff/redeem", strings.NewReader(`{"code":"any"}`)))
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", w.Code)
	}
	if strings.Contains(w.Body.String(), handoffTestSecret) {
		t.Errorf("body leaks the client secret: %s", w.Body.String())
	}
}
