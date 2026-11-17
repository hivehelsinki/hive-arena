package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"math/rand"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"
)

func usage() {
	fmt.Fprintln(os.Stderr, `Usage:
  ladder [-config ladder.json]
      Run the ladder: runs at the scheduled times, and the website.
      Send SIGUSR1 to the process to start a run now.

Other commands, for debugging and testing. Except leaderboard and web,
they play matches or build images: do not use them while the ladder runs.

  ladder version
      Print the git commit the ladder was built from
  ladder check [-config ladder.json]
      Check the configuration, and print the address of the website and the
      data directory
  ladder web [-config ladder.json]
      Run the website only
  ladder leaderboard [-config ladder.json]
      Print the current leaderboard
  ladder run [-config ladder.json]
      Play one ladder run now, and exit
  ladder tournament [-config ladder.json] -formats <map:players,...> [-repeats N] [-teams a,b,...] [-name NAME]
      Play a one-off round-robin now with its own formats, among the
      current bots (all of them, or the given teams). It is shown with the
      runs on the website, and does not count for the leaderboard.
  ladder build [-config ladder.json]
      Fetch the registered teams, build and smoke-test their new versions
  ladder rebuild [-config ladder.json] <team>
      Forget the failed build of a team's current version, so it is built again
  ladder match [-server-image IMAGE] [-cores N] [-report FILE] [-logs DIR] <map> <name>=<image>...
      Play one match in containers with the given images, and print its
      result as JSON (nothing is recorded)`)
	os.Exit(2)
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	// Without a command, run the ladder
	if len(os.Args) < 2 || strings.HasPrefix(os.Args[1], "-") && os.Args[1] != "-h" && os.Args[1] != "-help" && os.Args[1] != "--help" {
		cmdServe(ctx, os.Args[1:])
		return
	}

	switch os.Args[1] {
	case "version":
		fmt.Println(gitRevision())
	case "check":
		cmdCheck(os.Args[2:])
	case "web":
		cmdWeb(ctx, os.Args[2:])
	case "leaderboard":
		cmdLeaderboard(os.Args[2:])
	case "run":
		cmdRun(ctx, os.Args[2:])
	case "tournament":
		cmdTournament(ctx, os.Args[2:])
	case "build":
		cmdBuild(ctx, os.Args[2:])
	case "rebuild":
		cmdRebuild(os.Args[2:])
	case "match":
		cmdMatch(ctx, os.Args[2:])
	default:
		usage()
	}
}

func cmdMatch(ctx context.Context, args []string) {
	config := DefaultRunnerConfig()

	flags := flag.NewFlagSet("match", flag.ExitOnError)
	flags.StringVar(&config.ServerImage, "server-image", config.ServerImage, "docker image of the arena server")
	flags.Float64Var(&config.Cores, "cores", config.Cores, "CPU cores split between the bots")
	report := flags.String("report", "", "file to save the game report in")
	logs := flags.String("logs", "", "directory to save the bot logs in")
	flags.Parse(args)

	if flags.NArg() < 2 {
		usage()
	}

	match := Match{Map: flags.Arg(0)}
	for _, arg := range flags.Args()[1:] {
		name, image, ok := strings.Cut(arg, "=")
		if !ok {
			usage()
		}
		match.Bots = append(match.Bots, Bot{Name: name, Image: image})
	}

	runner := Runner{Config: config}
	result, err := runner.Run(ctx, match)
	if result == nil {
		log.Fatal(err)
	}
	defer result.RemoveReport()

	if *report != "" && result.ReportFile != "" {
		data, err := readGzip(result.ReportFile)
		if err == nil {
			err = os.WriteFile(*report, data, 0o644)
		}
		if err != nil {
			log.Fatal(err)
		}
	}

	if *logs != "" {
		for _, bot := range result.Bots {
			path := filepath.Join(*logs, bot.Name+".log")
			if err := os.WriteFile(path, []byte(bot.Log), 0o644); err != nil {
				log.Fatal(err)
			}
		}
	}

	result.ReportFile = ""
	for i := range result.Bots {
		result.Bots[i].Log = fmt.Sprintf("(%d bytes)", len(result.Bots[i].Log))
	}
	out, _ := json.MarshalIndent(result, "", "  ")
	fmt.Println(string(out))
	if err != nil {
		result.RemoveReport()
		log.Fatal(err)
	}
}

