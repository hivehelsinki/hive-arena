package common

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// newState builds a game state from map rows, in the same format as the files in maps/
func newState(t *testing.T, numPlayers int, rows ...string) *GameState {
	t.Helper()

	path := filepath.Join(t.TempDir(), "map.txt")
	if err := os.WriteFile(path, []byte(strings.Join(rows, "\n")), 0o644); err != nil {
		t.Fatal(err)
	}

	mapdata, err := LoadMap(path)
	if err != nil {
		t.Fatal(err)
	}

	gs := NewGameState(mapdata, numPlayers)
	if gs == nil {
		t.Fatal("could not create game state")
	}
	return gs
}

func order(t OrderType, row, col int, dir Direction) *Order {
	return &Order{Type: t, Coords: Coords{row, col}, Direction: dir}
}

// play processes one turn where only player 0 gives orders
func play(t *testing.T, gs *GameState, orders ...*Order) {
	t.Helper()

	all := make([][]*Order, gs.NumPlayers)
	all[0] = orders
	if _, err := gs.ProcessOrders(all); err != nil {
		t.Fatal(err)
	}
}

func expectStatus(t *testing.T, o *Order, status OrderStatus) {
	t.Helper()
	if o.Status != status {
		t.Errorf("%s order at %v: expected status %s, got %s", o.Type, o.Coords, status, o.Status)
	}
}

func expectEntity(t *testing.T, gs *GameState, row, col int, kind EntityType, player int) *Entity {
	t.Helper()
	entity := gs.EntityAt(Coords{row, col})
	if entity == nil || entity.Type != kind || entity.Player != player {
		t.Fatalf("expected %s of player %d at %d,%d, got %+v", kind, player, row, col, entity)
	}
	return entity
}

// H0 at 0,0, B0 at 0,2, a field at 0,4, a rock at 0,8
var smallMap = []string{
	"H0  B0  F   .   R",
	"  .   .   .   .  ",
}

func TestLoadMap(t *testing.T) {
	gs := newState(t, 1, smallMap...)

	terrains := map[Coords]Terrain{
		{0, 0}: EMPTY, {0, 2}: EMPTY, {0, 4}: FIELD, {0, 6}: EMPTY, {0, 8}: ROCK,
		{1, 1}: EMPTY, {1, 3}: EMPTY, {1, 5}: EMPTY, {1, 7}: EMPTY,
	}
	if len(gs.Hexes) != len(terrains) {
		t.Errorf("expected %d hexes, got %d", len(terrains), len(gs.Hexes))
	}
	for coords, terrain := range terrains {
		if hex := gs.Hexes[coords]; hex == nil || hex.Terrain != terrain {
			t.Errorf("expected %s at %v, got %+v", terrain, coords, hex)
		}
	}

	expectEntity(t, gs, 0, 0, HIVE, 0)
	expectEntity(t, gs, 0, 2, BEE, 0)

	if gs.Hexes[Coords{0, 4}].Resources != INIT_FIELD_FLOWERS {
		t.Errorf("expected field to start with %d flowers", INIT_FIELD_FLOWERS)
	}
}

func TestLoadInvalidMap(t *testing.T) {
	for _, content := range []string{"F   H", "F   Hx  .", "F   B6  ."} {
		path := filepath.Join(t.TempDir(), "map.txt")
		os.WriteFile(path, []byte(content), 0o644)

		if _, err := LoadMap(path); err == nil {
			t.Errorf("loading %q should fail", content)
		}
	}
}

func TestLoadAllMaps(t *testing.T) {
	paths, _ := filepath.Glob("../maps/*.txt")
	if len(paths) == 0 {
		t.Fatal("no maps found")
	}

	for _, path := range paths {
		mapdata, err := LoadMap(path)
		if err != nil {
			t.Errorf("%s: %s", path, err)
			continue
		}

		for players := 1; players <= MaxPlayers; players++ {
			gs := NewGameState(mapdata, players)
			for player := range players {
				if len(gs.PlayerView(player).Hexes) == 0 {
					t.Errorf("%s with %d players: player %d has no units", path, players, player)
				}
			}
		}
	}
}

