package tests

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

func TestLoadConfig(t *testing.T) {
	c := config.ImportServerConfig()
	t.Log(c)
	t.Log("---------Config Test-------------")
}

func TestRegisterWithServer(t *testing.T) {
	t.Log("------------------Register-----------------")
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

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

	payload, regResp := createTestUser(t, "John", "meow", "john@gmail.com")
	if err := regResp.Body.Close(); err != nil {
		t.Logf("Warning: failed to close setup response body: %v", err)
	}

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

func TestLoginWithServer_UnknownUser_TakesAsLongAsWrongPassword(t *testing.T) {
	t.Log("------------------Login Unknown User Timing-----------------")

	_, regResp := createTestUser(t, "Trent", "meow", "trent@gmail.com")
	if err := regResp.Body.Close(); err != nil {
		t.Logf("Warning: failed to close setup response body: %v", err)
	}

	// fastest of 3, to filter scheduling noise
	timeLogin := func(username string) time.Duration {
		payload, err := json.Marshal(api.CreateUserRequest{Username: username, Password: "wrong"})
		if err != nil {
			t.Fatalf("failed to marshal login payload: %v", err)
		}
		fastest := time.Hour
		for range 3 {
			start := time.Now()
			resp, err := http.Post(testServer.URL+"/login", "application/json", bytes.NewBuffer(payload))
			if err != nil {
				t.Fatalf("HTTP POST /login failed: %v", err)
			}
			resp.Body.Close()
			if resp.StatusCode != http.StatusUnauthorized {
				t.Fatalf("login as %q: expected 401, got %d", username, resp.StatusCode)
			}
			fastest = min(fastest, time.Since(start))
		}
		return fastest
	}

	wrongPassword := timeLogin("Trent")
	unknownUser := timeLogin("NoSuchUser")
	t.Logf("wrong password: %v, unknown user: %v", wrongPassword, unknownUser)
	// without a dummy bcrypt compare, an unknown user returns ~100x faster
	if unknownUser < wrongPassword/2 {
		t.Errorf("unknown user took %v vs %v for a wrong password; timing reveals which usernames exist", unknownUser, wrongPassword)
	}
}
