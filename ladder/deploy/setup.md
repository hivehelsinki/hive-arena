# Deploying the ladder

On a dedicated Linux VM (Debian or Ubuntu for the automatic installation of the tools; the plan assumes 2 cores). Memory: the bots of a match take up to 256 MB each (1.5 GB for 6 players), and the match server up to `runner.serverMemory` (2 GB): 4 GB or more.

## Setup and updates

```sh
git clone https://github.com/hivehelsinki/hive-arena /opt/hive-arena
cd /opt/hive-arena
sudo ladder/deploy/setup.sh
```

The first time, the script creates `ladder.json` (from [ladder.example.json](../ladder.example.json), with a random backup token) and stops: edit it, then run the script again. Set at least:

- `registry.url`: the public repository where teams register with pull requests (holding `teams.json`, initially `[]`)
- `ladder`: formats and quota (matches per team per format per 24 hours). They are shown publicly on the website, and can be changed at any time: they apply from the next match, without a restart.
- `web.listen`: e.g. `:80`, or a high port behind a reverse proxy

To update the ladder later, run the same script again. Every time, it:

1. installs the missing tools (git, Go, Docker, curl, gzip, iptables) with apt, and creates the `ladder` user that runs the service, in the `docker` group;
2. if a tournament is in progress, stops there, before changing anything: it asks on a terminal, and fails otherwise (`--force` updates and restarts anyway; the tournament is then marked incomplete, keeping its recorded matches). A ladder match in progress is simply lost: the ladder resumes after the restart;
3. pulls the latest commit of the current branch (it refuses if the repository has local changes);
4. builds the ladder, the browser viewer, and the arena server image used by every match;
5. creates the data directory (`dataDir`, by default `data/` in the repository) and gives it to the `ladder` user;
6. installs the systemd service `hive-ladder` and restarts it;
7. checks that the running ladder reports the commit that was just pulled, and shows its health.

The repository must be readable by the `ladder` user (clone it in `/opt`, not in a private home directory). When a new version changes the database schema, the ladder copies the database (`ladder-v<version>-<time>.db` in the data directory) before migrating it.

The version is also shown at the bottom of every page of the website, and by `ladder/ladder version`.

Being in the `docker` group is equivalent to root access on the machine: the VM should be dedicated to the ladder.

## Day to day

- Logs: `journalctl -u hive-ladder -f`
- Change the quota or formats, pause the ladder (`"paused": true`): edit the `ladder` section of `ladder.json`; it applies at the next match (within a minute when idle). An invalid file is not applied, and `/health` says why.
- Build new versions now: `systemctl kill -s USR1 hive-ladder`
- A team's build failed for a transient reason (e.g. network): `sudo -u ladder ladder/ladder rebuild -config ladder.json <team>` (safe while the service runs)

The other commands (`ladder/ladder help`) are for debugging and testing. `tournament` and `build` play matches or build images: they refuse to start while the service runs (stop it first: `systemctl stop hive-ladder`). Run them as the `ladder` user (`sudo -u ladder ...`), so that the files they write in the data directory belong to it.

## Network access

The website has no write routes and no accounts. Restrict access to the campus network (firewall or reverse proxy), as for the arena server.

Match servers and bots run on private Docker networks; bots have no network access other than their match server. The service adds a firewall rule at start (`iptables -I INPUT -i hl-+ -j DROP`): match networks have interfaces named `hl-*`, and without it bots could reach services listening on the host (the website, sshd) through their network's gateway. Docker images and repository mirrors grow over time: old team images are pruned automatically (`build.keepImages`), and the build cache is kept under `build.cacheLimit`. A missing team image is simply built again, so `docker system prune -a` does no harm (pause the ladder first, so that no match is running).

## Monitoring

- `/health` answers `ok` (200), or lists the problems (503): low disk space, Docker unavailable, matches failing in a row, a matchmaker stuck for longer than a match can last, a configuration file that could not be applied. Point the campus monitoring at it.
- `/api/status` gives the details as JSON: disk space, Docker, database size and counts, what the ladder is doing, the matches of the last 24 hours (and the time they took) against what the quotas need per day, teams whose latest version is not playing or that are left out, problems of the registry. When the matches take most of the day, lower the quota.
- Below `minFreeDiskGB` (default 5) of free space, the ladder deletes the report and logs of the oldest matches, and logs a warning.

## Backups

Results are never deleted by the ladder (only the reports and logs of old matches: each team keeps those of its last `ladder.keepMatchFiles` matches), and each match is written durably before it is recorded: a crash, a restart of the service or of Docker, or a reboot loses at most the match being played. What remains is losing the VM or its disk: back up the data elsewhere.

Set `web.backupToken` to a long random string (e.g. `openssl rand -hex 24`), then have the backup machine fetch, for instance daily:

```sh
curl -fsS -OJ "https://<ladder>/backup?token=$TOKEN"
```

This downloads `ladder-backup-<UTC time>.tar`: a consistent snapshot of the database, taken while the ladder keeps running, and the build logs. Each archive is complete on its own, and small. `curl -f` fails on an error status; if the server fails midway, the archive is truncated, which `tar tf` detects.

What is backed up: the database (`ladder.db`, with every result), build logs, the last team states. Not backed up: match reports and bot logs (they are large, and expire anyway), so a restored ladder has no replays of past matches. Not backed up, because they can be rebuilt: repository mirrors and Docker images.

**Restoring**: stop the service, extract the latest archive into an empty `dataDir`, and start the service.

**Before upgrading the ladder**, take a backup: a new version may change the database (it also copies the database itself before migrating it).
