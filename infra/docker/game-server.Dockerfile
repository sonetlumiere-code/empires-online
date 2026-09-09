# Imagen del Game Server de Empires Online.
#
# Construir desde la RAÍZ del repositorio:
#   docker build -f infra/docker/game-server.Dockerfile -t empires-online/game-server .
#
# El binario embebe las migraciones y los JSON Schema del protocolo, así que la
# imagen final es autosuficiente: no hay forma de desplegarla y "olvidar" copiar
# los archivos que necesita.

# ─────────────────────────────────────────────────────────────
# Etapa de compilación
# ─────────────────────────────────────────────────────────────
FROM golang:1.25-alpine AS builder

WORKDIR /src

# Las dependencias se descargan en una capa propia: cambiar el código no invalida
# la caché de módulos.
COPY services/game-server/go.mod services/game-server/go.sum ./
RUN go mod download

COPY services/game-server/ ./

# Binario estático: la imagen final no necesita libc ni ningún runtime.
#   -trimpath  elimina rutas absolutas del build (reproducibilidad)
#   -w -s      quita la información de depuración (imagen más pequeña)
RUN CGO_ENABLED=0 GOOS=linux go build \
    -trimpath \
    -ldflags="-w -s" \
    -o /out/empires-server \
    ./cmd/server

# El migrador viaja en la MISMA imagen que el servidor, a propósito: así es
# imposible migrar con una versión del esquema distinta de la que espera el
# binario que va a arrancar después.
RUN CGO_ENABLED=0 GOOS=linux go build \
    -trimpath \
    -ldflags="-w -s" \
    -o /out/migrate \
    ./cmd/migrate

# ─────────────────────────────────────────────────────────────
# Imagen final
# ─────────────────────────────────────────────────────────────
FROM alpine:3.20

# Certificados raíz: necesarios para TLS contra PostgreSQL y Redis gestionados.
RUN apk add --no-cache ca-certificates tzdata wget && \
    addgroup -g 10001 -S empires && \
    adduser -u 10001 -S -G empires empires

COPY --from=builder /out/empires-server /usr/local/bin/empires-server
COPY --from=builder /out/migrate /usr/local/bin/migrate

# Nunca root.
USER empires:empires

EXPOSE 8080 9090

# Liveness: no toca dependencias externas a propósito. La readiness (/ready) la
# consulta el balanceador, no el runtime del contenedor: confundirlas provoca
# reinicios en cadena justo cuando la base de datos va lenta.
HEALTHCHECK --interval=15s --timeout=3s --start-period=20s --retries=3 \
    CMD wget --quiet --tries=1 --spider http://localhost:8080/health || exit 1

ENTRYPOINT ["/usr/local/bin/empires-server"]