// openLadder parses the common flags, loads the configuration and opens the store
func openLadder(name string, args []string) (*Ladder, *flag.FlagSet) {
	return openLadderWithFlags(flag.NewFlagSet(name, flag.ExitOnError), args)
}

func openLadderWithFlags(flags *flag.FlagSet, args []string) (*Ladder, *flag.FlagSet) {
	path := flags.String("config", "ladder.json", "configuration file")
	flags.Parse(args)

	log.Printf("Hive Arena ladder, git revision %s", gitRevision())

	config, err := LoadConfig(*path)
	if err != nil {
		log.Fatal(err)
	}
	store, err := OpenStore(config.DataDir)
	if err != nil {
		log.Fatal(err)
	}
	LogDiskWarning(config)

	runnerConfig := config.RunnerConfig()
	runnerConfig.TempDir = store.TempDir()
	smoke := runnerConfig
	smoke.MatchTimeout = 5 * time.Minute
	builder := &Builder{
		Store:        store,
		DataDir:      config.DataDir,
		Smoke:        &Runner{Config: smoke},
		SmokeMap:     config.Build.SmokeMap,
		BuildTimeout: config.Build.Timeout.Duration,
		KeepImages:   config.Build.KeepImages,
		MaxImageSize: config.Build.MaxImageMB << 20,
		// This machine is dedicated to the ladder
		IsolateCaches: true,
		CacheLimit:    config.Build.CacheLimit,
	}

	ladder := &Ladder{
		Config: config,
		Store:  store,
		Player: &Runner{Config: runnerConfig},
		Rand:   rand.New(rand.NewSource(time.Now().UnixNano())),
	}
	pool := &PoolResolver{
		Registry:    config.Registry,
		DataDir:     config.DataDir,
		MaxFailures: config.Schedule.MaxFailures,
		Store:       store,
		Builder:     builder,
	}
	ladder.Pool = pool.Resolve
	return ladder, flags
}

func cmdServe(ctx context.Context, args []string) {
	ladder, _ := openLadder("ladder", args)
	defer ladder.Store.Close()

	if err := RemoveLeftovers(ctx); err != nil {
		log.Fatal(err)
	}

	trigger := make(chan struct{}, 1)
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGUSR1)
	go func() {
		for range signals {
			select {
			case trigger <- struct{}{}:
			default:
			}
		}
	}()

	go serveWeb(ladder)

	if err := ladder.Serve(ctx, trigger); err != nil {
		log.Fatal(err)
	}
}

func serveWeb(ladder *Ladder) {
	web, err := NewWeb(ladder.Config, ladder.Store)
	if err != nil {
		log.Fatal(err)
	}
	log.Printf("Website on %s", ladder.Config.Web.Listen)
	// No write timeout: backups and reports can take a while to download
	server := &http.Server{
		Addr:              ladder.Config.Web.Listen,
		Handler:           web.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		IdleTimeout:       2 * time.Minute,
	}
	log.Fatal(server.ListenAndServe())
}

func cmdWeb(ctx context.Context, args []string) {
	ladder, _ := openLadder("web", args)
	defer ladder.Store.Close()
	go serveWeb(ladder)
	<-ctx.Done()
}

func cmdRun(ctx context.Context, args []string) {
	ladder, _ := openLadder("run", args)
	defer ladder.Store.Close()

	if err := RemoveLeftovers(ctx); err != nil {
		log.Fatal(err)
	}
	loc, _ := ladder.Config.Location()
	deadline := ladder.Config.Schedule.NextRunTime(time.Now(), loc).Add(-ladder.Config.DeadlineMargin.Duration)

	if _, err := ladder.RunOnce(ctx, deadline); err != nil {
		log.Fatal(err)
	}
	printLeaderboard(ladder)
}

func cmdLeaderboard(args []string) {
	ladder, _ := openLadder("leaderboard", args)
	defer ladder.Store.Close()
	printLeaderboard(ladder)
}

func printLeaderboard(ladder *Ladder) {
	board, runs, err := ladder.Store.Leaderboard(ladder.Config.Schedule.Window)
	if err != nil {
		log.Fatal(err)
	}

	var ids []string
	for _, run := range runs {
		ids = append(ids, fmt.Sprint(run.ID))
	}
	fmt.Printf("Leaderboard over runs %s\n\n", strings.Join(ids, ", "))
	fmt.Printf("%-4s %-24s %8s %8s %6s  %s\n", "#", "team", "score", "± err", "games", "commit")
	for i, s := range board.Standings {
		fmt.Printf("%-4d %-24s %8.1f %8.1f %6d  %s\n", i+1, s.Team, s.Score.Mean, s.Score.StdErr, s.Score.Games, s.Commit)
	}
}

