package config

const ExampleConfigYAML = `server:
  address: ":8080"
  static_dir: "./resource/web"
  read_timeout_seconds: 15
  write_timeout_seconds: 30
  shutdown_timeout_seconds: 10

log:
  level: "info"
  format: "text"

storage:
  sqlite_path: "./resource/config/modelintegrator.db"
  model_root_dir: "./resource/models"

auth:
  token: ""

integrations:
  lmstudio:
    enabled: true
    endpoint: "http://192.168.50.241:1234"
    token: ""
    cache_enabled: true
    cache_refresh_seconds: 30
  docker:
    enabled: true
    endpoint: "unix:///var/run/docker.sock"
    token: ""
  portainer:
    enabled: true
    endpoint: "http://portainer:9000"
    token: ""

nodes: []
models: []
`
