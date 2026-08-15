package tests

import (
	"encoding/json"
	"io"
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

func TestProtectedSecret_RoundRobin_DistributesAcrossBackends(t *testing.T) {
	t.Log("-----------------------Test Round Robin Distribution--------------------")

	// 1. Create user + get a valid token once
	payload, regResp := createTestUser(t, "Robin", "meow", "robin@gmail.com")
	if err := regResp.Body.Close(); err != nil {
		t.Logf("Warning: failed to close setup response body: %v", err)
	}
	_, data := performLoginAndValidateToken(t, payload)
	tokenStr, exists := data["token"]
	if !exists || tokenStr == "" {
		t.Fatalf("TestProtectedSecret_RoundRobin_DistributesAcrossBackends: missing token in login data map")
	}

	// 2. Fire several requests, track which backend answered each one
	seen := map[string]int{}
	const numRequests = 9 // 3 backends × 3 full cycles

	for i := range numRequests {
		req, err := http.NewRequest("GET", testServer.URL+"/protected/secret", nil)
		if err != nil {
			t.Fatalf("failed to build request %d: %v", i, err)
		}
		req.Header.Set("Authorization", "Bearer "+tokenStr)

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("request %d failed: %v", i, err)
		}

		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			t.Fatalf("failed to read response body for request %d: %v", i, err)
		}
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("request %d: expected 200, got %d: %s", i, resp.StatusCode, body)
		}

		var parsed map[string]string
		if err := json.Unmarshal(body, &parsed); err != nil {
			t.Fatalf("request %d: failed to unmarshal response: %v", i, err)
		}
		seen[parsed["backend"]]++
	}

	// 3. Assert all three backends were hit
	t.Logf("distribution: %+v", seen)
	for _, backendID := range []string{"1", "2", "3"} {
		if seen[backendID] == 0 {
			t.Errorf("expected backend %s to receive at least one request, got none", backendID)
		}
	}

	// 4. With round-robin over 9 requests / 3 backends, expect an even 3/3/3 split
	for backendID, count := range seen {
		if count != numRequests/3 {
			t.Errorf("expected backend %s to receive exactly %d requests, got %d", backendID, numRequests/3, count)
		}
	}
}
