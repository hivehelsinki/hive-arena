package main

import (
	"cmp"
	"compress/gzip"
	"crypto/subtle"
	"encoding/json"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"
)

// The operator's page, for manual matches, is /live: the teams to
// choose from, or the manual match, live, until the operator closes it.
//
// The routes under /live ask for a password (HTTP basic authentication,
// user "admin", password web.adminPassword), and are disabled without one. The
// password travels with every request: the website must be served over
// HTTPS (by the reverse proxy). Browsers send it by themselves, including
// with requests from other sites: requests that change something are only
// accepted from the ladder's own pages (http.CrossOriginProtection).

// The user name of the operator
const adminUser = "admin"

// A cookie tells the public pages that the visitor is the operator, to show
// a link to the operator's page. It grants nothing: the operator's pages
// always ask for the password.
const adminCookie = "hive-admin"

func isAdmin(r *http.Request) bool {
	_, err := r.Cookie(adminCookie)
	return err == nil
}

// admin protects an operator's route with the password
func (web *Web) admin(handler http.HandlerFunc) http.Handler {
	protected := http.NewCrossOriginProtection().Handler(handler)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		expected := web.Config.Web.AdminPassword
		if expected == "" {
			http.Error(w, "The operator's routes are disabled: set web.adminPassword in the configuration.", http.StatusNotFound)
			return
		}
		user, password, ok := r.BasicAuth()
		// Both compared in constant time, every time
		valid := subtle.ConstantTimeCompare([]byte(user), []byte(adminUser)) & subtle.ConstantTimeCompare([]byte(password), []byte(expected))
		if !ok || valid != 1 {
			w.Header().Set("WWW-Authenticate", `Basic realm="Hive Arena ladder operator", charset="UTF-8"`)
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
		if !isAdmin(r) {
			http.SetCookie(w, &http.Cookie{Name: adminCookie, Value: "1", Path: "/", MaxAge: 30 * 24 * 3600,
				HttpOnly: true, SameSite: http.SameSiteLaxMode, Secure: r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https"})
		}
		protected.ServeHTTP(w, r)
	})
}

// A team as the operator chooses it for a manual match
type ManualChoice struct {
	Team    Team
	Version *TeamVersion // nil: no working version at that time
	Rank    int          // on the global ladder (0: not ranked)
	Rating  string
	Problem string // why its current version does not play on the ladder
}

// The time given in the form, in the ladder's time zone ("" or invalid: now)
func (web *Web) formTime(value string) (time.Time, bool) {
	loc, err := web.Config.Location()
	if err != nil {
		loc = time.Local
	}
	if t, err := time.ParseInLocation("2006-01-02T15:04", value, loc); err == nil {
		return t, true
	}
	return time.Now().In(loc), false
}

// choices returns the registered teams, best ranked first, with their
// version at a time
func (web *Web) choices(at time.Time) ([]ManualChoice, map[string]Team, error) {
	states, err := web.Store.TeamStates()
	if err != nil {
		return nil, nil, err
	}
	versions, err := web.Store.VersionsAt(at)
	if err != nil {
		return nil, nil, err
	}
	view, err := web.Store.Ladder(web.Settings.Get(), time.Now())
	if err != nil {
		return nil, nil, err
	}

	teams := make(map[string]Team)
	var choices []ManualChoice
	for _, state := range states {
		teams[state.Team.ID] = state.Team
		choice := ManualChoice{Team: state.Team, Problem: cmp.Or(state.LeftOut, state.Error)}
		if v, ok := versions[state.Team.ID]; ok {
			choice.Version = &v
		}
		for i, row := range view.Rows {
			if row.Team == state.Team.ID {
				choice.Rank = i + 1
				choice.Rating = strconv.FormatFloat(row.Global.Score, 'f', 1, 64)
			}
		}
		choices = append(choices, choice)
	}
	// Ranked teams first, by rank, then by name
	slices.SortStableFunc(choices, func(a, b ManualChoice) int {
		if (a.Rank == 0) != (b.Rank == 0) {
			if a.Rank == 0 {
				return 1
			}
			return -1
		}
		return cmp.Or(cmp.Compare(a.Rank, b.Rank), strings.Compare(strings.ToLower(a.Team.Name), strings.ToLower(b.Team.Name)))
	})
	return choices, teams, nil
}

// handleAdmin shows the manual match being played (or over, until it is
// closed), or else the teams to choose from
func (web *Web) handleAdmin(w http.ResponseWriter, r *http.Request) {
	if match, _ := web.Manual.Current(); match != nil {
		data, err := webFiles.ReadFile("web/live.html")
		if err != nil {
			serverError(w, err)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write(data)
		return
	}

	at, given := web.formTime(r.URL.Query().Get("at"))
	choices, _, err := web.choices(at)
	if err != nil {
		serverError(w, err)
		return
	}
	var maps []string
	for _, f := range web.formats() {
		if !slices.Contains(maps, f.Map) {
			maps = append(maps, f.Map)
		}
	}
	if len(maps) == 0 {
		maps = []string{"random"}
	}
	web.render(w, r, "admin", map[string]any{
		"Choices": choices, "At": at.Format("2006-01-02T15:04"), "AtGiven": given, "Maps": maps,
		"Running": web.Manual != nil, "Paused": web.Settings.Get().Paused, "Error": r.URL.Query().Get("error"),
	})
}

// handleAdminPlay starts a manual match, and shows it live
func (web *Web) handleAdminPlay(w http.ResponseWriter, r *http.Request) {
	back := func(err string) {
		query := url.Values{"error": {err}}
		if at := r.FormValue("at"); at != "" {
			query.Set("at", at)
		}
		http.Redirect(w, r, "/live?"+query.Encode(), http.StatusSeeOther)
	}
	if web.Manual == nil {
		back("Manual matches are played by the ladder: they cannot be played from the website alone (ladder web).")
		return
	}
	if err := r.ParseForm(); err != nil {
		back(err.Error())
		return
	}
	at, _ := web.formTime(r.FormValue("at"))
	choices, teams, err := web.choices(at)
	if err != nil {
		serverError(w, err)
		return
	}
	// In the order of the ladder
	var bots []ManualBot
	for _, c := range choices {
		if !slices.Contains(r.Form["team"], c.Team.ID) {
			continue
		}
		if c.Version == nil {
			back(c.Team.Name + " has no working version at that time.")
			return
		}
		bots = append(bots, ManualBot{TeamVersion: *c.Version, Name: c.Team.Name, Rank: c.Rank, Rating: c.Rating})
	}
	if len(bots) != len(r.Form["team"]) {
		back("Unknown team.")
		return
	}
	match, err := NewManualMatch(cmp.Or(r.FormValue("map"), "random"), bots, teams)
	if err == nil {
		err = web.Manual.Start(match)
	}
	if err != nil {
		back(err.Error())
		return
	}
	http.Redirect(w, r, "/live", http.StatusSeeOther)
}

func (web *Web) handleAdminAbort(w http.ResponseWriter, r *http.Request) {
	if web.Manual != nil {
		web.Manual.Abort()
	}
	http.Redirect(w, r, "/live", http.StatusSeeOther)
}

func (web *Web) handleAdminClose(w http.ResponseWriter, r *http.Request) {
	if web.Manual != nil {
		web.Manual.Close()
	}
	http.Redirect(w, r, "/live", http.StatusSeeOther)
}

// apiLive gives the manual match, for the live view (null if none)
func (web *Web) apiLive(w http.ResponseWriter, r *http.Request) {
	match, live := web.Manual.Current()
	if match != nil && live != nil {
		match.GameID = live.GameID()
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, match)
}

// apiLiveStates gives the states of the live game, from a turn on (?from=)
func (web *Web) apiLiveStates(w http.ResponseWriter, r *http.Request) {
	_, live := web.Manual.Current()
	if live == nil {
		http.NotFound(w, r)
		return
	}
	from, _ := strconv.Atoi(r.URL.Query().Get("from"))
	states := live.Since(from)
	if game := r.URL.Query().Get("game"); game != "" && game != states.GameID {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	// A viewer that opens during the game gets all the turns so far (tens
	// of kilobytes each), which compress well
	if !strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") {
		writeJSON(w, states)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Encoding", "gzip")
	w.Header().Set("Vary", "Accept-Encoding")
	zip := gzip.NewWriter(w)
	json.NewEncoder(zip).Encode(states)
	zip.Close()
}
