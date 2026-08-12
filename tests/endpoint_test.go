package tests // Change return type from auth.Claims to *auth.Claimpackage tests

import (
	"context"
	"io"
	"net/http"
	"testing"
	"time"

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
