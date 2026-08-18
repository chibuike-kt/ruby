# Ruby

WhatsApp-native financial assistant for informal traders. WhatsApp is
the interface; Postgres is the financial record.

## Stack

- Go 1.26.4
- PostgreSQL 18.4 (Docker)
- Redis 8.10.0 (Docker)
- Docker Desktop (Windows, WSL2 backend)

## Local setup

```
cp .env.example .env
# fill in WhatsApp Cloud API and AI provider credentials in .env

docker compose up -d --build
make migrate-up
make run
```

Before running `docker compose up`, make sure nothing else is bound to
port 6379 or 5432 on your machine (an old native Redis install, for
example) — Docker's containers will fail to bind if the port's taken.

## Common commands

| Command | What it does |
|---|---|
| `make run` | Run the API locally against Docker's Postgres/Redis |
| `make test` | Unit tests |
| `make test-race` | Full suite with `-race`, matches CI |
| `make lint` | golangci-lint, matches CI |
| `make migrate-new name=add_x` | New migration pair |

## Repo conventions

- Trunk-based: `main` is always deployable. Short-lived branches per
  slice (`feat/debt-creation`, `fix/idempotency-race`), merged via PR.
- Atomic commits — one logical change per commit, imperative subject
  line (`add payment idempotency check`, not `updates`).
- CI (`.github/workflows/ci.yml`) runs lint, vet, `go mod tidy` drift
  check, migrations, build, and `-race` tests against real Postgres/Redis
  service containers on every push and PR to `main`/`develop`.

See `CLAUDE.md` for constraints that apply when Claude Code is working
in this repo.
