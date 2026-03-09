package lmstudio

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

type Client struct {
	endpoint   string
	token      string
	httpClient *http.Client
}

func NewClient(endpoint, token string, timeout time.Duration) *Client {
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	return &Client{
		endpoint: strings.TrimRight(endpoint, "/"),
		token:    token,
		httpClient: &http.Client{
			Timeout: timeout,
		},
	}
}

func (c *Client) Endpoint() string {
	return c.endpoint
}

func (c *Client) Call(ctx context.Context, method, path string, reqBody interface{}) (int, []byte, error) {
	if c.endpoint == "" {
		return 0, nil, fmt.Errorf("LM Studio endpoint 为空")
	}

	url := c.endpoint + path
	var bodyReader io.Reader
	if reqBody != nil {
		payload, err := json.Marshal(reqBody)
		if err != nil {
			return 0, nil, fmt.Errorf("请求序列化失败: %w", err)
		}
		bodyReader = bytes.NewReader(payload)
	}

	req, err := http.NewRequestWithContext(ctx, method, url, bodyReader)
	if err != nil {
		return 0, nil, fmt.Errorf("创建请求失败: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return 0, nil, fmt.Errorf("调用 LM Studio 失败: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return resp.StatusCode, nil, fmt.Errorf("读取响应失败: %w", err)
	}

	return resp.StatusCode, respBody, nil
}
