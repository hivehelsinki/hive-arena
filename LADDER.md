# Hive Arena Ladder: Plan

A staff-run service that plays registered bots against each other **continuously**, as a ladder (each team challenges the teams ranked just above it, automatically), records every game, and publishes ratings, results and replays on a public website. It gives participants automatic feedback within minutes of pushing a new version, without anyone having to run matches by hand. The same machinery can also run a quick tournament for a live event.

## Background

In the 2019 Arena, bots were built and run by the organizers, which turned into a mess of language versions, virtual environments and OS differences. In Arena 2025 each team ran its own bot against the server API, which avoided that problem but meant the only interaction between teams was a preview tournament and the final, both needing an organizer present and teams typing in match names on screen. The goal now is for a team to register once, keep pushing, and see its new version climb or fall on the ladder.

The compromise that keeps environment problems out of the organizers' hands: **teams provide a working Dockerfile**. The organizers only build and run it.

## Scope and principles

- **Staff run.** Only staff operate it, so there are no user accounts, permissions or admin API.
- **Separate service from the game server.** The existing server (`server/`) keeps its current role and API, apart from a few small backward-compatible additions (see "Server changes"), and keeps being used alone for live events. The ladder is its own program.
- **The ladder spawns its own servers and agents via Docker.** Every match gets a fresh server container and one container per bot, on a private Docker network. The ladder never talks to the long-running public server.
- **The public side only displays results.** No login, no upload, no write routes.
- **Registration is a pull request** on a public, organizer-owned repo (or edits by staff).
- **Everything is recorded and replayable**: every game is stored, downloadable, and viewable in the browser.
- **Security is "friendly competition" level**: equal resource limits and no network access beyond the match. Cheating is handled by people, not code.

## Architecture

```
                 registry repo (teams.json, PRs)
                           |
                    +------v-------+        +--------------------+
                    |   ladder     |------->|  Docker daemon     |
                    |  (one Go     |        |  - build images    |
                    |   binary)    |        |  - per match:      |
                    |              |        |    server + N bots |
                    | scheduler    |        |    on private net  |
                    | runner       |        +--------------------+
                    | store (SQLite + files)
                    | web (read-only HTTP)
                    +------+-------+
                           |
          public website: leaderboard, head-to-head, match list,
          report download, in-browser replay (viewer compiled to wasm)
```

One Go binary, `ladder`, with these parts:

1. **Registry sync**: reads the team list and resolves what to run.
2. **Builder**: turns a team's repo at a tag into a Docker image.
3. **Matchmaker**: decides which match to play next (ladder challenges, within a daily quota).
4. **Runner**: plays one match in containers and collects the result.
5. **Store**: SQLite for match records, plus the report files on disk.
6. **Web**: a read-only HTTP site over the store, including the wasm viewer.

It lives in the same repo (`ladder/`, `go run ./ladder`), reuses the `arena` package for types and report parsing, and the server image already built by the existing `Dockerfile`.

## Registration

A public, organizer-owned repo holds `teams.json`:

```json
[
  { "name": "SuperTeam", "repo": "https://github.com/someone/superteam-bot", "members": ["alice", "bob"] }
]
```

- To register or change a team, open a pull request; staff review and merge. Reviewing the PR is the "authentication".
- Team size is limited to 4-5 (checked by staff when merging).
- The ladder pulls the registry repo at every build check (every 15 minutes) and picks up changes.
- **What runs:** the most recent tag (by commit date) among the tags that point to a commit on the repo's `main` branch. Tags elsewhere are ignored. Pushing a new tag on `main` is how a team "submits" an update. Images are keyed by commit, so moved or deleted tags are harmless: whatever commit the resolved tag points to at sync time is what runs. An optional `ref` field in the registry pins a specific tag or commit, for staff overrides and for the final.
- Repos must be public (or readable with a staff deploy key); we don't hide bots from each other.

## The bot contract

