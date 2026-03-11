package service

import (
	"net"
	"net/url"
	"os"
	"strings"
)

const defaultContainerHostAlias = "host.docker.internal"

func normalizeControllerAccessibleEndpoint(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return raw
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" {
		return raw
	}
	if !isControllerRunningInContainer() {
		return raw
	}
	host := strings.ToLower(strings.TrimSpace(parsed.Hostname()))
	if host != "127.0.0.1" && host != "localhost" && host != "::1" {
		return raw
	}
	alias := strings.TrimSpace(os.Getenv("MCP_CONTAINER_HOST_ALIAS"))
	if alias == "" {
		alias = defaultContainerHostAlias
	}
	if port := strings.TrimSpace(parsed.Port()); port != "" {
		parsed.Host = net.JoinHostPort(alias, port)
	} else {
		parsed.Host = alias
	}
	return parsed.String()
}

func isControllerRunningInContainer() bool {
	if raw := strings.TrimSpace(os.Getenv("MCP_RUNNING_IN_CONTAINER")); raw != "" {
		switch strings.ToLower(raw) {
		case "1", "true", "yes", "on":
			return true
		case "0", "false", "no", "off":
			return false
		}
	}
	_, err := os.Stat("/.dockerenv")
	return err == nil
}
