//go:build js

package main

import (
	"fmt"
	"net/url"
	"strings"
	"syscall/js"
)

type Args struct {
	URL, File           string
	Host, GameID, Token string
	Autoplay            bool
}

// readArgs reads the options from the query string of the page:
// ?url=<report URL>, or ?match=<id> for a match of the ladder website, and
// optionally autoplay=1
func readArgs() Args {
	search := js.Global().Get("location").Get("search").String()
	query, _ := url.ParseQuery(strings.TrimPrefix(search, "?"))

	args := Args{URL: query.Get("url"), Autoplay: query.Get("autoplay") != ""}
	if match := query.Get("match"); match != "" {
		args.URL = "/matches/" + url.PathEscape(match) + "/report.json"
	}
	return args
}

func printUsage() {
	fmt.Println("Usage: viewer/?url=<report URL> or viewer/?match=<ladder match ID>")
	js.Global().Get("document").Get("body").Set("innerHTML", "Nothing to show: add <code>?url=</code> with the URL of a game report to the address.")
}