A team's repo must contain, at its root, a `Dockerfile` that builds an image which, when run, plays one game and exits. The conventions:

- **No arguments, no ports, no volumes.** The image is started with `docker run` using the default `CMD`/`ENTRYPOINT`. The bot only makes outgoing HTTP connections.
- **Configuration is through environment variables**, set by the runner:
  - `ARENA_HOST`: host and port of the match's server (e.g. `server:8080`)
  - `ARENA_GAME_ID`: the game to join
  - `ARENA_NAME`: the name to announce (`/join?name=`)
- The bot joins the game (`/join`), plays until `gameOver` (or until the connection is refused/closed), then exits with status 0.
- No network access other than the match's server (not even the host: the service drops traffic from match networks to it); the filesystem is read-only except `/tmp`; the process runs as an unprivileged user (uid 65534, enforced by the runner whatever the image says).
- `ARENA_GAME_ID` is private to each bot: it is the ID of the bot's own reserved seat (see "Server changes"), so a bot always plays under its team's name, and cannot take another bot's seat.
- Builds must work offline-free of our help: the build is done on the ladder machine with network access (to fetch dependencies), but under a time limit.
- Dockerfile templates for Go and Lua are added to `example-agent-go` and `example-agent-lua` (mapping the env variables to the programs' arguments), and a short "bot contract" section in the docs. These templates are the reference for other languages.

## Building

- **Every 15 minutes** (between matches), for every team, resolve the latest tag to a commit; if no image exists for that commit, fetch it and `docker build` it with a time limit (10 minutes) and a size cap (2 GB). Build cache mounts are emptied before each build (they are shared between all builds), and the build cache is kept under a size limit.
- Images are tagged `hive-ladder/<team>:<commit>`; old images are pruned (keep the last few per team). A good build whose image disappeared is built again.
- Failures that are not the team's (an interrupted build, docker or the image registry failing, a smoke test that could not run) are not recorded: the build is tried again next time.
- The build log is stored and shown on the team's page.
- **A failed build never removes a team from the ladder**: the previous good image keeps playing, and the website shows the failure and its log.
- **Smoke test before admission:** a new image must complete a 1-player match on `tiny` (join, and play until the end of the game) before it is used in the ladder. Failure is shown like a build failure. This is the CI/CD-style feedback teams get for free.

## Running a match

For each match the runner:

1. Creates a private Docker network (`--internal`, so nothing in it can reach the outside).
2. Starts a server container from the arena server image on that network, with `--dev` (no minimum turn duration, so games play as fast as the bots can answer; the standard 2-second turn timeout still applies to every agent), accepting a single game and keeping no files, with memory and process limits.
3. Calls `/newgame?map=...&players=n&names=...` on it, which reserves one seat per bot.
4. Starts one container per bot with the contract's env variables (each with its own seat's game ID), in a random order.
5. Polls `/status` until the game is over (within the match timeout), and takes the result from it.
6. Streams the final report (`/report`) to a compressed file, and records it.
7. Stops and removes all containers and the network, whatever happened.

A match that fails (timeout, server failure) is recorded as **void**, with the error and the bots' output: it does not count, and is shown as failed on the website.

Container limits, the same for every bot: `--read-only`, `--tmpfs /tmp`, `--user 65534:65534`, `--cpus <cores / n>` (see below), `--memory 256m`, `--pids-limit 64`, `--network <match net>`, no extra capabilities (`--cap-drop ALL`, `--security-opt no-new-privileges`), at most 1 MB of output kept by docker, `--pull=never`. The limits are tuned after testing on real example bots. Docker's daemon is the only privileged component, and the ladder machine is a dedicated VM.

