package arena

import (
	"bytes"
	"encoding/json"
	"reflect"
	"slices"
	"testing"
)

func TestNewGameResult(t *testing.T) {
	state := &GameState{
		NumPlayers:      4,
		Turn:            42,
		PlayerResources: []uint{3, 7, 3, 0},
		Winners:         []int{1},
		GameOver:        true,
	}

	result := NewGameResult(state, []string{"a", "b", "c", "d"})

	if result.Turns != 42 || !slices.Equal(result.Winners, []int{1}) {
		t.Errorf("unexpected result: %+v", result)
	}

	expected := []PlayerResult{
		{Name: "a", Flowers: 3, Rank: 2},
		{Name: "b", Flowers: 7, Rank: 1},
		{Name: "c", Flowers: 3, Rank: 2},
		{Name: "d", Flowers: 0, Rank: 4},
	}
	if !slices.Equal(result.Players, expected) {
		t.Errorf("expected %+v, got %+v", expected, result.Players)
	}
}

func TestNewGameResultAllTied(t *testing.T) {
	state := &GameState{
		NumPlayers:      3,
		PlayerResources: []uint{0, 0, 0},
		Winners:         []int{0, 1, 2},
		GameOver:        true,
	}

	result := NewGameResult(state, []string{"a", "b", "c"})

	for _, player := range result.Players {
		if player.Rank != 1 {
			t.Errorf("tied players should all rank first, got %+v", result.Players)
		}
	}
}

func TestWriteJSON(t *testing.T) {
	state := &GameState{NumPlayers: 1, Hexes: map[Coords]*Hex{{0, 0}: {Terrain: FIELD, Resources: 3}}, PlayerResources: []uint{0}}
	game := &PersistedGame{
		Id: "game", Map: "tiny", Players: []string{"a"},
		History: []Turn{{State: state}, {State: state, Orders: []*Order{{Type: FORAGE}}}},
		Result:  &GameResult{Turns: 1},
	}

	var streamed bytes.Buffer
	if err := game.WriteJSON(&streamed); err != nil {
		t.Fatal(err)
	}
	var decoded, expected any
	if err := json.Unmarshal(streamed.Bytes(), &decoded); err != nil {
		t.Fatalf("invalid JSON: %s\n%s", err, streamed.String())
	}
	data, _ := json.Marshal(game)
	json.Unmarshal(data, &expected)
	if !reflect.DeepEqual(decoded, expected) {
		t.Errorf("streamed JSON differs:\n%s\n%s", streamed.String(), data)
	}

	// Without history
	game.History = nil
	streamed.Reset()
	game.WriteJSON(&streamed)
	if err := json.Unmarshal(streamed.Bytes(), &decoded); err != nil {
		t.Errorf("invalid JSON without history: %s", streamed.String())
	}
}
