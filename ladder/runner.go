package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"math/rand"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/hivehelsinki/hive-arena/arena"
)

type RunnerConfig struct {
	ServerImage string  // image of the arena server
	Cores       float64 // CPU cores split equally between the bots of a match
	Memory      string  // memory limit per bot, in docker format (e.g. "256m")
	Pids        int     // process limit per bot

	JoinTimeout  time.Duration // time for all bots to join
	MatchTimeout time.Duration // time for the whole match
	ExitGrace    time.Duration // time for bots to exit by themselves after the end
	LogLimit     int           // maximum size of the log kept per bot, in bytes
}

func DefaultRunnerConfig() RunnerConfig {
	return RunnerConfig{
		ServerImage:  "hive-arena-server",
		Cores:        2,
		Memory:       "256m",
		Pids:         64,
		JoinTimeout:  time.Minute,
		MatchTimeout: 30 * time.Minute,
		ExitGrace:    5 * time.Second,
		LogLimit:     256 * 1024,
	}
}

type Bot struct {
	Name  string
	Image string
}

type Match struct {
	Map  string
	Bots []Bot
}

type BotStatus string

const (
	// Played until the end of the game
	Finished BotStatus = "finished"
	// Joined, but exited before the end of the game
	Crashed BotStatus = "crashed"
	// Did not join in time. A stand-in played its seat, posting no orders.
	NoJoin BotStatus = "no-join"
)

type BotOutcome struct {
	Bot
	Seat     int
	Status   BotStatus
	ExitCode int // -1 if the bot did not exit by itself
	Log      string
}

type MatchResult struct {
	GameID  string
	Map     string
	Started time.Time
	Ended   time.Time
	Report  json.RawMessage
	Result  *arena.GameResult
	Bots    []BotOutcome
}

type Runner struct {
	Config RunnerConfig
}

// A running match
type matchRun struct {
	config RunnerConfig
	match  Match
	prefix string // prefix of all docker object names

	client *ArenaClient
	gameID string

	mutex      sync.Mutex
	exitCodes  map[int]int  // per bot index, for bots that exited
	exitedLive map[int]bool // per bot index, bots that exited before the end of the game
	missing    map[int]bool // per bot index, bots that did not join
}

func validateMatch(match Match) error {
	if !arena.IsValidNumPlayers(len(match.Bots)) {
		return fmt.Errorf("invalid number of bots: %d", len(match.Bots))
	}
	names := make(map[string]bool)
	for _, bot := range match.Bots {
		if bot.Name == "" || names[bot.Name] {
			return fmt.Errorf("bot names must be unique and not empty: %q", bot.Name)
		}
		names[bot.Name] = true
	}
	return nil
}

// Run plays one match in containers, and returns its result. All containers
// and the network are removed when it returns.
func (runner *Runner) Run(ctx context.Context, match Match) (*MatchResult, error) {
	if err := validateMatch(match); err != nil {
		return nil, err
	}

	run := &matchRun{
		config:     runner.Config,
		match:      match,
		prefix:     fmt.Sprintf("hive-ladder-%x", rand.Uint32()),
		exitCodes:  make(map[int]int),
		exitedLive: make(map[int]bool),
		missing:    make(map[int]bool),
	}

	ctx, cancel := context.WithTimeout(ctx, runner.Config.MatchTimeout)
	defer cancel()
	defer run.cleanup()

	return run.play(ctx)
}

func (run *matchRun) network() string           { return run.prefix }
func (run *matchRun) server() string            { return run.prefix + "-server" }
func (run *matchRun) botContainer(i int) string { return fmt.Sprintf("%s-bot%d", run.prefix, i) }

func (run *matchRun) cleanup() {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	containers := []string{run.server()}
	for i := range run.match.Bots {
		containers = append(containers, run.botContainer(i))
	}
	docker(ctx, append([]string{"rm", "-f"}, containers...)...)
	docker(ctx, "network", "rm", run.network())
}

