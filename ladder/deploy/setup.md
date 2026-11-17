# Deploying the ladder

On a Linux VM (the plan assumes 2 cores), as root unless noted.

## 1. Install

- Docker Engine, git, and Go (the version in `go.mod` or newer).
- A user for the service, allowed to use Docker:

  ```sh
  useradd --system --create-home --groups docker ladder
  ```

  Being in the `docker` group is equivalent to root access on the machine: the VM should be dedicated to the ladder.

- The repository, owned by that user:

  ```sh
  git clone https://github.com/hivehelsinki/hive-arena /opt/hive-arena
  chown -R ladder: /opt/hive-arena
  ```

## 2. Build (as `ladder`, in /opt/hive-arena)

```sh
go build -o ladder/ladder ./ladder
viewer/build-web.sh

docker build -t hive-arena-server .
docker build -t example-agent-go -f example-agent-go/Dockerfile .
docker build -t example-agent-lua example-agent-lua
```

Repeat these after each update of the repository (`git pull`), then restart the service. Rebuild `hive-arena-server` whenever the game or server changes: every match uses it.

## 3. Configure

```sh
cp ladder/ladder.example.json ladder.json
```

Set at least:

- `registry.url`: the public repository where teams register with pull requests (holding `teams.json`, initially `[]`)
- `dataDir`: e.g. `/opt/hive-arena/data` (database, reports, logs, repository mirrors)
- `schedule`: run times, formats, repeats, window. They are shown publicly on the website.
- `web.listen`: e.g. `:80` (needs `AmbientCapabilities=CAP_NET_BIND_SERVICE` in the unit), or a high port behind a reverse proxy

Check the setup before enabling the service:

```sh
ladder/ladder build -config ladder.json        # fetches and builds the registered teams
ladder/ladder run -config ladder.json          # plays a complete run now
ladder/ladder leaderboard -config ladder.json
```

## 4. Run

```sh
cp ladder/deploy/hive-ladder.service /etc/systemd/system/
systemctl daemon-reload
systemctl enable --now hive-ladder
journalctl -u hive-ladder -f
```

- Start a run now: `systemctl kill -s USR1 hive-ladder`
- A team's build failed for a transient reason (e.g. network): `ladder/ladder rebuild -config ladder.json <team>`
- Lunchtime tournament: `ladder/ladder tournament -config ladder.json -formats balanced:6 -name "Lunch cup"`. It uses the CPU like a run: avoid running it during a ladder run.

## 5. Network access

The website has no write routes and no accounts. Restrict access to the campus network (firewall or reverse proxy), as for the arena server.

Match servers and bots run on private Docker networks; bots have no network access other than their match server. Docker images and repository mirrors grow over time: old team images are pruned automatically (`build.keepImages`), but `docker system prune` from time to time does no harm while no run is in progress.

## Backups

Everything is in `dataDir`: copy it (the SQLite database is `ladder.db`, use `sqlite3 ladder.db ".backup backup.db"` for a consistent copy while the service runs).
