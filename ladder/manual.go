package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"math/rand"
	"slices"
	"strings"
	"sync"
	"time"
)

// A manual match is chosen by the operator (e.g. for the finals): any teams,
// with their latest version or the one they had at a given time. There is at
// most one at a time. It is played as soon as the matchmaker is free, even
// when the ladder is paused (a ladder match being played is interrupted, and
// not recorded), in real time, and watched live. It is recorded as
// KindManual.

// Statuses of a manual match
const (
	ManualStarting  = "starting"  // waiting for the matchmaker
	ManualPreparing = "preparing" // building missing images
	ManualPlaying   = "playing"
	ManualFinished  = "finished"
	ManualFailed    = "failed"
	ManualAborted   = "aborted"
)

// The numbers of players of manual matches
var ManualPlayers = []int{2, 3, 4, 6}

// A bot of a manual match
type ManualBot struct {
	TeamVersion
	Name string `json:"name"` // the team's name
	// On the global ladder when the match was started (0: not ranked)
	Rank   int    `json:"rank,omitempty"`
	Rating string `json:"rating,omitempty"`
}

type ManualMatch struct {
	Map     string      `json:"map"`
	Bots    []ManualBot `json:"bots"` // in the order of the ladder
	Started time.Time   `json:"started"`
	Status  string      `json:"status"`
	Error   string      `json:"error,omitempty"`
	GameID  string      `json:"gameId,omitempty"`  // once the game started
	MatchID int64       `json:"matchId,omitempty"` // once recorded

	teams  map[string]Team // by identifier, to build missing images
	live   *LiveGame
	cancel context.CancelFunc
}

// Over tells whether the match ended (or never started)
func (m *ManualMatch) Over() bool {
	return m.Status == ManualFinished || m.Status == ManualFailed || m.Status == ManualAborted
}

// Teams returns the names of the teams, for people
func (m *ManualMatch) Teams() string {
	var names []string
	for _, bot := range m.Bots {
		names = append(names, bot.Name)
	}
	return strings.Join(names, ", ")
}

// NewManualMatch prepares a manual match between versions of teams
func NewManualMatch(mapName string, bots []ManualBot, teams map[string]Team) (*ManualMatch, error) {
	if !slices.Contains(ManualPlayers, len(bots)) {
		return nil, fmt.Errorf("choose 2, 3, 4 or 6 teams (not %d)", len(bots))
	}
	seen := make(map[string]bool)
	for _, bot := range bots {
		if seen[bot.Team] {
			return nil, fmt.Errorf("team %s is chosen twice", bot.Team)
		}
		seen[bot.Team] = true
	}
	match := &ManualMatch{Map: mapName, Bots: bots, Started: time.Now(), Status: ManualStarting, teams: teams}
	if err := validateMatch(match.match()); err != nil {
		return nil, err
	}
	return match, nil
}

// match is what the runner plays, with random seats
func (m *ManualMatch) match() Match {
	match := Match{Map: m.Map, RealTime: true, Live: m.live}
	for _, bot := range m.Bots {
		match.Bots = append(match.Bots, Bot{Name: bot.Team, Image: bot.Image, Commit: bot.Commit})
	}
	rand.Shuffle(len(match.Bots), func(i, j int) { match.Bots[i], match.Bots[j] = match.Bots[j], match.Bots[i] })
	return match
}

// The manual match, shared by the operator's page, which starts it, and the
// matchmaker, which plays it
type ManualGame struct {
	mutex   sync.Mutex
	current *ManualMatch // nil: none (or closed)
	wake    chan struct{}
	// Interrupts the ladder match being played, if any
	interrupt context.CancelFunc
}

func NewManualGame() *ManualGame {
	return &ManualGame{wake: make(chan struct{}, 1)}
}

// Start starts a manual match, unless one is not over, and interrupts the
// ladder match being played
func (g *ManualGame) Start(m *ManualMatch) error {
	g.mutex.Lock()
	if g.current != nil && !g.current.Over() {
		g.mutex.Unlock()
		return fmt.Errorf("a manual match is already being played")
	}
	g.current = m
	if g.interrupt != nil {
		g.interrupt()
	}
	g.mutex.Unlock()

	select {
	case g.wake <- struct{}{}:
	default:
	}
	return nil
}

// Abort stops the manual match (it is not recorded). Returns false if there
// is none, or it is over.
func (g *ManualGame) Abort() bool {
	g.mutex.Lock()
	defer g.mutex.Unlock()
	m := g.current
	if m == nil || m.Over() {
		return false
	}
	if m.cancel != nil {
		m.cancel()
	} else {
		// Not picked up by the matchmaker yet
		m.Status = ManualAborted
	}
	return true
}

// Close forgets the manual match once it is over. Returns false if it is not.
func (g *ManualGame) Close() bool {
	g.mutex.Lock()
	defer g.mutex.Unlock()
	if g.current == nil || !g.current.Over() {
		return false
	}
	g.current = nil
	return true
}

// Current returns a copy of the manual match (nil if none), and its live game
func (g *ManualGame) Current() (*ManualMatch, *LiveGame) {
	if g == nil {
		return nil, nil
	}
	g.mutex.Lock()
	defer g.mutex.Unlock()
	if g.current == nil {
		return nil, nil
	}
	copy := *g.current
	return &copy, g.current.live
}

// Wake returns a channel that receives a value when a manual match is
// started (nil for a nil game: never)
func (g *ManualGame) Wake() <-chan struct{} {
	if g == nil {
		return nil
	}
	return g.wake
}