The target VM has only **2 virtual cores**, and with `--dev` matches are CPU-bound rather than paced by the clock. So **matches run one at a time**: nothing else competes for the CPU, and a bot's result does not depend on what else is running. Within a match, the CPU is split equally between the `n` bots: `--cpus = cores / n` (2 cores give 1.0 each in a 2-player match, 0.5 in a 4-player one, 0.33 in a 6-player one). The server container has no limit of its own: it only works between turns (validating and applying orders), when the bots are mostly waiting, so it should not take much from them. This is an assumption to verify: in step 2, measure match duration and the server's CPU use with the example bots, and adjust (e.g. reserve a fraction of a core for the server) if it turns out to matter. The per-bot CPU share differs between formats, but is identical for all bots in a match, which is what matters for fairness. If the quotas turn out too large for the machine, the answer is a larger VM or a lower quota, not unequal shares.

A bot is counted as **crashed** if its container exits before the game is over, and as **no-join** if it does not join in time (or its container exits first). The match goes on: the runner joins a stand-in in place of a missing bot, which posts empty orders every turn so the game is not slowed down. Either way the bot is ranked last (see "Scoring").

When a bot crashes (mid-game, or after joining but before the game starts), the runner forfeits its seat through the server's `/forfeit` route (admin token only): the server stops waiting for its orders, so the rest of the game is not slowed down by the turn timeout.

## Games and lineups

Maps are fixed by the files in `maps/` (the number of players per map is chosen by the configuration: any `n` the map supports). A **format** is a map and a number of players, for instance:

- 2-player matches, the most direct comparison,
- 3 and 4 player matches,
- 6-player matches as the showcase format.

Every format gets the same quota of matches (see Matchmaking), and its own ladder. With about 10 teams, a 6-player match takes most of the field: it says less about who beats whom, and one bot can decide the winner by interfering with another. Smaller lineups show direct strength.

**Seats are random.** All maps are symmetrical, so a start position is neither an advantage nor a handicap: it only decides who your neighbours are. Seats are drawn at random for every match, and the luck of the neighbour draw averages out over many matches.

## Matchmaking: a continuous ladder

The ladder plays one match at a time, continuously, until every team has played its quota; then it waits. There are no runs, no fixed schedule and no frozen pool: a team's new version enters the pool as soon as it is built, and the leaderboard is always current. The first design (full round-robins twice a day, scored by mean points over a window of runs) was replaced because the number of lineups grows combinatorially with the pool (10 bots: 585 lineups per round over the four formats) and turned into random sampling anyway, while teams waited up to 12 hours to see a new version play.

**Quota: N matches per 24 hours in each format, per team.** Every match a team plays counts, whether it started it or was challenged. With about 10 teams and the four formats, the cost is about `teams × formats × N / average players per match`: N = 20 gives about 210 matches a day.

**Before each match**, the matchmaker:

1. **Picks the challenger**: the team and format furthest below the quota (matches played in the last 24 hours, a rolling window), ties broken at random. A format needs as many teams as players, otherwise it is skipped. If every team has met its quota, nothing is played, and the matchmaker looks again a minute later (or as soon as a new version is built or the configuration changes).
2. **Picks the opponents**, in that format's ladder: the other `n - 1` players are drawn at random among the `max(M, n - 1)` teams ranked just above the challenger. Near the top, where there are not enough teams above, the window extends to the teams just below. A fraction of the matches (say 10%) take `n - 1` opponents at random among all teams instead, so that every team keeps meeting the whole field.
3. **Draws the seats** at random (see Games and lineups), and plays the match.

Because challenges go upward, top teams are pulled into more matches than they start; counting every match in the quota keeps the totals per team close (a team that is often challenged starts fewer challenges itself). A team can exceed its quota by being challenged.

**Nothing is kept in memory.** Every decision is made from the match history and the configuration file, both read before each match. A restart (an update, a crash, a reboot) resumes where the ladder left off: the match that was being played is lost and not counted, and the next pick looks at the same history.

