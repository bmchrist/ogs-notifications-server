package main

import (
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"
)

// STORAGE AND PERSISTENCE TESTS

// Test: Storage persistence
func TestStoragePersistence(t *testing.T) {
	setupTestStorage()
	defer cleanupTestStorage()

	// Add test data
	storage.mu.Lock()
	storage.deviceTokens["user1"] = testDeviceToken
	storage.moves["user1"] = map[int]int64{123: 1000}
	storage.lastNotificationTime["user1"] = 2000
	storage.mu.Unlock()

	// Save storage
	saveStorage()

	// Clear in-memory storage
	setupTestStorage()

	// Load storage
	loadStorage()

	// Verify data was persisted
	storage.mu.RLock()
	defer storage.mu.RUnlock()

	if token, exists := storage.deviceTokens["user1"]; !exists || token != testDeviceToken {
		t.Errorf("Device token not persisted correctly")
	}

	if moves, exists := storage.moves["user1"]; !exists || moves[123] != 1000 {
		t.Errorf("Moves not persisted correctly")
	}

	if time, exists := storage.lastNotificationTime["user1"]; !exists || time != 2000 {
		t.Errorf("Last notification time not persisted correctly")
	}
}

// Test: File permissions
func TestFilePermissions(t *testing.T) {
	setupTestStorage()
	defer cleanupTestStorage()

	// Save storage to create file
	saveStorage()

	// Check file permissions
	info, err := os.Stat("moves.json")
	if err != nil {
		t.Fatalf("Failed to stat moves.json: %v", err)
	}

	mode := info.Mode()
	expectedMode := os.FileMode(0600)

	if mode.Perm() != expectedMode {
		t.Errorf("Expected file permissions %o, got %o", expectedMode, mode.Perm())
	}
}

// Test: Concurrent access protection
func TestConcurrentAccess(t *testing.T) {
	setupTestStorage()
	defer cleanupTestStorage()

	const numGoroutines = 100
	const numOperations = 50

	var wg sync.WaitGroup
	wg.Add(numGoroutines)

	// Race condition test: multiple goroutines updating storage
	for i := 0; i < numGoroutines; i++ {
		go func(id int) {
			defer wg.Done()
			userID := fmt.Sprintf("user%d", id)

			for j := 0; j < numOperations; j++ {
				// Write operation
				storage.mu.Lock()
				storage.deviceTokens[userID] = fmt.Sprintf("token%d", j)
				if storage.moves[userID] == nil {
					storage.moves[userID] = make(map[int]int64)
				}
				storage.moves[userID][j] = int64(j)
				storage.mu.Unlock()

				// Read operation
				storage.mu.RLock()
				_ = storage.deviceTokens[userID]
				_ = storage.moves[userID]
				storage.mu.RUnlock()
			}
		}(i)
	}

	wg.Wait()

	// Verify no data corruption
	storage.mu.RLock()
	defer storage.mu.RUnlock()

	if len(storage.deviceTokens) != numGoroutines {
		t.Errorf("Expected %d users, got %d", numGoroutines, len(storage.deviceTokens))
	}
}

// Test: Storage migration from old to new format
func TestStorageMigration(t *testing.T) {
	defer cleanupTestStorage()

	// Create old format storage (just moves)
	oldFormat := map[string]map[int]int64{
		"user1": {123: 1000, 456: 2000},
		"user2": {789: 3000},
	}

	oldData, _ := json.MarshalIndent(oldFormat, "", "  ")
	err := os.WriteFile("moves.json", oldData, 0600)
	if err != nil {
		t.Fatalf("Failed to create old format file: %v", err)
	}

	// Initialize storage (should handle migration)
	setupTestStorage()
	loadStorage()

	// Verify old data was loaded
	storage.mu.RLock()
	defer storage.mu.RUnlock()

	if len(storage.moves) != 2 {
		t.Errorf("Expected 2 users in moves, got %d", len(storage.moves))
	}

	if storage.moves["user1"][123] != 1000 {
		t.Error("Old format data not correctly migrated")
	}

	// Verify new fields are initialized
	if storage.deviceTokens == nil {
		t.Error("Device tokens map not initialized after migration")
	}

	if storage.lastNotificationTime == nil {
		t.Error("Last notification time map not initialized after migration")
	}
}

// Test: Corrupted storage file handling
func TestCorruptedStorageHandling(t *testing.T) {
	defer cleanupTestStorage()

	// Create corrupted JSON file
	err := os.WriteFile("moves.json", []byte("not valid json{]"), 0600)
	if err != nil {
		t.Fatalf("Failed to create corrupted file: %v", err)
	}

	// Should handle corrupted file gracefully
	setupTestStorage()
	loadStorage()

	// Verify storage is initialized to empty state
	storage.mu.RLock()
	defer storage.mu.RUnlock()

	if storage.moves == nil || storage.deviceTokens == nil || storage.lastNotificationTime == nil {
		t.Error("Storage not properly initialized after corrupted file")
	}
}

// Test: Large storage handling performance
func TestLargeStoragePerformance(t *testing.T) {
	setupTestStorage()
	defer cleanupTestStorage()

	const numUsers = 1000
	const numGamesPerUser = 10

	// Create large dataset
	storage.mu.Lock()
	for i := 0; i < numUsers; i++ {
		userID := fmt.Sprintf("user%d", i)
		storage.deviceTokens[userID] = fmt.Sprintf("%064d", i)
		storage.moves[userID] = make(map[int]int64)

		for j := 0; j < numGamesPerUser; j++ {
			storage.moves[userID][j] = int64(i * 1000 + j)
		}

		storage.lastNotificationTime[userID] = int64(i * 10000)
	}
	storage.mu.Unlock()

	// Test save performance
	start := time.Now()
	saveStorage()
	saveDuration := time.Since(start)

	if saveDuration > 5*time.Second {
		t.Errorf("Save took too long for large dataset: %v", saveDuration)
	}

	// Test load performance
	setupTestStorage()
	start = time.Now()
	loadStorage()
	loadDuration := time.Since(start)

	if loadDuration > 5*time.Second {
		t.Errorf("Load took too long for large dataset: %v", loadDuration)
	}

	// Verify data integrity
	storage.mu.RLock()
	defer storage.mu.RUnlock()

	if len(storage.deviceTokens) != numUsers {
		t.Errorf("Expected %d users, got %d", numUsers, len(storage.deviceTokens))
	}

	// Spot check some data
	if storage.moves["user500"][5] != 500005 {
		t.Error("Data corruption in large dataset")
	}
}