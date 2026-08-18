package tests

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/samippp/ReverseProxy/api"
	"github.com/samippp/ReverseProxy/auth"
	"github.com/samippp/ReverseProxy/config"
	"github.com/samippp/ReverseProxy/db"
	"github.com/samippp/ReverseProxy/proxy"
	"github.com/samippp/ReverseProxy/store"
)

var (
	testPool   *pgxpool.Pool
	testStore  *store.Store
	testServer *httptest.Server
)

func TestMain(m *testing.M) {
	env := config.LoadEnv()
	testPool = db.PostgreSQLConnectionPoll(env.DatabaseURL)
	testStore = store.New(testPool)

	// dummy backends that /protected/* proxies to during tests —
	// three, so round-robin behavior can actually be observed/tested
	backend1 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"secret":"ok","backend":"1"}`))
	}))
	backend2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"secret":"ok","backend":"2"}`))
	}))
	backend3 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"secret":"ok","backend":"3"}`))
	}))

	cfg := config.ImportServerConfig()
	cfg.Backends = map[string][]config.Backend{
		"secret": {
			{Name: "test-backend-1", URL: backend1.URL},
			{Name: "test-backend-2", URL: backend2.URL},
			{Name: "test-backend-3", URL: backend3.URL},
		},
	}

	router := proxy.NewRouter(testPool, cfg, env, 100*time.Millisecond)
	testServer = httptest.NewServer(router)

	code := m.Run()

	testServer.Close()
	backend1.Close()
	backend2.Close()
	backend3.Close()
	testPool.Close()

	os.Exit(code)
}

func register(payloadToSend []byte) (*http.Response, error) {
	resp, err := http.Post(testServer.URL+"/register", "application/json",
		bytes.NewBuffer(payloadToSend))
	return resp, err
}

// Helper: Serializes user, handles automatic DB cleanup, and sends login/registration payload
func createTestUser(t *testing.T, username, password, email string) ([]byte, *http.Response) {
	t.Helper()

	// 1. Register automatic cleanup for this user
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cleanupCancel()
		if err := testStore.DeleteUserByUserName(cleanupCtx, username); err != nil {
			t.Logf("Cleanup warning: failed to delete user '%s': %v", username, err)
		}
	})

	// 2. Marshal user payload
	user := api.CreateUserRequest{Username: username, Password: password, Email: email}
	payload, err := json.Marshal(user)
	if err != nil {
		t.Fatalf("createTestUser: failed to marshal user payload: %v", err)
	}

	// 3. Perform login request
	resp, err := register(payload)
	if err != nil {
		t.Fatalf("createTestUser: failed to send request: %v", err)
	}

	return payload, resp
}

// Helper: Executes /login endpoint, logs response body, and validates returned JWT token
func performLoginAndValidateToken(t *testing.T, payload []byte) (*auth.Claims, map[string]string) {
	t.Helper()

	resp, err := http.Post(
		testServer.URL+"/login",
		"application/json",
		bytes.NewBuffer(payload),
	)
	if err != nil {
		t.Fatalf("performLoginAndValidateToken: HTTP POST /login failed: %v", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("performLoginAndValidateToken: failed to read response body: %v", err)
	}

	// Print raw response body from /login
	t.Logf("Login HTTP Status: %d", resp.StatusCode)
	t.Logf("Login Response Body: %s", string(body))

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("performLoginAndValidateToken: expected status 200, got %d", resp.StatusCode)
	}

	var data map[string]string
	if err := json.Unmarshal(body, &data); err != nil {
		t.Fatalf("performLoginAndValidateToken: failed to unmarshal JSON: %v", err)
	}

	tokenStr, exists := data["token"]
	if !exists || tokenStr == "" {
		t.Fatalf("performLoginAndValidateToken: missing 'token' in response: %s", string(body))
	}

	claims, err := auth.ValidateToken(tokenStr)
	if err != nil {
		t.Fatalf("performLoginAndValidateToken: invalid JWT token: %v", err)
	}

	return claims, data
}
