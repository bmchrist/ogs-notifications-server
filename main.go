package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strconv"
	"sync"
	"time"

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
	ID    int       `json:"id"`
	Name  string    `json:"name"`
	Black Player    `json:"black"`
	White Player    `json:"white"`
	JSON  GameState `json:"json"`
}

type Player struct {
	ID       int    `json:"id"`
	Username string `json:"username"`
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

type GameMove struct {
	GameID   int   `json:"game_id"`
	LastMove int64 `json:"last_move"`
}

type MoveStorage struct {
	mu           sync.RWMutex
	moves        map[string]map[int]int64 // userID -> gameID -> lastMove
	deviceTokens map[string]string        // userID -> deviceToken
}

var storage = &MoveStorage{
	moves:        make(map[string]map[int]int64),
	deviceTokens: make(map[string]string),
}

type DeviceRegistration struct {
	UserID      string `json:"user_id"`
	DeviceToken string `json:"device_token"`
}

type TestNotificationRequest struct {
	DeviceToken string `json:"device_token"`
	Title       string `json:"title,omitempty"`
	Body        string `json:"body,omitempty"`
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
	r.HandleFunc("/test-notification", testNotification).Methods("POST")
	r.HandleFunc("/health", healthCheck).Methods("GET")

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
		http.Error(w, err.Error(), http.StatusInternalServerError)
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
		log.Printf("Analyzing game %d: current_player=%d, last_move=%d",
			game.ID, game.JSON.Clock.CurrentPlayer, game.JSON.Clock.LastMove)

		if game.JSON.Clock.CurrentPlayer == userID {
			// Check if this is a new turn vs old turn
			isNew := isNewTurn(userIDStr, game.ID, game.JSON.Clock.LastMove)
			log.Printf("Game %d: Your turn (new=%t)", game.ID, isNew)

			if isNew {
				status.YourTurnNew = append(status.YourTurnNew, game.ID)
				newTurnGames = append(newTurnGames, game)
				// Update stored move for new turns
				updateStoredMove(userIDStr, game.ID, game.JSON.Clock.LastMove)
				log.Printf("Game %d added to new turns list", game.ID)
			} else {
				status.YourTurnOld = append(status.YourTurnOld, game.ID)
				log.Printf("Game %d is an old turn", game.ID)
			}
		} else {
			status.NotYourTurn = append(status.NotYourTurn, game.ID)
			log.Printf("Game %d: Not your turn (current_player=%d)", game.ID, game.JSON.Clock.CurrentPlayer)
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
		log.Printf("OGS API request failed: %v", err)
		return nil, fmt.Errorf("failed to fetch games: %v", err)
	}
	defer resp.Body.Close()

	log.Printf("OGS API response status: %d", resp.StatusCode)

	if resp.StatusCode != http.StatusOK {
		log.Printf("OGS API returned non-200 status: %d", resp.StatusCode)
		return nil, fmt.Errorf("API returned status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %v", err)
	}

	var response PlayerResponse
	if err := json.Unmarshal(body, &response); err != nil {
		return nil, fmt.Errorf("failed to parse response: %v", err)
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
		return
	}

	// Try to load new format first (with device tokens)
	var storageData struct {
		Moves        map[string]map[int]int64 `json:"moves"`
		DeviceTokens map[string]string        `json:"device_tokens"`
	}

	if err := json.Unmarshal(data, &storageData); err == nil && storageData.Moves != nil {
		storage.moves = storageData.Moves
		if storageData.DeviceTokens != nil {
			storage.deviceTokens = storageData.DeviceTokens
			log.Printf("Loaded storage: %d users with device tokens, %d users with move history",
				len(storage.deviceTokens), len(storage.moves))
		}
		return
	}

	// Fallback to old format (just moves)
	if err := json.Unmarshal(data, &storage.moves); err != nil {
		log.Printf("Error loading moves.json: %v", err)
		storage.moves = make(map[string]map[int]int64)
		storage.deviceTokens = make(map[string]string)
	}
}

func saveStorage() {
	storage.mu.RLock()
	defer storage.mu.RUnlock()

	storageData := struct {
		Moves        map[string]map[int]int64 `json:"moves"`
		DeviceTokens map[string]string        `json:"device_tokens"`
	}{
		Moves:        storage.moves,
		DeviceTokens: storage.deviceTokens,
	}

	data, err := json.MarshalIndent(storageData, "", "  ")
	if err != nil {
		log.Printf("Error marshaling storage: %v", err)
		return
	}

	if err := os.WriteFile("moves.json", data, 0644); err != nil {
		log.Printf("Error saving moves.json: %v", err)
	} else {
		log.Printf("Storage saved: %d users with device tokens, %d users with move history",
			len(storage.deviceTokens), len(storage.moves))
	}
}

func initAPNS() {
	keyPath := os.Getenv("APNS_KEY_PATH")
	keyID := os.Getenv("APNS_KEY_ID")
	teamID := os.Getenv("APNS_TEAM_ID")

	if keyPath == "" || keyID == "" || teamID == "" {
		log.Printf("APNs configuration incomplete. Required: APNS_KEY_PATH, APNS_KEY_ID, APNS_TEAM_ID. Push notifications will be disabled.")
		return
	}

	if _, err := os.Stat(keyPath); os.IsNotExist(err) {
		log.Printf("APNs key file not found at %s. Push notifications will be disabled.", keyPath)
		return
	}

	authKey, err := token.AuthKeyFromFile(keyPath)
	if err != nil {
		log.Printf("Error loading APNs auth key: %v. Push notifications will be disabled.", err)
		return
	}

	tokenProvider := &token.Token{
		AuthKey: authKey,
		KeyID:   keyID,
		TeamID:  teamID,
	}

	// Use sandbox for development, production for release
	isDevelopment := os.Getenv("APNS_DEVELOPMENT") == "true"
	if isDevelopment {
		apnsClient = apns2.NewTokenClient(tokenProvider).Development()
		log.Println("APNs client initialized for development")
	} else {
		apnsClient = apns2.NewTokenClient(tokenProvider).Production()
		log.Println("APNs client initialized for production")
	}
}

func registerDevice(w http.ResponseWriter, r *http.Request) {
	log.Printf("Registration request received from %s", r.RemoteAddr)

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

	log.Printf("Registering device for user %s (token: %s...)",
		registration.UserID, registration.DeviceToken[:min(16, len(registration.DeviceToken))])

	storage.mu.Lock()
	storage.deviceTokens[registration.UserID] = registration.DeviceToken
	storage.mu.Unlock()

	saveStorage()
	log.Printf("Successfully registered device for user %s", registration.UserID)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "registered"})
}

