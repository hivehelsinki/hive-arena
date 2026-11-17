#!/bin/sh
# Sets up the ladder on its machine, or updates it, then (re)starts the service.
#
#   cd /opt/hive-arena && sudo ladder/deploy/setup.sh [--force]
#
# Run it after the first clone, and after every update: it is safe to run
# again at any time. It:
#   - installs the missing tools (Debian/Ubuntu), creates the service user
#   - pulls the latest commit of the current branch
#   - builds the ladder, the browser viewer and the arena server image
#   - creates ladder.json on the first run (then stops, so you can edit it)
#   - installs and (re)starts the systemd service, and checks that it runs
#     the commit that was just pulled
#
# --force: restart even if a ladder run is in progress (it is then marked
# incomplete, and its recorded matches are kept).

set -eu

SERVICE=hive-ladder
SERVICE_USER=ladder
FORCE=0
[ "${1:-}" = "--force" ] && FORCE=1

REPO=$(cd "$(dirname "$0")/../.." && pwd)
CONFIG=$REPO/ladder.json
LADDER=$REPO/ladder/ladder

# sudo resets PATH: add the usual locations of Go
export PATH="$PATH:/usr/local/go/bin:/usr/lib/go/bin:/snap/bin"

step() { printf '\n\033[1m==> %s\033[0m\n' "$*"; }
fail() { printf '\n\033[1;31mERROR: %s\033[0m\n' "$*" >&2; exit 1; }

[ "$(id -u)" = 0 ] || fail "run as root: sudo $0"
cd "$REPO"

# Files of the repository stay owned by the user who cloned it
OWNER=$(stat -c %U "$REPO")
OWNER_HOME=$(getent passwd "$OWNER" | cut -d: -f6)
as_owner() {
	if [ "$OWNER" = root ]; then
		"$@"
	else
		runuser -u "$OWNER" -- env HOME="$OWNER_HOME" PATH="$PATH" "$@"
	fi
}

# Go version required by go.mod
GO_REQUIRED=$(sed -n 's/^go \([0-9.]*\)$/\1/p' go.mod)

# Whether version $1 is at least version $2
version_at_least() {
	[ "$(printf '%s\n%s\n' "$2" "$1" | sort -V | head -n1)" = "$2" ]
}

# Installs the official Go release required by go.mod in /usr/local/go
install_go() {
	arch=$(dpkg --print-architecture 2>/dev/null || uname -m)
	case $arch in
		amd64 | x86_64) arch=amd64 ;;
		arm64 | aarch64) arch=arm64 ;;
	esac
	echo "Installing Go $GO_REQUIRED in /usr/local/go"
	curl -fsSL "https://go.dev/dl/go$GO_REQUIRED.linux-$arch.tar.gz" -o /tmp/go.tar.gz
	rm -rf /usr/local/go
	tar -C /usr/local -xzf /tmp/go.tar.gz
	rm /tmp/go.tar.gz
	export PATH="/usr/local/go/bin:$PATH"
}

step "Tools"
missing=""
for tool in git docker curl gzip; do
	command -v "$tool" >/dev/null 2>&1 || missing="$missing $tool"
done
if [ -n "$missing" ]; then
	command -v apt-get >/dev/null 2>&1 || fail "missing:$missing. Install them, then run this script again."
	echo "Installing:$missing"
	packages=""
	for tool in $missing; do
		case $tool in
			docker) packages="$packages docker.io" ;;
			*) packages="$packages $tool" ;;
		esac
	done
	apt-get update -q
	# shellcheck disable=SC2086
	apt-get install -y -q $packages
fi
current_go=$(go env GOVERSION 2>/dev/null | sed 's/^go//') || true
if [ -z "$current_go" ] || ! version_at_least "$current_go" "$GO_REQUIRED"; then
	install_go
fi
systemctl enable --now docker >/dev/null 2>&1 || true
docker info >/dev/null 2>&1 || fail "docker is installed but not running"
echo "git, go ($(go env GOVERSION)), docker ($(docker version --format '{{.Server.Version}}')): ok"

if [ -z "${SETUP_UPDATED:-}" ]; then
	step "Update"
	if [ -n "$(as_owner git status --porcelain --untracked-files=no)" ]; then
		as_owner git status --short --untracked-files=no
		fail "the repository has local changes: commit or discard them first"
	fi
	before=$(as_owner git rev-parse HEAD)
	as_owner git pull --ff-only
	after=$(as_owner git rev-parse HEAD)

	# This script may have changed: run the new version
	if ! as_owner git diff --quiet "$before" "$after" -- ladder/deploy/setup.sh; then
		echo "The setup script was updated, running the new version"
		SETUP_UPDATED=1 exec "$0" "$@"
	fi