func cmdBuild(ctx context.Context, args []string) {
	ladder, _ := openLadder("build", args)
	defer ladder.Store.Close()

	bots, err := ladder.Pool(ctx)
	if err != nil {
		log.Fatal(err)
	}
	for _, bot := range bots {
		fmt.Printf("%-24s %s\n", bot.Name, bot.Image)
	}
}

func cmdRebuild(args []string) {
	ladder, flags := openLadder("rebuild", args)
	defer ladder.Store.Close()
	if flags.NArg() != 1 {
		usage()
	}

	builds, err := ladder.Store.Builds(flags.Arg(0), 1)
	if err != nil {
		log.Fatal(err)
	}
	if len(builds) == 0 || builds[0].Status == BuildOK {
		log.Fatalf("no failed build to forget for %s", flags.Arg(0))
	}
	if err := ladder.Store.DeleteBuild(builds[0].ID); err != nil {
		log.Fatal(err)
	}
	fmt.Printf("Forgot build %d (%s, %s): it will be built again at the next run\n", builds[0].ID, builds[0].Tag, builds[0].Status)
}

// Tournament configuration, recorded with the run
type TournamentConfig struct {
	Name    string   `json:"name"`
	Formats []Format `json:"formats"`
	Repeats int      `json:"repeats"`
}

func parseFormats(s string) ([]Format, error) {
	var formats []Format
	for _, part := range strings.Split(s, ",") {
		name, players, ok := strings.Cut(strings.TrimSpace(part), ":")
		n, err := strconv.Atoi(players)
		if !ok || err != nil || name == "" || n < 2 || n > 6 {
			return nil, fmt.Errorf("invalid format %q (expected map:players)", part)
		}
		formats = append(formats, Format{Map: name, Players: n})
	}
	return formats, nil
}

func cmdTournament(ctx context.Context, args []string) {
	flags := flag.NewFlagSet("tournament", flag.ExitOnError)
	formatsFlag := flags.String("formats", "", "formats, as map:players separated by commas")
	repeats := flags.Int("repeats", 1, "times each lineup is played")
	teamsFlag := flags.String("teams", "", "teams taking part, separated by commas (default: all current bots)")
	name := flags.String("name", "Tournament", "name of the tournament")

	ladder, _ := openLadderWithFlags(flags, args)
	defer ladder.Store.Close()

	formats, err := parseFormats(*formatsFlag)
	if err != nil || *repeats < 1 {
		log.Fatalf("invalid formats or repeats: %v", err)
	}
	if err := RemoveLeftovers(ctx); err != nil {
		log.Fatal(err)
	}

	bots, err := ladder.Pool(ctx)
	if err != nil {
		log.Fatal(err)
	}
	pool := bots
	if *teamsFlag != "" {
		var selected []Bot
		for _, team := range strings.Split(*teamsFlag, ",") {
			i := slices.IndexFunc(pool, func(b Bot) bool { return b.Name == strings.TrimSpace(team) })
			if i < 0 {
				log.Fatalf("no current bot for team %q", team)
			}
			selected = append(selected, pool[i])
		}
		pool = selected
	}

	config := TournamentConfig{Name: *name, Formats: formats, Repeats: *repeats}
	runID, err := ladder.Play(ctx, "tournament", config, pool, formats, *repeats, time.Now().Add(24*time.Hour))
	if err != nil {
		log.Fatal(err)
	}

	results, err := ladder.Store.Results([]int64{runID})
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("%s (run %d)\n\n", *name, runID)
	for i, s := range NewLeaderboard(results).Standings {
		fmt.Printf("%-4d %-24s %8.1f %8.1f %6d\n", i+1, s.Team, s.Score.Mean, s.Score.StdErr, s.Score.Games)
	}
}

func cmdCheck(args []string) {
	flags := flag.NewFlagSet("check", flag.ExitOnError)
	path := flags.String("config", "ladder.json", "configuration file")
	flags.Parse(args)

	config, err := LoadConfig(*path)
	if err != nil {
		log.Fatal(err)
	}
	dataDir, err := filepath.Abs(config.DataDir)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("listen %s\ndataDir %s\n", config.Web.Listen, dataDir)
}
