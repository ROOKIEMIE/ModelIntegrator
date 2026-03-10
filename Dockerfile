FROM golang:1.22-alpine AS builder

WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o /out/controller ./src/cmd/controller

FROM alpine:3.20
WORKDIR /opt/modelintegrator

RUN addgroup -S app && adduser -S app -G app \
    && mkdir -p /opt/modelintegrator/resources /opt/modelintegrator/models \
    && chown -R app:app /opt/modelintegrator

COPY --from=builder --chown=app:app /out/controller /opt/modelintegrator/controller
COPY --chown=app:app resources /opt/modelintegrator/resources

EXPOSE 8080
ENV MCP_CONFIG=/opt/modelintegrator/resources/config/config.example.yaml

USER app
CMD ["/opt/modelintegrator/controller"]
