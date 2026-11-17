//go:build !js

package main

import "flag"

type Args struct {
	URL, File           string
	Host, GameID, Token string
}

// readArgs reads the command line options
func readArgs() Args {
	var args Args
	flag.StringVar(&args.URL, "url", "", "URL of the history file to view")
	flag.StringVar(&args.File, "file", "", "path to the history file to view")
	flag.StringVar(&args.Host, "host", "", "host for the live game to watch")
	flag.StringVar(&args.GameID, "id", "", "ID of the live game to watch")
	flag.StringVar(&args.Token, "token", "", "access token for the live game to watch")
	flag.Parse()
	return args
}

func printUsage() {
	flag.PrintDefaults()
}
