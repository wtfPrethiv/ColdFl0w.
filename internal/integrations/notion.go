package integrations

import (
	"context"
	"errors"
	"os"
	"strings"
	"time"

	"coldflow/internal/config"
	"coldflow/internal/domain"
)

type Notion struct {
	Config config.Config
	HTTP   *HTTP
	last   time.Time
}

func (n *Notion) request(ctx context.Context, method, path string, in, out any) error {
	if n.Config.NotionDataSource == "" || os.Getenv("NOTION_TOKEN") == "" {
		return errors.New("Notion not configured")
	}
	timer := time.NewTimer(max(time.Duration(0), time.Until(n.last.Add(400*time.Millisecond))))
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
	}
	n.last = time.Now()
	return n.HTTP.Do(ctx, method, "https://api.notion.com/v1/"+path, map[string]string{"Authorization": "Bearer " + os.Getenv("NOTION_TOKEN"), "Notion-Version": "2025-09-03"}, in, out)
}
func rich(text string) []any {
	if len(text) > 1800 {
		text = text[:1800]
	}
	return []any{map[string]any{"type": "text", "text": map[string]string{"content": text}}}
}

type NotionPage struct {
	ID         string `json:"id"`
	Edited     string `json:"last_edited_time"`
	Properties map[string]struct {
		Rich []struct {
			Plain string `json:"plain_text"`
		} `json:"rich_text"`
		Select *struct {
			Name string `json:"name"`
		} `json:"select"`
		Date *struct {
			Start string `json:"start"`
		} `json:"date"`
	} `json:"properties"`
}

func (p NotionPage) Text(k string) string {
	var b strings.Builder
	for _, v := range p.Properties[k].Rich {
		b.WriteString(v.Plain)
	}
	return b.String()
}
func (p NotionPage) Status() string {
	if v := p.Properties["Status"].Select; v != nil {
		return strings.ToLower(v.Name)
	}
	return ""
}
func (p NotionPage) Next() string {
	if v := p.Properties["Next action"].Date; v != nil {
		return v.Start
	}
	return ""
}
func (n *Notion) Find(ctx context.Context, id string) (*NotionPage, error) {
	var out struct {
		Results []NotionPage `json:"results"`
	}
	err := n.request(ctx, "POST", "data_sources/"+n.Config.NotionDataSource+"/query", map[string]any{"filter": map[string]any{"property": "Coldflow ID", "rich_text": map[string]string{"equals": id}}, "page_size": 2}, &out)
	if err != nil {
		return nil, err
	}
	if len(out.Results) > 0 {
		return &out.Results[0], nil
	}
	return nil, nil
}
func (n *Notion) Fetch(ctx context.Context, id string) (NotionPage, error) {
	var p NotionPage
	err := n.request(ctx, "GET", "pages/"+id, nil, &p)
	return p, err
}

// Changes reads only pages edited since the previous synchronization watermark.
func (n *Notion) Changes(ctx context.Context, after string) ([]NotionPage, error) {
	out := []NotionPage{}
	cursor := ""
	for {
		body := map[string]any{"page_size": 100}
		if after != "" {
			body["filter"] = map[string]any{"timestamp": "last_edited_time", "last_edited_time": map[string]string{"on_or_after": after}}
		}
		if cursor != "" {
			body["start_cursor"] = cursor
		}
		var v struct {
			Results []NotionPage `json:"results"`
			More    bool         `json:"has_more"`
			Cursor  string       `json:"next_cursor"`
		}
		if err := n.request(ctx, "POST", "data_sources/"+n.Config.NotionDataSource+"/query", body, &v); err != nil {
			return nil, err
		}
		out = append(out, v.Results...)
		if !v.More {
			return out, nil
		}
		if len(out) >= 1000 {
			return nil, errors.New("Notion recovery exceeds 1000 changed pages")
		}
		if v.Cursor == "" || v.Cursor == cursor {
			return nil, errors.New("invalid Notion pagination cursor")
		}
		cursor = v.Cursor
	}
}
func (n *Notion) Push(ctx context.Context, c domain.Contact, campaign string, paper domain.Paper, sentAt, repliedAt string) (NotionPage, error) {
	var p NotionPage
	props := map[string]any{"Name": map[string]any{"title": rich(c.Name)}, "Coldflow ID": map[string]any{"rich_text": rich(c.ID)}, "University": map[string]any{"rich_text": rich(c.University)}, "Email": map[string]any{"email": c.Email}, "Campaign": map[string]any{"rich_text": rich(campaign)}, "Paper": map[string]any{"rich_text": rich(paper.Title)}, "Status": map[string]any{"select": map[string]string{"name": c.Status}}, "Notes": map[string]any{"rich_text": rich(c.Notes)}}
	for name, value := range map[string]string{"Next action": c.NextAction, "Sent": sentAt, "Replied": repliedAt} {
		var date any
		if value != "" {
			date = map[string]string{"start": value}
		}
		props[name] = map[string]any{"date": date}
	}
	var link any
	if c.URL != "" {
		link = c.URL
	}
	props["Source"] = map[string]any{"url": link}
	body := map[string]any{"properties": props}
	path := "pages"
	method := "POST"
	if c.NotionID == "" {
		body["parent"] = map[string]string{"type": "data_source_id", "data_source_id": n.Config.NotionDataSource}
	} else {
		method = "PATCH"
		path += "/" + c.NotionID
	}
	err := n.request(ctx, method, path, body, &p)
	return p, err
}
