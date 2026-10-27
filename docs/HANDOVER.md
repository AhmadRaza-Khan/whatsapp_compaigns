# Handover: WhatsApp Campaign Platform

Fill in every blank before handing over. Keep secrets in a password manager, not in this file.

## Bird

| Item | Value |
|---|---|
| Organization / workspace | |
| Region | eu1 |
| Plan | Startup / Growth |
| Owner (billing) | client: |
| Cyberboost member | |
| WABA ID | |
| Phone number (E.164) | |
| Phone number ID | |
| Messaging limit at go-live | |
| API key name (value in password manager) | campaign-platform |
| Webhook ID | |
| Webhook URL | https://DOMAIN/webhooks/bird |
| Template slugs | collection_launch, … |

## Hosting

| Item | Value |
|---|---|
| VPS provider / account owner | |
| Server IP | |
| SSH access | user@IP (key held by …) |
| Domain / DNS provider | |
| App URL | https://DOMAIN |
| Code repository | |
| Install path on server | /opt/wacampaigns |

## How it runs

One Docker Compose stack (`docker-compose.yml`):

- **api**: Go web server; serves the React app, `/api`, `/webhooks/bird` and `/r/...` click redirects.
- **worker**: sends messages through Bird (throttled, retried, never doubled), starts scheduled campaigns (Asia/Qatar), processes webhooks, fetches per-message costs, syncs templates hourly.
- **db**: PostgreSQL 16 (volume `pgdata`).
- **caddy**: HTTPS (Let's Encrypt, automatic renewal).
- **backup**: daily `pg_dump` at 02:00 Qatar time into `deploy/backups/` (7 daily, 4 weekly, 6 monthly).

Secrets live only in `/opt/wacampaigns/.env` on the server.

## Deploy / update

```bash
ssh user@SERVER
cd /opt/wacampaigns
git pull
docker compose up -d --build      # migrations run automatically on start
docker compose logs -f --tail=100 api worker
```

First install:

```bash
# Ubuntu 24.04: install Docker
curl -fsSL https://get.docker.com | sh
git clone REPO /opt/wacampaigns && cd /opt/wacampaigns
cp .env.example .env && nano .env          # DOMAIN, POSTGRES_PASSWORD, JWT_SECRET, BIRD_*
docker compose up -d --build
docker compose exec api /app/app create-admin -email client@example.com -name "Client Name"
```

Open ports 80 and 443 only (`ufw allow 22,80,443/tcp`).

## Common tasks

| Task | Command |
|---|---|
| Admin locked out | `docker compose exec api /app/app create-admin -email EMAIL` (prints a new password) |
| Backup now | `docker compose exec backup /backup.sh` |
| Restore | `gunzip -c deploy/backups/daily/FILE.sql.gz \| docker compose exec -T db psql -U wacampaigns wacampaigns` |
| Rotate Bird webhook secret | Bird API `POST /v1/webhooks/{id}/rotate-secret`, update `.env`, `docker compose up -d` |
| Change sending speed after plan change | Platform > Settings > Send speed |
| Change Qatar marketing rate | Platform > Settings > Marketing rate |

Copy `deploy/backups/` off the server regularly (e.g. `rclone` to the client's Google Drive or S3) so a lost VPS doesn't lose data.

## Support

Bug-fix support: 2 weeks from go-live (DATE → DATE). Contact:
