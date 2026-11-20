package main

import (
	"compress/gzip"
	"embed"
	"encoding/json"
	"fmt"
	"github.com/hivehelsinki/hive-arena/docs"
	"html/template"
	"io"
	"io/fs"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

//go:embed web
var webFiles embed.FS

// The public website: read-only views of the store
type Web struct {
	Config *Config
	// The ladder settings, shared with the matchmaker when it runs
	Settings  *LiveSettings
	Store     *Store
	templates map[string]*template.Template
	docker    dockerCheck
}

func NewWeb(config *Config, store *Store) (*Web, error) {
	loc, err := config.Location()
	if err != nil {
		loc = time.Local
	}
	funcs := template.FuncMap{
		"revision": func(revision string) string {
			base, dirty := strings.CutSuffix(revision, "-dirty")
			if len(base) > 8 {
				base = base[:8]
			}
			if dirty {
				base += " (modified)"
			}
			return base
		},
		"short": func(commit string) string {
			if len(commit) > 8 {
				return commit[:8]
			}
			return commit
		},
		"time": func(t time.Time) string {
			return t.In(loc).Format("2006-01-02 15:04")
		},
		"timeptr": func(t *time.Time) string {
			if t == nil {
				return ""
			}
			return t.In(loc).Format("2006-01-02 15:04")
		},
		"duration": func(a, b time.Time) string {
			return b.Sub(a).Round(time.Second).String()
		},
		// 1st, 2nd... (0: no place, in a void match)
		"place": func(place int) string {
			if place <= 0 {
				return ""
			}
			suffix := "th"
			if place%100 < 11 || place%100 > 13 {
				switch place % 10 {
				case 1:
					suffix = "st"
				case 2:
					suffix = "nd"
				case 3:
					suffix = "rd"
				}
			}
			return fmt.Sprintf("%d%s", place, suffix)
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
		"rating": func(t TeamRating) string {
			if t.Games == 0 {
				return ""
			}
			return fmt.Sprintf("%.1f", t.Rating.Ordinal())
		},
		"musigma": func(t TeamRating) string {
			if t.Games == 0 {
				return ""
			}
			return fmt.Sprintf("%.1f ± %.1f", t.Rating.Mu, t.Rating.Sigma)
		},
		"days": func(d Duration) string {
			if days := d.Hours() / 24; days == float64(int(days)) {
				if days == 1 {
					return "day"
				}
				return fmt.Sprintf("%d days", int(days))
			}
			return d.String()
		},
		"rules":      func() template.HTML { return renderMarkdown(docs.Rules, 1) },
		"percent":    func(f float64) string { return fmt.Sprintf("%.0f%%", 100*f) },
		"add":        func(a, b int) int { return a + b },
		"trimPrefix": strings.TrimPrefix,
	}

	pages, err := fs.Glob(webFiles, "web/*.html")
	if err != nil {
		return nil, err
	}
	web := &Web{Config: config, Settings: NewLiveSettings("", config.Ladder), Store: store, templates: make(map[string]*template.Template)}
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
	mux.HandleFunc("GET /matches", web.handleMatches)
	mux.HandleFunc("GET /matches/{id}", web.handleMatch)
	mux.HandleFunc("GET /matches/{id}/report.json", web.handleReport)
	mux.HandleFunc("GET /matches/{id}/logs/{team}", web.handleMatchLog)
	mux.HandleFunc("GET /teams/{name}", web.handleTeam)
	mux.HandleFunc("GET /specs", web.handleSpecs)
	mux.HandleFunc("GET /screen", web.handleScreen)
	mux.HandleFunc("GET /builds/{id}/log", web.handleBuildLog)

	mux.HandleFunc("GET /backup", web.handleBackup)

	mux.HandleFunc("GET /health", web.handleHealth)
	mux.HandleFunc("GET /api/status", web.handleStatus)
	mux.HandleFunc("GET /api/leaderboard", web.apiLeaderboard)
	mux.HandleFunc("GET /api/matches", web.apiMatches)
	mux.HandleFunc("GET /api/matches/{id}", web.apiMatch)
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
	data["Settings"] = web.Settings.Get()
	data["Revision"] = gitRevision()
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

// formats returns the formats of the ladder, for the per-format scores
func (web *Web) formats() []Format {
	return distinctFormats(web.Settings.Get().Formats)
}

func (web *Web) handleLeaderboard(w http.ResponseWriter, r *http.Request) {
	view, err := web.Store.Ladder(web.Settings.Get(), time.Now())
	if err != nil {
		serverError(w, err)
		return
	}
	played, err := web.Store.PlayedSince(time.Now().Add(-24 * time.Hour))
	if err != nil {
		serverError(w, err)
		return
	}
	web.render(w, "leaderboard", map[string]any{
		"Ladder":     view,
		"HeadToHead": view.HeadToHead,
		"Teams":      view.Teams,
		"Played":     played,
		"Activity":   Activity(),
	})
}

func matchFilter(r *http.Request) MatchFilter {
	q := r.URL.Query()
	before, _ := strconv.ParseInt(q.Get("before"), 10, 64)
	limit, err := strconv.Atoi(q.Get("limit"))
	if err != nil || limit < 1 || limit > 200 {
		limit = 50
	}
	players, _ := strconv.Atoi(q.Get("players"))
	filter := MatchFilter{Team: q.Get("team"), Players: players, Before: before, Limit: limit}
	if q.Has("after") {
		after, _ := strconv.ParseInt(q.Get("after"), 10, 64)
		filter.After = &after
	}
	return filter
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
	web.render(w, "matches", map[string]any{"Matches": matches, "Filter": filter, "Next": next, "Formats": web.formats()})
}

func (web *Web) handleMatch(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	match, err := web.Store.Match(id)
	if !ok || match == nil || err != nil {
		http.NotFound(w, r)
		return
	}
	// Files missing without being deleted by the ladder (e.g. after restoring a backup)
	if !match.FilesDeleted && match.Turns > 0 {
		if _, err := os.Stat(web.Store.ReportPath(id)); os.IsNotExist(err) {
			match.FilesDeleted = true
		}
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
	w.Header().Set("X-Content-Type-Options", "nosniff")
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
	// Decompressed as it is sent: reports can be large
	file, err := os.Open(path)
	if err != nil {
		serverError(w, err)
		return
	}
	defer file.Close()
	zip, err := gzip.NewReader(file)
	if err != nil {
		serverError(w, err)
		return
	}
	io.Copy(w, zip)
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
	settings := web.Settings.Get()
	view, err := web.Store.Ladder(settings, time.Now())
	if err != nil {
		serverError(w, err)
		return
	}
	commits := make(map[string]string)
	if state != nil && state.Bot != nil {
		commits[name] = state.Bot.Commit
	}
	counts, err := web.Store.MatchCounts(time.Now().Add(-24*time.Hour), commits)
	if err != nil {
		serverError(w, err)
		return
	}

	if state == nil && len(builds) == 0 && len(matches) == 0 {
		http.NotFound(w, r)
		return
	}
	web.render(w, "team", map[string]any{
		"Name": name, "State": state, "Builds": builds, "Matches": matches,
		"Row": view.Row(name), "History": view.TeamHistory(name), "Formats": view.Formats, "H2H": view.HeadToHead[name],
		"HeadToHeadPeriod": view.HeadToHeadPeriod, "Counts": counts[name], "Quota": settings.Quota,
	})
}

func (web *Web) apiLeaderboard(w http.ResponseWriter, r *http.Request) {
	view, err := web.Store.Ladder(web.Settings.Get(), time.Now())
	if err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, view)
}

func (web *Web) apiMatches(w http.ResponseWriter, r *http.Request) {
	matches, err := web.Store.Matches(matchFilter(r))
	if err != nil {
		serverError(w, err)
		return
	}
	if matches == nil {
		matches = []MatchSummary{} // [], not null
	}
	writeJSON(w, matches)
}

func (web *Web) apiMatch(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	match, err := web.Store.Match(id)
	if err != nil {
		serverError(w, err)
		return
	}
	if !ok || match == nil {
		http.NotFound(w, r)
		return
	}
	writeJSON(w, match)
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

func (web *Web) handleSpecs(w http.ResponseWriter, r *http.Request) {
	web.render(w, "specs", map[string]any{"Config": web.Config})
}
