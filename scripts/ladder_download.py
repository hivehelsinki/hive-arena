#!/usr/bin/env python3
"""Downloads a team's ladder matches: results, replays and the bot's output.

    python3 scripts/ladder_download.py <ladder URL> <team> [directory]

For each match, in the directory (default: ladder-<team>):
  <id>.result.json   the results (always available)
  <id>.json.gz       the replay, for the viewer: go run ./viewer --file <file>
                     (gunzip it first), while the ladder still keeps it
  <id>.log           the team's bot output in the match

Run it again at any time: it only fetches the matches played since the last
one it has. The ladder keeps the replays of each team's last matches only,
so run it regularly to keep them all. Only uses the Python standard library.
"""

import json
import os
import sys
import urllib.error
import urllib.parse
import urllib.request


def get(url, gzip_ok=False):
    request = urllib.request.Request(url)
    if gzip_ok:
        request.add_header("Accept-Encoding", "gzip")
    with urllib.request.urlopen(request, timeout=120) as response:
        return response.read()


def save(path, data):
    # Under a temporary name first: an interrupted download leaves no
    # partial file behind
    with open(path + ".tmp", "wb") as f:
        f.write(data)
    os.replace(path + ".tmp", path)


def last_match(directory):
    ids = [int(name.split(".")[0]) for name in os.listdir(directory) if name.endswith(".result.json")]
    return max(ids, default=0)


def main():
    if len(sys.argv) < 3:
        print(__doc__)
        sys.exit(2)
    base, team = sys.argv[1].rstrip("/"), sys.argv[2]
    directory = sys.argv[3] if len(sys.argv) > 3 else "ladder-" + team
    os.makedirs(directory, exist_ok=True)

    after = last_match(directory)
    count = 0
    while True:
        query = urllib.parse.urlencode({"team": team, "after": after, "limit": 200})
        matches = json.loads(get(f"{base}/api/matches?{query}"))
        if not matches:
            break
        for match in matches:
            mid = match["id"]
            # Files first, the results last: they mark the match as done
            if not match["void"] and not match.get("filesDeleted") and match["turns"] > 0:
                try:
                    save(os.path.join(directory, f"{mid}.json.gz"), get(f"{base}/matches/{mid}/report.json", gzip_ok=True))
                    log = get(f"{base}/matches/{mid}/logs/{urllib.parse.quote(team)}")
                    save(os.path.join(directory, f"{mid}.log"), log)
                except urllib.error.HTTPError as e:
                    if e.code != 404:  # expired meanwhile, or no output
                        raise
            save(os.path.join(directory, f"{mid}.result.json"), json.dumps(match, indent=2).encode())
            after = max(after, mid)
            count += 1
        print(f"{count} matches downloaded...", file=sys.stderr)

    print(f"{count} new matches in {directory}")


if __name__ == "__main__":
    main()
