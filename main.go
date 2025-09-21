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
)

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
	mu    sync.RWMutex
	moves map[string]map[int]int64 // userID -> gameID -> lastMove
}

var storage = &MoveStorage{
	moves: make(map[string]map[int]int64),
}

func main() {
	loadStorage()

	r := mux.NewRouter()

	r.HandleFunc("/check/{userID}", checkUserTurn).Methods("GET")
	r.HandleFunc("/health", healthCheck).Methods("GET")

	log.Println("Server starting on :8080")
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
	games, err := getActiveGames(userID)
	if err != nil {
		return nil, err
	}

	status := &TurnStatus{
		NotYourTurn: []int{},
		YourTurnNew: []int{},
		YourTurnOld: []int{},
	}

	userIDStr := strconv.Itoa(userID)

	for _, game := range games {
		if game.JSON.Clock.CurrentPlayer == userID {
			// Check if this is a new turn vs old turn
			if isNewTurn(userIDStr, game.ID, game.JSON.Clock.LastMove) {
				status.YourTurnNew = append(status.YourTurnNew, game.ID)
				// Update stored move for new turns
				updateStoredMove(userIDStr, game.ID, game.JSON.Clock.LastMove)
			} else {
				status.YourTurnOld = append(status.YourTurnOld, game.ID)
			}
		} else {
			status.NotYourTurn = append(status.NotYourTurn, game.ID)
		}
	}

	saveStorage()
	return status, nil
}

func getActiveGames(userID int) ([]Game, error) {
	url := fmt.Sprintf("https://online-go.com/api/v1/players/%d/full", userID)

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch games: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
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

	data, err := os.ReadFile("moves.json")
	if err != nil {
		log.Println("No existing moves.json file, starting fresh")
		return
	}

	if err := json.Unmarshal(data, &storage.moves); err != nil {
		log.Printf("Error loading moves.json: %v", err)
		storage.moves = make(map[string]map[int]int64)
	}
}

func saveStorage() {
	storage.mu.RLock()
	defer storage.mu.RUnlock()

	data, err := json.MarshalIndent(storage.moves, "", "  ")
	if err != nil {
		log.Printf("Error marshaling moves: %v", err)
		return
	}

	if err := os.WriteFile("moves.json", data, 0644); err != nil {
		log.Printf("Error saving moves.json: %v", err)
	}
}