func testNotification(w http.ResponseWriter, r *http.Request) {
	var req TestNotificationRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	if req.DeviceToken == "" {
		http.Error(w, "device_token is required", http.StatusBadRequest)
		return
	}

	if apnsClient == nil {
		http.Error(w, "APNs client not initialized. Check certificate configuration.", http.StatusServiceUnavailable)
		return
	}

	// Set default values if not provided
	title := req.Title
	if title == "" {
		title = "Test Notification"
	}

	body := req.Body
	if body == "" {
		body = "This is a test push notification from your Go server!"
	}

	// Create notification payload
	notification := &apns2.Notification{}
	notification.DeviceToken = req.DeviceToken
	notification.Topic = os.Getenv("APNS_BUNDLE_ID")

	payload := payload.NewPayload().Alert(title).
		AlertBody(body).
		Badge(1).
		Sound("default")

	notification.Payload = payload

	// Send the notification
	res, err := apnsClient.Push(notification)
	if err != nil {
		log.Printf("Error sending test notification: %v", err)
		http.Error(w, fmt.Sprintf("Failed to send notification: %v", err), http.StatusInternalServerError)
		return
	}

	response := map[string]interface{}{
		"success":     res.Sent(),
		"status_code": res.StatusCode,
		"apns_id":     res.ApnsID,
	}

	if res.Sent() {
		log.Printf("Test notification sent successfully. APNs ID: %s", res.ApnsID)
		response["message"] = "Notification sent successfully"
	} else {
		log.Printf("Test notification failed: %v", res.Reason)
		response["reason"] = res.Reason
		response["message"] = "Notification failed"
	}

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

	log.Printf("Found device token for user %s (token: %s...)", userID, deviceToken[:min(16, len(deviceToken))])

	// Create notification title and body based on number of games
	var title, body string
	if len(newTurnGames) == 1 {
		title = "Your turn in Go!"
		body = fmt.Sprintf("It's your turn in: %s", newTurnGames[0].Name)
	} else {
		title = "Your turn in Go!"
		body = fmt.Sprintf("It's your turn in %d games", len(newTurnGames))
	}

	// Use the first game for the deep link
	firstGame := newTurnGames[0]
	webURL := fmt.Sprintf("https://online-go.com/game/%d", firstGame.ID)
	appURL := fmt.Sprintf("ogs://game/%d", firstGame.ID)  // Custom URL scheme for the app

	// Create notification payload with both web and app URLs
	notification := &apns2.Notification{}
	notification.DeviceToken = deviceToken
	notification.Topic = os.Getenv("APNS_BUNDLE_ID")

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
	} else {
		log.Printf("Push notification failed for user %s: %v", userID, res.Reason)
	}
}

func sendPushNotification(userID, gameName string) {
	// Legacy function - keeping for backward compatibility with test endpoint
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

	// Create notification payload
	notification := &apns2.Notification{}
	notification.DeviceToken = deviceToken
	notification.Topic = os.Getenv("APNS_BUNDLE_ID")

	payload := payload.NewPayload().Alert("Your turn in Go!").
		AlertBody(fmt.Sprintf("It's your turn in: %s", gameName)).
		Badge(1).
		Sound("default")

	notification.Payload = payload

	// Send the notification
	res, err := apnsClient.Push(notification)
	if err != nil {
		log.Printf("Error sending push notification to user %s: %v", userID, err)
		return
	}

	if res.Sent() {
		log.Printf("Push notification sent successfully to user %s for game: %s", userID, gameName)
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
	} else if intervalStr := os.Getenv("CHECK_INTERVAL_MINUTES"); intervalStr != "" {
		if interval, err := strconv.Atoi(intervalStr); err == nil {
			checkInterval = time.Duration(interval) * time.Minute
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
		log.Printf("Checking user %s...", userIDStr)

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