func (run *matchRun) play(ctx context.Context) (*MatchResult, error) {
	label := "--label=" + DockerLabel

	// Bots only get the internal network. The server is also on the default
	// bridge network, with a port published on localhost for the runner.
	if _, err := docker(ctx, "network", "create", "--internal", label, run.network()); err != nil {
		return nil, err
	}

	if _, err := docker(ctx, "run", "-d", label, "--name", run.server(),
		"-p", "127.0.0.1::8080",
		run.config.ServerImage, "server/server", "-p", "8080", "--dev"); err != nil {
		return nil, err
	}
	if _, err := docker(ctx, "network", "connect", "--alias", "server", run.network(), run.server()); err != nil {
		return nil, err
	}

	address, err := docker(ctx, "port", run.server(), "8080/tcp")
	if err != nil {
		return nil, err
	}
	// May list several addresses (IPv4 and IPv6)
	run.client = NewArenaClient("http://" + strings.Fields(address)[0])

	if err := run.waitForServer(ctx); err != nil {
		return nil, err
	}

	game, err := run.client.NewGame(ctx, run.match.Map, len(run.match.Bots))
	if err != nil {
		return nil, fmt.Errorf("creating the game: %w", err)
	}
	run.gameID = game.Id

	result := &MatchResult{GameID: game.Id, Map: run.match.Map, Started: time.Now()}

	var waiting sync.WaitGroup
	for i := range run.match.Bots {
		if err := run.startBot(ctx, i); err != nil {
			log.Printf("%s: could not start bot %s: %s", run.prefix, run.match.Bots[i].Name, err)
			run.setMissing(i)
			continue
		}
		waiting.Add(1)
		go func() {
			defer waiting.Done()
			run.waitForBot(ctx, i)
		}()
	}

	if err := run.waitForJoins(ctx); err != nil {
		return nil, err
	}

	report, err := run.waitForReport(ctx)
	if err != nil {
		return nil, err
	}
	result.Ended = time.Now()
	result.Report = report

	var parsed arena.PersistedGame
	if err := json.Unmarshal(report, &parsed); err != nil {
		return nil, fmt.Errorf("invalid report: %w", err)
	}
	if parsed.Result == nil {
		return nil, fmt.Errorf("the report has no result (server too old?)")
	}
	result.Result = parsed.Result

	// Leave a little time for the bots to exit by themselves
	exited := make(chan struct{})
	go func() {
		waiting.Wait()
		close(exited)
	}()
	select {
	case <-exited:
	case <-time.After(run.config.ExitGrace):
	case <-ctx.Done():
	}

	result.Bots = run.outcomes(ctx, parsed.Result)
	return result, nil
}

func (run *matchRun) waitForServer(ctx context.Context) error {
	deadline := time.Now().Add(30 * time.Second)
	for {
		_, err := run.client.GameStatus(ctx, "")
		if err == nil {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("the server did not start: %w", err)
		}
		if err := sleep(ctx, 100*time.Millisecond); err != nil {
			return err
		}
	}
}

func (run *matchRun) startBot(ctx context.Context, i int) error {
	bot := run.match.Bots[i]
	cpus := run.config.Cores / float64(len(run.match.Bots))

	_, err := docker(ctx, "run", "-d",
		"--label="+DockerLabel,
		"--name", run.botContainer(i),
		"--network", run.network(),
		"--read-only", "--tmpfs", "/tmp",
		"--cap-drop", "ALL", "--security-opt", "no-new-privileges",
		"--cpus", strconv.FormatFloat(cpus, 'f', 3, 64),
		"--memory", run.config.Memory, "--memory-swap", run.config.Memory,
		"--pids-limit", strconv.Itoa(run.config.Pids),
		"-e", "ARENA_HOST=server:8080",
		"-e", "ARENA_GAME_ID="+run.gameID,
		"-e", "ARENA_NAME="+bot.Name,
		bot.Image)
	return err
}

// waitForBot waits until a bot's container exits, and records whether the
// game was still running at that point
func (run *matchRun) waitForBot(ctx context.Context, i int) {
	out, err := docker(ctx, "wait", run.botContainer(i))
	if err != nil {
		return
	}
	code, _ := strconv.Atoi(out)

	// A bot can only see the end of the game after the server has ended it,
	// so a game still running means the bot stopped too early
	status, err := run.client.GameStatus(ctx, run.gameID)
	live := err == nil && status != nil && !status.GameOver

	run.mutex.Lock()
	defer run.mutex.Unlock()
	run.exitCodes[i] = code
	if live {
		run.exitedLive[i] = true
	}
}

