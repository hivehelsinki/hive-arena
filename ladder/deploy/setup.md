# Deploying the ladder

On a dedicated Linux VM (Debian or Ubuntu for the automatic installation of the tools; the plan assumes 2 cores).

## Setup and updates

```sh
git clone https://github.com/hivehelsinki/hive-arena /opt/hive-arena
cd /opt/hive-arena
sudo ladder/deploy/setup.sh
```

The first time, the script creates `ladder.json` (from [ladder.example.json](../ladder.example.json), with a random backup token) and stops: edit it, then run the script again. Set at least:

- `registry.url`: the public repository where teams register with pull requests (holding `teams.json`, initially `[]`)
- `schedule`: run times, formats, repeats, window. They are shown publicly on the website.
- `web.listen`: e.g. `:80`, or a high port behind a reverse proxy

To update the ladder later, run the same script again. Every time, it:

1. installs the missing tools (git, Go, Docker, curl, gzip) with apt, and creates the `ladder` user that runs the service, in the `docker` group;
2. pulls the latest commit of the current branch (it refuses if the repository has local changes);
3. builds the ladder, the browser viewer, and the arena server image used by every match;
4. creates the data directory (`dataDir`, by default `data/` in the repository) and gives it to the `ladder` user;
5. installs the systemd service `hive-ladder` and restarts it. If a ladder run is in progress, it asks first (or use `--force`): the run would be marked incomplete, keeping its recorded matches;
6. checks that the running ladder reports the commit that was just pulled, and shows its health.

The version is also shown at the bottom of every page of the website, and by `ladder/ladder version`.

Being in the `docker` group is equivalent to root access on the machine: the VM should be dedicated to the ladder.

## Day to day

- Logs: `journalctl -u hive-ladder -f`
- Start a run now: `systemctl kill -s USR1 hive-ladder`
- A team's build failed for a transient reason (e.g. network): `sudo -u ladder ladder/ladder rebuild -config ladder.json <team>` (safe while the service runs)

The other commands (`ladder/ladder help`) are for debugging and testing. Except `leaderboard`, `web`, `check`, `version` and `rebuild`, stop the service before using them (`systemctl stop hive-ladder`): they play matches or build images, and would disturb the ladder's matches. Run them as the `ladder` user (`sudo -u ladder ...`), so that the files they write in the data directory belong to it.

## Network access

The website has no write routes and no accounts. Restrict access to the campus network (firewall or reverse proxy), as for the arena server.

Match servers and bots run on private Docker networks; bots have no network access other than their match server. Docker images and repository mirrors grow over time: old team images are pruned automatically (`build.keepImages`), but `docker system prune` from time to time does no harm while no run is in progress.

## Monitoring

- `/health` answers `ok` (200), or lists the problems (503): low disk space, Docker unavailable, last run failed, no run for more than 25 hours. Point the campus monitoring at it.
- `/api/status` gives the details as JSON: disk space, Docker, database size and counts, current, last and next run, teams whose latest version is not playing.
- Low disk space is also logged at start-up and before each run (`minFreeDiskGB`, default 5).

## Backups

Results are never deleted by the ladder, and each match is written durably before it is recorded: a crash, a restart of the service or of Docker, or a reboot loses at most the match being played. What remains is losing the VM or its disk: back up the data elsewhere.

Set `web.backupToken` to a long random string (e.g. `openssl rand -hex 24`), then have the backup machine fetch, for instance daily:

```sh
curl -fsS -OJ "https://<ladder>/backup?token=$TOKEN"
```

This downloads `ladder-backup-<UTC time>.tar`: a consistent snapshot of the database, taken while the ladder keeps running, and all the reports and logs. Each archive is complete on its own. `curl -f` fails on an error status; if the server fails midway, the archive is truncated, which `tar tf` detects.

What is backed up: the database (`ladder.db`), match reports and bot logs, build logs, the last team states. Not backed up, because they can be rebuilt: repository mirrors and Docker images.

**Restoring**: stop the service, extract the latest archive into an empty `dataDir`, and start the service.

**Before upgrading the ladder**, take a backup: a new version may change the database.
