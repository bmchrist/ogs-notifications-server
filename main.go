package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strconv"
	"sync"
	"time"

	secretmanager "cloud.google.com/go/secretmanager/apiv1"
	"cloud.google.com/go/secretmanager/apiv1/secretmanagerpb"
	"github.com/gorilla/mux"
	"github.com/sideshow/apns2"
	"github.com/sideshow/apns2/payload"
	"github.com/sideshow/apns2/token"
)

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

type Game struct {
	ID   int       `json:"id"`
	Name string    `json:"name"`
	JSON GameState `json:"json"`
}

type GameState struct {
	Clock Clock `json:"clock"`
}

type Clock struct {
	CurrentPlayer int   `json:"current_player"`
	LastMove      int64 `json:"last_move"`
}

type PlayerResponse struct {
	ActiveGames []Game `json:"active_games"`
}

type TurnStatus struct {
	NotYourTurn  []int `json:"not_your_turn"`
	YourTurnNew  []int `json:"your_turn_new"`
	YourTurnOld  []int `json:"your_turn_old"`
}


type MoveStorage struct {
	mu                   sync.RWMutex
	moves                map[string]map[int]int64 // userID -> gameID -> lastMove
	deviceTokens         map[string]string        // userID -> deviceToken
	lastNotificationTime map[string]int64         // userID -> unix timestamp
}

var storage = &MoveStorage{
	moves:                make(map[string]map[int]int64),
	deviceTokens:         make(map[string]string),
	lastNotificationTime: make(map[string]int64),
}

type DeviceRegistration struct {
	UserID      string `json:"user_id"`
	DeviceToken string `json:"device_token"`
}

type GameDiagnostic struct {
	GameID              int    `json:"game_id"`
	LastMoveTimestamp   int64  `json:"last_move_timestamp"`
	CurrentPlayer       int    `json:"current_player"`
	IsYourTurn          bool   `json:"is_your_turn"`
	GameName            string `json:"game_name,omitempty"`
}

type UserDiagnostics struct {
	UserID                   string           `json:"user_id"`
	DeviceTokenRegistered    bool             `json:"device_token_registered"`
	DeviceTokenPreview       string           `json:"device_token_preview,omitempty"`
	LastNotificationTime     int64            `json:"last_notification_time"`
	MonitoredGames           []GameDiagnostic `json:"monitored_games"`
	TotalActiveGames         int              `json:"total_active_games"`
	ServerCheckInterval      string           `json:"server_check_interval"`
	LastServerCheckTime      int64            `json:"last_server_check_time"`
}

type DeviceTokenUsers struct {
	DeviceToken string   `json:"device_token"`
	UserIDs     []string `json:"user_ids"`
}


var apnsClient *apns2.Client

func main() {
	loadStorage()
	initAPNS()

	// Start periodic checking in background
	go startPeriodicChecking()

	r := mux.NewRouter()

	r.HandleFunc("/check/{userID}", checkUserTurn).Methods("GET")
	r.HandleFunc("/register", registerDevice).Methods("POST")
	r.HandleFunc("/users-by-token/{deviceToken}", getUsersByDeviceToken).Methods("GET")
	r.HandleFunc("/health", healthCheck).Methods("GET")
	r.HandleFunc("/diagnostics/{userID}", getUserDiagnostics).Methods("GET")

	log.Println("Server starting on :8080")
	log.Println("Automatic turn checking enabled")
	log.Fatal(http.ListenAndServe(":8080", r))
}