// anyBotMayJoin reports whether a bot that has not joined yet is still running
func (run *matchRun) anyBotMayJoin(joined []string) bool {
	run.mutex.Lock()
	defer run.mutex.Unlock()

	for i, bot := range run.match.Bots {
		_, exited := run.exitCodes[i]
		if !slices.Contains(joined, bot.Name) && !exited && !run.missing[i] {
			return true
		}
	}
	return false
}

func (run *matchRun) setMissing(i int) {
	run.mutex.Lock()
	defer run.mutex.Unlock()
	run.missing[i] = true
}

// waitForJoins waits for all bots to join. Bots that don't join in time are
// stopped, and stand-ins take their seats, so that the game can be played.
func (run *matchRun) waitForJoins(ctx context.Context) error {
	deadline := time.Now().Add(run.config.JoinTimeout)
	var joined []string

	for time.Now().Before(deadline) {
		status, err := run.client.GameStatus(ctx, run.gameID)
		if err != nil {
			return err
		}
		if status == nil {
			return fmt.Errorf("game %s disappeared", run.gameID)
		}
		joined = status.Players
		if len(joined) == len(run.match.Bots) {
			return nil
		}

		// Stop waiting once every bot has either joined, or exited or could
		// not even be started (it will not join anymore)
		if !run.anyBotMayJoin(joined) {
			break
		}

		if err := sleep(ctx, 200*time.Millisecond); err != nil {
			return err
		}
	}

	for i, bot := range run.match.Bots {
		if slices.Contains(joined, bot.Name) {
			continue
		}
		docker(ctx, "rm", "-f", run.botContainer(i))
		run.setMissing(i)
		log.Printf("%s: bot %s did not join, a stand-in takes its seat", run.prefix, bot.Name)

		player, err := run.client.Join(ctx, run.gameID, bot.Name)
		if err != nil {
			return fmt.Errorf("joining a stand-in for %s: %w", bot.Name, err)
		}
		go run.standIn(ctx, player.Token)
	}
	return nil
}

// standIn plays a seat by posting empty orders every turn, until the game is over
func (run *matchRun) standIn(ctx context.Context, token string) {
	posted := -1
	for ctx.Err() == nil {
		state, err := run.client.Game(ctx, run.gameID, token)
		if err == nil {
			if state.GameOver {
				return
			}
			if int(state.Turn) != posted {
				if run.client.PostOrders(ctx, run.gameID, token, state.Turn, "[]") == nil {
					posted = int(state.Turn)
				}
			}
		}
		sleep(ctx, 10*time.Millisecond)
	}
}

func (run *matchRun) waitForReport(ctx context.Context) ([]byte, error) {
	for {
		report, err := run.client.Report(ctx, run.gameID)
		if err != nil {
			return nil, err
		}
		if report != nil {
			return report, nil
		}
		if err := sleep(ctx, 200*time.Millisecond); err != nil {
			return nil, fmt.Errorf("waiting for the end of the game: %w", err)
		}
	}
}

func (run *matchRun) outcomes(ctx context.Context, result *arena.GameResult) []BotOutcome {
	run.mutex.Lock()
	defer run.mutex.Unlock()

	var outcomes []BotOutcome
	for i, bot := range run.match.Bots {
		outcome := BotOutcome{Bot: bot, Seat: -1, Status: Finished, ExitCode: -1}

		for seat, player := range result.Players {
			if player.Name == bot.Name {
				outcome.Seat = seat
			}
		}
		if code, exited := run.exitCodes[i]; exited {
			outcome.ExitCode = code
		}

		switch {
		case run.missing[i]:
			outcome.Status = NoJoin
		case run.exitedLive[i]:
			outcome.Status = Crashed
		}

		if !run.missing[i] {
			outcome.Log = run.botLog(ctx, i)
		}
		outcomes = append(outcomes, outcome)
	}
	return outcomes
}

// botLog returns the end of a bot's output (stdout and stderr), up to the configured limit
func (run *matchRun) botLog(ctx context.Context, i int) string {
	logs, err := dockerCombined(ctx, "logs", run.botContainer(i))
	if err != nil {
		return ""
	}
	if len(logs) > run.config.LogLimit {
		logs = "[...]\n" + logs[len(logs)-run.config.LogLimit:]
	}
	return logs
}

func sleep(ctx context.Context, d time.Duration) error {
	select {
	case <-time.After(d):
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
