# WhatsApp Campaign Platform

Bulk WhatsApp campaigns on top of [Bird](https://bird.com) for a retail client (about 2,500 customers, Qatar). The client imports and groups customers, picks an approved template, and sends or schedules a campaign. Every campaign reports delivered, read, clicked, failed, replies and actual cost. STOP opt-outs are always respected.

Scope: `WhatsApp-Campaign-Platform-Scope-of-Work.pdf`.

```
backend/    Go 1.26 · Gin · PostgreSQL (pgx) — one binary: server | worker | all | migrate | create-admin
frontend/   React 19 · TypeScript · Vite · Tailwind · TanStack Query
deploy/     Caddyfile (HTTPS); backups land in deploy/backups
docs/       BIRD_SETUP.md (runbook) · HANDOVER.md · CLIENT_GUIDE.md
samples/    example contacts CSV
```

## Local development

```bash
make dev-db                      # Postgres in Docker on :5433
make admin EMAIL=you@example.com # prints a generated password
make dev-api                     # API + worker on :8080 (APP_ENV=development)
make dev-web                     # http://localhost:5173
```

Without `BIRD_API_KEY` everything works except sending and syncing. For real sends, export `BIRD_API_KEY=bk_eu1_…` before `make dev-api`. To receive webhooks locally, expose port 8080 with a tunnel (e.g. `cloudflared tunnel --url http://localhost:8080`) and point a Bird webhook at `https://…/webhooks/bird`.

## Tests

```bash
make test       # go vet, unit tests (phone normalisation, webhook signatures, template building), TS typecheck
make test-e2e   # acceptance tests against real Postgres and a fake Bird API
```

`internal/worker/e2e_test.go` covers the scope's acceptance tests:

| Acceptance test | Covered by |
|---|---|
| Import 2,500 rows; duplicates and invalid flagged | 2,500-row CSV with 30 duplicates and 20 invalid numbers |
| 10-person campaign; delivered, read, clicks in report | webhooks (including out of order), click redirect, crawler ignored |
| STOP reply excluded from next campaign | inbound `stop` → opted out, synced to Bird, excluded |
| Scheduled campaign starts on time | Asia/Qatar input, not started early, starts when due |
| Worker stopped mid-campaign → no double sends | crash after Bird accepted but before the result was saved, plus a stop/restart |
| Cost estimate matches actual | estimate vs. per-message cost from Bird |

The tests also cover pause/resume/cancel, retry on temporary errors, staff role limits, login rate limiting, and rejection of bad webhook signatures.

## How the important parts work

- **Sending** (`internal/worker/sender.go`): only the worker sends, never a web request. A campaign's recipients are frozen into `campaign_recipients` (unique per campaign + contact) when it starts. Each row is claimed with `FOR UPDATE SKIP LOCKED` and moves `pending → sending → accepted → sent → delivered → read`. One request per recipient to `POST /v1/whatsapp/messages`, throttled to the plan limit from Settings.
- **No double sends**: every send carries `Idempotency-Key: campaign/{id}/recipient/{id}`. If the worker dies mid-request, the row is retried on restart with the same key, and Bird replays the original result instead of sending again. Rows stuck longer than Bird's ~3h key window become `unknown` and are never re-sent. A Postgres advisory lock allows only one active worker.
- **Retries**: only temporary errors (network, 5xx, 429, idempotency in progress), with backoff 30s → 2m → 8m → 32m, up to 5 attempts. 422s (invalid recipient, template mismatch) fail immediately. 401/402 (bad key, empty wallet) pause the campaign with a reason.
- **Opt-outs**: checked when the campaign starts and again right before each send. They come from STOP keywords (Settings), Bird `preference.revoked` / `whatsapp_suppression.created` webhooks, suppression rejections, a manual action, or Contacts > Sync opt-outs.
- **Webhooks** (`/webhooks/bird`): Standard Webhooks HMAC signature checked against the raw body (±5 min), stored, answered 200 immediately, deduplicated on `webhook-id`, and processed by the worker. Events map back to recipients through the `recipient_id` tag on each send.
- **Click tracking**: the template's URL button is `https://DOMAIN/r/{{1}}`. Each send fills `{campaign_id}/{random token}`. `/r/...` records the click (link-preview bots ignored) and redirects to the campaign's shop link. Only stored campaign URLs are used, so it can't be abused as an open redirect.
- **Cost**: estimate = eligible recipients × the marketing rate setting (snapshotted at launch). Actual cost = sum of each message's `cost.amount`, fetched from `GET /v1/whatsapp/messages/{id}` (webhooks carry no cost).
- **Security**: bcrypt passwords; httpOnly SameSite=Strict session cookie plus a required `X-Requested-With` header against CSRF; login rate limits per email+IP and per IP; sessions invalidated on password change; roles enforced server-side; secrets only in env; CSV exports protected against formula injection.

## Deploy

See `docs/HANDOVER.md`. In short, on an Ubuntu VPS with Docker: `cp .env.example .env`, fill it in, `docker compose up -d --build`, then `docker compose exec api /app/app create-admin -email client@example.com`.
