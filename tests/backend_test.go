package tests

import (
	"net/http"
	"testing"
)

func TestProtectedSecret_NoToken_Returns401(t *testing.T) {
	t.Log("-----------------------Test Secret No Token--------------------")
	req, err := http.NewRequest("GET", testServer.URL+"/protected/secret", nil)
	if err != nil {
		t.Fatalf("failed to build request: %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", resp.StatusCode)
	}
}

func TestProtectedSecret_ValidToken_Returns200(t *testing.T) {
	t.Log("---------------------Test Secret Valid Token--------------------")

	// 1. Create user (handles payload serialization + DB cleanup)
	payload, regResp := createTestUser(t, "John", "meow", "john@gmail.com")
	if err := regResp.Body.Close(); err != nil {
		t.Logf("Warning: failed to close setup response body: %v", err)
	}

	// 2. Perform reusable login call to get token data
	_, data := performLoginAndValidateToken(t, payload)
	tokenStr, exists := data["token"]
	if !exists || tokenStr == "" {
		t.Fatalf("TestProtectedSecret_ValidToken_Returns200: missing token in login data map")
	}

	// 3. Make request to protected endpoint using the dynamic token
	req, err := http.NewRequest("GET", testServer.URL+"/protected/secret", nil)
	if err != nil {
		t.Fatalf("failed to build request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+tokenStr)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}
}
