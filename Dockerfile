# One image for both the API server and the worker (different commands).

FROM node:24-alpine AS web
WORKDIR /web
COPY frontend/package.json frontend/package-lock.json ./
RUN npm ci
COPY frontend/ ./
RUN npm run build

FROM golang:1.26-alpine AS api
WORKDIR /src
COPY backend/go.mod backend/go.sum ./
RUN go mod download
COPY backend/ ./
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/app ./cmd/app

FROM alpine:3.22
RUN apk add --no-cache ca-certificates tzdata && adduser -D -u 10001 app \
    && mkdir -p /app/data/uploads && chown -R app:app /app/data
WORKDIR /app
COPY --from=api /out/app /app/app
COPY --from=web /web/dist /app/web
ENV STATIC_DIR=/app/web UPLOAD_DIR=/app/data/uploads PORT=8080
USER app
EXPOSE 8080
ENTRYPOINT ["/app/app"]
CMD ["server"]
