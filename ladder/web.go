package main

import (
	"embed"
	"encoding/json"
	"fmt"
	"html/template"
	"io/fs"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"
)

//go:embed web
var webFiles embed.FS

// The public website: read-only views of the store
type Web struct {
	Config    *Config
	Store     *Store
	templates map[string]*template.Template
	docker    dockerCheck
}

func NewWeb(config *Config, store *Store) (*Web, error) {
	funcs := template.FuncMap{
		"short": func(commit string) string {
			if len(commit) > 8 {
				return commit[:8]
			}
			return commit
		},
		"time": func(t time.Time) string {
			loc, err := config.Location()
			if err != nil {
				loc = time.Local
			}
			return t.In(loc).Format("2006-01-02 15:04")
		},
		"timeptr": func(t *time.Time) string {
			if t == nil {
				return ""
			}
			loc, _ := config.Location()
			return t.In(loc).Format("2006-01-02 15:04")
		},
		"duration": func(a, b time.Time) string {
			return b.Sub(a).Round(time.Second).String()
		},
		"score": func(s Score) string {
			if s.Games == 0 {
				return ""
			}
			return fmt.Sprintf("%.1f", s.Mean)
		},
		"stderr": func(s Score) string {
			if s.Games < 2 {
				return ""
			}
			return fmt.Sprintf("± %.1f", s.StdErr)
		},
		"h2h": func(h HeadToHead) string {
			if h.Games == 0 {
				return ""
			}
			return fmt.Sprintf("%.0f%%", 100*h.Wins/float64(h.Games))
		},
		"h2hclass": func(h HeadToHead) string {
			if h.Games == 0 {
				return ""
			}
			switch r := h.Wins / float64(h.Games); {
			case r > 0.6:
				return "good"
			case r < 0.4:
				return "bad"
			}
			return ""
		},
		"add": func(a, b int) int { return a + b },
		"runName": func(run Run) string {
			var config struct{ Name string }
			json.Unmarshal(run.Config, &config)
			if run.Kind == "tournament" && config.Name != "" {
				return config.Name
			}
			return fmt.Sprintf("Run #%d", run.ID)
		},
	}

	pages, err := fs.Glob(webFiles, "web/*.html")
	if err != nil {
		return nil, err
	}
	web := &Web{Config: config, Store: store, templates: make(map[string]*template.Template)}
	for _, page := range pages {
		if page == "web/layout.html" {
			continue
		}
		t, err := template.New("").Funcs(funcs).ParseFS(webFiles, "web/layout.html", page)
		if err != nil {
			return nil, err
		}
		web.templates[strings.TrimSuffix(strings.TrimPrefix(page, "web/"), ".html")] = t
	}
	return web, nil
}

func (web *Web) Handler() http.Handler {
	mux := http.NewServeMux()

	static, _ := fs.Sub(webFiles, "web/static")
	mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServerFS(static)))

	if web.Config.Web.ViewerDir != "" {
		mux.Handle("GET /viewer/", http.StripPrefix("/viewer/", viewerHandler(web.Config.Web.ViewerDir)))
	}
	mux.HandleFunc("GET /{$}", web.handleLeaderboard)
	mux.HandleFunc("GET /runs", web.handleRuns)
	mux.HandleFunc("GET /runs/{id}", web.handleRun)
	mux.HandleFunc("GET /matches", web.handleMatches)
	mux.HandleFunc("GET /matches/{id}", web.handleMatch)
	mux.HandleFunc("GET /matches/{id}/report.json", web.handleReport)
	mux.HandleFunc("GET /matches/{id}/logs/{team}", web.handleMatchLog)
	mux.HandleFunc("GET /teams/{name}", web.handleTeam)
	mux.HandleFunc("GET /screen", web.handleScreen)
	mux.HandleFunc("GET /builds/{id}/log", web.handleBuildLog)

	mux.HandleFunc("GET /backup", web.handleBackup)

	mux.HandleFunc("GET /health", web.handleHealth)
	mux.HandleFunc("GET /api/status", web.handleStatus)
	mux.HandleFunc("GET /api/leaderboard", web.apiLeaderboard)
	mux.HandleFunc("GET /api/runs", web.apiRuns)
	mux.HandleFunc("GET /api/matches", web.apiMatches)
	mux.HandleFunc("GET /api/teams", web.apiTeams)

	return mux
}

