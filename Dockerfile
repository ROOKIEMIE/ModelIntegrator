FROM golang:1.22-alpine AS builder

WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o /out/controller ./src/cmd/controller

FROM alpine:3.20
WORKDIR /opt/controller

RUN addgroup -S app && adduser -S app -G app \
    && mkdir -p /opt/controller/resources /opt/controller/models \
    && chown -R app:app /opt/controller

COPY --from=builder --chown=app:app /out/controller /opt/controller/controller
COPY --chown=app:app resources /opt/controller/resources

EXPOSE 8080
ENV MCP_CONFIG=/opt/controller/resources/config/config.example.yaml

USER app
CMD ["/opt/controller/controller"]
