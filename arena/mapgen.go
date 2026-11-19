package arena

import (
	"fmt"
	"math/rand"
	"slices"
	"strings"
)

// Random maps, exactly fair: every feature (rocks, fields, bases) is placed
// once, then copied by the symmetries of the map, so that every player sees
// the same surroundings. A hexagonal map has the symmetries needed for 2, 3
// and 6 players (rotations), and 4 players (two mirrors); there is no exact
// symmetry for 5 players on a hex grid.

// MapSettings shape random maps
type MapSettings struct {
	// Radius of the hexagonal map, by number of players
	Radius map[int]int
	// Shares of the map covered by fields and rocks (roughly)
	FieldShare float64
	RockShare  float64
	// Distance from a base to the nearest field, at least and at most
	HomeFieldDistance [2]int
}

func DefaultMapSettings() MapSettings {
	return MapSettings{
		Radius:            map[int]int{2: 8, 3: 10, 4: 11, 6: 12},
		FieldShare:        0.14,
		RockShare:         0.10,
		HomeFieldDistance: [2]int{2, 4},
	}
}

// RandomMapPlayers are the numbers of players random maps can be made for
var RandomMapPlayers = []int{2, 3, 4, 6}

// cube coordinates, centered on the middle of the map
type cube struct{ x, y, z int }

func (c cube) add(o cube) cube { return cube{c.x + o.x, c.y + o.y, c.z + o.z} }

func (c cube) distance(o cube) int {
	return max(abs(c.x-o.x), abs(c.y-o.y), abs(c.z-o.z))
}

var cubeDirections = []cube{{1, -1, 0}, {1, 0, -1}, {0, 1, -1}, {-1, 1, 0}, {-1, 0, 1}, {0, -1, 1}}

func rotate60(c cube) cube { return cube{-c.z, -c.x, -c.y} }

func rotate(c cube, times int) cube {
	for range ((times % 6) + 6) % 6 {
		c = rotate60(c)
	}
	return c
}

// symmetries returns the transformations of a map for n players: the first
// one is the identity, and the i-th one takes player 0's base to player i's
func symmetries(n int) []func(cube) cube {
	switch n {
	case 2, 3, 6:
		var result []func(cube) cube
		for i := range n {
			turns := i * 6 / n
			result = append(result, func(c cube) cube { return rotate(c, turns) })
		}
		return result
	case 4:
		// Two perpendicular mirrors, and their composition (a half turn).
		// Seats in order around the map: identity, mirror, half turn, mirror.
		mirror := func(c cube) cube { return cube{c.x, c.z, c.y} }
		other := func(c cube) cube { return cube{-c.x, -c.z, -c.y} }
		return []func(cube) cube{
			func(c cube) cube { return c },
			mirror,
			func(c cube) cube { return rotate(c, 3) },
			other,
		}
	}
	return nil
}

func (c cube) coords(radius int) Coords {
	// Doubled coordinates (see Coords), shifted so that they are not
	// negative, and so that row + column stays even as in map files
	q, r := c.x, c.z
	return Coords{Row: r + radius, Col: 2*q + r + 2*radius + radius%2}
}

// GenerateMap generates a random map for numPlayers players (see
// RandomMapPlayers). The same seed always gives the same map.
func GenerateMap(numPlayers int, seed int64, settings MapSettings) (MapData, error) {
	syms := symmetries(numPlayers)
	if syms == nil {
		return MapData{}, fmt.Errorf("random maps are for %v players, not %d", RandomMapPlayers, numPlayers)
	}
	radius := settings.Radius[numPlayers]
	if radius < 5 {
		return MapData{}, fmt.Errorf("no map radius for %d players", numPlayers)
	}
	rng := rand.New(rand.NewSource(seed))

	for range 1000 {
		if m, ok := tryMap(numPlayers, radius, syms, settings, rng); ok {
			return m, nil
		}
	}
	return MapData{}, fmt.Errorf("could not generate a map for %d players with seed %d", numPlayers, seed)
}

