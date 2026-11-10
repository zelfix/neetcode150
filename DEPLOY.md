# Running this for a few friends

A single instance, each person signing in with their own link, tests running in a
container. Written for a Debian or Ubuntu VPS with a domain and TLS.

Nothing here is about the owner's own machine: keep using `nc serve` locally as before.
The server gets its **own database**, so the schema migration never runs against your
real study data and a bug on the server cannot reach it.

---

## 1. What the box needs

- **Go 1.26 or newer.** `go.mod` asks for `go 1.26.0`, and the sandbox pins
  `GOTOOLCHAIN=local`, so an older toolchain fails rather than quietly downloading one.
- **Docker**, with the service user able to use it (`usermod -aG docker nc`). Test runs
  will not start without it.
- **A reverse proxy with TLS** (Caddy or nginx below).
- **About 4 GB of RAM and 2 vCPU** for ten people. Each test run is capped at 1 GiB and
  one CPU, four may run at once by default, and the tutor's live sessions are a separate
  pool — see step 6 if the box is smaller.
- **Disk:** the sandbox image is ~1.3 GB, plus ~60 MB of build cache per person.

```sh
adduser --system --group --home /srv/neetcode150 nc
usermod -aG docker nc
```

## 2. Get the code and the content

The problem statements are not in the repository — they are fetched, and `solutions/`,
`_work/` and the database never leave the machine they are on.

```sh
sudo -u nc -H bash
cd /srv/neetcode150
git clone https://github.com/zelfix/neetcode150.git .
go build -o /srv/neetcode150/bin/nc ./cmd/nc
./bin/nc fetch            # a few minutes; re-runs only fill in what is missing
```

## 3. Build the sandbox images

```sh
docker build -t nc-sandbox:go1.26 -f sandbox/Dockerfile .
docker build -t nc-sandbox:py3.12 -f sandbox/Dockerfile.python .
```

The second one is for people solving in Python. It is optional: without it the server
starts, says so, and offers Python nowhere. It never falls back to running Python
unisolated.

This compiles a package shaped like a generated practice test so the image carries a warm
build cache: a cold run then takes about a second instead of four. Rebuild it when
`internal/testkit` or `internal/testbank` change — staleness costs speed, never
correctness, because the source is mounted from disk at run time.

Check it before going further. The server refuses to start without it anyway:

```sh
docker run --rm nc-sandbox:go1.26 go version
docker run --rm nc-sandbox:py3.12 python3 -c 'import heapq; print("ok")'
```

## 4. The API key

The tutor on a shared instance must be the `deepseek` provider. The CLI providers run an
agent with the repository as its working directory, so a guest's tutor there could read
every other person's solutions and the whole study database; the site refuses them for
that reason.

```sh
install -o nc -g nc -m 0600 /dev/null /srv/neetcode150/env
printf 'DEEPSEEK_API_KEY=sk-...\n' > /srv/neetcode150/env
```

## 5. Issue the links

Everyone signs in by opening a link once. There are no passwords, and **no exception for
the owner** — a request with no cookie is refused whoever it is from, because any
exception would have to trust either the listening address or a forwarded-for header, and
behind a proxy every request looks local.

Pass your real address, or the printed links will point at `127.0.0.1`:

```sh
./bin/nc user link owner  --base-url https://study.example.com
./bin/nc user add  dima   --base-url https://study.example.com --budget 5
./bin/nc user list
```

Only the hash of a token is stored, so a link can be reissued but never shown again.
`nc user link <name>` issues a new one and invalidates the old; `nc user revoke <name>`
locks somebody out while keeping their work; `nc user rm <name> --yes` deletes them and
everything of theirs.

`--budget 5` is a monthly ceiling in dollars, checked before each turn and reset on the
1st. The owner's row has no ceiling.

## 6. The service

```ini
# /etc/systemd/system/neetcode150.service
[Unit]
Description=neetcode150 study site
After=network-online.target docker.service
Requires=docker.service

[Service]
User=nc
Group=nc
WorkingDirectory=/srv/neetcode150
EnvironmentFile=/srv/neetcode150/env
ExecStart=/srv/neetcode150/bin/nc serve \
  --host 127.0.0.1 --port 8150 \
  --tutor-provider deepseek \
  --tutor-rates /srv/neetcode150/deepseek-rates.json \
  --sandbox docker \
  --max-runs 4
Restart=on-failure
RestartSec=5s

[Install]
WantedBy=multi-user.target
```

`--host 127.0.0.1` keeps the process off the public interface; the proxy is the only way
in. Lower `--max-runs` on a small box: four concurrent runs at a CPU each, plus up to six
live tutor sessions, will oversubscribe two vCPU.

Do not use `--sandbox local` or `--sandbox auto` here. `local` runs a guest's own Go as
the service user, with the service user's filesystem and database — the server refuses to
start that way once a guest exists, and `auto` would silently choose it if Docker were
missing.

```sh
systemctl enable --now neetcode150
systemctl status neetcode150
```

The startup lines say what it chose:

```
neetcode150 on http://127.0.0.1:8150  (150 problems, Ctrl-C to stop)
sandbox: nc-sandbox:go1.26, 4 at a time; python: nc-sandbox:py3.12
3 people, 3 of them with a link.
tutor panel: deepseek, tutor mode
```

If it says `No one has a link yet`, go back to step 5 — every page will refuse.

## 7. The proxy

Two settings are not optional, and both fail in ways that look like something else.

**Pass the original `Host`.** Mutations are accepted only from this site's own pages, and
that check compares the `Origin` header against the host the client asked for. A proxy
that rewrites `Host` makes every save, run and rating fail with "this endpoint only
accepts requests from the study site itself", while plain pages keep working — so it
reads as a broken editor rather than a proxy problem.

**Pass `X-Forwarded-Proto`.** The sign-in cookie is marked `Secure` only when the request
knows it arrived over HTTPS. Without this header the cookie goes out unmarked.

And event streams must not be buffered, or the tutor's replies arrive all at once when
the turn ends.

### Caddy

Caddy gets all three right by default, including the TLS certificate:

```
study.example.com {
	reverse_proxy 127.0.0.1:8150 {
		flush_interval -1          # do not buffer the tutor's event stream
	}
}
```

### nginx

```nginx
server {
    listen 443 ssl http2;
    server_name study.example.com;

    ssl_certificate     /etc/letsencrypt/live/study.example.com/fullchain.pem;
    ssl_certificate_key /etc/letsencrypt/live/study.example.com/privkey.pem;

    location / {
        proxy_pass http://127.0.0.1:8150;

        # Required: the same-origin check compares Origin against this.
        proxy_set_header Host              $host;
        # Required: without it the sign-in cookie is not marked Secure.
        proxy_set_header X-Forwarded-Proto $scheme;
        proxy_set_header X-Real-IP         $remote_addr;

        # The tutor streams its reply; buffering holds it until the turn ends.
        proxy_buffering    off;
        proxy_cache        off;
        proxy_read_timeout 1h;
    }
}
```

## 8. Check it end to end

From your own machine, not the server:

```sh
# The process is up. This is the one page that answers without a cookie and
# says nothing, so point a monitor at it.
curl -s https://study.example.com/health                                     # ok
# No cookie: every personal page is refused, and says what to do about it.
curl -s -o /dev/null -w '%{http_code}\n' https://study.example.com/          # 403
# Stylesheets still load, or the refusal page renders unstyled.
curl -s -o /dev/null -w '%{http_code}\n' https://study.example.com/static/app.css  # 200
```

Then in a browser: open your own join link, confirm it lands on the dashboard, press
**Start** on a problem, write something wrong, **Run tests** and see it fail, fix it and
see it pass. Ask the tutor one question. Then open a friend's link in a different browser
profile and confirm their dashboard is empty — that is the whole point of the exercise.

## 9. Backups

Everything personal is in one SQLite file, and it is in WAL mode, so do not copy it as a
single file.

```sh
# /etc/cron.daily/neetcode150-backup
#!/bin/sh
cd /srv/neetcode150 && ./bin/nc backup /var/backups/neetcode150/study-$(date +%F).sqlite3
```

`nc backup` runs an integrity check first and writes a consistent copy with
`VACUUM INTO`. It is whole-database, token hashes included, so it is yours to keep
rather than something to hand out. When one person wants their own work back:

```sh
./bin/nc export dima /tmp/dima.json
```

That is their progress, notes, archived attempts, test runs and tutor transcripts as
plain JSON, keyed by table and column, and nothing of anybody else's.

Not in the backup, and not needing one: `content/` (`nc fetch` rebuilds it), the sandbox
image, `.nc-cache/`. **`solutions/` and `_work/` are** people's working files — the
submitted versions are archived in the database, but whatever is mid-edit is not, so
include them if you care about that.

## 10. Updating

```sh
sudo -u nc -H bash -c 'cd /srv/neetcode150 && ./bin/nc backup /var/backups/neetcode150/pre-update.sqlite3'
sudo -u nc -H bash -c 'cd /srv/neetcode150 && git pull && go build -o bin/nc ./cmd/nc'
sudo -u nc -H bash -c 'cd /srv/neetcode150 && docker build -t nc-sandbox:go1.26 -f sandbox/Dockerfile .'
sudo -u nc -H bash -c 'cd /srv/neetcode150 && docker build -t nc-sandbox:py3.12 -f sandbox/Dockerfile.python .'
systemctl restart neetcode150
```

Back up first: the database migrates itself on open, forwards only.

---

## What to expect it to cost

At the measured rates, the tutor is not the expensive part. Ten people doing about four
problems a week each, on `deepseek-pro`, comes to roughly **$6 a month**; the `$5` per
person ceiling is there to stop a runaway conversation, not as a real limit. The VPS is
the bill.

## When DeepSeek changes its prices

The allowance is computed from the price list compiled into the binary, which is dated in
`internal/tutor/deepseek.go`. When the vendor publishes new numbers, write them to a file
and start the server with `--tutor-rates`:

```json
{
  "deepseek-pro":   {"peak":     {"cache_hit": 0.044, "cache_miss": 1.32, "output": 3.96},
                     "off_peak": {"cache_hit": 0.022, "cache_miss": 0.66, "output": 1.98}},
  "deepseek-flash": {"peak":     {"cache_hit": 0.006, "cache_miss": 0.30, "output": 1.20},
                     "off_peak": {"cache_hit": 0.003, "cache_miss": 0.15, "output": 0.60}}
}
```

Prices are dollars per million tokens. Every number must be present and positive; a model
not in the list is charged as the dearest one listed, so a renamed model costs more on
paper rather than less. The peak window (01:00–04:00 and 06:00–10:00 UTC on weekdays) is
still in the code; if the vendor moves it, that is a rebuild.

## Known limits

- **The tutor has not been evaluated against this protocol.** Whether `deepseek-pro`
  actually holds the hint ladder — reveals one rung per request, and does not hand over a
  solution — is unverified. That matters more than the money: a tutor that leaks the
  answer does not merely teach worse, it destroys the signal the review schedule runs on.
  The 12 sessions already recorded in your own database are a baseline to compare against.
- **`--tutor-budget` is a different thing** from `nc user add --budget`: the first caps a
  single Claude CLI session, the second is the monthly per-person ceiling used here. The
  server refuses the flag on a non-Claude provider so they cannot be confused silently.
