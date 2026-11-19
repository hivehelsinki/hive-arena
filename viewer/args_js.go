//go:build js

package main

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"syscall/js"
)

type Args struct {
	URL, File           string
	Host, GameID, Token string
	Autoplay            bool
	Speed               float64 // turns per second when playing
}

// readArgs reads the options from the query string of the page:
// ?url=<report URL>, or ?match=<id> for a match of the ladder website, and
// optionally autoplay=1 and speed=<turns per second>
func readArgs() Args {
	search := js.Global().Get("location").Get("search").String()
	query, _ := url.ParseQuery(strings.TrimPrefix(search, "?"))

	args := Args{URL: query.Get("url"), Autoplay: query.Get("autoplay") != ""}
	args.Speed, _ = strconv.ParseFloat(query.Get("speed"), 64)
	if match := query.Get("match"); match != "" {
		args.URL = "/matches/" + url.PathEscape(match) + "/report.json"
	}
	return args
}

// replayEnded tells the page that embeds the viewer (such as the ladder's big
// screen) that an autoplayed replay reached its last turn, with a
// {type: "hive-replay-ended"} message
func replayEnded() {
	parent := js.Global().Get("parent")
	if parent.IsUndefined() || parent.Equal(js.Global()) {
		return
	}
	parent.Call("postMessage", map[string]any{"type": "hive-replay-ended"}, "*")
}

func printUsage() {
	fmt.Println("Usage: viewer/?url=<report URL> or viewer/?match=<ladder match ID>")
	js.Global().Get("document").Get("body").Set("innerHTML", "Nothing to show: add <code>?url=</code> with the URL of a game report to the address.")
}