// viewerHandler serves the browser build of the viewer, with the
// precompressed WebAssembly file when the client accepts it
func viewerHandler(dir string) http.Handler {
	files := http.FileServer(http.Dir(dir))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "viewer.wasm" && strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") {
			path := filepath.Join(dir, "viewer.wasm.gz")
			if _, err := os.Stat(path); err == nil {
				w.Header().Set("Content-Type", "application/wasm")
				w.Header().Set("Content-Encoding", "gzip")
				w.Header().Set("Vary", "Accept-Encoding")
				http.ServeFile(w, r, path)
				return
			}
		}
		files.ServeHTTP(w, r)
	})
}

func (web *Web) render(w http.ResponseWriter, page string, data map[string]any) {
	data["Schedule"] = web.Config.Schedule
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := web.templates[page].ExecuteTemplate(w, "layout", data); err != nil {
		log.Printf("Rendering %s: %s", page, err)
	}
}

func serverError(w http.ResponseWriter, err error) {
	log.Printf("Web: %s", err)
	http.Error(w, "Internal error", http.StatusInternalServerError)
}

func writeJSON(w http.ResponseWriter, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	json.NewEncoder(w).Encode(payload)
}

func pathID(r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	return id, err == nil
}

// Map columns of the leaderboard: the maps of the schedule, in order
func (web *Web) maps() []string {
	var maps []string
	for _, f := range web.Config.Schedule.Formats {
		if !slices.Contains(maps, f.Map) {
			maps = append(maps, f.Map)
		}
	}
	return maps
}

func (web *Web) handleLeaderboard(w http.ResponseWriter, r *http.Request) {
	board, runs, err := web.Store.Leaderboard(web.Config.Schedule.Window)
	if err != nil {
		serverError(w, err)
		return
	}
	var teams []string
	for _, s := range board.Standings {
		teams = append(teams, s.Team)
	}
	loc, _ := web.Config.Location()
	web.render(w, "leaderboard", map[string]any{
		"Board":   board,
		"Teams":   teams,
		"Runs":    runs,
		"Maps":    web.maps(),
		"NextRun": web.Config.Schedule.NextRunTime(time.Now(), loc),
	})
}

func (web *Web) handleRuns(w http.ResponseWriter, r *http.Request) {
	runs, err := web.Store.Runs("", 100)
	if err != nil {
		serverError(w, err)
		return
	}
	web.render(w, "runs", map[string]any{"Runs": runs})
}

func (web *Web) handleRun(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	run, err := web.Store.Run(id)
	if !ok || run == nil || err != nil {
		http.NotFound(w, r)
		return
	}
	results, err := web.Store.Results([]int64{id})
	if err != nil {
		serverError(w, err)
		return
	}
	matches, err := web.Store.Matches(MatchFilter{RunID: id})
	if err != nil {
		serverError(w, err)
		return
	}
	board := NewLeaderboard(results)
	var teams []string
	for _, s := range board.Standings {
		teams = append(teams, s.Team)
	}
	web.render(w, "run", map[string]any{"Run": run, "Board": board, "Teams": teams, "Maps": web.maps(), "Matches": matches})
}

func matchFilter(r *http.Request) MatchFilter {
	q := r.URL.Query()
	before, _ := strconv.ParseInt(q.Get("before"), 10, 64)
	run, _ := strconv.ParseInt(q.Get("run"), 10, 64)
	limit, err := strconv.Atoi(q.Get("limit"))
	if err != nil || limit < 1 || limit > 200 {
		limit = 50
	}
	return MatchFilter{Team: q.Get("team"), Map: q.Get("map"), RunID: run, Before: before, Limit: limit}
}

func (web *Web) handleMatches(w http.ResponseWriter, r *http.Request) {
	filter := matchFilter(r)
	matches, err := web.Store.Matches(filter)
	if err != nil {
		serverError(w, err)
		return
	}
	var next string
	if len(matches) == filter.Limit {
		q := r.URL.Query()
		q.Set("before", fmt.Sprint(matches[len(matches)-1].ID))
		next = "/matches?" + q.Encode()
	}
	web.render(w, "matches", map[string]any{"Matches": matches, "Filter": filter, "Next": next})
}