func TestPlayerMapping(t *testing.T) {
	// With 2 players, the spawns of map players 0 and 3 are used
	gs := newState(t, 2, "H0  H1  H2  H3  H4  H5  F")

	expectEntity(t, gs, 0, 0, HIVE, 0)
	expectEntity(t, gs, 0, 6, HIVE, 1)
	for _, col := range []int{2, 4, 8, 10} {
		if entity := gs.EntityAt(Coords{0, col}); entity != nil {
			t.Errorf("expected no entity at 0,%d, got %+v", col, entity)
		}
	}
}

func TestMove(t *testing.T) {
	gs := newState(t, 1, smallMap...)

	move := order(MOVE, 0, 2, SE)
	play(t, gs, move)

	expectStatus(t, move, OK)
	expectEntity(t, gs, 1, 3, BEE, 0)
	if gs.EntityAt(Coords{0, 2}) != nil {
		t.Error("bee should have left its hex")
	}
}

func TestMoveBlocked(t *testing.T) {
	gs := newState(t, 1,
		"H0  B0  F   B0  R",
		"  .   .   .   .  ",
	)

	intoHive := order(MOVE, 0, 2, W)
	offMap := order(MOVE, 0, 2, NE)
	intoRock := order(MOVE, 0, 6, E)
	play(t, gs, intoHive, offMap, intoRock)

	expectStatus(t, intoHive, BLOCKED)
	expectStatus(t, offMap, UNIT_ALREADY_ACTED)
	expectStatus(t, intoRock, BLOCKED)
	expectEntity(t, gs, 0, 2, BEE, 0)
	expectEntity(t, gs, 0, 6, BEE, 0)

	offMap = order(MOVE, 0, 2, NE)
	play(t, gs, offMap)
	expectStatus(t, offMap, BLOCKED)
}

func TestOneActionPerUnit(t *testing.T) {
	gs := newState(t, 1, smallMap...)

	first := order(MOVE, 0, 2, SE)
	second := order(MOVE, 1, 3, E)
	play(t, gs, first, second)

	expectStatus(t, first, OK)
	expectStatus(t, second, UNIT_ALREADY_ACTED)
	expectEntity(t, gs, 1, 3, BEE, 0)
}

func TestInvalidUnit(t *testing.T) {
	gs := newState(t, 1, smallMap...)

	empty := order(MOVE, 1, 1, E)
	hiveMoving := order(MOVE, 0, 0, SE)
	beeSpawning := order(SPAWN, 0, 2, SE)
	play(t, gs, empty, hiveMoving, beeSpawning)

	expectStatus(t, empty, INVALID_UNIT)
	expectStatus(t, hiveMoving, INVALID_UNIT)
	expectStatus(t, beeSpawning, INVALID_UNIT)
}

func TestOrdersForEnemyUnitsDoNotUseTheirAction(t *testing.T) {
	gs := newState(t, 2,
		"H0  B0  F   .   F   B3  H3",
	)

	// Player 0 tries to use up the action of player 1's bee before player 1's own order
	sabotage := order(MOVE, 0, 10, E)
	padding := order(MOVE, 9, 9, W)
	move := order(MOVE, 0, 10, W)

	if _, err := gs.ProcessOrders([][]*Order{{sabotage}, {padding, move}}); err != nil {
		t.Fatal(err)
	}

	expectStatus(t, sabotage, INVALID_UNIT)
	expectStatus(t, move, OK)
	expectEntity(t, gs, 0, 8, BEE, 1)
}

func TestNullOrdersAreIgnored(t *testing.T) {
	gs := newState(t, 1, smallMap...)

	move := order(MOVE, 0, 2, SE)
	play(t, gs, nil, move, nil)

	expectStatus(t, move, OK)
	expectEntity(t, gs, 1, 3, BEE, 0)
}

func TestInvalidOrders(t *testing.T) {
	gs := newState(t, 1, smallMap...)

	unknownType := order("DANCE", 0, 2, E)
	noDirection := order(ATTACK, 0, 2, "")
	badDirection := order(MOVE, 0, 2, "UP")
	fakeStatus := &Order{Type: "DANCE", Coords: Coords{0, 2}, Status: OK}
	valid := order(MOVE, 0, 2, SE)
	play(t, gs, unknownType, noDirection, badDirection, fakeStatus, valid)

	expectStatus(t, unknownType, INVALID_ORDER)
	expectStatus(t, noDirection, INVALID_ORDER)
	expectStatus(t, badDirection, INVALID_ORDER)
	expectStatus(t, fakeStatus, INVALID_ORDER)

	// Invalid orders do not use up the unit's action
	expectStatus(t, valid, OK)
}