func healthCheck(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

func checkUserTurn(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	userIDStr := vars["userID"]

	userID, err := strconv.Atoi(userIDStr)
	if err != nil {
		http.Error(w, "Invalid user ID", http.StatusBadRequest)
		return
	}

	status, err := getUserTurnStatus(userID)
	if err != nil {
		log.Printf("Error getting user turn status for user %d: %v", userID, err)
		http.Error(w, "Failed to fetch turn status", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(status)
}

func getUserTurnStatus(userID int) (*TurnStatus, error) {
	log.Printf("Fetching turn status for user %d", userID)

	games, err := getActiveGames(userID)
	if err != nil {
		log.Printf("Failed to get active games for user %d: %v", userID, err)
		return nil, err
	}

	log.Printf("User %d has %d active games", userID, len(games))

	status := &TurnStatus{
		NotYourTurn: []int{},
		YourTurnNew: []int{},
		YourTurnOld: []int{},
	}

	userIDStr := strconv.Itoa(userID)

	var newTurnGames []Game

	for _, game := range games {

		if game.JSON.Clock.CurrentPlayer == userID {
			// Check if this is a new turn vs old turn
			isNew := isNewTurn(userIDStr, game.ID, game.JSON.Clock.LastMove)

			if isNew {
				status.YourTurnNew = append(status.YourTurnNew, game.ID)
				newTurnGames = append(newTurnGames, game)
				// Update stored move for new turns
				updateStoredMove(userIDStr, game.ID, game.JSON.Clock.LastMove)
			} else {
				status.YourTurnOld = append(status.YourTurnOld, game.ID)
			}
		} else {
			status.NotYourTurn = append(status.NotYourTurn, game.ID)
		}
	}

	// Send single consolidated push notification if there are new turns
	if len(newTurnGames) > 0 {
		go sendConsolidatedPushNotification(userIDStr, newTurnGames)
	}

	saveStorage()
	return status, nil
}

func getActiveGames(userID int) ([]Game, error) {
	url := fmt.Sprintf("https://online-go.com/api/v1/players/%d/full", userID)
	log.Printf("Making OGS API request: %s", url)

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		log.Printf("OGS API request failed for user %d: %v", userID, err)
		return nil, fmt.Errorf("failed to fetch games")
	}
	defer resp.Body.Close()

	log.Printf("OGS API response status: %d", resp.StatusCode)

	if resp.StatusCode != http.StatusOK {
		log.Printf("OGS API returned non-200 status: %d for user %d", resp.StatusCode, userID)
		return nil, fmt.Errorf("API request failed")
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		log.Printf("Failed to read OGS API response for user %d: %v", userID, err)
		return nil, fmt.Errorf("failed to process response")
	}

	var response PlayerResponse
	if err := json.Unmarshal(body, &response); err != nil {
		log.Printf("Failed to parse OGS API response for user %d: %v", userID, err)
		return nil, fmt.Errorf("failed to process response")
	}

	return response.ActiveGames, nil
}

func isNewTurn(userID string, gameID int, currentMove int64) bool {
	storage.mu.RLock()
	defer storage.mu.RUnlock()

	userMoves, exists := storage.moves[userID]
	if !exists {
		return true // First time seeing this user
	}

	lastMove, exists := userMoves[gameID]
	if !exists {
		return true // First time seeing this game for this user
	}

	return currentMove > lastMove // New move since last check
}

func updateStoredMove(userID string, gameID int, lastMove int64) {
	storage.mu.Lock()
	defer storage.mu.Unlock()

	if storage.moves[userID] == nil {
		storage.moves[userID] = make(map[int]int64)
	}
	storage.moves[userID][gameID] = lastMove
}

func loadStorage() {
	storage.mu.Lock()
	defer storage.mu.Unlock()

	log.Println("Loading storage from moves.json...")

	data, err := os.ReadFile("moves.json")
	if err != nil {
		log.Println("No existing moves.json file, starting fresh")
		storage.moves = make(map[string]map[int]int64)
		storage.deviceTokens = make(map[string]string)
		storage.lastNotificationTime = make(map[string]int64)
		return
	}

	// Try to load new format first (with device tokens and notification times)
	var storageData struct {
		Moves                map[string]map[int]int64 `json:"moves"`
		DeviceTokens         map[string]string        `json:"device_tokens"`
		LastNotificationTime map[string]int64         `json:"last_notification_time"`
	}

	if err := json.Unmarshal(data, &storageData); err == nil && storageData.Moves != nil {
		storage.moves = storageData.Moves
		if storageData.DeviceTokens != nil {
			storage.deviceTokens = storageData.DeviceTokens
		}
		if storageData.LastNotificationTime != nil {
			storage.lastNotificationTime = storageData.LastNotificationTime
		}
		log.Printf("Loaded storage: %d users with device tokens, %d users with move history, %d users with notification times",
			len(storage.deviceTokens), len(storage.moves), len(storage.lastNotificationTime))
		return
	}

	// Fallback to old format (just moves)
	if err := json.Unmarshal(data, &storage.moves); err != nil {
		log.Printf("Error loading moves.json: %v", err)
		storage.moves = make(map[string]map[int]int64)
		storage.deviceTokens = make(map[string]string)
		storage.lastNotificationTime = make(map[string]int64)
	}
}

func saveStorage() {
	storage.mu.RLock()
	defer storage.mu.RUnlock()

	storageData := struct {
		Moves                map[string]map[int]int64 `json:"moves"`
		DeviceTokens         map[string]string        `json:"device_tokens"`
		LastNotificationTime map[string]int64         `json:"last_notification_time"`
	}{
		Moves:                storage.moves,
		DeviceTokens:         storage.deviceTokens,
		LastNotificationTime: storage.lastNotificationTime,
	}

	data, err := json.MarshalIndent(storageData, "", "  ")
	if err != nil {
		log.Printf("Error marshaling storage: %v", err)
		return
	}

	if err := os.WriteFile("moves.json", data, 0600); err != nil {
		log.Printf("Error saving moves.json: %v", err)
	} else {
		log.Printf("Storage saved: %d users with device tokens, %d users with move history, %d notification times",
			len(storage.deviceTokens), len(storage.moves), len(storage.lastNotificationTime))
	}
}

func getSecret(secretName string) (string, error) {
	projectID := os.Getenv("GOOGLE_CLOUD_PROJECT")
	if projectID == "" {
		return "", fmt.Errorf("GOOGLE_CLOUD_PROJECT environment variable not set")
	}

	ctx := context.Background()
	client, err := secretmanager.NewClient(ctx)
	if err != nil {
		return "", fmt.Errorf("failed to create secretmanager client: %v", err)
	}
	defer client.Close()

	req := &secretmanagerpb.AccessSecretVersionRequest{
		Name: fmt.Sprintf("projects/%s/secrets/%s/versions/latest", projectID, secretName),
	}

	result, err := client.AccessSecretVersion(ctx, req)
	if err != nil {
		log.Printf("Failed to access secret %s: %v", secretName, err)
		return "", fmt.Errorf("failed to access secret")
	}

	return string(result.Payload.Data), nil
}

func getAPNSConfig() (keyData []byte, keyID, teamID, bundleID string, isDevelopment bool, err error) {
	//environment := os.Getenv("ENVIRONMENT")

	if true { //environment == "production" {
		log.Println("Loading APNs configuration from Secret Manager...")

		// Get configuration from Secret Manager
		keyDataStr, err := getSecret("apns-key")
		if err != nil {
			log.Printf("Failed to get APNs key: %v", err)
			return nil, "", "", "", false, fmt.Errorf("failed to load APNs configuration")
		}
		keyData = []byte(keyDataStr)

		keyID, err = getSecret("apns-key-id")
		if err != nil {
			log.Printf("Failed to get APNs key ID: %v", err)
			return nil, "", "", "", false, fmt.Errorf("failed to load APNs configuration")
		}

		teamID, err = getSecret("apns-team-id")
		if err != nil {
			log.Printf("Failed to get APNs team ID: %v", err)
			return nil, "", "", "", false, fmt.Errorf("failed to load APNs configuration")
		}

		bundleID, err = getSecret("apns-bundle-id")
		if err != nil {
			log.Printf("Failed to get APNs bundle ID: %v", err)
			return nil, "", "", "", false, fmt.Errorf("failed to load APNs configuration")
		}

		isDevelopment = false // Production always uses production APNs
		log.Println("APNs configuration loaded from Secret Manager")
	} else {
		log.Println("Loading APNs configuration from environment variables...")

		// Get configuration from environment variables
		keyPath := os.Getenv("APNS_KEY_PATH")
		if keyPath == "" {
			return nil, "", "", "", false, fmt.Errorf("APNS_KEY_PATH environment variable not set")
		}

		if _, err := os.Stat(keyPath); os.IsNotExist(err) {
			return nil, "", "", "", false, fmt.Errorf("APNs key file not found at %s", keyPath)
		}

		keyData, err = os.ReadFile(keyPath)
		if err != nil {
			log.Printf("Failed to read APNs key file: %v", err)
			return nil, "", "", "", false, fmt.Errorf("failed to load APNs configuration")
		}

		keyID = os.Getenv("APNS_KEY_ID")
		teamID = os.Getenv("APNS_TEAM_ID")
		bundleID = os.Getenv("APNS_BUNDLE_ID")
		isDevelopment = os.Getenv("APNS_DEVELOPMENT") == "true"

		log.Printf("APNs configuration loaded from environment variables (development=%t)", isDevelopment)
	}

	if keyID == "" || teamID == "" || bundleID == "" {
		return nil, "", "", "", false, fmt.Errorf("missing required APNs configuration (key_id, team_id, or bundle_id)")
	}

	return keyData, keyID, teamID, bundleID, isDevelopment, nil
}

func initAPNS() {
	keyData, keyID, teamID, bundleID, isDevelopment, err := getAPNSConfig()

	if err != nil {
		log.Printf("APNs configuration error: %v. Push notifications will be disabled.", err)
		return
	}

	// Store bundle ID in environment for later use
	os.Setenv("APNS_BUNDLE_ID", bundleID)

	authKey, err := token.AuthKeyFromBytes(keyData)
	if err != nil {
		log.Printf("Error loading APNs auth key: %v. Push notifications will be disabled.", err)
		return
	}

	tokenProvider := &token.Token{
		AuthKey: authKey,
		KeyID:   keyID,
		TeamID:  teamID,
	}

	if isDevelopment {
		apnsClient = apns2.NewTokenClient(tokenProvider).Development()
		log.Println("APNs client initialized for development")
	} else {
		apnsClient = apns2.NewTokenClient(tokenProvider).Development()
		log.Println("APNs client initialized for production")
	}
}

func registerDevice(w http.ResponseWriter, r *http.Request) {

	var registration DeviceRegistration
	if err := json.NewDecoder(r.Body).Decode(&registration); err != nil {
		log.Printf("Registration failed: Invalid JSON from %s - %v", r.RemoteAddr, err)
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	if registration.UserID == "" || registration.DeviceToken == "" {
		log.Printf("Registration failed: Missing required fields (user_id=%s, token_length=%d)",
			registration.UserID, len(registration.DeviceToken))
		http.Error(w, "user_id and device_token are required", http.StatusBadRequest)
		return
	}

	log.Printf("Registering device for user %s (token length: %d)",
		registration.UserID, len(registration.DeviceToken))

	storage.mu.Lock()
	storage.deviceTokens[registration.UserID] = registration.DeviceToken
	storage.mu.Unlock()

	saveStorage()
	log.Printf("Successfully registered device for user %s", registration.UserID)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "registered"})
}


func getUserDiagnostics(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	userIDStr := vars["userID"]

	log.Printf("Diagnostics request for user %s from %s", userIDStr, r.RemoteAddr)

	userID, err := strconv.Atoi(userIDStr)
	if err != nil {
		log.Printf("Invalid user ID in diagnostics request: %s", userIDStr)
		http.Error(w, "Invalid user ID", http.StatusBadRequest)
		return
	}

	// Check if user is registered
	storage.mu.RLock()
	_, hasDeviceToken := storage.deviceTokens[userIDStr]
	lastNotificationTime := storage.lastNotificationTime[userIDStr]
	storage.mu.RUnlock()

	// Get current games from OGS API
	games, err := getActiveGames(userID)
	if err != nil {
		log.Printf("Failed to get active games for user %s in diagnostics: %v", userIDStr, err)
		http.Error(w, "Failed to fetch user games", http.StatusServiceUnavailable)
		return
	}

	// Build diagnostics response
	diagnostics := UserDiagnostics{
		UserID:                userIDStr,
		DeviceTokenRegistered: hasDeviceToken,
		LastNotificationTime:  lastNotificationTime,
		TotalActiveGames:      len(games),
		ServerCheckInterval:   "30s", // Could make this dynamic
		LastServerCheckTime:   time.Now().Unix(),
		MonitoredGames:        make([]GameDiagnostic, 0),
	}

	// Add device token preview if available
	if hasDeviceToken {
		diagnostics.DeviceTokenPreview = "[REGISTERED]"
	}

	// Build game diagnostics
	for _, game := range games {
		gameDiag := GameDiagnostic{
			GameID:            game.ID,
			LastMoveTimestamp: game.JSON.Clock.LastMove,
			CurrentPlayer:     game.JSON.Clock.CurrentPlayer,
			IsYourTurn:        game.JSON.Clock.CurrentPlayer == userID,
			GameName:          game.Name,
		}
		diagnostics.MonitoredGames = append(diagnostics.MonitoredGames, gameDiag)
	}

	log.Printf("Diagnostics generated for user %s: %d games, device_registered=%t, last_notification=%d",
		userIDStr, len(games), hasDeviceToken, lastNotificationTime)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(diagnostics)
}

func getUsersByDeviceToken(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	deviceToken := vars["deviceToken"]

	log.Printf("Users by device token request (token length: %d)", len(deviceToken))

	if deviceToken == "" {
		http.Error(w, "Device token is required", http.StatusBadRequest)
		return
	}

	// Search through all device tokens to find matching user IDs
	storage.mu.RLock()
	var matchingUserIDs []string
	for userID, token := range storage.deviceTokens {
		if token == deviceToken {
			matchingUserIDs = append(matchingUserIDs, userID)
		}
	}
	storage.mu.RUnlock()

	response := DeviceTokenUsers{
		DeviceToken: deviceToken,
		UserIDs:     matchingUserIDs,
	}

	log.Printf("Found %d user(s) for device token: %v", len(matchingUserIDs), matchingUserIDs)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

func sendConsolidatedPushNotification(userID string, newTurnGames []Game) {
	log.Printf("Preparing push notification for user %s with %d new turn games", userID, len(newTurnGames))

	if apnsClient == nil {
		log.Printf("APNs client not initialized, skipping push notification for user %s", userID)
		return
	}

	storage.mu.RLock()
	deviceToken, exists := storage.deviceTokens[userID]
	storage.mu.RUnlock()

	if !exists {
		log.Printf("No device token found for user %s", userID)
		return
	}

	if len(newTurnGames) == 0 {
		log.Printf("No new turn games for user %s, skipping notification", userID)
		return
	}

	log.Printf("Found device token for user %s", userID)

	// Get environment name (defaults to "none" if not set)
	environment := os.Getenv("ENVIRONMENT")
	if environment == "" {
		environment = "none"
	}

	// Create notification title and body based on number of games
	var title, body string
	if len(newTurnGames) == 1 {
		title = "Your turn in Go!"
		if environment != "none" {
			body = fmt.Sprintf("[%s] It's your turn in: %s", environment, newTurnGames[0].Name)
		} else {
			body = fmt.Sprintf("It's your turn in: %s", newTurnGames[0].Name)
		}
	} else {
		title = "Your turn in Go!"
		if environment != "none" {
			body = fmt.Sprintf("[%s] It's your turn in %d games", environment, len(newTurnGames))
		} else {
			body = fmt.Sprintf("It's your turn in %d games", len(newTurnGames))
		}
	}

	// Use the first game for the deep link
	firstGame := newTurnGames[0]
	webURL := fmt.Sprintf("https://online-go.com/game/%d", firstGame.ID)
	appURL := fmt.Sprintf("ogs://game/%d", firstGame.ID)  // Custom URL scheme for the app

	// Create notification payload with both web and app URLs
	notification := &apns2.Notification{}
	notification.DeviceToken = deviceToken
	notification.Topic = "online-go-server-push-notification"

	// Add URLs and action data for iOS app to handle
	payload := payload.NewPayload().Alert(title).
		AlertBody(body).
		Badge(len(newTurnGames)).
		Sound("default").
		Custom("web_url", webURL).        // For opening in Safari as fallback
		Custom("app_url", appURL).        // For opening in app
		Custom("game_id", firstGame.ID).
		Custom("action", "open_game").
		Custom("game_name", firstGame.Name)

	notification.Payload = payload
	notification.CollapseID = "game_turn"  // Group similar notifications

	// Send the notification
	res, err := apnsClient.Push(notification)
	if err != nil {
		log.Printf("Error sending push notification to user %s: %v", userID, err)
		return
	}

	if res.Sent() {
		log.Printf("Push notification sent successfully to user %s for %d game(s). Web URL: %s, App URL: %s", userID, len(newTurnGames), webURL, appURL)

		// Update last notification time
		storage.mu.Lock()
		storage.lastNotificationTime[userID] = time.Now().Unix()
		storage.mu.Unlock()

		saveStorage()
	} else {
		log.Printf("Push notification failed for user %s: %v", userID, res.Reason)
	}
}


func startPeriodicChecking() {
	// Get check interval from environment, default to 30 seconds
	checkInterval := 30 * time.Second

	// Support both seconds and minutes for flexibility
	if intervalStr := os.Getenv("CHECK_INTERVAL_SECONDS"); intervalStr != "" {
		if interval, err := strconv.Atoi(intervalStr); err == nil {
			checkInterval = time.Duration(interval) * time.Second
		}
	}

	log.Printf("Starting periodic turn checking every %v", checkInterval)

	ticker := time.NewTicker(checkInterval)
	defer ticker.Stop()

	// Run initial check after 5 seconds
	time.Sleep(5 * time.Second)
	checkAllUsers()

	// Then run on schedule
	for range ticker.C {
		checkAllUsers()
	}
}

func checkAllUsers() {
	storage.mu.RLock()
	deviceTokens := make(map[string]string)
	for userID, token := range storage.deviceTokens {
		deviceTokens[userID] = token
	}
	storage.mu.RUnlock()

	if len(deviceTokens) == 0 {
		log.Println("No registered users to check")
		return
	}

	log.Printf("Checking turns for %d registered users", len(deviceTokens))

	for userIDStr := range deviceTokens {

		userID, err := strconv.Atoi(userIDStr)
		if err != nil {
			log.Printf("Invalid user ID: %s", userIDStr)
			continue
		}

		// Use the existing getUserTurnStatus function which handles notifications
		status, err := getUserTurnStatus(userID)
		if err != nil {
			log.Printf("Error checking user %s: %v", userIDStr, err)
			continue
		}

		log.Printf("User %s status: %d not_your_turn, %d your_turn_new, %d your_turn_old",
			userIDStr, len(status.NotYourTurn), len(status.YourTurnNew), len(status.YourTurnOld))

		if len(status.YourTurnNew) > 0 {
			log.Printf("User %s has %d new turns - notification should be sent", userIDStr, len(status.YourTurnNew))
		}
	}

	log.Println("Turn checking cycle complete")
}

