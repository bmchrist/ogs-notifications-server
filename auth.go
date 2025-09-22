package main

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"sync"
	"time"
)

type APIKey struct {
	Key         string    `json:"key"`
	UserID      string    `json:"user_id"`
	CreatedAt   time.Time `json:"created_at"`
	LastUsed    time.Time `json:"last_used"`
	Description string    `json:"description"`
}

type APIKeyStorage struct {
	mu      sync.RWMutex
	keys    map[string]*APIKey // key -> APIKey
	userKeys map[string]string  // userID -> key (one key per user for simplicity)
}

var apiKeyStorage = &APIKeyStorage{
	keys:     make(map[string]*APIKey),
	userKeys: make(map[string]string),
}

// generateAPIKey creates a cryptographically secure random API key
func generateAPIKey() (string, error) {
	bytes := make([]byte, 32) // 256 bits of entropy
	if _, err := rand.Read(bytes); err != nil {
		return "", fmt.Errorf("failed to generate random key: %v", err)
	}
	return hex.EncodeToString(bytes), nil
}

// createAPIKey generates a new API key for a user
func createAPIKey(userID string, description string) (*APIKey, error) {
	key, err := generateAPIKey()
	if err != nil {
		return nil, err
	}

	apiKey := &APIKey{
		Key:         key,
		UserID:      userID,
		CreatedAt:   time.Now(),
		LastUsed:    time.Now(),
		Description: description,
	}

	apiKeyStorage.mu.Lock()
	defer apiKeyStorage.mu.Unlock()

	// Revoke existing key if one exists
	if existingKey, exists := apiKeyStorage.userKeys[userID]; exists {
		delete(apiKeyStorage.keys, existingKey)
	}

	apiKeyStorage.keys[key] = apiKey
	apiKeyStorage.userKeys[userID] = key

	saveAPIKeys()

	log.Printf("Created new API key for user %s: %s", userID, description)
	return apiKey, nil
}

// validateAPIKey checks if an API key is valid and updates last used time
func validateAPIKey(key string) (*APIKey, bool) {
	apiKeyStorage.mu.Lock()
	defer apiKeyStorage.mu.Unlock()

	apiKey, exists := apiKeyStorage.keys[key]
	if !exists {
		return nil, false
	}

	// Update last used time
	apiKey.LastUsed = time.Now()
	saveAPIKeys()

	return apiKey, true
}

// requireAPIKey is middleware that validates API key for protected endpoints
func requireAPIKey(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		apiKey := r.Header.Get("X-API-Key")
		if apiKey == "" {
			log.Printf("Request to %s from %s without API key", r.URL.Path, r.RemoteAddr)
			http.Error(w, "API key required", http.StatusUnauthorized)
			return
		}

		key, valid := validateAPIKey(apiKey)
		if !valid {
			log.Printf("Invalid API key attempt for %s from %s", r.URL.Path, r.RemoteAddr)
			http.Error(w, "Invalid API key", http.StatusUnauthorized)
			return
		}

		log.Printf("Authenticated request to %s from user %s", r.URL.Path, key.UserID)

		// Add user ID to request context for use in handlers
		r.Header.Set("X-User-ID", key.UserID)

		next(w, r)
	}
}

// generateAPIKeyHandler creates a new API key for a user
func generateAPIKeyHandler(w http.ResponseWriter, r *http.Request) {
	var request struct {
		UserID      string `json:"user_id"`
		MasterKey   string `json:"master_key"`
		Description string `json:"description"`
	}

	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		http.Error(w, "Invalid request", http.StatusBadRequest)
		return
	}

	// Validate master key for API key generation
	masterKey := os.Getenv("MASTER_API_KEY")
	if masterKey == "" {
		// Generate and log a master key if not set
		newMasterKey, _ := generateAPIKey()
		log.Printf("WARNING: No MASTER_API_KEY set. Generated temporary key: %s", newMasterKey)
		log.Printf("Set this as MASTER_API_KEY environment variable to persist it")
		masterKey = newMasterKey
		os.Setenv("MASTER_API_KEY", masterKey)
	}

	// Use constant-time comparison to prevent timing attacks
	if subtle.ConstantTimeCompare([]byte(request.MasterKey), []byte(masterKey)) != 1 {
		log.Printf("Invalid master key attempt from %s", r.RemoteAddr)
		http.Error(w, "Invalid master key", http.StatusUnauthorized)
		return
	}

	if request.UserID == "" {
		http.Error(w, "user_id is required", http.StatusBadRequest)
		return
	}

	if request.Description == "" {
		request.Description = "iOS App API Key"
	}

	apiKey, err := createAPIKey(request.UserID, request.Description)
	if err != nil {
		log.Printf("Failed to create API key: %v", err)
		http.Error(w, "Failed to create API key", http.StatusInternalServerError)
		return
	}

	response := map[string]interface{}{
		"api_key":     apiKey.Key,
		"user_id":     apiKey.UserID,
		"created_at":  apiKey.CreatedAt,
		"description": apiKey.Description,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

// loadAPIKeys loads API keys from storage
func loadAPIKeys() {
	apiKeyStorage.mu.Lock()
	defer apiKeyStorage.mu.Unlock()

	data, err := os.ReadFile("api_keys.json")
	if err != nil {
		log.Println("No existing api_keys.json file, starting fresh")
		return
	}

	var keys []*APIKey
	if err := json.Unmarshal(data, &keys); err != nil {
		log.Printf("Error loading api_keys.json: %v", err)
		return
	}

	for _, key := range keys {
		apiKeyStorage.keys[key.Key] = key
		apiKeyStorage.userKeys[key.UserID] = key.Key
	}

	log.Printf("Loaded %d API keys", len(keys))
}

// saveAPIKeys saves API keys to storage
func saveAPIKeys() {
	// Convert map to slice for JSON storage
	var keys []*APIKey
	for _, key := range apiKeyStorage.keys {
		keys = append(keys, key)
	}

	data, err := json.MarshalIndent(keys, "", "  ")
	if err != nil {
		log.Printf("Error marshaling API keys: %v", err)
		return
	}

	if err := os.WriteFile("api_keys.json", data, 0600); err != nil {
		log.Printf("Error saving api_keys.json: %v", err)
	}
}

// getUserAPIKey returns the API key for a user (for diagnostics)
func getUserAPIKey(userID string) (string, bool) {
	apiKeyStorage.mu.RLock()
	defer apiKeyStorage.mu.RUnlock()

	key, exists := apiKeyStorage.userKeys[userID]
	return key, exists
}