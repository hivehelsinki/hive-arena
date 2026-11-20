package main

import (
	"flag"
	"fmt"
	"runtime/debug"
	"time"
)

func GitRevision() string {
	buildInfo, ok := debug.ReadBuildInfo()
	if !ok {
		return ""
	}

	for _, info := range buildInfo.Settings {
		if info.Key == "vcs.revision" {
			return info.Value
		}
	}

	return ""
}

var DevMode bool

// TimeBudget is the total time a player may take to answer, over a game
// (0: no limit). The time of a turn is the time from its start to the
// player's orders, or the whole turn timeout if they do not come.
var TimeBudget time.Duration

func main() {
	port := flag.Int("p", 8000, "port on which the server will listen")
	flag.BoolVar(&DevMode, "dev", false, "run the server in development mode")
	flag.StringVar(&MapDir, "maps", MapDir, "directory of map files to load, for specific scenarios (random maps need none)")
	flag.StringVar(&HistoryDir, "history", HistoryDir, "directory to save the reports of finished games in (empty: not saved)")
	flag.IntVar(&MaxGames, "max-games", MaxGames, "maximum number of games the server creates (0: no limit)")
	flag.DurationVar(&TimeBudget, "time-budget", 0, "total time each player may take to answer over a game, e.g. 5m; a player who exceeds it is forfeited (0: no limit)")
	flag.Parse()

	fmt.Println("git revision: " + GitRevision())
	RunServer(*port)
}
