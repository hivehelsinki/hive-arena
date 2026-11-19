package arena

import (
	"os"
	"testing"
)

func toCube(c Coords, radius int) cube {
	r := c.Row - radius
	q := (c.Col - r - 2*radius - radius%2) / 2
	return cube{q, -q - r, r}
}

func TestGeneratedMapsAreSymmetric(t *testing.T) {
	settings := DefaultMapSettings()
	for _, n := range RandomMapPlayers {
		radius := settings.Radius[n]
		syms := symmetries(n)
		for seed := range int64(50) {
			m, err := GenerateMap(n, seed, settings)
			if err != nil {
				t.Fatal(err)
			}
			terrain := make(map[cube]Terrain)
			for c, ter := range m.Map {
				if (c.Row+c.Col)%2 != 0 {
					t.Fatalf("%d players: %v is not a doubled coordinate (row + column must be even)", n, c)
				}
				terrain[toCube(c, radius)] = ter
			}
			for c, ter := range terrain {
				for i, s := range syms {
					if terrain[s(c)] != ter {
						t.Fatalf("%d players, seed %d: %v is %s but its image %d %v is %s", n, seed, c, ter, i, s(c), terrain[s(c)])
					}
				}
			}

			// Each player's spawns are the images of player 0's
			spawns := make(map[cube]Spawn)
			for _, sp := range m.Spawns {
				spawns[toCube(sp.Coords, radius)] = sp
			}
			for c, sp := range spawns {
				if sp.Player != 0 {
					continue
				}
				for i, s := range syms {
					image, ok := spawns[s(c)]
					if !ok || image.Kind != sp.Kind || image.Player != i {
						t.Fatalf("%d players, seed %d: the %s of player 0 at %v has no counterpart for player %d", n, seed, sp.Kind, c, i)
					}
				}
			}
			if len(m.Spawns) != 4*n {
				t.Errorf("expected a hive and 3 bees per player, got %d spawns", len(m.Spawns))
			}

			// Playable: every walkable cell is reachable from every base
			gs := NewGame(m, n, DefaultRules(), seed)
			if gs == nil {
				t.Fatalf("%d players, seed %d: could not start a game", n, seed)
			}
			var start Coords
			walkable := 0
			for c, hex := range gs.Hexes {
				if hex.Terrain.IsWalkable() {
					walkable++
					start = c
				}
			}
			reached := map[Coords]bool{start: true}
			queue := []Coords{start}
			for len(queue) > 0 {
				c := queue[0]
				queue = queue[1:]
				for _, nb := range c.Neighbours() {
					if hex := gs.Hexes[nb]; hex != nil && hex.Terrain.IsWalkable() && !reached[nb] {
						reached[nb] = true
						queue = append(queue, nb)
					}
				}
			}
			if len(reached) != walkable {
				t.Errorf("%d players, seed %d: %d of %d walkable cells are reachable", n, seed, len(reached), walkable)
			}
		}
	}
}

func TestGeneratedMapsAreReproducible(t *testing.T) {
	a, _ := GenerateMap(4, 42, DefaultMapSettings())
	b, _ := GenerateMap(4, 42, DefaultMapSettings())
	c, _ := GenerateMap(4, 43, DefaultMapSettings())
	if a.String() != b.String() {
		t.Error("the same seed should give the same map")
	}
	if a.String() == c.String() {
		t.Error("another seed should give another map")
	}
	if _, err := GenerateMap(5, 1, DefaultMapSettings()); err == nil {
		t.Error("there are no fair random maps for 5 players")
	}
}

func TestGeneratedMapsRoundTrip(t *testing.T) {
	m, _ := GenerateMap(3, 7, DefaultMapSettings())
	path := t.TempDir() + "/map.txt"
	if err := writeFile(path, m.String()); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadMap(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.Map) != len(m.Map) || len(loaded.Spawns) != len(m.Spawns) {
		t.Errorf("the rendered map should load back: %d cells and %d spawns, expected %d and %d",
			len(loaded.Map), len(loaded.Spawns), len(m.Map), len(m.Spawns))
	}
	for c, ter := range m.Map {
		if loaded.Map[c] != ter {
			t.Fatalf("%v: %s, expected %s", c, loaded.Map[c], ter)
		}
	}
}

func writeFile(path, content string) error {
	return os.WriteFile(path, []byte(content), 0o644)
}
