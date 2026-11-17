package arena

// The final result of a game, for each player
type PlayerResult struct {
	Name    string `json:"name"`
	Flowers uint   `json:"flowers"`
	// Rank is 1 + the number of players with strictly more flowers, so that
	// tied players share the same rank (e.g. 1, 2, 2, 4)
	Rank int `json:"rank"`
	// Whether the server stopped waiting for the player's orders (see the /forfeit route)
	Forfeited bool `json:"forfeited,omitempty"`
}

type GameResult struct {
	Turns   uint           `json:"turns"`
	Winners []int          `json:"winners"`
	Players []PlayerResult `json:"players"` // indexed by player ID
}

// NewGameResult summarizes a finished game. names are the player names,
// indexed by player ID.
func NewGameResult(state *GameState, names []string) *GameResult {
	result := &GameResult{
		Turns:   state.Turn,
		Winners: append([]int{}, state.Winners...),
		Players: make([]PlayerResult, state.NumPlayers),
	}

	for player, flowers := range state.PlayerResources {
		rank := 1
		for _, other := range state.PlayerResources {
			if other > flowers {
				rank++
			}
		}

		result.Players[player] = PlayerResult{Flowers: flowers, Rank: rank}
		if player < len(names) {
			result.Players[player].Name = names[player]
		}
	}

	return result
}