func TestForageAndDrop(t *testing.T) {
	gs := newState(t, 1, smallMap...)

	notOnField := order(FORAGE, 0, 2, "")
	play(t, gs, notOnField)
	expectStatus(t, notOnField, CANNOT_FORAGE)

	play(t, gs, order(MOVE, 0, 2, E))

	forage := order(FORAGE, 0, 4, "")
	play(t, gs, forage)
	expectStatus(t, forage, OK)
	if !gs.EntityAt(Coords{0, 4}).HasFlower {
		t.Fatal("bee should carry a flower")
	}
	if gs.Hexes[Coords{0, 4}].Resources != INIT_FIELD_FLOWERS-1 {
		t.Error("field should have lost a flower")
	}

	farFromHive := order(FORAGE, 0, 4, "")
	play(t, gs, farFromHive)
	expectStatus(t, farFromHive, CANNOT_FORAGE)

	play(t, gs, order(MOVE, 0, 4, W))

	drop := order(FORAGE, 0, 2, "")
	play(t, gs, drop)
	expectStatus(t, drop, OK)
	if gs.EntityAt(Coords{0, 2}).HasFlower {
		t.Error("bee should have dropped its flower")
	}
	if gs.PlayerResources[0] != 1 {
		t.Errorf("expected 1 flower in reserve, got %d", gs.PlayerResources[0])
	}
	if gs.LastResourceChange != gs.Turn-1 {
		t.Errorf("expected last resource change on turn %d, got %d", gs.Turn-1, gs.LastResourceChange)
	}
}

func TestSpawn(t *testing.T) {
	gs := newState(t, 1, smallMap...)

	tooPoor := order(SPAWN, 0, 0, SE)
	play(t, gs, tooPoor)
	expectStatus(t, tooPoor, NOT_ENOUGH_RESOURCES)

	gs.PlayerResources[0] = BEE_COST + 1
	spawn := order(SPAWN, 0, 0, SE)
	play(t, gs, spawn)

	expectStatus(t, spawn, OK)
	expectEntity(t, gs, 1, 1, BEE, 0)
	if gs.PlayerResources[0] != 1 {
		t.Errorf("expected 1 flower left, got %d", gs.PlayerResources[0])
	}
}

func TestBuildWall(t *testing.T) {
	gs := newState(t, 1, smallMap...)

	tooPoor := order(BUILD_WALL, 0, 2, SE)
	play(t, gs, tooPoor)
	expectStatus(t, tooPoor, NOT_ENOUGH_RESOURCES)

	gs.PlayerResources[0] = WALL_COST
	build := order(BUILD_WALL, 0, 2, SE)
	play(t, gs, build)

	expectStatus(t, build, OK)
	expectEntity(t, gs, 1, 3, WALL, 0)
	if gs.PlayerResources[0] != 0 {
		t.Errorf("expected no flower left, got %d", gs.PlayerResources[0])
	}

	blocked := order(MOVE, 0, 2, SE)
	play(t, gs, blocked)
	expectStatus(t, blocked, BLOCKED)
}

func TestBuildHive(t *testing.T) {
	gs := newState(t, 1, smallMap...)

	gs.PlayerResources[0] = HIVE_COST
	build := order(BUILD_HIVE, 0, 2, "")
	play(t, gs, build)

	expectStatus(t, build, OK)
	expectEntity(t, gs, 0, 2, HIVE, 0)
	if gs.PlayerResources[0] != 0 {
		t.Errorf("expected no flower left, got %d", gs.PlayerResources[0])
	}
}

func TestAttack(t *testing.T) {
	gs := newState(t, 1, smallMap...)

	nothing := order(ATTACK, 0, 2, SE)
	play(t, gs, nothing)
	expectStatus(t, nothing, INVALID_TARGET)

	gs.Hexes[Coords{1, 3}].Entity = &Entity{Type: WALL}
	for range 1000 {
		play(t, gs, order(ATTACK, 0, 2, SE))
		if gs.EntityAt(Coords{1, 3}) == nil {
			return
		}
	}
	t.Error("wall should eventually be destroyed")
}

