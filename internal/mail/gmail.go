package mail

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	stdmail "net/mail"
	"net/textproto"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"coldflow/internal/config"
	"coldflow/internal/domain"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
)

type Sent struct {
	ID       string `json:"id"`
	ThreadID string `json:"threadId"`
}
type Message struct {
	ID           string   `json:"id"`
	ThreadID     string   `json:"threadId"`
	Labels       []string `json:"labelIds"`
	InternalDate string   `json:"internalDate"`
	Payload      Part     `json:"payload"`
}
type Part struct {
	MIMEType string `json:"mimeType"`
	Filename string `json:"filename"`
	Headers  []struct {
		Name  string `json:"name"`
		Value string `json:"value"`
	} `json:"headers"`
	Body struct {
		Data string `json:"data"`
	} `json:"body"`
	Parts []Part `json:"parts"`
}

func (m Message) Header(name string) string {
	for _, h := range m.Payload.Headers {
		if strings.EqualFold(h.Name, name) {
			return h.Value
		}
	}
	return ""
}
func (m Message) Text() string {
	var out strings.Builder
	var walk func(Part)
	walk = func(p Part) {
		if p.MIMEType == "text/plain" && p.Filename == "" {
			b, _ := base64.RawURLEncoding.DecodeString(strings.TrimRight(p.Body.Data, "="))
			out.Write(b)
			out.WriteByte('\n')
		}
		for _, ch := range p.Parts {
			walk(ch)
		}
	}
	walk(m.Payload)
	s := out.String()
	if len(s) > 12000 {
		s = s[:12000]
	}
	return s
}

// BounceRecipients extracts structured DSN recipients, never addresses guessed
// from free-form incoming text.
func (m Message) BounceRecipients() []string {
	var out []string
	var walk func(Part)
	walk = func(p Part) {
		if p.MIMEType == "message/delivery-status" {
			b, _ := base64.RawURLEncoding.DecodeString(strings.TrimRight(p.Body.Data, "="))
			for _, line := range strings.Split(string(b), "\n") {
				k, v, ok := strings.Cut(strings.TrimSpace(line), ":")
				if !ok || (!strings.EqualFold(k, "Final-Recipient") && !strings.EqualFold(k, "Original-Recipient")) {
					continue
				}
				_, address, ok := strings.Cut(v, ";")
				if ok {
					address = strings.TrimSpace(address)
					if SafeAddress(address) == nil {
						out = append(out, address)
					}
				}
			}
		}
		for _, ch := range p.Parts {
			walk(ch)
		}
	}
	walk(m.Payload)
	return out
}

type Mailer interface {
	Send(context.Context, domain.Draft, domain.Contact, []domain.Attachment) (Sent, error)
	Find(context.Context, string) (*Sent, error)
	Changes(context.Context, string) ([]Message, string, error)
	Latest(context.Context) (string, error)
}
type Gmail struct {
	Config config.Config
	mu     sync.Mutex
	sender string
}

