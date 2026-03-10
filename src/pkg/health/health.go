package health

import (
	"time"

	"model-control-plane/src/pkg/version"
)

type Status struct {
	Status    string       `json:"status"`
	Timestamp time.Time    `json:"timestamp"`
	Version   version.Info `json:"version"`
}

func NewStatus(v version.Info) Status {
	return Status{
		Status:    "ok",
		Timestamp: time.Now().UTC(),
		Version:   v,
	}
}