func TestGameOverWhenNoFlowersLeft(t *testing.T) {
	gs := newState(t, 1, smallMap...)

	gs.Hexes[Coords{0, 4}].Resources = 0
	gs.EntityAt(Coords{0, 2}).HasFlower = true
	play(t, gs)
	if gs.GameOver {
		t.Fatal("game should not be over while a bee carries a flower")
	}

	gs.EntityAt(Coords{0, 2}).HasFlower = false
	play(t, gs)
	if !gs.GameOver {
		t.Fatal("game should be over")
	}

	if _, err := gs.ProcessOrders(make([][]*Order, 1)); err == nil {
		t.Error("processing orders after the game is over should fail")
	}
}

func TestGameOverOnResourceTimeout(t *testing.T) {
	gs := newState(t, 2,
		"H0  B0  F   .   .   .   B3  H3",
	)

	gs.PlayerResources[0] = 3
	gs.PlayerResources[1] = 3

	for range RESOURCE_TIMEOUT {
		play(t, gs)
	}
	if gs.GameOver {
		t.Fatal("game should not be over yet")
	}

	play(t, gs)
	if !gs.GameOver {
		t.Fatal("game should be over")
	}
	if !slices.Equal(gs.Winners, []int{0, 1}) {
		t.Errorf("expected both players to win, got %v", gs.Winners)
	}
}

func TestPlayerView(t *testing.T) {
	// 0,20 is 10 hexes away from player 0's hive
	gs := newState(t, 2,
		"H0  .   .   .   .   .   .   .   .   .   H3  F",
	)
	gs.PlayerResources[0] = 4
	gs.PlayerResources[1] = 7

	view := gs.PlayerView(0)

	for col := 0; col <= 8; col += 2 {
		if view.Hexes[Coords{0, col}] == nil {
			t.Errorf("0,%d should be visible", col)
		}
	}
	for col := 10; col <= 22; col += 2 {
		if view.Hexes[Coords{0, col}] != nil {
			t.Errorf("0,%d should not be visible", col)
		}
	}

	if !slices.Equal(view.PlayerResources, []uint{4, 0}) {
		t.Errorf("expected only own resources to be visible, got %v", view.PlayerResources)
	}
}

func TestClone(t *testing.T) {
	gs := newState(t, 1, smallMap...)

	clone := gs.Clone()
	clone.Hexes[Coords{0, 4}].Resources = 0
	clone.EntityAt(Coords{0, 2}).HasFlower = true
	clone.PlayerResources[0] = 10

	if gs.Hexes[Coords{0, 4}].Resources != INIT_FIELD_FLOWERS ||
		gs.EntityAt(Coords{0, 2}).HasFlower ||
		gs.PlayerResources[0] != 0 {
		t.Error("modifying a clone should not modify the original")
	}
}

func TestDistance(t *testing.T) {
	cases := []struct {
		a, b     Coords
		distance int
	}{
		{Coords{0, 0}, Coords{0, 0}, 0},
		{Coords{0, 0}, Coords{0, 2}, 1},
		{Coords{0, 0}, Coords{1, 1}, 1},
		{Coords{0, 0}, Coords{0, 8}, 4},
		{Coords{0, 0}, Coords{3, 3}, 3},
		{Coords{0, 0}, Coords{3, 1}, 3},
		{Coords{2, 2}, Coords{0, 8}, 4},
	}

	for _, c := range cases {
		if d := c.a.Distance(c.b); d != c.distance {
			t.Errorf("distance from %v to %v: expected %d, got %d", c.a, c.b, c.distance, d)
		}
		for _, dir := range []Direction{E, NE, NW, W, SW, SE} {
			if c.a.Distance(c.a.Neighbour(dir)) != 1 {
				t.Errorf("neighbour %s of %v should be at distance 1", dir, c.a)
			}
		}
	}
}

func BenchmarkPlayerView(b *testing.B) {
	mapdata, err := LoadMap("../maps/balanced.txt")
	if err != nil {
		b.Fatal(err)
	}
	gs := NewGameState(mapdata, 6)

	for b.Loop() {
		gs.PlayerView(0)
	}
}
