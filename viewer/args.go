//go:build !js

package main

import "flag"

type Args struct {
	URL, File           string
	Host, GameID, Token string
	Autoplay            bool
	Speed               float64 // turns per second when playing
}

// readArgs reads the command line options
func readArgs() Args {
	var args Args
	flag.StringVar(&args.URL, "url", "", "URL of the history file to view")
	flag.StringVar(&args.File, "file", "", "path to the history file to view")
	flag.StringVar(&args.Host, "host", "", "host for the live game to watch")
	flag.StringVar(&args.GameID, "id", "", "ID of the live game to watch")
	flag.StringVar(&args.Token, "token", "", "access token for the live game to watch")
	flag.BoolVar(&args.Autoplay, "autoplay", false, "start playing the game right away")
	flag.Float64Var(&args.Speed, "speed", DefaultSpeed, "turns per second when playing")
	flag.Parse()
	return args
}

// replayEnded is called when an autoplayed replay reaches its last turn
func replayEnded() {}

func printUsage() {
	flag.PrintDefaults()
}
