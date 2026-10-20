package main

import (
	"fmt"
	"math/rand"
	"os"

	"github.com/hivehelsinki/hive-arena/common"
)

var dirs = []common.Direction{common.E, common.SE, common.SW, common.W, common.NW, common.NE}

func think(state *common.GameState, player int) []common.Order {

	var orders []common.Order

	for coords, hex := range state.Hexes {
		unit := hex.Entity

		if unit != nil && unit.Type == common.BEE && unit.Player == player {
			fmt.Println(coords, unit)
			orders = append(orders, common.Order{
				Type:      common.MOVE,
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
