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
| `project-map.example.json` | A worked example: one project with GitHub, Gmail and Docs connections. | yes |

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
