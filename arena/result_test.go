package arena

import (
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
