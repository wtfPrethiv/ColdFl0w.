package engine

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"coldflow/internal/domain"
	"coldflow/internal/store"
)

type notionTransport func(*http.Request) (*http.Response, error)

func (f notionTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestNotionImportsStopAndOnlyPushesDirtyContacts(t *testing.T) {
	t.Setenv("NOTION_TOKEN", "fixture-token")
	e := fixture(t)
	e.Notion.Config.NotionDataSource = "source"
	contacts, _ := store.List[domain.Contact](e.Store, "contact")
	for _, c := range contacts {
		c.NotionID = "page-" + c.ID
		c.NotionEdited = "2026-10-26T10:00:00Z"
		e.Store.Put("contact", c.ID, c)
		e.Store.Set("notion:local:"+c.ID, c.UpdatedAt)
	}
	query, push := 0, 0
	e.Notion.HTTP.Client.Transport = notionTransport(func(r *http.Request) (*http.Response, error) {
		text := ""
		if strings.HasSuffix(r.URL.Path, "/query") {
			query++
			if query == 1 {
				text = `{"results":[{"id":"page-ada","last_edited_time":"2026-10-26T12:00:00Z","properties":{"Coldflow ID":{"rich_text":[{"plain_text":"ada"}]},"Status":{"select":{"name":"declined"}},"Notes":{"rich_text":[{"plain_text":"Not accepting students"}]},"Next action":{"date":null}}}],"has_more":false}`
			} else {
				text = `{"results":[],"has_more":false}`
			}
		} else if r.Method == "PATCH" && strings.HasSuffix(r.URL.Path, "/page-ada") {
			push++
			b, _ := io.ReadAll(r.Body)
			if !strings.Contains(string(b), "A local correction") {
				t.Fatalf("missing local note: %s", b)
			}
			text = `{"id":"page-ada","last_edited_time":"2026-10-26T13:00:00Z"}`
		} else {
			t.Fatalf("unexpected Notion request: %s %s", r.Method, r.URL.Path)
		}
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(text))}, nil
	})
	if err := e.syncNotion(context.Background()); err != nil {
		t.Fatal(err)
	}
	c, _ := store.Get[domain.Contact](e.Store, "contact", "ada")
	d, _ := store.Get[domain.Draft](e.Store, "draft", "draft-ada")
	if c.Status != "declined" || d.Status != "cancelled" || push != 0 {
		t.Fatalf("contact=%s draft=%s pushes=%d", c.Status, d.Status, push)
	}
	if err := e.UpdateContact(c.ID, c.Status, "A local correction", ""); err != nil {
		t.Fatal(err)
	}
	if err := e.syncNotion(context.Background()); err != nil {
		t.Fatal(err)
	}
	if query != 2 || push != 1 {
		t.Fatalf("queries=%d pushes=%d", query, push)
	}
}