func (web *Web) handleMatch(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	match, err := web.Store.Match(id)
	if !ok || match == nil || err != nil {
		http.NotFound(w, r)
		return
	}
	logs := make(map[string]bool)
	for _, bot := range match.Bots {
		if _, err := os.Stat(web.Store.LogPath(id, bot.Team)); err == nil {
			logs[bot.Team] = true
		}
	}
	web.render(w, "match", map[string]any{"Match": match, "Logs": logs})
}

// serveGzip serves a gzipped file, compressed if the client accepts it
func serveGzip(w http.ResponseWriter, r *http.Request, path string, contentType string, download string) {
	if _, err := os.Stat(path); err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Access-Control-Allow-Origin", "*")
	if download != "" {
		w.Header().Set("Content-Disposition", `attachment; filename="`+download+`"`)
	}

	if strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") {
		w.Header().Set("Content-Encoding", "gzip")
		w.Header().Set("Vary", "Accept-Encoding")
		http.ServeFile(w, r, path)
		return
	}
	data, err := readGzip(path)
	if err != nil {
		serverError(w, err)
		return
	}
	w.Write(data)
}

func (web *Web) handleReport(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		http.NotFound(w, r)
		return
	}
	download := ""
	if r.URL.Query().Has("download") {
		download = fmt.Sprintf("match-%d.json", id)
	}
	serveGzip(w, r, web.Store.ReportPath(id), "application/json", download)
}

func (web *Web) handleMatchLog(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		http.NotFound(w, r)
		return
	}
	serveGzip(w, r, web.Store.LogPath(id, r.PathValue("team")), "text/plain; charset=utf-8", "")
}

func (web *Web) handleBuildLog(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		http.NotFound(w, r)
		return
	}
	serveGzip(w, r, web.Store.BuildLogPath(id), "text/plain; charset=utf-8", "")
}

func (web *Web) handleTeam(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")

	states, err := web.Store.TeamStates()
	if err != nil {
		serverError(w, err)
		return
	}
	var state *TeamState
	for i := range states {
		if states[i].Team.Name == name {
			state = &states[i]
		}
	}

	builds, err := web.Store.Builds(name, 20)
	if err != nil {
		serverError(w, err)
		return
	}
	matches, err := web.Store.Matches(MatchFilter{Team: name, Limit: 30})
	if err != nil {
		serverError(w, err)
		return
	}
	board, _, err := web.Store.Leaderboard(web.Config.Schedule.Window)
	if err != nil {
		serverError(w, err)
		return
	}
	var standing *Standing
	for i := range board.Standings {
		if board.Standings[i].Team == name {
			standing = &board.Standings[i]
		}
	}

	if state == nil && len(builds) == 0 && len(matches) == 0 {
		http.NotFound(w, r)
		return
	}
	web.render(w, "team", map[string]any{
		"Name": name, "State": state, "Builds": builds, "Matches": matches,
		"Standing": standing, "Maps": web.maps(), "H2H": board.HeadToHead[name],
	})
}

func (web *Web) apiLeaderboard(w http.ResponseWriter, r *http.Request) {
	board, runs, err := web.Store.Leaderboard(web.Config.Schedule.Window)
	if err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, map[string]any{"leaderboard": board, "runs": runs, "schedule": web.Config.Schedule})
}

func (web *Web) apiRuns(w http.ResponseWriter, r *http.Request) {
	runs, err := web.Store.Runs("ladder", 100)
	if err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, runs)
}

func (web *Web) apiMatches(w http.ResponseWriter, r *http.Request) {
	matches, err := web.Store.Matches(matchFilter(r))
	if err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, matches)
}

func (web *Web) apiTeams(w http.ResponseWriter, r *http.Request) {
	states, err := web.Store.TeamStates()
	if err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, states)
}

// handleScreen is a full-screen page for a big screen: the leaderboard, and
// replays of the latest matches, one after the other
func (web *Web) handleScreen(w http.ResponseWriter, r *http.Request) {
	data, err := webFiles.ReadFile("web/screen.html")
	if err != nil {
		serverError(w, err)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write(data)
}
