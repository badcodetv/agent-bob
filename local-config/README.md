# local-config/

Operator configuration for the compose stack that must not be committed. Docker
Compose mounts this folder **read-only** into `agentd` at `/etc/agent-bob/`, and
loads `connections.env` from it as an optional `env_file`. Everything in here is
gitignored except this README and `project-map.example.json`.

The full guide, including how to obtain each token by hand, is
[`docs/22-connections.md`](../docs/22-connections.md). The design is
`design/2026-09-11-project-connections.md`.

## What goes here

| File | What | Committed? |
| --- | --- | --- |
| `project-map.json` | The project map: users → projects, and per project its API key variable, framing origins and **connections**. Names environment variables; never holds a secret. | no |
| `connections.env` | The tokens the project map names, one `NAME=value` per line, any names you like. | no |
| `project-map.example.json` | A worked example: `wolf` with GitHub, Gmail and Docs connections on env-var tokens, and `enc` with Gmail and Drive on a Connect Google account. | yes |

## Setting it up

1. `cp local-config/project-map.example.json local-config/project-map.json` and edit it.
2. Put the tokens it names in `local-config/connections.env`:

   ```sh
   WOLF_GITHUB_PAT=github_pat_...
   GOOGLE_CLIENT_SECRET=...
   KAI_GOOGLE_REFRESH=...
   ```

3. In `.env`, point agentd at the mounted file:

   ```sh
   AGENTKIT_PROJECT_MAP_FILE=/etc/agent-bob/project-map.json
   ```

4. `docker compose up -d agentd`. Connections are read **once, at boot**: after
   editing either file, restart agentd. Its log prints one line per project
   listing each connection as `available` or `UNAVAILABLE (env var X is not set)`.

## Connect Google (a button instead of a hand-held token)

A connection with `"auth": {"type": "google_account"}` (see the `enc` project in
`project-map.example.json`) gets its credential from the **Connect Google**
button in that project's Settings, not from a variable. Everyone named in the
project's `operators` list (and each must also be in `users` for that project)
may press it. Connections that name the same `account` (default `"google"`)
share one sign-in. agentd needs, once:

- **`AGENTKIT_CONNECTIONS_KEY`** in `connections.env` — the key the stored
  refresh token is encrypted under in Postgres. Generate it with
  `openssl rand -base64 32`. It must differ from `AGENTKIT_JWT_SECRET` (agentd
  refuses to boot otherwise, and on a malformed value). **Losing or changing it
  means every project has to press Connect Google again** — the stored tokens
  can no longer be decrypted. Keep it with your other secrets.
- **`GOOGLE_CLIENT_SECRET`** in `connections.env` — the secret of the same
  OAuth client as Google login.
- **`GOOGLE_CLIENT_ID`** in `.env` (not here: compose already lists it under
  agentd's `environment:`, see the trap below).
- On that OAuth client in the Google Cloud console, the authorized redirect URI
  `<public base URL>/auth/connections/google/callback` — locally
  `http://localhost:8080/auth/connections/google/callback`.

```sh
# local-config/connections.env
AGENTKIT_CONNECTIONS_KEY=<output of openssl rand -base64 32>
GOOGLE_CLIENT_SECRET=...
```

Never add `AGENTKIT_CONNECTIONS_KEY` or `GOOGLE_CLIENT_SECRET` to agentd's
`environment:` in `docker-compose.yml`: a name listed there overrides this file
and is blanked when `.env` leaves it unset. agentd's boot log says
`connect google: enabled (redirect …)` or `connect google: DISABLED (<reason>)`.
Unlike the rest of this folder, connecting needs **no restart**.

## Two traps

- **Do not also set `AGENTKIT_PROJECT_MAP`** (the inline form). It wins, this
  file is ignored entirely, and agentd logs a `WARNING` saying so.
- **Do not put a variable in `connections.env` that `docker-compose.yml` already
  lists under agentd's `environment:`** — `GOOGLE_CLIENT_ID`, `WOLF_API_KEY`,
  `ANTHROPIC_API_KEY` and the rest. Compose's `environment:` overrides
  `env_file`, so the value here is silently replaced (usually by a blank from
  `.env`). Set those in `.env`. `GOOGLE_CLIENT_ID` in the example is one of them.

## What never happens

The tokens stay in agentd. A session container gets an MCP entry pointing at
agentd's `/connect/<name>/` with its own session token; agentd checks the
worker's grant on every request and swaps in the real credential. Nothing in
this folder is copied into a container, the database or the git projection.
The one credential that does reach the database is a Connect Google refresh
token, and only encrypted under `AGENTKIT_CONNECTIONS_KEY`, which never leaves
agentd's environment: a database backup on its own cannot open it.
