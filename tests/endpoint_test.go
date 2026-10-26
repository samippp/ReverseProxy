package tests // Change return type from auth.Claims to *auth.Claimpackage tests

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/samippp/ReverseProxy/api"
	"github.com/samippp/ReverseProxy/config"
)

// --- Tests ---

func TestLoadConfig(t *testing.T) {
	c := config.ImportServerConfig()
	t.Log(c)
	t.Log("---------Config Test-------------")
}

func TestRegisterWithServer(t *testing.T) {
	t.Log("------------------Register-----------------")
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	// Reusable creation + cleanup setup
	_, resp := createTestUser(t, "John", "meow", "john@gmail.com")
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("Failed to read response body: %v", err)
	}
	t.Log(string(body))

	if resp.StatusCode != http.StatusOK {
		t.Errorf("Expected status 200 but got %d", resp.StatusCode)
	}

	u, err := testStore.GetUserByUsername(ctx, "John")
	if err != nil {
		t.Errorf("User 'John' not found in db: %v", err)
	} else {
		t.Log(u)
	}
}

func TestLoginWithServer(t *testing.T) {
	t.Log("------------------Login-----------------")

	// Create user (handles payload serialization + DB cleanup)
	payload, regResp := createTestUser(t, "John", "meow", "john@gmail.com")
	if err := regResp.Body.Close(); err != nil {
		t.Logf("Warning: failed to close setup response body: %v", err)
	}

	// Reusable login call + token validation
	claims, _ := performLoginAndValidateToken(t, payload)
	t.Log(claims)
}

func TestLoginWithServer_WrongPassword_Returns401(t *testing.T) {
	t.Log("------------------Login Wrong Password-----------------")

	_, regResp := createTestUser(t, "Mallory", "meow", "mallory@gmail.com")
	if err := regResp.Body.Close(); err != nil {
		t.Logf("Warning: failed to close setup response body: %v", err)
	}

	payload, err := json.Marshal(api.CreateUserRequest{Username: "Mallory", Password: "wrong"})
	if err != nil {
		t.Fatalf("failed to marshal login payload: %v", err)
	}
	resp, err := http.Post(testServer.URL+"/login", "application/json", bytes.NewBuffer(payload))
	if err != nil {
		t.Fatalf("HTTP POST /login failed: %v", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("failed to read response body: %v", err)
	}

	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", resp.StatusCode)
	}
	if bytes.Contains(body, []byte("token")) {
		t.Errorf("expected no token in response body, got: %s", body)
	}
}
