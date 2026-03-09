package api

import (
	"encoding/json"
	"net/http"
	"time"
)

type Response struct {
	Success   bool        `json:"success"`
	Message   string      `json:"message,omitempty"`
	Data      interface{} `json:"data,omitempty"`
	Error     interface{} `json:"error,omitempty"`
	Timestamp time.Time   `json:"timestamp"`
}

func WriteJSON(w http.ResponseWriter, status int, payload Response) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func OK(w http.ResponseWriter, data interface{}) {
	WriteJSON(w, http.StatusOK, Response{
		Success:   true,
		Data:      data,
		Timestamp: time.Now().UTC(),
	})
}

func Fail(w http.ResponseWriter, status int, message string, errDetail interface{}) {
	WriteJSON(w, status, Response{
		Success:   false,
		Message:   message,
		Error:     errDetail,
		Timestamp: time.Now().UTC(),
	})
}
