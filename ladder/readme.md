# Hive Arena ladder

The ladder service: plays registered bots against each other in Docker containers, records the results, and publishes them. See [LADDER.md](../LADDER.md) for the design.

Work in progress. Currently available:

## Running the ladder

Copy [ladder.example.json](ladder.example.json) to `ladder.json` and adjust it, then:

- `go run ./ladder serve`: runs the ladder at the scheduled times, and the website (send `SIGUSR1` to the process to start a run right away)
- `go run ./ladder web`: runs the website only
- `go run ./ladder run`: plays one run now and exits
- `go run ./ladder leaderboard`: prints the leaderboard
- `go run ./ladder build`: fetches the registered teams, builds and smoke-tests their new versions (also done at the start of each run)
- `go run ./ladder rebuild <team>`: forgets the failed build of a team's current version, so that it is built again (for instance after a network failure during the build)

Configuration (`ladder.json`):

- `dataDir`: where the database, reports and logs are kept
- `schedule`: published as is with each run.
  - `times`, `timezone`: daily start times of the runs
  - `formats`: each run plays every lineup of the pool for each format (map and number of players)
  - `repeats`: how many times each lineup is played in a run
  - `window`: the leaderboard is computed over this many recent runs
  - `maxFailures`: a bot that fails (crashes or doesn't join) this many matches in a row with the same commit is left out of the next runs, until its commit changes
- `deadlineMargin`: a run stops this long before the next scheduled run, even if not all matches were played (it is then marked incomplete)
- `runner`: server image, CPU cores split between the bots of a match, per-bot memory and process limits, timeouts
- `registry`: where teams are registered: a git repository (`url`, `branch`, `file`), or a local file (`path`)
- `build`: build timeout, number of successful images kept per team, map of the smoke test
- `web.listen`: address of the website
- `houseBots`: bots that always take part (reference bots), given as images
- `bots`: other bots given directly as images, outside of the registry

## Teams and builds

The registry file lists the teams:

```json
[
  { "name": "SuperTeam", "repo": "https://github.com/someone/superteam-bot", "members": ["alice", "bob"] }
]
```

Optional fields: `ref` (a tag, branch or commit to use instead of the latest tag on `main`), `dockerfile` and `context` (paths in the repository, defaults `Dockerfile` and the root).

At the start of each run, for each team, the ladder updates a mirror of its repository, takes the most recent tag on `main`, and builds it if it is new (`git archive | docker build -`, no checkout). A new image must pass a smoke test: a 1-player game, played to the end. Builds and their logs are recorded; a failed build is not retried (see `rebuild`), and the team's last good image keeps playing.

Matches are played one at a time. A crashed bot's seat is forfeited (the server stops waiting for its orders), so it does not slow the game down.

## Playing one match

```sh
# Images of the server and of the bots
docker build -t hive-arena-server .
docker build -t example-agent-go -f example-agent-go/Dockerfile .
docker build -t example-agent-lua example-agent-lua

go run ./ladder match [-report report.json] [-logs dir] balanced a=example-agent-go b=example-agent-lua
```

Each bot is given as `name=image`. The runner creates a private network, starts a server and the bots (with the resource limits of the ladder), waits for the end of the game, and prints the outcome of each bot:

- `finished`: played until the end
- `crashed`: joined, but exited before the end of the game
- `no-join`: did not join in time (or could not be started); a stand-in played its seat, posting no orders

Containers and networks are always removed at the end. All of them have the `hive-ladder` label.

## Tests

`go test ./ladder` runs the runner tests with Docker (skipped with `-short` or when Docker is unavailable). 

## Website

Read-only, no accounts: leaderboard (with per-map scores and the head-to-head table) over the window of recent runs, runs, matches (filterable by team and map), team pages with builds and their logs, match pages with bot outputs and report downloads. The same data is available as JSON under `/api/` (`leaderboard`, `runs`, `matches`, `teams`).
