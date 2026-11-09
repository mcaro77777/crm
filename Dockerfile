# syntax=docker/dockerfile:1.7

FROM golang:1.27.3-alpine AS dependencias
WORKDIR /src
COPY go.mod go.sum* ./
RUN go mod download

FROM dependencias AS compilacion
COPY . .
ARG VERSION=dev
ARG COMMIT=desconocido
RUN CGO_ENABLED=0 GOOS=linux go build \
    -trimpath \
    -ldflags="-s -w -X crm-api/internal/version.Version=${VERSION} -X crm-api/internal/version.Commit=${COMMIT}" \
    -o /salida/crm-api ./cmd/api

FROM alpine:3.22 AS final
RUN apk add --no-cache ca-certificates tzdata \
    && addgroup -S crm \
    && adduser -S -G crm crm \
    && mkdir -p /app/ClientApp /data/media \
    && chown -R crm:crm /app /data

WORKDIR /app
COPY --from=compilacion --chown=crm:crm /salida/crm-api /app/crm-api
COPY --chown=crm:crm ClientApp/ /app/ClientApp/

USER crm
ENV APP_ENV=production
ENV PORT=8080
ENV CLIENT_APP_DIR=/app/ClientApp
ENV MEDIA_DIR=/data/media
EXPOSE 8080

HEALTHCHECK --interval=15s --timeout=5s --start-period=20s --retries=5 \
  CMD wget -q -O /dev/null http://127.0.0.1:8080/api/health || exit 1

ENTRYPOINT ["/app/crm-api"]
CMD ["serve"]
