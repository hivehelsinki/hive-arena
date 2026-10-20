package arena

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

type Terrain string

const (
	EMPTY Terrain = "EMPTY"
	ROCK  Terrain = "ROCK"
	FIELD Terrain = "FIELD"
)

func (t Terrain) IsWalkable() bool {
	return t == EMPTY || t == FIELD
}

// Doubled coordinates system (https://www.redblobgames.com/grids/hexagons/)
// Pointy tops (horizontal rows)
// Top-left corner is 0,0
// Rows increase by 1, (vertical) columns increase by 2

type Coords struct {
	Row int
	Col int
}

func (c Coords) Neighbour(dir Direction) Coords {
	offset := DirectionToOffset[dir]
	return Coords{Row: c.Row + offset.Row, Col: c.Col + offset.Col}
}

func (c Coords) Neighbours() []Coords {
	neighbors := make([]Coords, 0, 6)
	for _, offset := range DirectionToOffset {
		neighbors = append(neighbors, Coords{Row: c.Row + offset.Row, Col: c.Col + offset.Col})
	}
	return neighbors
}

func abs(x int) int {
	return max(x, -x)
}

func (c Coords) Distance(b Coords) int {
	dcol := abs(c.Col - b.Col)
	drow := abs(c.Row - b.Row)
	return drow + max(0, (dcol-drow)/2)
}

type Direction string

const (
	E  Direction = "E"
	SE Direction = "SE"
	SW Direction = "SW"
	W  Direction = "W"
	NW Direction = "NW"
	NE Direction = "NE"
)

var DirectionToOffset = map[Direction]Coords{
	E:  {0, 2},
	NE: {-1, 1},
	NW: {-1, -1},
	W:  {0, -2},
	SW: {1, -1},
	SE: {1, 1},
}

type Spawn struct {
	Kind   EntityType
	Player int
	Coords Coords
}

type MapData struct {
	Map    map[Coords]Terrain
	Spawns []Spawn
}

var charToTerrain = map[rune]Terrain{
	'.': EMPTY,
	'F': FIELD,
	'R': ROCK,
}

var charToSpawn = map[rune]EntityType{
	'H': HIVE,
	'B': BEE,
}

func LoadMap(path string) (MapData, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return MapData{}, err
	}

	lines := strings.Split(string(content), "\n")
	gameMap := make(map[Coords]Terrain)
	spawns := []Spawn{}

	for row, line := range lines {
		for col, char := range line {
			coords := Coords{row, col / 2}

			if terrain, ok := charToTerrain[char]; ok {
				gameMap[coords] = terrain
			} else if kind, ok := charToSpawn[char]; ok {
				if col+1 >= len(line) {
					return MapData{}, fmt.Errorf("line %d: missing player number after %c", row+1, char)
				}
				player, err := strconv.Atoi(line[col+1 : col+2])
				if err != nil || player < 0 || player >= MaxPlayers {
					return MapData{}, fmt.Errorf("line %d: invalid player number after %c: %c", row+1, char, line[col+1])
				}

				spawns = append(spawns, Spawn{
					Kind:   kind,
					Player: player,
					Coords: coords,
				})
				gameMap[coords] = EMPTY
			}
		}
	}

	return MapData{
		Map:    gameMap,
		Spawns: spawns,
	}, nil
}