**Tunable while the ladder runs.** These settings are re-read from the configuration file before each match: the quota N, the window M, the share of random matches, the formats, the rating parameters, the build interval, and a `paused` switch (to stop playing, for instance while staff run the final tournament). An invalid file keeps the previous settings, with a warning on the status page. Changing a rating parameter recomputes every rating from the full history, so nothing is left in an inconsistent state. Settings that shape the machine (data directory, resource limits, listen address) still need a restart.

**Demand and capacity.** The status page shows how many matches the quotas need per day and how many were played in the last 24 hours, with the time spent idle, so staff see when N is too high for the number of teams.

**Pool changes.** A new team enters at the bottom of every ladder, and is challenged and challenges like the others; its first matches move its rating fast (see Rating). A team whose new tag is built plays with it from its next match. A team whose bot fails (crashes or does not join) K matches in a row with the same version is left out until it pushes a new tag.

**Staff bots.** There are no special house bots: the only way into the ladder is the registry. If wanted, staff register bots like any team (for instance the example agents, or a deliberately weak bot as a reference point), and they are ranked like everyone else.

## Rating

Each team has a **rating per format** (map and number of players), and a **global rating** fitted on all its matches in all formats. A rating is a skill estimate μ and an uncertainty σ, in the style of TrueSkill. Teams are **ranked by μ − 3σ**, a conservative estimate: a new team starts at μ = 25, σ = 25/3, which shows as 0, at the bottom, and climbs as it wins; a large σ means large updates, so a strong newcomer climbs within a few dozen matches.

The model is **OpenSkill's Plackett-Luce model** (Weng and Lin, 2011): a closed-form update for free-for-all matches of any number of players, with ties, so 2, 3, 4 and 6 player matches feed the same ratings. True TrueSkill needs an iterative approximation over a factor graph for more than 2 players, and is a Microsoft trademark; OpenSkill was published as an open alternative, and behaves practically the same with the same parameters. Among OpenSkill's models, Plackett-Luce was the most accurate in a simulation of 10 teams with noisy results (2, 4 and 6 player matches): 94-96% of pairs of teams in the right order after 20 matches per team, 97-99% after 50, all of them after 200. The "full" pairwise models (Thurstone-Mosteller, Bradley-Terry) let a newcomer reach the top faster, but by overreacting: they count every pair of players in a match as an independent result, so one 6-player match can drop a player's σ from 8 to 0.1 and freeze its rating; they were less accurate. With Plackett-Luce, the best newcomer reached 1st in a median of 25-45 matches (fewer in larger matches), so the quotas should give each team many matches per day.

- **Order of a match**: by flowers in reserve, equal counts tied. A bot that crashes or does not join is ranked last, tied with the other failed bots, as before.
- **Skill changes**: a dynamics term τ adds a little uncertainty before every match, so σ never reaches 0 and the rating keeps following a team that improves (as in esports ladders).
- **New versions**: bots change in jumps rather than gradually. When a team's version changes, its σ is raised to at least a configured value (half the initial σ, for instance), so the rating reacts within a few matches to a rewrite or a regression. 0 disables it.
- **Recomputed, not stored**: ratings are computed by replaying all valid ladder matches in order, every time they are needed (thousands of matches take milliseconds). There is no rating state to corrupt, a rating parameter can change at any time, and the ratings at a past date are those of the matches before it (used to pick the top N for the final, see Live events).

Parameters (μ₀ = 25, σ₀ = 25/3, β = 25/6, τ = 25/300, the TrueSkill defaults) are configuration. Void matches and tournaments do not count.

**Also shown, as statistics**: matches played, the mean points per match (`points = 60 * (n - rank) / (n - 1)`, ties averaged, the scale of the first design: 60 for the winner, 0 for the last) and the **head-to-head table**: how often A finished ahead of B (ties count half), in the matches they played together. These are computed on the matches of the last few days, and are what participants will want to look at; the ladder order is the rating.

Since challenges are automatic, a team cannot choose its opponents or when it plays, which was the concern with rating systems in the first design.

