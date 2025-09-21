package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"
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
	CurrentPlayer int `json:"current_player"`
}

type PlayerResponse struct {
	ActiveGames []Game `json:"active_games"`
}

type TurnStatus struct {
	UserID      int    `json:"user_id"`
	GamesCount  int    `json:"games_count"`
	YourTurn    []Game `json:"your_turn"`
	WaitingFor  []Game `json:"waiting_for"`
}

func main() {
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
		UserID:     userID,
		GamesCount: len(games),
		YourTurn:   []Game{},
		WaitingFor: []Game{},
	}

	for _, game := range games {
		if game.JSON.Clock.CurrentPlayer == userID {
			status.YourTurn = append(status.YourTurn, game)
		} else {
			status.WaitingFor = append(status.WaitingFor, game)
		}
	}

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