func OAuth(c config.Config, redirect string) *oauth2.Config {
	return &oauth2.Config{ClientID: c.GmailClientID, ClientSecret: os.Getenv("COLDFLOW_GMAIL_CLIENT_SECRET"), RedirectURL: redirect, Endpoint: google.Endpoint, Scopes: []string{"https://www.googleapis.com/auth/gmail.readonly", "https://www.googleapis.com/auth/gmail.send"}}
}
func SaveToken(c config.Config, t *oauth2.Token) error {
	b, err := json.Marshal(t)
	if err != nil {
		return err
	}
	tmp := filepath.Join(c.DataDir, "gmail-token.tmp")
	if err = os.WriteFile(tmp, b, 0600); err != nil {
		return err
	}
	return os.Rename(tmp, filepath.Join(c.DataDir, "gmail-token.json"))
}
func (g *Gmail) client(ctx context.Context) (*http.Client, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	b, err := os.ReadFile(filepath.Join(g.Config.DataDir, "gmail-token.json"))
	if err != nil {
		return nil, errors.New("Gmail not connected; run coldflow auth gmail")
	}
	var t oauth2.Token
	if err = json.Unmarshal(b, &t); err != nil {
		return nil, err
	}
	fresh, err := OAuth(g.Config, "").TokenSource(ctx, &t).Token()
	if err != nil {
		return nil, errors.New("Gmail token refresh failed; reconnect Gmail")
	}
	if fresh.AccessToken != t.AccessToken {
		if fresh.RefreshToken == "" {
			fresh.RefreshToken = t.RefreshToken
		}
		if err = SaveToken(g.Config, fresh); err != nil {
			return nil, err
		}
	}
	cl := oauth2.NewClient(ctx, oauth2.StaticTokenSource(fresh))
	cl.Timeout = 40 * time.Second
	return cl, nil
}
func (g *Gmail) api(ctx context.Context, method, path string, in, out any) error {
	cl, err := g.client(ctx)
	if err != nil {
		return err
	}
	var body io.Reader
	if in != nil {
		b, e := json.Marshal(in)
		if e != nil {
			return e
		}
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, "https://gmail.googleapis.com/gmail/v1/users/me/"+path, body)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := cl.Do(req)
	if err != nil {
		return errors.New("Gmail transport failed; delivery may be uncertain")
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("Gmail HTTP %d", resp.StatusCode)
	}
	if out == nil {
		return nil
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 18<<20)).Decode(out)
}
func (g *Gmail) Latest(ctx context.Context) (string, error) {
	var v struct {
		HistoryID string `json:"historyId"`
		Email     string `json:"emailAddress"`
	}
	err := g.api(ctx, "GET", "profile", nil, &v)
	if err == nil {
		g.mu.Lock()
		g.sender = v.Email
		g.mu.Unlock()
	}
	return v.HistoryID, err
}
func (g *Gmail) Prepare(ctx context.Context, d *domain.Draft) error {
	g.mu.Lock()
	from := g.sender
	g.mu.Unlock()
	if from == "" {
		if _, err := g.Latest(ctx); err != nil {
			return err
		}
		g.mu.Lock()
		from = g.sender
		g.mu.Unlock()
	}
	if err := SafeAddress(from); err != nil {
		return errors.New("Gmail returned an invalid sender identity")
	}
	d.From = from
	return nil
}
func SafeAddress(raw string) error {
	a, err := stdmail.ParseAddress(raw)
	if err != nil || a.Address != raw || strings.ContainsAny(raw, "\r\n") {
		return errors.New("invalid email address")
	}
	return nil
}
func MIME(d domain.Draft, c domain.Contact, attachments []domain.Attachment) ([]byte, error) {
	if err := SafeAddress(c.Email); err != nil {
		return nil, err
	}
	if err := SafeAddress(d.From); err != nil {
		return nil, errors.New("a verified sender address is required")
	}
	if strings.ContainsAny(d.Subject+d.RFCMessageID, "\r\n") {
		return nil, errors.New("invalid message header")
	}
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fmt.Fprintf(&buf, "From: %s\r\nTo: %s\r\nDate: %s\r\nSubject: %s\r\nMessage-ID: <%s>\r\nMIME-Version: 1.0\r\n", d.From, c.Email, time.Now().Format(time.RFC1123Z), mime.QEncoding.Encode("utf-8", d.Subject), d.RFCMessageID)
	if d.InReplyTo != "" {
		if strings.ContainsAny(d.InReplyTo, "\r\n<>") {
			return nil, errors.New("invalid reference header")
		}
		fmt.Fprintf(&buf, "In-Reply-To: <%s>\r\nReferences: <%s>\r\n", d.InReplyTo, d.InReplyTo)
	}
	fmt.Fprintf(&buf, "Content-Type: multipart/mixed; boundary=%q\r\n\r\n", mw.Boundary())
	h := textproto.MIMEHeader{}
	h.Set("Content-Type", "text/plain; charset=utf-8")
	h.Set("Content-Transfer-Encoding", "base64")
	part, err := mw.CreatePart(h)
	if err != nil {
		return nil, err
	}
	if err = writeBase64(part, []byte(d.Body)); err != nil {
		return nil, err
	}
	var total int64
	for _, a := range attachments {
		if a.Size > 10<<20 || a.Size < 0 {
			return nil, errors.New("attachment exceeds 10 MiB")
		}
		total += a.Size
		if total > 15<<20 {
			return nil, errors.New("attachments exceed combined 15 MiB limit")
		}
		b, err := os.ReadFile(a.Path)
		if err != nil {
			return nil, err
		}
		if int64(len(b)) != a.Size {
			return nil, errors.New("attachment size changed")
		}
		h = textproto.MIMEHeader{}
		h.Set("Content-Type", "application/octet-stream")
		h.Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": a.Name}))
		h.Set("Content-Transfer-Encoding", "base64")
		part, err = mw.CreatePart(h)
		if err != nil {
			return nil, err
		}
		if err = writeBase64(part, b); err != nil {
			return nil, err
		}
	}
	if err = mw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
func writeBase64(w io.Writer, b []byte) error {
	s := base64.StdEncoding.EncodeToString(b)
	for len(s) > 0 {
		n := min(76, len(s))
		if _, err := io.WriteString(w, s[:n]+"\r\n"); err != nil {
			return err
		}
		s = s[n:]
	}
	return nil
}
func (g *Gmail) Send(ctx context.Context, d domain.Draft, c domain.Contact, as []domain.Attachment) (Sent, error) {
	var v Sent
	b, err := MIME(d, c, as)
	if err != nil {
		return v, err
	}
	body := map[string]string{"raw": base64.RawURLEncoding.EncodeToString(b)}
	if d.ThreadID != "" {
		body["threadId"] = d.ThreadID
	}
	err = g.api(ctx, "POST", "messages/send", body, &v)
	return v, err
}
func (g *Gmail) Find(ctx context.Context, id string) (*Sent, error) {
	var v struct {
		Messages []Sent `json:"messages"`
	}
	err := g.api(ctx, "GET", "messages?q="+url.QueryEscape("in:sent rfc822msgid:"+id), nil, &v)
	if err != nil {
		return nil, err
	}
	if len(v.Messages) > 0 {
		return &v.Messages[0], nil
	}
	return nil, nil
}
func (g *Gmail) Changes(ctx context.Context, history string) ([]Message, string, error) {
	var ids []string
	latest := history
	page := ""
	for {
		var v struct {
			HistoryID string `json:"historyId"`
			Next      string `json:"nextPageToken"`
			History   []struct {
				Added []struct {
					Message Sent `json:"message"`
				} `json:"messagesAdded"`
			} `json:"history"`
		}
		path := "history?historyTypes=messageAdded&maxResults=100&startHistoryId=" + url.QueryEscape(history)
		if page != "" {
			path += "&pageToken=" + url.QueryEscape(page)
		}
		err := g.api(ctx, "GET", path, nil, &v)
		if err != nil {
			return nil, history, err
		}
		for _, h := range v.History {
			for _, m := range h.Added {
				ids = append(ids, m.Message.ID)
			}
		}
		latest = v.HistoryID
		page = v.Next
		if page == "" {
			break
		}
		if len(ids) > 1000 {
			return nil, history, errors.New("inbox catch-up exceeds 1000 messages; use coldflow inbox-resync")
		}
	}
	out := []Message{}
	seen := map[string]bool{}
	for _, id := range ids {
		if seen[id] {
			continue
		}
		seen[id] = true
		var m Message
		if err := g.api(ctx, "GET", "messages/"+url.PathEscape(id)+"?format=full", nil, &m); err != nil {
			return nil, history, err
		}
		sent := false
		for _, l := range m.Labels {
			if l == "SENT" || l == "DRAFT" {
				sent = true
			}
		}
		if !sent {
			out = append(out, m)
		}
	}
	return out, latest, nil
}

// Resync scans recent incoming mail after an expired Gmail history cursor.
func (g *Gmail) Resync(ctx context.Context, since time.Time) ([]Message, string, error) {
	latest, err := g.Latest(ctx)
	if err != nil {
		return nil, "", err
	}
	var ids []Sent
	page := ""
	for {
		var v struct {
			Messages []Sent `json:"messages"`
			Next     string `json:"nextPageToken"`
		}
		path := "messages?maxResults=100&q=" + url.QueryEscape("-in:sent -in:drafts after:"+strconv.FormatInt(since.Unix(), 10))
		if page != "" {
			path += "&pageToken=" + url.QueryEscape(page)
		}
		if err = g.api(ctx, "GET", path, nil, &v); err != nil {
			return nil, "", err
		}
		ids = append(ids, v.Messages...)
		page = v.Next
		if page == "" {
			break
		}
		if len(ids) > 1000 {
			return nil, "", errors.New("too many messages to resync")
		}
	}
	out := []Message{}
	for _, id := range ids {
		var m Message
		if err = g.api(ctx, "GET", "messages/"+url.PathEscape(id.ID)+"?format=full", nil, &m); err != nil {
			return nil, "", err
		}
		out = append(out, m)
	}
	return out, latest, nil
}
func (g *Gmail) Watch(ctx context.Context) (time.Time, error) {
	var v struct {
		Expiration string `json:"expiration"`
	}
	if g.Config.GmailTopic == "" {
		return time.Time{}, errors.New("gmail_topic is empty")
	}
	err := g.api(ctx, "POST", "watch", map[string]any{"topicName": g.Config.GmailTopic}, &v)
	ms, _ := strconv.ParseInt(v.Expiration, 10, 64)
	return time.UnixMilli(ms), err
}

// Pull wakes the inbox worker without requiring a public Gmail webhook.
func (g *Gmail) Pull(ctx context.Context, wake chan<- struct{}) error {
	cl, err := google.DefaultClient(ctx, "https://www.googleapis.com/auth/pubsub")
	if err != nil {
		return errors.New("Pub/Sub requires Google application default credentials")
	}
	cl.Timeout = 45 * time.Second
	endpoint := "https://pubsub.googleapis.com/v1/" + g.Config.GmailSubscription
	req, _ := http.NewRequestWithContext(ctx, "POST", endpoint+":pull", strings.NewReader(`{"maxMessages":10}`))
	req.Header.Set("Content-Type", "application/json")
	resp, err := cl.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("Pub/Sub HTTP %d", resp.StatusCode)
	}
	var v struct {
		Messages []struct {
			AckID string `json:"ackId"`
		} `json:"receivedMessages"`
	}
	if err = json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&v); err != nil {
		return err
	}
	if len(v.Messages) == 0 {
		return nil
	}
	select {
	case wake <- struct{}{}:
	default:
	}
	ids := []string{}
	for _, m := range v.Messages {
		ids = append(ids, m.AckID)
	}
	b, _ := json.Marshal(map[string]any{"ackIds": ids})
	req, _ = http.NewRequestWithContext(ctx, "POST", endpoint+":acknowledge", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	res, err := cl.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode >= 300 {
		return fmt.Errorf("Pub/Sub ack HTTP %d", res.StatusCode)
	}
	return nil
}
