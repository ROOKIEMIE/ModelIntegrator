FROM golang:1.22-alpine AS builder

WORKDIR /app
COPY go.mod ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o /out/model-integrator ./src/cmd/model-integrator

FROM alpine:3.20
WORKDIR /opt/modelintegrator

RUN addgroup -S app && adduser -S app -G app

COPY --from=builder /out/model-integrator /opt/modelintegrator/model-integrator
COPY resource /opt/modelintegrator/resource

EXPOSE 8080
ENV MCP_CONFIG=/opt/modelintegrator/resource/config/config.example.yaml

USER app
CMD ["/opt/modelintegrator/model-integrator"]
