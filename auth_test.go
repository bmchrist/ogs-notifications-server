package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
)

func TestGenerateAPIKey(t *testing.T) {
	// Test API key generation
	key, err := generateAPIKey()
	if err != nil {
		t.Fatalf("Failed to generate API key: %v", err)
	}

	// Check key length (32 bytes = 64 hex characters)
	if len(key) != 64 {
		t.Errorf("Expected API key length 64, got %d", len(key))
	}

	// Check that keys are unique
	key2, err := generateAPIKey()
	if err != nil {
		t.Fatalf("Failed to generate second API key: %v", err)
	}

	if key == key2 {
		t.Error("Generated API keys should be unique")
	}
}

func TestCreateAndValidateAPIKey(t *testing.T) {
	// Clear storage for test
	apiKeyStorage = &APIKeyStorage{
		keys:     make(map[string]*APIKey),
		userKeys: make(map[string]string),
	}

	// Create API key
	userID := "testuser123"
	description := "Test API Key"
	apiKey, err := createAPIKey(userID, description)
	if err != nil {
		t.Fatalf("Failed to create API key: %v", err)
	}

	// Validate the created key
	validatedKey, valid := validateAPIKey(apiKey.Key)
	if !valid {
		t.Error("API key should be valid")
	}

	if validatedKey.UserID != userID {
		t.Errorf("Expected user ID %s, got %s", userID, validatedKey.UserID)
	}

	// Test invalid key
	_, valid = validateAPIKey("invalid-key")
	if valid {
		t.Error("Invalid API key should not validate")
	}

	// Clean up
	os.Remove("api_keys.json")
}

func TestRequireAPIKeyMiddleware(t *testing.T) {
	// Clear storage for test
	apiKeyStorage = &APIKeyStorage{
		keys:     make(map[string]*APIKey),
		userKeys: make(map[string]string),
	}

	// Create test API key
	userID := "testuser456"
	apiKey, _ := createAPIKey(userID, "Test Key")

	// Create a test handler
	testHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("Success"))
	})

	// Wrap with middleware
	protectedHandler := requireAPIKey(testHandler)

	// Test without API key
	req := httptest.NewRequest("GET", "/test", nil)
	w := httptest.NewRecorder()
	protectedHandler(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("Expected status 401, got %d", w.Code)
	}

	// Test with invalid API key
	req = httptest.NewRequest("GET", "/test", nil)
	req.Header.Set("X-API-Key", "invalid-key")
	w = httptest.NewRecorder()
	protectedHandler(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("Expected status 401 for invalid key, got %d", w.Code)
	}

	// Test with valid API key
	req = httptest.NewRequest("GET", "/test", nil)
	req.Header.Set("X-API-Key", apiKey.Key)
	w = httptest.NewRecorder()
	protectedHandler(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status 200 for valid key, got %d", w.Code)
	}

	// Clean up
	os.Remove("api_keys.json")
}

func TestGenerateAPIKeyHandler(t *testing.T) {
	// Set master key for testing
	testMasterKey := "test-master-key-123"
	os.Setenv("MASTER_API_KEY", testMasterKey)
	defer os.Unsetenv("MASTER_API_KEY")

	// Clear storage for test
	apiKeyStorage = &APIKeyStorage{
		keys:     make(map[string]*APIKey),
		userKeys: make(map[string]string),
	}

	// Test with invalid master key
	payload := map[string]string{
		"user_id":     "789",
		"master_key":  "wrong-key",
		"description": "Test",
	}
	jsonPayload, _ := json.Marshal(payload)
	req := httptest.NewRequest("POST", "/generate-api-key", bytes.NewBuffer(jsonPayload))
	w := httptest.NewRecorder()
	generateAPIKeyHandler(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("Expected status 401 for invalid master key, got %d", w.Code)
	}

	// Test with valid master key
	payload["master_key"] = testMasterKey
	jsonPayload, _ = json.Marshal(payload)
	req = httptest.NewRequest("POST", "/generate-api-key", bytes.NewBuffer(jsonPayload))
	w = httptest.NewRecorder()
	generateAPIKeyHandler(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status 200 for valid request, got %d", w.Code)
	}

	// Check response
	var response map[string]interface{}
	json.NewDecoder(w.Body).Decode(&response)

	if response["user_id"] != "789" {
		t.Errorf("Expected user_id 789, got %v", response["user_id"])
	}

	if response["api_key"] == nil || response["api_key"] == "" {
		t.Error("Response should contain api_key")
	}

	// Clean up
	os.Remove("api_keys.json")
}

func TestLoadAndSaveAPIKeys(t *testing.T) {
	// Clear storage for test
	apiKeyStorage = &APIKeyStorage{
		keys:     make(map[string]*APIKey),
		userKeys: make(map[string]string),
	}

	// Create some test keys
	key1, _ := createAPIKey("user1", "Key 1")
	key2, _ := createAPIKey("user2", "Key 2")

	// Save keys
	saveAPIKeys()

	// Clear storage
	apiKeyStorage = &APIKeyStorage{
		keys:     make(map[string]*APIKey),
		userKeys: make(map[string]string),
	}

	// Load keys
	loadAPIKeys()

	// Validate loaded keys
	_, valid1 := validateAPIKey(key1.Key)
	_, valid2 := validateAPIKey(key2.Key)

	if !valid1 || !valid2 {
		t.Error("Keys should be valid after loading")
	}

	// Clean up
	os.Remove("api_keys.json")
}