// next returns the manual match to play, marked as preparing (nil if none)
func (g *ManualGame) next(cancel context.CancelFunc) *ManualMatch {
	if g == nil {
		return nil
	}
	g.mutex.Lock()
	defer g.mutex.Unlock()
	if g.current == nil || g.current.Status != ManualStarting {
		return nil
	}
	g.current.Status = ManualPreparing
	g.current.cancel = cancel
	return g.current
}

// update changes the manual match, under the lock
func (g *ManualGame) update(change func()) {
	g.mutex.Lock()
	defer g.mutex.Unlock()
	change()
}

// setInterrupt sets (or clears, with nil) what interrupts the ladder match
// being played. With a manual match waiting, it interrupts right away.
func (g *ManualGame) setInterrupt(interrupt context.CancelFunc) {
	if g == nil {
		return
	}
	g.mutex.Lock()
	defer g.mutex.Unlock()
	g.interrupt = interrupt
	if interrupt != nil && g.current != nil && g.current.Status == ManualStarting {
		interrupt()
	}
}

// playManual plays the manual match, if one is waiting, and records it.
// Returns false if there was none.
func (l *Ladder) playManual(ctx context.Context, info MatchInfo) bool {
	matchCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	m := l.Manual.next(cancel)
	if m == nil {
		return false
	}
	end := func(status string, err error) {
		if err != nil {
			log.Printf("Manual match: %s: %s", status, err)
		} else {
			log.Printf("Manual match: %s", status)
		}
		l.Manual.update(func() {
			m.Status = status
			if err != nil {
				m.Error = err.Error()
			}
		})
	}

	// Older versions may have lost their image
	SetActivity("preparing a manual match: " + m.Teams())
	log.Printf("Manual match: %s, %s", Format{m.Map, len(m.Bots)}, m.Teams())
	if l.EnsureImage != nil {
		for _, bot := range m.Bots {
			if err := l.EnsureImage(matchCtx, m.teams[bot.Team], bot.TeamVersion); err != nil {
				if matchCtx.Err() != nil {
					end(ManualAborted, nil)
				} else {
					end(ManualFailed, fmt.Errorf("the image of %s (%s) is missing and could not be built: %w", bot.Name, bot.Tag, err))
				}
				return true
			}
		}
	}

	names := make(map[string]string)
	for _, bot := range m.Bots {
		names[bot.Team] = bot.Name
	}
	live := NewLiveGame(names)
	l.Manual.update(func() {
		m.Status = ManualPlaying
		m.live = live
	})
	SetActivity("playing a manual match: " + m.Teams())

	result, err := l.playMatch(matchCtx, m.match())
	if ctx.Err() != nil {
		// Stopping: the match is lost
		result.RemoveReport()
		end(ManualAborted, fmt.Errorf("the ladder stopped"))
		return true
	}
	if matchCtx.Err() != nil {
		result.RemoveReport()
		end(ManualAborted, nil)
		return true
	}
	if result != nil {
		info.Kind = KindManual
		id, rerr := l.Store.RecordMatch(info, result)
		result.RemoveReport()
		if rerr != nil {
			end(ManualFailed, fmt.Errorf("recording the match: %w", rerr))
			return true
		}
		l.Manual.update(func() { m.MatchID = id })
		log.Printf("Manual match recorded as #%d", id)
	}
	if err != nil {
		end(ManualFailed, err)
		return true
	}
	end(ManualFinished, nil)
	return true
}

// A game being played, as live viewers see it: its full state at every turn
// so far, kept in memory
type LiveGame struct {
	mutex   sync.Mutex
	names   map[string]string // the teams' names, by identifier
	gameID  string
	mapName string
	created time.Time
	players []string // by seat (identifiers)
	states  []json.RawMessage
	turn    int // of the last state, -1 if none
	over    bool
}

func NewLiveGame(names map[string]string) *LiveGame {
	return &LiveGame{names: names, turn: -1}
}

// GameID returns the ID of the game, once it started
func (g *LiveGame) GameID() string {
	g.mutex.Lock()
	defer g.mutex.Unlock()
	return g.gameID
}

// Start sets what is known once all the bots have joined
func (g *LiveGame) Start(gameID string, mapName string, created time.Time, players []string) {
	g.mutex.Lock()
	defer g.mutex.Unlock()
	g.gameID, g.mapName, g.created, g.players = gameID, mapName, created, players
}

// Add records the state of a new turn (as the server sends it)
func (g *LiveGame) Add(state []byte) {
	var header struct {
		Turn int `json:"turn"`
	}
	if err := json.Unmarshal(state, &header); err != nil {
		return
	}
	g.mutex.Lock()
	defer g.mutex.Unlock()
	if header.Turn > g.turn {
		g.states = append(g.states, state)
		g.turn = header.Turn
	}
}

// End marks the end of the game (or of following it)
func (g *LiveGame) End() {
	g.mutex.Lock()
	defer g.mutex.Unlock()
	g.over = true
}

// What a live viewer gets: the states from a turn on
type LiveStates struct {
	GameID      string            `json:"gameId"`
	Map         string            `json:"map"`
	CreatedDate time.Time         `json:"createdDate"`
	Players     []string          `json:"players"` // the teams' names, by seat
	From        int               `json:"from"`    // index of the first state
	States      []json.RawMessage `json:"states"`
	Over        bool              `json:"over"` // no more states will come
}

// Since returns the states from the given index on
func (g *LiveGame) Since(from int) LiveStates {
	g.mutex.Lock()
	defer g.mutex.Unlock()
	from = max(0, min(from, len(g.states)))
	live := LiveStates{GameID: g.gameID, Map: g.mapName, CreatedDate: g.created, From: from,
		States: slices.Clone(g.states[from:]), Over: g.over}
	for _, player := range g.players {
		live.Players = append(live.Players, teamName(g.names, player))
	}
	if live.States == nil {
		live.States = []json.RawMessage{}
	}
	return live
}