fi
COMMIT=$(as_owner git rev-parse HEAD)
echo "At $(as_owner git log -1 --format='%h %s (%cr)')"

step "Service user"
if ! id "$SERVICE_USER" >/dev/null 2>&1; then
	useradd --system --create-home --shell /usr/sbin/nologin "$SERVICE_USER"
	echo "Created user $SERVICE_USER"
fi
usermod -aG docker "$SERVICE_USER"
echo "$SERVICE_USER, in the docker group: ok"

step "Build"
as_owner go build -o "$LADDER" ./ladder
echo "ladder: $("$LADDER" version)"
as_owner viewer/build-web.sh
docker build -q -t hive-arena-server . >/dev/null
echo "Docker image hive-arena-server: ok"
docker image prune -f >/dev/null

step "Configuration"
if [ ! -f "$CONFIG" ]; then
	token=$(od -An -N24 -tx1 /dev/urandom | tr -d ' \n')
	sed "s/\"backupToken\": \"\"/\"backupToken\": \"$token\"/" ladder/ladder.example.json > "$CONFIG"
	chown "root:$SERVICE_USER" "$CONFIG"
	chmod 640 "$CONFIG"
	echo "Created $CONFIG, with a random backup token."
	echo "Edit it (at least registry.url, and the schedule), then run this script again."
	exit 1
fi
checked=$("$LADDER" check -config "$CONFIG") || fail "invalid configuration in $CONFIG"
LISTEN=$(echo "$checked" | sed -n 's/^listen //p')
DATA=$(echo "$checked" | sed -n 's/^dataDir //p')
PORT=${LISTEN##*:}
mkdir -p "$DATA"
chown -R "$SERVICE_USER:$SERVICE_USER" "$DATA"
chown "root:$SERVICE_USER" "$CONFIG"
chmod 640 "$CONFIG"
echo "Website on port $PORT, data in $DATA: ok"

step "Service"
cat > "/etc/systemd/system/$SERVICE.service" <<EOF
# Written by $REPO/ladder/deploy/setup.sh: changes are overwritten
[Unit]
Description=Hive Arena ladder
After=docker.service network-online.target
Requires=docker.service

[Service]
User=$SERVICE_USER
Group=$SERVICE_USER
SupplementaryGroups=docker
WorkingDirectory=$REPO
ExecStart=$LADDER -config $CONFIG
Restart=on-failure
RestartSec=10
# Lets the current match clean up its containers
KillSignal=SIGINT
TimeoutStopSec=60
# Allows listening on ports below 1024
AmbientCapabilities=CAP_NET_BIND_SERVICE

[Install]
WantedBy=multi-user.target
EOF
systemctl daemon-reload
systemctl enable "$SERVICE" >/dev/null 2>&1

status_url="http://127.0.0.1:$PORT/api/status"
if systemctl is-active --quiet "$SERVICE" && [ "$FORCE" = 0 ]; then
	if curl -fsS "$status_url" 2>/dev/null | grep -q '"currentRun"'; then
		if [ -t 0 ]; then
			printf "A ladder run is in progress. Restart anyway (it will be marked incomplete)? [y/N] "
			read -r answer
			[ "$answer" = y ] || [ "$answer" = Y ] || fail "not restarted: run this script again after the run, or with --force"
		else
			fail "a ladder run is in progress: run this script again after it, or with --force"
		fi
	fi
fi
systemctl restart "$SERVICE"

step "Check"
revision=""
for _ in $(seq 30); do
	revision=$(curl -fsS "$status_url" 2>/dev/null | sed -n 's/.*"revision":"\([^"]*\)".*/\1/p') || true
	[ -n "$revision" ] && break
	sleep 1
done
[ -n "$revision" ] || fail "the ladder does not answer on $status_url: see journalctl -u $SERVICE"

health=$(curl -sS "http://127.0.0.1:$PORT/health" 2>/dev/null || true)
echo "Repository:     $COMMIT"
echo "Ladder running: $revision"
echo "Health:         $health"
[ "$revision" = "$COMMIT" ] || fail "the running ladder is not the commit of the repository"

printf '\n\033[1;32mThe ladder is up to date and running.\033[0m\n'
echo "Logs: journalctl -u $SERVICE -f"
echo "Start a run now: systemctl kill -s USR1 $SERVICE"
