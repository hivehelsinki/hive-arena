# The ladder: website and data

The ladder plays the registered bots against each other all day, on random maps, and publishes everything on its website: the ladder, every match with its replay, and each team's builds. The same data is available as JSON, without any account, to analyse your matches or download your replays.

How bots are built and run: [docker.md](docker.md). The rules of the game: [rules.md](rules.md).

## Taking part

- **Register with a pull request** on the registry repository, adding your team to its `teams.json`:

  ```json
  { "name": "SuperTeam", "repo": "https://github.com/someone/superteam-bot", "members": ["alice", "bob"] }
  ```

  Staff review and merge it; the ladder picks it up within about 15 minutes. Teams have at most 5 members. Repositories are public, with an `https://` URL: everyone can read everyone's bot.
- **Submit a version by pushing a tag** on a commit of your `main` branch: the most recent tag (by commit date) among those on `main` plays. Tags elsewhere are ignored, and moving or deleting a tag changes nothing once its commit is built.
- **A new team starts at the bottom** of the ladder, and climbs as it wins. Matches are automatic: teams are challenged by, and challenge, the teams near them on the ladder, every day. A new version keeps the team's rating, but makes it more uncertain, so that it can move quickly.
- **A version that keeps failing** (crashing or not joining) in its last matches is left out until you push a new tag. A version that does not build or does not pass the smoke test never plays: your previous version keeps playing, and your team's page shows why.

## What is kept

- **Results are kept forever**: every match, its players, their places and flowers.
- **Replays and bot outputs are kept for each team's last 100 matches.** A match keeps them as long as one of its teams keeps it; older ones are deleted. To keep all of yours, download them regularly (see the script below).

## The data

All routes are `GET`, and answer JSON unless said otherwise.

### Matches

`/api/matches`: matches, most recent first. Filters, all optional:

- `team`: only the matches of a team
- `players`: only the matches with this number of players
- `limit`: how many matches (50 by default, at most 200)
- `before=<id>`: only older matches than this one, to page back
- `after=<id>`: only newer matches than this one, **oldest first**; `after=0` starts from the first match. To fetch the matches played since the last one you have, ask for `after=<its id>` until the answer is empty.

`/api/matches/<id>`: one match. A match is:

```
{
	"id": (int) the match number, increasing,
	"gameId": (string) the ID of the game on its arena server,
	"map": (string) "random": the map's seed is in the replay,
	"players": (int) the number of players,
	"startedAt", "endedAt": (string) ISO 8601 times,
	"turns": (int) the turns played,
	"void": (bool) the match failed (for instance the server crashed): it is not rated, and has no replay,
	"error": (string) why, for a void match,
	"filesDeleted": (bool) the replay and the bot outputs have expired,
	"bots": [ by seat (player number in the replay)
		{
			"team": (string),
			"commit": (string) the version of the team's bot,
			"seat": (int),
			"flowers": (int) at the end of the game,
			"place": (int) 1 for the winner; tied bots share the first place they span; a bot that crashed or did not join is last,
			"status": (string) "finished", "crashed" (exited before the end), or "no-join" (did not join in time),
			"exitCode": (int) of the bot's container
		}
	]
}
```

### Replays and outputs

- `/matches/<id>/report.json`: the replay, in the same format as the reports of the arena server: the full state at every turn, the orders, and the result. Open it with the viewer: `go run ./viewer --file report.json`. Large: ask for it compressed (`Accept-Encoding: gzip`, as browsers and `curl --compressed` do).
- `/matches/<id>/logs/<team>`: what the team's bot printed during the match (text, the last 256 kB at most).

### The ladder

`/api/leaderboard`: the ladder:

- `ladder`: the global ladder, best first. For each team: `team`, `global` (its rating: `rating.mu` the estimated skill, `rating.sigma` the uncertainty, ranked by `mu - 3 * sigma`; `games`, `commit`), `perFormat` (the same, for each number of players: keys like `random:4`), and `playing` (whether it plays at the moment)
- `perFormat`: the ladder of each format
- `headToHead`: `headToHead[a][b]` is how often `a` finished ahead of `b` (`wins`, ties count half) in the `games` they played together, over `headToHeadPeriod`
- `history`: each team's global rating over time (`t`: Unix time, `r`: rating, `v`: the version that started there)

`/api/teams`: the registered teams, with their current version and, if it is not playing, why (for instance a failed build).

## Downloading your matches

[scripts/ladder_download.py](../scripts/ladder_download.py) downloads a team's matches into a directory: the results (`<id>.result.json`), the replays (`<id>.json.gz`) and the bot's output (`<id>.log`). Run it again at any time: it only fetches the matches played since the last one it has. It needs Python 3, nothing else.

```sh
python3 scripts/ladder_download.py https://<ladder address> <your team> [directory]
```

Replays are compressed: `gunzip -k 1234.json.gz` before opening one with the viewer.