## Recording

Every match is stored:

- **In SQLite**: match id, timestamp, map, `n`, seat/player index, team and commit per seat, flowers, points, status per bot (finished, crashed, no-join), and whether the match is valid (failed matches are void, with their error, and not counted). Each match records the arena server image and the ladder revision that played it. The schema is versioned, with migrations (the database is copied before being migrated).
- **As files**: the server's JSON report (the same format as `history/` today, so the existing viewer reads it unchanged), stored compressed, with container logs of each bot (truncated to a size limit) for debugging. Without accounts, logs are public like the rest of the site; teams should not print anything secret.

Reports are large: the full state of every turn, so a long game (several thousand turns: the game has no turn limit) gives a report of 100-200 MB, 2-4 MB compressed (they are compressed as they are downloaded, never stored raw). At a few hundred matches a day this adds up, so **results are kept forever, replays are not**:

- A team keeps the files (report and bot logs) of its last 100 matches; a match's files are deleted once none of its participants keeps it. Disk use is then bounded by the number of teams, and every team always has recent replays to study, however long ago it last played.
- When disk space runs low, the oldest files are deleted first, instead of stopping the ladder.
- An expired match keeps its page and its results; the replay link says the replay expired.
- Backups contain the database (and the last registry), not the files: replays cannot be regenerated, but they are a convenience, and the backups stay small.

A turn limit or a more compact replay format, for the game review, would reduce both the files and what the browser viewer has to load.

The store is the source of truth: ratings, quotas, head-to-head tables and statistics are computed from it.

**Durability.** Past data must never be lost:

- The data lives on the VM's disk, outside any container: match servers and bots are disposable, restarting them or Docker touches nothing.
- Each match is recorded as soon as it ends, in one transaction. Its files are written and synced to disk (written under a temporary name, synced, renamed) before the transaction that refers to them is committed. SQLite runs in WAL mode with full synchronization. A crash, a restart or a power loss loses at most the match being played.
- The ladder never deletes results (only replay files, see above). On restart, leftover containers are removed; the match that was being played is lost.
- Low disk space is logged at start-up and before each match, and reported by `/health` (with Docker problems, repeated void matches, and a matchmaker that stopped running), for the campus monitoring.
- Against the loss of the VM: a backup route protected by a URL token (`/backup?token=`) serves a tar archive named after the time, with a consistent database snapshot and all the files, to be fetched regularly by a backup system elsewhere.

## Public website

Read-only, served by the same binary. No accounts.

- **Ladder**: the global ladder and one per format (map and number of players), ranked by μ − 3σ, with μ, σ, matches played and mean points; the head-to-head table; a team's position over time.
- **Match list**, filterable by team and map, each entry linking to its replay and report.
- **Team page**: ratings (global and per format) and their history, its matches in the last 24 hours against the quota, recent results, current commit, builds with their logs, and why the team does not play if it does not (no working build, left out for failing).
- **Status**: what is being played, quota demand against matches played in the last 24 hours, the settings in use, and configuration warnings.
- **Report download**: the game JSON (`/matches/<id>/report.json`), usable with the desktop viewer.
- **Replay in the browser**: the Ebitengine viewer compiled to WebAssembly, loading a match's report by URL (`viewer?match=<id>`), with the same controls as the desktop version. A proof of concept exists. The page for a match embeds it. Because of staff-run and campus-only access, no further access control is needed.
- A JSON API for the same data (`/api/leaderboard`, `/api/matches`, ...) is provided for display purposes only, so that for instance the big-screen view can be a simple page polling it.

Access is restricted to the campus network at the network level, as for the arena server.

## Live events and the big screen