func tryMap(numPlayers, radius int, syms []func(cube) cube, settings MapSettings, rng *rand.Rand) (MapData, bool) {
	var cells []cube
	terrain := make(map[cube]Terrain)
	for x := -radius; x <= radius; x++ {
		for y := max(-radius, -x-radius); y <= min(radius, -x+radius); y++ {
			c := cube{x, y, -x - y}
			cells = append(cells, c)
			terrain[c] = EMPTY
		}
	}
	center := cube{}
	inMap := func(c cube) bool { _, ok := terrain[c]; return ok }

	// Every feature is stamped with all the symmetries
	stamp := func(c cube, t Terrain) {
		for _, s := range syms {
			if p := s(c); inMap(p) {
				terrain[p] = t
			}
		}
	}

	// Player 0's base: near the edge, in a direction that keeps the bases
	// apart (for rotations, any angle works; for mirrors, off the axes)
	var base cube
	for {
		base = cells[rng.Intn(len(cells))]
		d := base.distance(center)
		if d < radius-3 || d > radius-1 {
			continue
		}
		far := true
		for _, s := range syms[1:] {
			if s(base).distance(base) < radius*3/4 {
				far = false
			}
		}
		if far {
			break
		}
	}
	bases := make([]cube, numPlayers)
	for i, s := range syms {
		bases[i] = s(base)
	}
	nearBase := func(c cube, dist int) bool {
		for _, b := range bases {
			if c.distance(b) <= dist {
				return true
			}
		}
		return false
	}

	// Rocks: ridges (random walks) and boulders, away from the bases
	target := int(settings.RockShare * float64(len(cells)) / float64(numPlayers))
	for placed := 0; placed < target; {
		c := cells[rng.Intn(len(cells))]
		length := 2 + rng.Intn(5)
		dir := rng.Intn(6)
		for range length {
			if !nearBase(c, 3) && c != center {
				stamp(c, ROCK)
				placed++
			}
			if rng.Intn(3) == 0 {
				dir = (dir + []int{1, 5}[rng.Intn(2)]) % 6
			}
			c = c.add(cubeDirections[dir])
			if !inMap(c) {
				break
			}
		}
	}

	// Fields: a home cluster near each base, contested clusters elsewhere
	cluster := func(c cube, size int) int {
		placed := 0
		for range size * 3 {
			if placed >= size {
				break
			}
			if inMap(c) && terrain[c] != FIELD && !nearBase(c, 1) {
				stamp(c, FIELD)
				placed++
			}
			c = c.add(cubeDirections[rng.Intn(6)])
		}
		return placed
	}
	lo, hi := settings.HomeFieldDistance[0], settings.HomeFieldDistance[1]
	for {
		c := base.add(cube{})
		for range lo + rng.Intn(hi-lo+1) {
			c = c.add(cubeDirections[rng.Intn(6)])
		}
		if d := c.distance(base); d >= lo && d <= hi && inMap(c) {
			cluster(c, 3+rng.Intn(3))
			break
		}
	}
	target = int(settings.FieldShare * float64(len(cells)) / float64(numPlayers))
	for placed := 0; placed < target; {
		c := cells[rng.Intn(len(cells))]
		if nearBase(c, hi) {
			continue
		}
		placed += cluster(c, 2+rng.Intn(5))
	}

	// The bases: a hive and three bees, on clear ground
	for _, b := range bases {
		for _, c := range cells {
			if c.distance(b) <= 1 {
				terrain[c] = EMPTY
			}
		}
	}

	// Walkable cells must all be reachable from the bases: closed pockets
	// become rock (symmetric, since reachability is)
	reached := map[cube]bool{bases[0]: true}
	queue := []cube{bases[0]}
	for len(queue) > 0 {
		c := queue[0]
		queue = queue[1:]
		for _, d := range cubeDirections {
			n := c.add(d)
			if inMap(n) && terrain[n] != ROCK && !reached[n] {
				reached[n] = true
				queue = append(queue, n)
			}
		}
	}
	for _, b := range bases {
		if !reached[b] {
			return MapData{}, false
		}
	}
	fields := 0
	for _, c := range cells {
		if terrain[c] != ROCK && !reached[c] {
			terrain[c] = ROCK
		}
		if terrain[c] == FIELD {
			fields++
		}
	}
	if fields < numPlayers*8 {
		return MapData{}, false
	}

	m := MapData{Map: make(map[Coords]Terrain), Players: numPlayers}
	for c, t := range terrain {
		m.Map[c.coords(radius)] = t
	}
	// Player 0's three bees face the middle of the map; the others' are
	// their images
	var around []cube
	for _, d := range cubeDirections {
		around = append(around, base.add(d))
	}
	slices.SortStableFunc(around, func(a, b cube) int { return a.distance(center) - b.distance(center) })
	for player, sym := range syms {
		m.Spawns = append(m.Spawns, Spawn{Kind: HIVE, Player: player, Coords: bases[player].coords(radius)})
		for _, c := range around[:3] {
			m.Spawns = append(m.Spawns, Spawn{Kind: BEE, Player: player, Coords: sym(c).coords(radius)})
		}
	}
	return m, true
}

// String renders a map in the format of the files in maps/
func (m MapData) String() string {
	var rows, cols int
	for c := range m.Map {
		rows, cols = max(rows, c.Row+1), max(cols, c.Col+1)
	}
	lines := make([][]byte, rows)
	for i := range lines {
		lines[i] = []byte(strings.Repeat(" ", 2*cols+2))
	}
	put := func(c Coords, s string) {
		copy(lines[c.Row][2*c.Col:], s)
	}
	for c, t := range m.Map {
		for char, terrain := range charToTerrain {
			if terrain == t {
				put(c, string(char))
			}
		}
	}
	for _, s := range m.Spawns {
		for char, kind := range charToSpawn {
			if kind == s.Kind {
				put(s.Coords, fmt.Sprintf("%c%d", char, s.Player))
			}
		}
	}
	var out strings.Builder
	for _, line := range lines {
		out.WriteString(strings.TrimRight(string(line), " "))
		out.WriteByte('\n')
	}
	return out.String()
}
