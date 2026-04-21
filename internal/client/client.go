package client

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"coldflow/internal/config"
	"coldflow/internal/engine"
	"coldflow/internal/server"
)

type Client struct {
	URL   string
	Token string
	HTTP  *http.Client
}

func New(c config.Config) (*Client, error) {
	b, err := os.ReadFile(filepath.Join(c.DataDir, "api-token"))
	if err != nil {
		return nil, errors.New("start coldflow serve before opening the dashboard")
	}
	host := c.Listen
	if strings.HasPrefix(host, "0.0.0.0:") {
		host = "127.0.0.1:" + strings.TrimPrefix(host, "0.0.0.0:")
	}
	return &Client{URL: "http://" + host, Token: strings.TrimSpace(string(b)), HTTP: &http.Client{Timeout: 110 * time.Second}}, nil
}
func (c *Client) Request(method, path string, in, out any) error {
	var body io.Reader
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return err
		}
		body = bytes.NewReader(b)
	}
	return c.Raw(method, path, body, out)
}
func (c *Client) Raw(method, path string, body io.Reader, out any) error {
	req, err := http.NewRequest(method, c.URL+path, body)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return errors.New("cannot reach Coldflow service; start 'coldflow serve' and check listen address")
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 2000))
		return errors.New(strings.TrimSpace(string(b)))
	}
	if out != nil {
		return json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(out)
	}
	return nil
}
func (c *Client) Snapshot() (engine.Snapshot, error) {
	var s engine.Snapshot
	err := c.Request("GET", "/api/snapshot", nil, &s)
	return s, err
}
func (c *Client) Action(a server.Action) error { return c.Request("POST", "/api/action", a, nil) }