The ladder runs continuously during the competition week. For the end-of-week live event, the existing flow is kept: matches are played game by game on the regular server, with the teams' own bots, for show. The ladder helps with it: a **tournament mode** (`ladder tournament`) plays a one-off round-robin among chosen teams with its own formats, on the ladder machine, spawning matches the same way. It is recorded as a tournament, shown on the website, and not counted in the ratings. It runs inside the service (staff pause the ladder from the configuration, and start the tournament), so the two never compete for the machine. **The final tournament is run by staff with the top N of the global ladder at a given time** (`--top 8 --at "2026-12-04 16:00"`): ratings at a past time are those of the matches played before it, and the versions that played them are pinned for the tournament.

The **big-screen page** (`/screen`) shows the leaderboard next to the replays of the latest matches, autoplayed one after the other; a lunchtime tournament fills it with fresh matches. A true live view would need the ladder to relay the state of its private match servers to browsers; replays of matches that just ended are much simpler and look the same on a screen.

## Server changes

The server needs no change to work with the ladder, but a few small ones make the ladder and live events simpler and more robust. They are backward compatible (existing clients and agents are unaffected), and they come first in the implementation plan (step 0).

1. **Configurable directories.** `--maps` and `--history` flags (today the maps directory is a constant, and both are relative to the working directory). `--history ""` keeps no files (the ladder uses `/report`).
2. **Report route.** `GET /report?id=<game id>` returns the JSON report of a finished game (the same format as the files in `history/`), so the runner does not have to find a file whose name embeds a timestamp. The `/history/` file route stays for browsing.
3. **Random seats.** A joining player gets a random free seat instead of the next one in join order, so seating does not depend on container start-up timing (ladder) or on who joins first (live events). The `players` list in the report is indexed by seat.
4. **Results in the report.** A `result` block in the report and in the finished game's status, with the final flowers, rank and winners per player. The ladder and website then do not re-derive scores from the last state.
5. **CORS and websocket origin.** CORS headers on the read routes and an origin check that accepts the configured origins, so the web viewer can talk to the arena server directly during live events. Not needed for the ladder itself, since the ladder site serves its own reports.
6. **Lighter Docker image.** A multi-stage `Dockerfile` (build with the Go toolchain, run from a small image with the binary and `maps/`) and a `.dockerignore` excluding `history/`, so per-match server start-up is fast.
7. **Forfeit route** (added during implementation). `GET /forfeit?id=&token=<admin token>&player=` stops waiting for a player's orders. The runner uses it when a bot's container exits mid-game: otherwise every remaining turn waits for the 2-second timeout, and a crashing bot would make its matches last minutes.
8. **Reserved seats** (added during implementation). `/newgame?names=a,b,c` returns a private game ID per name; each joins once, under its name. The ladder gives each bot its own: a bot joining under another name crashed the ladder, and a malicious one could take another team's seat. Also `--max-games` (the ladder's servers accept a single game), limits on `/orders` (1 MB, 2 × units + 16 orders), and reports written one turn at a time (encoding a large report at once doubled the server's memory).

Not changed: `--dev` stays a process flag (the ladder spawns a server per match, with `--dev`; the 2-second turn timeout stays), and `/newgame` stays unauthenticated (the ladder's servers accept a single game, the live server is campus-only).

## Deployment

A campus VM, handed over with root. The ladder binary runs under systemd with access to the Docker daemon, with:

- Docker installed, the arena server image built from the repo (`runDocker.sh` / `Dockerfile`),
- a data directory (SQLite, reports, logs, cloned repos),
- the configuration file: registry repo URL, formats, quota and challenge window, rating parameters, resource limits (CPU cores to split), timeouts (the ladder settings are re-read before each match),
- the web server on one port, behind the campus network restriction.

If load grows, the one-VM design can be split later (runner vs web), as everything between them goes through the store.

## Implementation status

All the steps of the plan are implemented, on the `server-ladder-support` branch, one commit per step:

0. **Server changes**: items 1-4 and 6-8 above. Item 5 (CORS) is not done: the ladder serves the viewer and reports itself, so it is only needed if the web viewer should talk to a live arena server.
1. **Bot contract and templates**: [docs/docker.md](docs/docker.md); Dockerfiles for both example agents, which read `ARENA_HOST`, `ARENA_GAME_ID` and `ARENA_NAME`.
2. **Match runner** (`ladder/runner.go`): with the example bots, a match takes 1-2 seconds; a full test run of 25 matches took under a minute. Real bots will be slower: measure with the first real teams.
3. **Store and scoring** (`ladder/store.go`, `ladder/scoring.go`).
4. **Scheduler** (`ladder/scheduler.go`): `ladder` runs the ladder (scheduled runs, SIGUSR1 for a run now, and the website); the other commands are for debugging and testing.
5. **Registry and builder** (`ladder/registry.go`, `ladder/builder.go`). Failed builds are not retried automatically (a broken tag stays broken); `ladder rebuild <team>` forgets a failed build after a transient failure.
6. **Website** (`ladder/web.go`, `ladder/web/`).
7. **Web viewer**: `viewer/build-web.sh`, served at `/viewer/`.
8. **Tournament mode and big-screen page**: round-robin tournaments only (no elimination brackets, which matter little with a handful of teams).
9. **Deployment**: `ladder/deploy/setup.sh` sets up or updates everything and restarts the service, then checks that it runs the pulled commit (see [ladder/deploy/setup.md](ladder/deploy/setup.md)). To do on the VM: dry run with the example bots and a few volunteer bots, then open registration.
10. **Hardening**, after a review of security, reliability and maintainability: reserved seats, bounded logs, void matches, retried infrastructure failures, registry fallbacks, scheduler fixes, schema migrations, isolation of bots from the host, builds between runs. See the commit history for details.

**Next: the continuous ladder** (this plan's Matchmaking and Rating sections replace the scheduled runs), one commit per step:

11. **Ratings** (`ladder/rating.go`): the Plackett-Luce update with ties, τ and the version bump, replay over the history, global and per format; tested against reference values from the `openskill` Python library.
12. **Store**: matches outside runs, with the server image and ladder revision; counts per team and format over the last 24 hours; replay retention and pruning when disk is low. Runs stay only for tournaments.
13. **Matchmaker** (replaces `ladder/scheduler.go`): quotas, challenge windows, random matches, builds every 15 minutes between matches, the configuration re-read before each match, `paused`.
14. **Website and status**: the ladders, rating history, quota and capacity, expired replays.
15. **Tournament from the ladder**: `--top N --at <time>`, run inside the service while paused.
16. **Cleanup**: remove run windows and schedules from the configuration, the code, the docs and the setup script. Defaults are tuned after measuring match durations with realistic bots.

The game review happens after this work.

## Decisions

- **Game review.** A pass over rules and balance, so this year's game is similar in accessibility but not identical, is separate and comes after the ladder. Maps, viewer and example agents may change with it, so the ladder treats them as data (maps are read from `maps/`, reports are in the standard format).
- **Pacing.** Ladder servers run with `--dev`, with the standard turn timeout for all agents.
- **Tags.** The latest tag on the `main` branch of a team's repo.
- **Retention.** Results are kept forever; replays and logs for the last 100 matches of each team (see "Recording").
- **Hosting.** A campus VM with 2 virtual cores; see Running a match: matches run one at a time, never in parallel, so every bot gets the same CPU.
- **Scale.** About 10 teams. With N = 20 matches per format per day, about 210 matches a day.
- **Ranking.** TrueSkill-style ratings (OpenSkill Plackett-Luce), ranked by μ − 3σ, σ partly reset on a new version; the final is a tournament among the top N, run by staff.

## Open questions

- Default quota: N = 20 matches per format per day is a guess; measure match durations with realistic bots (from 15 s to 2 min per match gives 1 to 7 hours of play a day for 10 teams).
- Is the VM's Docker access and the campus-only restriction confirmed, and where will the public site be reachable from (hostname)?
