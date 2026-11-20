package main

import (
	"compress/gzip"
	"context"
	"fmt"
	"log"
	"math/rand"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/hivehelsinki/hive-arena/arena"
)

type RunnerConfig struct {
	ServerImage string  // image of the arena server
	CPUPerBot   float64 // CPU cores per bot (the server mostly waits while the bots think)
	Memory      string  // memory limit per bot, in docker format (e.g. "256m")
	Pids        int     // process limit per bot
	TempDir     string  // where reports are downloaded (default: the system's)
	// Memory limit of the server container, in docker format. The server
	// keeps the whole history of the game in memory.
	ServerMemory string

	JoinTimeout  time.Duration // time for all bots to join
	MatchTimeout time.Duration // time for the whole match
	TimeBudget   time.Duration // total time each bot may take to answer, over a game
	ExitGrace    time.Duration // time for bots to exit by themselves after the end
	LogLimit     int           // maximum size of the log kept per bot, in bytes
}

func DefaultRunnerConfig() RunnerConfig {
	return RunnerConfig{
		ServerImage:  "hive-arena-server",
		CPUPerBot:    1,
		Memory:       "256m",
		Pids:         64,
		JoinTimeout:  time.Minute,
		MatchTimeout: 10 * time.Minute,
		TimeBudget:   5 * time.Minute,
		ExitGrace:    5 * time.Second,
		LogLimit:     256 * 1024,
		ServerMemory: "512m",
	}
}

// Limits of the server container, and of the output docker keeps per container
const (
	serverPids   = "256"
	dockerLogMax = "1m"
)

type Bot struct {
	Name   string `json:"name"`
	Image  string `json:"image"`
	Commit string `json:"commit"` // for the records only
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
	// Used up its time budget (see runner.timeBudget): the server stopped waiting for it
	TimedOut BotStatus = "timeout"
	// Did not join in time. A stand-in played its seat, posting no orders.
	NoJoin BotStatus = "no-join"
)

type BotOutcome struct {
	Bot
	Seat     int // -1 if unknown (failed matches)
	Status   BotStatus
	ExitCode int // -1 if the bot did not exit by itself
	Log      string
}

type MatchResult struct {
	GameID     string
	Map        string
	Started    time.Time
	Ended      time.Time
	ReportFile string // the gzipped report, in a temporary file owned by the caller
	Result     *arena.GameResult
	Bots       []BotOutcome
	Error      string // why the match failed, for the partial result of a failed match
}

// RemoveReport deletes the temporary report file, if it is still there
func (result *MatchResult) RemoveReport() {
	if result != nil && result.ReportFile != "" {
		os.Remove(result.ReportFile)
	}
}

type Runner struct {
	Config RunnerConfig
}

