package main

import (
	"flag"
	"fmt"
	"runtime/debug"
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

func main() {
	port := flag.Int("p", 8000, "port on which the server will listen")
	flag.BoolVar(&DevMode, "dev", false, "run the server in development mode")
	flag.Parse()

	fmt.Println("git revision: " + GitRevision())
	RunServer(*port)
}
