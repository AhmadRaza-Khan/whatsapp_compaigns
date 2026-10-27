DEV_DB ?= postgres://postgres:postgres@localhost:5433/wacampaigns?sslmode=disable
DEV_ENV = APP_ENV=development DATABASE_URL="$(DEV_DB)" PUBLIC_URL=http://localhost:5173

.PHONY: dev-db dev-api dev-web admin test test-e2e build up logs backup-now

dev-db:            ## start local Postgres on :5433
	docker compose -f docker-compose.dev.yml up -d

dev-api:           ## run API + worker locally on :8080
	cd backend && $(DEV_ENV) go run ./cmd/app all

dev-web:           ## run the React dev server on :5173 (proxies /api to :8080)
	cd frontend && npm run dev

admin:             ## create a local admin: make admin EMAIL=you@example.com
	cd backend && $(DEV_ENV) go run ./cmd/app create-admin -email $(EMAIL) -name Admin

test:              ## unit tests + frontend typecheck
	cd backend && go vet ./... && go test ./...
	cd frontend && npm run typecheck

test-e2e:          ## acceptance tests against a throwaway database (needs dev-db)
	-docker compose -f docker-compose.dev.yml exec -T db psql -U postgres -c 'CREATE DATABASE wacampaigns_test' 2>/dev/null
	cd backend && TEST_DATABASE_URL="postgres://postgres:postgres@localhost:5433/wacampaigns_test?sslmode=disable" go test ./internal/worker -run E2E -count=1 -v

build:
	docker compose build

up:                ## production: build and start everything
	docker compose up -d --build

logs:
	docker compose logs -f --tail=100 api worker

backup-now:        ## take a database backup immediately
	docker compose exec backup /backup.sh
