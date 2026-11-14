# Runnel dashboard

A small web UI for one or more Runnel servers. It shows instances, namespaces, queues, pools with the slots in use, schedules, events, jobs, memory and storage. It can cancel and retry jobs, pause or resume queues and pools, and change their rate and burst.

It is one Go binary with the pages and htmx built in. There is no Node, no build step and no database. The browser never sees a Runnel password: the dashboard keeps it and calls Runnel itself.

## Run it

1. Make a password hash:

   ```sh
   go run ./cmd/runnel-dashboard hash
   ```

2. Copy `dashboard.example.yaml` to `dashboard.local.yaml` and set the user, the hash and your Runnel instances.

3. Start it:

   ```sh
   RUNNEL_ADMIN_TOKEN=$(openssl rand -hex 24) docker compose -f compose.example.yml up --build
   ```

   Open http://localhost:8710.

To run it without Docker: `go run ./cmd/runnel-dashboard serve -config dashboard.local.yaml`.

## Config

Every setting has a default. Only `users` and `instances` are required. See `dashboard.example.yaml` for the full list.

| Key | Default | What it does |
|---|---|---|
| `listen` | `:8710` | Address to listen on |
| `public_url` | none | The origin users open, such as `https://dash.example.com`. Set it when a proxy changes the Host header |
| `secure_cookies` | `true` | Cookies only over https (localhost counts as secure in browsers). Set `false` only for plain http on a private network |
| `tls_cert`, `tls_key` | none | Serve https directly. Otherwise put a TLS proxy in front |
| `client_ip_header` | none | Header with the client IP from your proxy, such as `X-Forwarded-For`. Leave empty when nothing sits in front, or clients can fake their IP |
| `session_ttl` | `12h` | A session ends this long after login |
| `idle_timeout` | `1h` | A session ends after this long without use |
| `refresh` | `5s` | How often pages update. At least `1s` |
| `running_scan_limit` | `10000` | Most running jobs read to count pool slots. `0` turns counting off |
| `page_size` | `50` | Jobs and events per page |
| `max_response_bytes` | `16 MiB` | Largest answer read from Runnel |
| `login.max_attempts`, `login.window` | `5`, `15m` | Failed logins allowed per IP and per user name before logins are blocked for the window |
| `http.*` | see the example | Server timeouts and the request body cap |

Each instance has a `name`, a `timeout` (default `10s`) and a connection `url`, like a Redis or Postgres URL:

```yaml
instances:
  - name: local
    url: http://localhost:8700                                # Runnel with server.auth: none
  - name: prod
    url: https://admin:${PROD_PASSWORD}@runnel.internal:8700  # user admin, the password is Runnel's admin token
  - name: own-ca
    url: https://admin:${PASS}@runnel.internal:8700?ca=/certs/ca.pem
  - name: mtls
    url: https://runnel.internal:8700?ca=/certs/ca.pem&cert=/certs/dash.pem&key=/certs/dash.key
  - name: whole
    url: ${STAGING_URL}                                        # the whole URL from one variable
```

- `${NAME}` is replaced by the environment variable `NAME`, so passwords can stay out of the file. Inside a URL the value is escaped, so any character is safe. A variable that fills the whole `url` is used as it is. An empty or missing variable stops the start.
- `ca` trusts your own CA. `cert` and `key` send a client certificate, which Runnel accepts as admin when it has `server.tls.client_ca`. They need https.
- The dashboard needs admin access: listing namespaces and reading memory and storage need it.
- Needs Runnel 0.0.3-beta.2 or newer: pause, resume and settings send `PATCH` with `If-Match`.

## Changing settings

Each queue and pool has a Settings page. It changes the start rate and burst, and a pool can be paused for a while, such as `30m`. Pool slots are read-only: the app that owns a pool sets them.

Saving sends only these fields as a merge patch, with the `ETag` the page was loaded with. Fields set elsewhere stay as they are. If someone changed the setting after the page loaded, nothing is saved and the page asks you to reload. Pause and resume change only `paused` (and `paused_until` on resume), so an app that sets slots at the same time keeps the pause.

## Security

- **Passwords** are bcrypt hashes, cost 12. A login for an unknown user still checks a hash, so response times do not reveal which users exist.
- **Sessions:**
  - A session ID is 32 random bytes, kept only in memory, so a restart signs everyone out.
  - The cookie is `__Host-runnel_dash`, set HttpOnly, Secure and SameSite=Strict.
  - Logging in creates a new session, and logging out deletes it on the server.
- **Cross-site requests:**
  - Every POST must come from the same origin; Go's `CrossOriginProtection` checks `Sec-Fetch-Site` and `Origin`.
  - It must also carry the session's CSRF token.
- **Failed logins** are counted per IP and per user name and blocked once over the limit. Logins, logouts and every action are logged with the user name.
- **Pages:**
  - Pages are rendered by Go's `html/template`, so job payloads and errors are always escaped.
  - The Content Security Policy allows only the dashboard's own scripts and styles, with no inline code and no eval.
  - htmx runs with eval turned off. Its file is part of the binary, and a test checks its hash.
- **Calls to Runnel:**
  - The dashboard calls only a fixed list of Runnel endpoints.
  - Names in URLs are checked before use.
  - Answers are capped in size.
  - Passwords from the instance URLs never show in pages, logs or errors.
  - Use https to Runnel whenever it is not on the same host or a private network: Basic auth over plain http sends the password in clear text.

Run it behind TLS (`tls_cert` or a proxy) whenever it is reachable beyond localhost.

## Develop

```sh
make test     # small, runs with one CPU at low priority
make vet
make build
make docker
```

## License

Apache 2.0. Copyright 2026 The Runnel Authors.
