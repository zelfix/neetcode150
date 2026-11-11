#!/bin/sh
# Bring the study site up on a fresh server, or update one already running.
#
# The same command does both, which is the point: every step here checks what
# is already true before changing anything, so a second run is an update and
# not a surprise. In particular it will not reissue a join link that exists —
# that would lock out whoever is holding it — and it will not overwrite the API
# key or the database.
#
# What it deliberately does NOT do:
#
#   * install Go, Docker or a proxy. A deploy script that apt-installs a
#     container runtime is a deploy script that decides your box's security
#     posture for you; this one checks and tells you what is missing.
#   * touch the proxy configuration. You may already have one, and the two
#     settings that matter are explained in DEPLOY.md rather than generated
#     where nobody reads them.
#   * run the server inside a container. The server is a Docker *client* — it
#     starts a sandbox per test run, mounting host paths by absolute path — so
#     containerising it would mean handing it the host's Docker socket, which
#     is the same as handing it root. DEPLOY.md says more.
#
# Usage:
#   sudo deploy/install.sh --domain study.example.com
#   sudo deploy/install.sh --domain study.example.com --port 8150 --max-runs 2
#
# Run it from the checkout you want to serve: that directory becomes the
# service's working directory, and the service user is given ownership of it.

set -eu

DOMAIN=""
PORT=8150
MAX_RUNS=4
SVC_USER=nc
UNIT=/etc/systemd/system/neetcode150.service
DIR=$(unset CDPATH; cd -- "$(dirname -- "$0")/.." && pwd)

die() { printf '\nerror: %s\n' "$*" >&2; exit 1; }
step() { printf '\n== %s\n' "$*"; }
note() { printf '   %s\n' "$*"; }

while [ $# -gt 0 ]; do
	case "$1" in
	--domain) DOMAIN=${2:-}; shift 2 ;;
	--port) PORT=${2:-}; shift 2 ;;
	--max-runs) MAX_RUNS=${2:-}; shift 2 ;;
	--user) SVC_USER=${2:-}; shift 2 ;;
	-h | --help) sed -n '2,30p' "$0"; exit 0 ;;
	*) die "unknown option $1 (try --help)" ;;
	esac
done

[ -n "$DOMAIN" ] || die "--domain is required; the join links are printed with it and would otherwise point at 127.0.0.1"
[ "$(id -u)" = 0 ] || die "run this with sudo: it creates a system user and a systemd unit"

# ---------------------------------------------------------------- preflight

step "Checking what the box has"

[ -f "$DIR/go.mod" ] || die "$DIR does not look like the checkout (no go.mod)"

command -v go >/dev/null 2>&1 || die "Go is not installed. go.mod asks for 1.26; the sandbox pins GOTOOLCHAIN=local, so an older toolchain fails rather than quietly downloading one."
GOVER=$(go env GOVERSION 2>/dev/null || echo unknown)
note "go      $GOVER"
case "$GOVER" in
go1.2[6-9]* | go1.[3-9][0-9]* | go[2-9]*) ;;
*) die "Go $GOVER is too old; 1.26 or newer is required" ;;
esac

command -v docker >/dev/null 2>&1 || die "Docker is not installed. Test runs are containerised and the server refuses to start without it."
docker info >/dev/null 2>&1 || die "the Docker daemon is not reachable from root; start it first (systemctl start docker)"
note "docker  $(docker version --format '{{.Server.Version}}' 2>/dev/null || echo present)"

command -v systemctl >/dev/null 2>&1 || die "no systemd here; the unit below assumes it"
command -v runuser >/dev/null 2>&1 || die "runuser is missing (util-linux); it is how the build and fetch run as the service user rather than as root"
command -v curl >/dev/null 2>&1 || die "curl is missing; the script uses it to prove the site answers before it declares success"

if command -v ruff >/dev/null 2>&1; then
	note "ruff    $(ruff --version 2>/dev/null) — the editor's format key will work for Python"
else
	note "ruff    not installed — optional; the format key will say so for Python, and nothing else changes"
fi

# ------------------------------------------------------------- service user

step "Service user and ownership"

if id "$SVC_USER" >/dev/null 2>&1; then
	note "$SVC_USER exists"
else
	adduser --system --group --home "$DIR" "$SVC_USER" >/dev/null
	note "created $SVC_USER"
fi

if id -nG "$SVC_USER" 2>/dev/null | tr ' ' '\n' | grep -qx docker; then
	note "$SVC_USER can use docker"
else
	usermod -aG docker "$SVC_USER"
	note "added $SVC_USER to the docker group"
fi

chown -R "$SVC_USER:$SVC_USER" "$DIR"
# The service user has to be able to walk into the directory. A checkout under
# /root cannot be served however the ownership reads, and the failure would
# otherwise arrive as a permission error on the first test run.
if ! runuser -u "$SVC_USER" -- test -r "$DIR/go.mod"; then
	die "$SVC_USER cannot read $DIR/go.mod — move the checkout somewhere it can reach, such as /srv/neetcode150"
fi
note "$DIR owned by $SVC_USER"

as_svc() { runuser -u "$SVC_USER" -- "$@"; }

# ------------------------------------------------------------------- binary

step "Building the binary"
( cd "$DIR" && as_svc go build -o "$DIR/bin/nc" ./cmd/nc )
note "built $DIR/bin/nc"

# ------------------------------------------------------------------- images

