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
	if len(os.Args) <= 3 {
		fmt.Println("Usage: ./agent <host> <gameid> <name>")
		os.Exit(1)
	}

	host := os.Args[1]
	id := os.Args[2]
	name := os.Args[3]

	Run(host, id, name, think)
}