// A running match
type matchRun struct {
	config RunnerConfig
	match  Match
	prefix string // prefix of all docker object names

	client     *ArenaClient
	gameID     string
	adminToken string
	joinIDs    map[string]string // private game ID of each bot's seat
	started    time.Time

	mutex      sync.Mutex
	exitCodes  map[int]int    // per bot index, for bots that exited
	exitedLive map[int]bool   // per bot index, bots that exited before the end of the game
	missing    map[int]bool   // per bot index, bots that did not join
	startErr   map[int]string // per bot index, why its container could not be started
	gameFull   bool           // all seats are taken: the game has started
	forfeited  map[int]bool
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
//
// If the match fails after the bots were started, the error comes with a
// partial result: the bots and their output, without a game result.
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
		startErr:   make(map[int]string),
		forfeited:  make(map[int]bool),
	}

	matchCtx, cancel := context.WithTimeout(ctx, runner.Config.MatchTimeout)
	defer cancel()
	defer run.cleanup()

	result, err := run.play(matchCtx)
	if err != nil {
		if matchCtx.Err() == context.DeadlineExceeded && ctx.Err() == nil {
			err = fmt.Errorf("the match took more than %s: %w", runner.Config.MatchTimeout, err)
		}
		result.RemoveReport()
		if run.started.IsZero() {
			return nil, err
		}
		// Keep what can be kept: the bots' output explains many failures
		logCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		return &MatchResult{
			GameID:  run.gameID,
			Map:     match.Map,
			Started: run.started,
			Ended:   time.Now(),
			Bots:    run.outcomes(logCtx, nil),
			Error:   err.Error(),
		}, err
	}
	return result, nil
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
	if err := run.createNetwork(ctx); err != nil {
		return nil, err
	}

	// The server accepts a single game (the runner's), and keeps no files
	if _, err := docker(ctx, "run", "-d", label, "--name", run.server(),
		"--pull=never",
		"--memory", run.config.ServerMemory, "--memory-swap", run.config.ServerMemory, "--pids-limit", serverPids,
		"--log-driver", "json-file", "--log-opt", "max-size="+dockerLogMax, "--log-opt", "max-file=1",
		"-p", "127.0.0.1::8080",
		run.config.ServerImage, "server/server", "-p", "8080", "--dev", "-time-budget", run.config.TimeBudget.String(), "-max-games", "1", "-history="); err != nil {
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

	// Each bot gets the private ID of its own seat: it can only join under
	// its name, and no one else can take its seat
	var names []string
	for _, bot := range run.match.Bots {
		names = append(names, bot.Name)
	}
	game, err := run.client.NewGame(ctx, run.match.Map, names)
	if err != nil {
		return nil, fmt.Errorf("creating the game: %w", err)
	}
	if len(game.JoinIds) != len(names) {
		return nil, fmt.Errorf("creating the game: the server did not reserve the seats (server too old?)")
	}
	run.gameID = game.Id
	run.adminToken = game.AdminToken
	run.joinIDs = game.JoinIds
	run.started = time.Now()

	result := &MatchResult{GameID: game.Id, Map: run.match.Map, Started: run.started}

	// In random order, so that no bot is consistently started first
	var waiting sync.WaitGroup
	for _, i := range rand.Perm(len(run.match.Bots)) {
		if err := run.startBot(ctx, i); err != nil {
			log.Printf("%s: could not start bot %s: %s", run.prefix, run.match.Bots[i].Name, err)
			run.mutex.Lock()
			run.startErr[i] = err.Error()
			run.mutex.Unlock()
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

	status, err := run.waitForEnd(ctx)
	if err != nil {
		return nil, err
	}
	result.Ended = time.Now()
	result.Result = status.Result

	result.ReportFile, err = run.downloadReport(ctx)
	if err != nil {
		return nil, fmt.Errorf("downloading the report: %w", err)
	}

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

	result.Bots = run.outcomes(ctx, status.Result)
	return result, nil
}

// Prefix of the network interfaces of match networks. The service drops
// traffic from these interfaces to the host (see deploy/setup.sh), so that
// bots cannot reach services listening on the host through the gateway.
const BridgePrefix = "hl-"

// createMatchNetwork creates the internal network of a match
func (run *matchRun) createNetwork(ctx context.Context) error {
	bridge := BridgePrefix + strings.TrimPrefix(run.prefix, "hive-ladder-")
	_, err := docker(ctx, "network", "create", "--internal", "--label="+DockerLabel,
		"-o", "com.docker.network.bridge.name="+bridge, run.network())
	return err
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
	cpus := run.config.CPUPerBot

	_, err := docker(ctx, "run", "-d",
		"--label="+DockerLabel,
		"--name", run.botContainer(i),
		"--pull=never",
		"--network", run.network(),
		"--read-only", "--tmpfs", "/tmp",
		"--user", "65534:65534",
		"--cap-drop", "ALL", "--security-opt", "no-new-privileges",
		"--log-driver", "json-file", "--log-opt", "max-size="+dockerLogMax, "--log-opt", "max-file=1",
		"--cpus", strconv.FormatFloat(cpus, 'f', 3, 64),
		"--memory", run.config.Memory, "--memory-swap", run.config.Memory,
		"--pids-limit", strconv.Itoa(run.config.Pids),
		"-e", "ARENA_HOST=server:8080",
		"-e", "ARENA_GAME_ID="+run.joinIDs[bot.Name],
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
	run.exitCodes[i] = code
	if live {
		run.exitedLive[i] = true
	}
	forfeit := live && run.gameFull && !run.missing[i]
	run.mutex.Unlock()

	// The server should not wait for its orders anymore. Before the game
	// starts, waitForJoins does it.
	if forfeit {
		run.forfeit(ctx, i)
	}
}

// forfeit tells the server to stop waiting for the orders of a bot that joined
func (run *matchRun) forfeit(ctx context.Context, i int) {
	run.mutex.Lock()
	if run.forfeited[i] {
		run.mutex.Unlock()
		return
	}
	run.forfeited[i] = true
	run.mutex.Unlock()

	name := run.match.Bots[i].Name
	status, err := run.client.GameStatus(ctx, run.gameID)
	if err != nil || status == nil || status.GameOver {
		return
	}
	seat := slices.Index(status.Players, name)
	if seat < 0 {
		return
	}
	if err := run.client.Forfeit(ctx, run.gameID, run.adminToken, seat); err != nil {
		log.Printf("%s: could not forfeit %s: %s", run.prefix, name, err)
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
			break
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

		// The stand-in takes the bot's reserved seat. If that fails because
		// the seat is taken, the bot joined at the last moment.
		player, err := run.client.Join(ctx, run.joinIDs[bot.Name], bot.Name)
		if err != nil {
			if herr, ok := err.(*httpError); ok && strings.Contains(herr.Message, "already taken") {
				continue
			}
			return fmt.Errorf("joining a stand-in for %s: %w", bot.Name, err)
		}
		// Stopped, but kept until the end, for its output
		docker(ctx, "kill", run.botContainer(i))
		run.setMissing(i)
		log.Printf("%s: bot %s did not join, a stand-in takes its seat", run.prefix, bot.Name)
		go run.standIn(ctx, run.joinIDs[bot.Name], player.Token)
	}

	// Bots that joined, then exited before everyone had joined
	run.mutex.Lock()
	run.gameFull = true
	var exited []int
	for i := range run.exitedLive {
		if !run.missing[i] {
			exited = append(exited, i)
		}
	}
	run.mutex.Unlock()
	for _, i := range exited {
		run.forfeit(ctx, i)
	}
	return nil
}

// standIn plays a seat by posting empty orders every turn, until the game is over
func (run *matchRun) standIn(ctx context.Context, gameID string, token string) {
	posted := -1
	for ctx.Err() == nil {
		state, err := run.client.Game(ctx, gameID, token)
		if err == nil {
			if state.GameOver {
				return
			}
			if int(state.Turn) != posted {
				if run.client.PostOrders(ctx, gameID, token, state.Turn, "[]") == nil {
					posted = int(state.Turn)
				}
			}
		}
		sleep(ctx, 10*time.Millisecond)
	}
}

// waitForEnd waits for the end of the game, and returns its final status
func (run *matchRun) waitForEnd(ctx context.Context) (*arena.SessionStatus, error) {
	for {
		status, err := run.client.GameStatus(ctx, run.gameID)
		if err != nil {
			return nil, err
		}
		if status == nil {
			return nil, fmt.Errorf("game %s disappeared", run.gameID)
		}
		if status.GameOver {
			if status.Result == nil {
				return nil, fmt.Errorf("the game has no result (server too old?)")
			}
			return status, nil
		}
		if err := sleep(ctx, 200*time.Millisecond); err != nil {
			return nil, fmt.Errorf("waiting for the end of the game: %w", err)
		}
	}
}

// downloadReport saves the report of the game, compressed, in a temporary
// file, without holding it in memory: reports of long games are large
func (run *matchRun) downloadReport(ctx context.Context) (string, error) {
	file, err := os.CreateTemp(run.config.TempDir, "report-*.json.gz")
	if err != nil {
		return "", err
	}
	zip := gzip.NewWriter(file)
	err = run.client.DownloadReport(ctx, run.gameID, zip)
	if err == nil {
		err = zip.Close()
	}
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		os.Remove(file.Name())
		return "", err
	}
	return file.Name(), nil
}

// outcomes returns the outcome of each bot. The result is nil for failed
// matches: the seats are then unknown.
func (run *matchRun) outcomes(ctx context.Context, result *arena.GameResult) []BotOutcome {
	run.mutex.Lock()
	defer run.mutex.Unlock()

	var outcomes []BotOutcome
	for i, bot := range run.match.Bots {
		outcome := BotOutcome{Bot: bot, Seat: -1, Status: Finished, ExitCode: -1}

		if result != nil {
			for seat, player := range result.Players {
				if player.Name == bot.Name {
					outcome.Seat = seat
				}
			}
		}
		if code, exited := run.exitCodes[i]; exited {
			outcome.ExitCode = code
		}

		switch {
		case run.missing[i]:
			outcome.Status = NoJoin
		case outcome.Seat >= 0 && result.Players[outcome.Seat].TimedOut:
			outcome.Status = TimedOut
		case run.exitedLive[i]:
			outcome.Status = Crashed
		}

		if reason, failed := run.startErr[i]; failed {
			outcome.Log = "The container could not be started: " + reason
		} else {
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
