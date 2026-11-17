package main

import (
	"fmt"
	"math/rand"
	"os"

	"github.com/hivehelsinki/hive-arena/arena"
)

var dirs = []arena.Direction{arena.E, arena.SE, arena.SW, arena.W, arena.NW, arena.NE}

func think(state *arena.GameState, player int) []arena.Order {

	var orders []arena.Order

	for coords, hex := range state.Hexes {
		unit := hex.Entity

		if unit != nil && unit.Type == arena.BEE && unit.Player == player {
			fmt.Println(coords, unit)
			orders = append(orders, arena.Order{
				Type:      arena.MOVE,
				Coords:    coords,
				Direction: dirs[rand.Intn(len(dirs))],
			})
		}
	}

	return orders
}

func main() {
	// Arguments, or environment variables when run by the ladder (see docs/docker.md)
	var host, id, name string
	if len(os.Args) > 3 {
		host, id, name = os.Args[1], os.Args[2], os.Args[3]
	} else {
		host, id, name = os.Getenv("ARENA_HOST"), os.Getenv("ARENA_GAME_ID"), os.Getenv("ARENA_NAME")
	}

	if host == "" || id == "" || name == "" {
		fmt.Println("Usage: ./agent <host> <gameid> <name>")
		fmt.Println("or set the ARENA_HOST, ARENA_GAME_ID and ARENA_NAME environment variables")
		os.Exit(1)
	}

	Run(host, id, name, think)
}