step "Sandbox images"
build_image() {
	tag=$1 file=$2
	if docker image inspect "$tag" >/dev/null 2>&1; then
		note "$tag already built (rebuild by hand when internal/testkit or internal/testbank change)"
		return 0
	fi
	( cd "$DIR" && docker build -q -t "$tag" -f "$file" . >/dev/null )
	note "built $tag"
}
build_image nc-sandbox:go1.26 sandbox/Dockerfile
build_image nc-sandbox:py3.12 sandbox/Dockerfile.python

docker run --rm nc-sandbox:go1.26 go version >/dev/null || die "the Go sandbox image does not run"
docker run --rm nc-sandbox:py3.12 python3 -c 'import heapq' >/dev/null || die "the Python sandbox image does not run"
note "both images run"

# ------------------------------------------------------------------ content

step "Problem statements"
if [ -d "$DIR/content/problems" ] && [ "$(find "$DIR/content/problems" -name problem.md | head -1)" ]; then
	note 'already fetched; nc fetch only fills in what is missing, so run it by hand for a top-up'
else
	note "fetching — a few minutes"
	( cd "$DIR" && as_svc ./bin/nc fetch )
fi

# -------------------------------------------------------------------- key

step "API key"
ENVFILE=$DIR/env
if [ -f "$ENVFILE" ]; then
	note "$ENVFILE exists, left untouched"
else
	install -o "$SVC_USER" -g "$SVC_USER" -m 0600 /dev/null "$ENVFILE"
	printf 'DEEPSEEK_API_KEY=\n' > "$ENVFILE"
	chmod 0600 "$ENVFILE"
	chown "$SVC_USER:$SVC_USER" "$ENVFILE"
	note "wrote an empty $ENVFILE (mode 0600)"
fi
if ! grep -q '^DEEPSEEK_API_KEY=.\+' "$ENVFILE"; then
	note "no key set yet: the site will run with the tutor panel off until you put one in $ENVFILE"
fi

# ------------------------------------------------------------------- backup

step "Database"
DB=$DIR/study.sqlite3
if [ -f "$DB" ]; then
	# The schema migrates itself on open, forwards only, so a copy before a
	# restart is the only way back.
	mkdir -p /var/backups/neetcode150
	OUT=/var/backups/neetcode150/pre-deploy-$(date +%F-%H%M).sqlite3
	( cd "$DIR" && as_svc ./bin/nc backup "$OUT" ) >/dev/null
	chmod 0600 "$OUT"
	note "backed up to $OUT"
else
	note "none yet; it is created on first start"
fi

# --------------------------------------------------------------------- unit

step "systemd unit"
cat > "$UNIT" <<UNITEOF
[Unit]
Description=neetcode150 study site
After=network-online.target docker.service
Requires=docker.service

[Service]
User=$SVC_USER
Group=$SVC_USER
WorkingDirectory=$DIR
EnvironmentFile=$ENVFILE
# --host 127.0.0.1 keeps the process off the public interface: the proxy is the
# only way in. --sandbox docker is not "auto" on purpose — auto would quietly
# choose local if the daemon went missing, and local runs a guest's own code as
# this user, against this database.
ExecStart=$DIR/bin/nc serve \\
  --host 127.0.0.1 --port $PORT \\
  --tutor-provider deepseek \\
  --sandbox docker \\
  --max-runs $MAX_RUNS
Restart=on-failure
RestartSec=5s

[Install]
WantedBy=multi-user.target
UNITEOF
note "wrote $UNIT"

systemctl daemon-reload
systemctl enable neetcode150 >/dev/null 2>&1 || true
systemctl restart neetcode150
note "restarted"

# -------------------------------------------------------------------- check

step "Is it answering?"
i=0
until [ "$i" -ge 30 ]; do
	if [ "$(curl -s -o /dev/null -w '%{http_code}' --max-time 2 "http://127.0.0.1:$PORT/health" || true)" = 200 ]; then
		note "/health ok on 127.0.0.1:$PORT"
		break
	fi
	i=$((i + 1))
	sleep 1
done
[ "$i" -lt 30 ] || die "it did not answer in 30s — journalctl -u neetcode150 -n 50"

# A page that is not /health must be refused without a cookie. If this ever
# returns 200 the sign-in gate is gone, which is worth failing the deploy over.
CODE=$(curl -s -o /dev/null -w '%{http_code}' --max-time 2 "http://127.0.0.1:$PORT/" || true)
[ "$CODE" = 403 ] || die "GET / returned $CODE, expected 403 — every personal page must refuse a request with no cookie"
note "GET / refuses without a cookie, as it must"

# --------------------------------------------------------------------- link

step "Sign-in"
HAS_LINK=$( (cd "$DIR" && as_svc ./bin/nc user list 2>/dev/null || true) | awk '$2 == "owner" { print $3 }')
if [ "$HAS_LINK" = yes ]; then
	note "the owner already has a link; not reissuing, because that would invalidate the one you are holding"
	note "to replace it:  sudo -u $SVC_USER $DIR/bin/nc user link owner --base-url https://$DOMAIN"
else
	( cd "$DIR" && as_svc ./bin/nc user link owner --base-url "https://$DOMAIN" )
fi

cat <<DONEEOF

== Done

Still yours to do, once:

  1. Point a reverse proxy at 127.0.0.1:$PORT. Two of its settings are not
     optional and both fail in ways that look like something else — it must pass
     the original Host, and X-Forwarded-Proto. DEPLOY.md section 7 has working
     Caddy and nginx blocks.
  2. Invite people:
       sudo -u $SVC_USER $DIR/bin/nc user add dima --base-url https://$DOMAIN --budget 5
  3. Daily backup: DEPLOY.md section 9.

Re-running this script updates the deployment: it rebuilds, backs the database
up, and restarts, without touching the key, the links or anybody's work.
DONEEOF
