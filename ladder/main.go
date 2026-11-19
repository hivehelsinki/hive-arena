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
	"strconv"
	"strings"
	"syscall"
	"time"
)

func usage() {
	fmt.Fprintln(os.Stderr, `Usage:
  ladder [-config ladder.json]
      Run the ladder: matches, one at a time, until every team has played
      its quota, and the website. The "ladder" settings of the
      configuration are re-read before every match. Send SIGUSR1 to the
      process to rebuild the pool now.

Other commands, for debugging and testing. Except leaderboard and web,
they play matches or build images: they refuse to run while the ladder does.

  ladder version
      Print the git commit the ladder was built from
  ladder check [-config ladder.json]
      Check the configuration, and print the address of the website and the
      data directory
  ladder web [-config ladder.json]
      Run the website only
  ladder leaderboard [-config ladder.json]
      Print the current global ladder
  ladder tournament [-config ladder.json] -formats <map:players,...> [-repeats N] [-name NAME]
                    [-teams a,b,... | -top N [-at "2006-01-02 15:04"]]
      Play a one-off round-robin with its own formats, among the current
      bots (all of them, or the given teams), or among the top N teams of
      the global ladder (now, or at the given time in the configured time
      zone) with the versions they had then. When the ladder service runs,
      it plays the tournament after its current match; otherwise, this
      command plays it. It is shown with the tournaments on the website,
      and is not rated.
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

// mustLock takes the lock of the data directory, or exits
func mustLock(ladder *Ladder) {
	if err := lockDataDir(ladder.Config.DataDir); err != nil {
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
		Config:   config,
		Settings: NewLiveSettings(*path, config.Ladder),
		Store:    store,
		Player:   &Runner{Config: runnerConfig},
		Rand:     rand.New(rand.NewSource(time.Now().UnixNano())),
		// Only one ladder process plays at a time (see mustLock)
		RemoveLeftovers: RemoveLeftovers,
	}
	pool := &PoolResolver{
		Registry: config.Registry,
		DataDir:  config.DataDir,
		Store:    store,
		Builder:  builder,
	}
	ladder.Pool = func(ctx context.Context) ([]Bot, error) {
		pool.MaxFailures = ladder.Settings.Get().MaxFailures
		return pool.Resolve(ctx)
	}
	ladder.TournamentBots = func(ctx context.Context, request TournamentRequest) ([]Bot, error) {
		settings := ladder.Settings.Get()
		pool.MaxFailures = settings.MaxFailures
		return pool.TournamentBots(ctx, request, settings.Rating)
	}
	return ladder, flags
}

func cmdServe(ctx context.Context, args []string) {
	ladder, _ := openLadder("ladder", args)
	defer ladder.Store.Close()
	mustLock(ladder)

	wake := make(chan struct{}, 1)
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGUSR1)
	go func() {
		for range signals {
			select {
			case wake <- struct{}{}:
			default:
			}
		}
	}()

	go serveWeb(ladder)

	if err := ladder.Serve(ctx, wake); err != nil {
		log.Fatal(err)
	}
}

func serveWeb(ladder *Ladder) {
	web, err := NewWeb(ladder.Config, ladder.Store)
	if err != nil {
		log.Fatal(err)
	}
	web.Settings = ladder.Settings
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

func cmdLeaderboard(args []string) {
	ladder, _ := openLadder("leaderboard", args)
	defer ladder.Store.Close()
	printLeaderboard(ladder)
}

func printLeaderboard(ladder *Ladder) {
	matches, err := ladder.Store.RatedMatches(time.Time{})
	if err != nil {
		log.Fatal(err)
	}
	ratings := ComputeRatings(ladder.Settings.Get().Rating, matches)

	fmt.Printf("Global ladder, over %d matches\n\n", len(matches))
	fmt.Printf("%-4s %-24s %8s %8s %8s %8s  %s\n", "#", "team", "rating", "mu", "sigma", "matches", "commit")
	for i, t := range ratings.Global.Standings() {
		fmt.Printf("%-4d %-24s %8.2f %8.2f %8.2f %8d  %s\n", i+1, t.Team, t.Rating.Ordinal(), t.Rating.Mu, t.Rating.Sigma, t.Games, t.Commit)
	}
}

func cmdBuild(ctx context.Context, args []string) {
	ladder, _ := openLadder("build", args)
	defer ladder.Store.Close()
	mustLock(ladder)

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
	fmt.Printf("Forgot build %d (%s, %s): it will be built again at the next build check\n", builds[0].ID, builds[0].Tag, builds[0].Status)
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
	top := flags.Int("top", 0, "the first N teams of the global ladder take part, with the version they had (see -at)")
	at := flags.String("at", "", `with -top: the ladder at this time ("2006-01-02 15:04", default: now)`)
	name := flags.String("name", "Tournament", "name of the tournament")

	ladder, _ := openLadderWithFlags(flags, args)
	defer ladder.Store.Close()

	formats, err := parseFormats(*formatsFlag)
	if err != nil {
		log.Fatal(err)
	}
	request := TournamentRequest{Name: *name, Formats: formats, Repeats: *repeats, Top: *top}
	if *teamsFlag != "" {
		for _, team := range strings.Split(*teamsFlag, ",") {
			request.Teams = append(request.Teams, strings.TrimSpace(team))
		}
	}
	if *at != "" {
		loc, _ := ladder.Config.Location()
		request.At, err = time.ParseInLocation("2006-01-02 15:04", *at, loc)
		if err != nil || *top == 0 {
			log.Fatalf("invalid -at %q (expected \"2006-01-02 15:04\", with -top)", *at)
		}
	} else if *top > 0 {
		request.At = time.Now()
	}
	if err := request.validate(ladder.Config); err != nil {
		log.Fatal(err)
	}

	// The service plays it after its current match
	if err := lockDataDir(ladder.Config.DataDir); err != nil {
		if err := QueueTournament(ladder.Config.DataDir, request); err != nil {
			log.Fatal(err)
		}
		fmt.Println("The ladder service will play the tournament after its current match.")
		fmt.Println("Follow it on the website (Tournaments), or in the service's logs.")
		return
	}

	if err := RemoveLeftovers(ctx); err != nil {
		log.Fatal(err)
	}
	runID, err := ladder.PlayTournament(ctx, request)
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
