# syntax=docker/dockerfile:1

# ---- build stage -----------------------------------------------------------
FROM golang:1.25-alpine AS build
WORKDIR /src

# Зависимости кешируются отдельно от исходников.
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download

COPY . .
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/minecraft-server .

# ---- runtime stage ---------------------------------------------------------
FROM alpine:3.21
# ca-certificates — для sessionserver.mojang.com в online mode.
RUN apk add --no-cache ca-certificates tzdata \
    && addgroup -S mc && adduser -S -G mc mc

WORKDIR /app
COPY --from=build /out/minecraft-server /usr/local/bin/minecraft-server

# Рантайм-файлы читаются относительно рабочей директории (см. main.go):
# шаблоны карт, banlist/auth/badwords и иконка сервера.
COPY --chown=mc:mc schem/templates ./schem/templates
COPY --chown=mc:mc banlist.json auth.json badwords.json server-icon.png ./

USER mc
EXPOSE 25565
# Метрики/pprof по умолчанию на loopback — внутри контейнера они недоступны
# снаружи; переопределяется через METRICS_ADDR=0.0.0.0:6060.
ENV PORT=25565
ENTRYPOINT ["/usr/local/bin/minecraft-server"]
