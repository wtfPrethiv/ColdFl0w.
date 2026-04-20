package integrations

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"coldflow/internal/config"
)

type HTTP struct{ Client *http.Client }

func NewHTTP() *HTTP { return &HTTP{Client: &http.Client{Timeout: 35 * time.Second}} }

// Do makes one attempt. The durable outbox owns retries, including Retry-After.
func (h *HTTP) Do(ctx context.Context, method, endpoint string, headers map[string]string, in, out any) error {
	var body io.Reader
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return err
		}
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint, body)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := h.Client.Do(req)
	if err != nil {
		return errors.New("integration connection failed")
	}
	defer resp.Body.Close()
	if resp.StatusCode == 429 {
		seconds, _ := strconv.Atoi(resp.Header.Get("Retry-After"))
		if seconds < 1 {
			seconds = 60
		}
		return &RateLimit{time.Duration(seconds) * time.Second}
	}
	if resp.StatusCode >= 300 {
		return fmt.Errorf("integration HTTP %d", resp.StatusCode)
	}
	if out != nil {
		return json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(out)
	}
	return nil
}

type RateLimit struct{ After time.Duration }

func (e *RateLimit) Error() string {
	return "integration rate limited; retry after " + e.After.String()
}

type Notifier struct {
	Config config.Config
	HTTP   *HTTP
}

func (n *Notifier) Discord(ctx context.Context, text string) error {
	if n.Config.Demo {
		return nil
	}
	if n.Config.DiscordChannel == "" || os.Getenv("DISCORD_BOT_TOKEN") == "" {
		return errors.New("Discord not configured")
	}
	if len(text) > 1900 {
		text = text[:1900]
	}
	return n.HTTP.Do(ctx, "POST", "https://discord.com/api/v10/channels/"+url.PathEscape(n.Config.DiscordChannel)+"/messages", map[string]string{"Authorization": "Bot " + os.Getenv("DISCORD_BOT_TOKEN")}, map[string]any{"content": text, "allowed_mentions": map[string]any{"parse": []string{}}}, nil)
}
func (n *Notifier) WhatsApp(ctx context.Context, text string, session bool) error {
	if n.Config.Demo {
		return nil
	}
	if n.Config.WhatsAppPhoneID == "" || n.Config.WhatsAppUser == "" || os.Getenv("WHATSAPP_TOKEN") == "" {
		return errors.New("WhatsApp not configured")
	}
	if len(text) > 900 {
		text = text[:900]
	}
	body := map[string]any{"messaging_product": "whatsapp", "to": n.Config.WhatsAppUser}
	if session {
		body["type"] = "text"
		body["text"] = map[string]string{"body": text}
	} else {
		if n.Config.WhatsAppTemplate == "" {
			return errors.New("configure whatsapp_template for proactive notifications")
		}
		body["type"] = "template"
		body["template"] = map[string]any{"name": n.Config.WhatsAppTemplate, "language": map[string]string{"code": n.Config.WhatsAppLanguage}, "components": []any{map[string]any{"type": "body", "parameters": []any{map[string]string{"type": "text", "text": text}}}}}
	}
	return n.HTTP.Do(ctx, "POST", "https://graph.facebook.com/"+n.Config.GraphVersion+"/"+url.PathEscape(n.Config.WhatsAppPhoneID)+"/messages", map[string]string{"Authorization": "Bearer " + os.Getenv("WHATSAPP_TOKEN")}, body, nil)
}
func CleanText(text string) string {
	var b strings.Builder
	for _, r := range text {
		if r == '\n' || r == '\t' || r >= 32 && r != 127 && r != 0x9b && r != 0x9d {
			b.WriteRune(r)
		}
	}
	return b.String()
}
