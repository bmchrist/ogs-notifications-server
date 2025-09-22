package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/gorilla/mux"
)

// HIGH PRIORITY FUNCTIONALITY TESTS

// Test: Registration endpoint
func TestRegistrationEndpoint(t *testing.T) {
	setupTestStorage()
	defer cleanupTestStorage()

	tests := []struct {
		name         string
		payload      DeviceRegistration
		expectedCode int
		description  string
	}{
		{
			name: "Valid registration",
			payload: DeviceRegistration{
				UserID:      "12345",
				DeviceToken: testDeviceToken,
			},
			expectedCode: http.StatusOK,
			description:  "Should successfully register valid device",
		},
		{
			name: "Missing user ID",
			payload: DeviceRegistration{
				DeviceToken: testDeviceToken,
			},
			expectedCode: http.StatusBadRequest,
			description:  "Should reject registration without user ID",
		},
		{
			name: "Missing device token",
			payload: DeviceRegistration{
				UserID: "12345",
			},
			expectedCode: http.StatusBadRequest,
			description:  "Should reject registration without device token",
		},
		{
			name: "Update existing registration",
			payload: DeviceRegistration{
				UserID:      "12345",
				DeviceToken: testDeviceToken + "updated",
			},
			expectedCode: http.StatusOK,
			description:  "Should update existing registration",
		},
	}

	r := mux.NewRouter()
	r.HandleFunc("/register", registerDevice).Methods("POST")

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body, _ := json.Marshal(tt.payload)
			req := httptest.NewRequest("POST", "/register", bytes.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)

			if w.Code != tt.expectedCode {
				t.Errorf("%s: Expected status %d, got %d", tt.description, tt.expectedCode, w.Code)
			}

			// Verify registration in storage
			if w.Code == http.StatusOK && tt.payload.UserID != "" {
				storage.mu.RLock()
				token, exists := storage.deviceTokens[tt.payload.UserID]
				storage.mu.RUnlock()

				if !exists {
					t.Errorf("Device token not stored after successful registration")
				}
				if token != tt.payload.DeviceToken {
					t.Errorf("Stored token doesn't match: expected %s, got %s", tt.payload.DeviceToken, token)
				}
			}
		})
	}
}

// Test: Turn detection logic
func TestTurnDetection(t *testing.T) {
	setupTestStorage()
	defer cleanupTestStorage()

	userID := "12345"

	tests := []struct {
		name           string
		storedMove     int64
		currentMove    int64
		expectedNewTurn bool
		description    string
	}{
		{
			name:           "New turn - move timestamp increased",
			storedMove:     1000,
			currentMove:    2000,
			expectedNewTurn: true,
			description:    "Should detect new turn when last_move > stored",
		},
		{
			name:           "Old turn - same timestamp",
			storedMove:     1000,
			currentMove:    1000,
			expectedNewTurn: false,
			description:    "Should not detect new turn when timestamps match",
		},
		{
			name:           "Old turn - older timestamp",
			storedMove:     2000,
			currentMove:    1000,
			expectedNewTurn: false,
			description:    "Should not detect new turn when last_move < stored",
		},
		{
			name:           "First time seeing game",
			storedMove:     0, // Will not be stored
			currentMove:    1000,
			expectedNewTurn: true,
			description:    "Should detect new turn for first-time game",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Reset storage for each test
			setupTestStorage()

			// Set up stored move if needed
			if tt.storedMove > 0 {
				storage.mu.Lock()
				storage.moves[userID] = map[int]int64{123: tt.storedMove}
				storage.mu.Unlock()
			}

			// Check if new turn
			isNew := isNewTurn(userID, 123, tt.currentMove)

			if isNew != tt.expectedNewTurn {
				t.Errorf("%s: Expected new turn = %v, got %v", tt.description, tt.expectedNewTurn, isNew)
			}
		})
	}
}

// Test: Notification deduplication
func TestNotificationDeduplication(t *testing.T) {
	setupTestStorage()
	defer cleanupTestStorage()

	userID := "12345"
	gameID := 123

	// Register device
	storage.mu.Lock()
	storage.deviceTokens[userID] = testDeviceToken
	storage.mu.Unlock()

	// First notification - should be new
	isNew := isNewTurn(userID, gameID, 1000)
	if !isNew {
		t.Error("First game check should be detected as new turn")
	}

	// Update stored move
	updateStoredMove(userID, gameID, 1000)

	// Same move timestamp - should not be new
	isNew = isNewTurn(userID, gameID, 1000)
	if isNew {
		t.Error("Same move timestamp should not trigger new notification")
	}

	// Updated move timestamp - should be new
	isNew = isNewTurn(userID, gameID, 2000)
	if !isNew {
		t.Error("Updated move timestamp should trigger new notification")
	}
}

// Test: Concurrent registrations
func TestConcurrentRegistrations(t *testing.T) {
	setupTestStorage()
	defer cleanupTestStorage()

	r := mux.NewRouter()
	r.HandleFunc("/register", registerDevice).Methods("POST")

	const numRequests = 50
	var wg sync.WaitGroup
	wg.Add(numRequests)

	errors := make([]error, 0)
	var errorsMu sync.Mutex

	for i := 0; i < numRequests; i++ {
		go func(id int) {
			defer wg.Done()

			payload := DeviceRegistration{
				UserID:      fmt.Sprintf("user%d", id),
				DeviceToken: fmt.Sprintf("%064d", id), // 64 char string
			}

			body, _ := json.Marshal(payload)
			req := httptest.NewRequest("POST", "/register", bytes.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()

			r.ServeHTTP(w, req)

			if w.Code != http.StatusOK {
				errorsMu.Lock()
				errors = append(errors, fmt.Errorf("Registration %d failed with status %d", id, w.Code))
				errorsMu.Unlock()
			}
		}(i)
	}

	wg.Wait()

	if len(errors) > 0 {
		t.Errorf("Concurrent registrations failed: %v", errors[0])
	}

	// Verify all registrations were stored
	storage.mu.RLock()
	defer storage.mu.RUnlock()

	if len(storage.deviceTokens) != numRequests {
		t.Errorf("Expected %d registered users, got %d", numRequests, len(storage.deviceTokens))
	}
}

// Test: Diagnostics endpoint
func TestDiagnosticsEndpoint(t *testing.T) {
	setupTestStorage()
	defer cleanupTestStorage()

	userID := "12345"

	// Set up test data
	storage.mu.Lock()
	storage.deviceTokens[userID] = testDeviceToken
	storage.lastNotificationTime[userID] = 1000
	storage.mu.Unlock()

	r := mux.NewRouter()
	r.HandleFunc("/diagnostics/{userID}", getUserDiagnostics).Methods("GET")

	// Test invalid user ID
	req := httptest.NewRequest("GET", "/diagnostics/invalid", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("Expected status 400 for invalid user ID, got %d", w.Code)
	}

	// Verify no sensitive data in response
	body := w.Body.String()
	if strings.Contains(body, testDeviceToken) {
		t.Error("Diagnostics response contains full device token - security issue!")
	}
}