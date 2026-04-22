package integrations

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"coldflow/internal/config"
	"coldflow/internal/domain"
)

type roundTrip func(*http.Request) (*http.Response, error)

func (f roundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestNotionCreateUsesDataSourceParent(t *testing.T) {
	t.Setenv("NOTION_TOKEN", "test-token")
	c := config.Default(t.TempDir())
	c.NotionDataSource = "source-id"
	h := NewHTTP()
	h.Client.Transport = roundTrip(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path != "/v1/pages" || r.Header.Get("Notion-Version") != "2025-09-03" {
			t.Fatal(r.URL, r.Header)
		}
		var b map[string]any
		if err := json.NewDecoder(r.Body).Decode(&b); err != nil {
			t.Fatal(err)
		}
		parent := b["parent"].(map[string]any)
		if parent["data_source_id"] != "source-id" {
			t.Fatal(parent)
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"id":"page","last_edited_time":"2026-10-26T12:00:00Z"}`)), Header: make(http.Header)}, nil
	})
	n := &Notion{Config: c, HTTP: h}
	p, err := n.Push(context.Background(), domain.Contact{ID: "contact", Name: "Ada", Email: "ada@example.edu", Status: "drafted"}, "Campaign", domain.Paper{}, "", "")
	if err != nil || p.ID != "page" {
		t.Fatalf("%+v %v", p, err)
	}
}
func TestRateLimitPreservesRetryAfter(t *testing.T) {
	h := NewHTTP()
	h.Client.Transport = roundTrip(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 429, Header: http.Header{"Retry-After": []string{"120"}}, Body: io.NopCloser(strings.NewReader(""))}, nil
	})
	err := h.Do(context.Background(), "GET", "https://example.org", nil, nil, nil)
	var rate *RateLimit
	if !errors.As(err, &rate) || rate.After != 120*time.Second {
		t.Fatalf("%v", err)
	}
}
