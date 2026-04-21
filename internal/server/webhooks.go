package server

import (
	"crypto/ed25519"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"coldflow/internal/domain"
)

func (s *Server) whatsappVerify(w http.ResponseWriter, r *http.Request) {
	want := os.Getenv("WHATSAPP_VERIFY_TOKEN")
	got := r.URL.Query().Get("hub.verify_token")
	if want == "" || subtle.ConstantTimeCompare([]byte(want), []byte(got)) != 1 || r.URL.Query().Get("hub.mode") != "subscribe" {
		http.Error(w, "verification failed", 403)
		return
	}
	w.Write([]byte(r.URL.Query().Get("hub.challenge")))
}
func WhatsAppSignature(secret string, body []byte, signature string) bool {
	if secret == "" {
		return false
	}
	got, err := hex.DecodeString(strings.TrimPrefix(signature, "sha256="))
	if err != nil {
		return false
	}
	m := hmac.New(sha256.New, []byte(secret))
	m.Write(body)
	return hmac.Equal(got, m.Sum(nil))
}
func DiscordSignature(publicKey, signature, timestamp string, body []byte, now time.Time) bool {
	pub, err := hex.DecodeString(publicKey)
	if err != nil || len(pub) != ed25519.PublicKeySize {
		return false
	}
	sig, err := hex.DecodeString(signature)
	if err != nil {
		return false
	}
	sec, err := strconv.ParseInt(timestamp, 10, 64)
	if err != nil || now.Sub(time.Unix(sec, 0)) > 5*time.Minute || time.Unix(sec, 0).Sub(now) > time.Minute {
		return false
	}
	return ed25519.Verify(ed25519.PublicKey(pub), append([]byte(timestamp), body...), sig)
}
func (s *Server) whatsapp(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		http.Error(w, "bad body", 400)
		return
	}
	if !WhatsAppSignature(os.Getenv("WHATSAPP_APP_SECRET"), body, r.Header.Get("X-Hub-Signature-256")) {
		http.Error(w, "invalid signature", 401)
		return
	}
	var v struct {
		Entry []struct {
			Changes []struct {
				Value struct {
					Messages []struct {
						ID   string `json:"id"`
						From string `json:"from"`
						Type string `json:"type"`
						Text struct {
							Body string `json:"body"`
						} `json:"text"`
					} `json:"messages"`
				} `json:"value"`
			} `json:"changes"`
		} `json:"entry"`
	}
	if err = json.Unmarshal(body, &v); err != nil {
		http.Error(w, "bad JSON", 400)
		return
	}
	for _, entry := range v.Entry {
		for _, change := range entry.Changes {
			for _, m := range change.Value.Messages {
				if m.From != s.Engine.Config.WhatsAppUser || m.From == "" || m.Type != "text" {
					continue
				}
				_ = s.Engine.Store.Set("whatsapp:last_user", domain.Now())
				if err = s.Engine.Incoming(m.ID, "whatsapp", m.Text.Body); err != nil {
					http.Error(w, "unable to queue message", 503)
					return
				}
			}
		}
	}
	write(w, map[string]bool{"ok": true})
}
func (s *Server) discord(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		http.Error(w, "bad body", 400)
		return
	}
	if !DiscordSignature(s.Engine.Config.DiscordPublicKey, r.Header.Get("X-Signature-Ed25519"), r.Header.Get("X-Signature-Timestamp"), body, time.Now()) {
		http.Error(w, "invalid signature", 401)
		return
	}
	var v struct {
		ID   string `json:"id"`
		Type int    `json:"type"`
		User struct {
			ID string `json:"id"`
		} `json:"user"`
		Member struct {
			User struct {
				ID string `json:"id"`
			} `json:"user"`
		} `json:"member"`
		Data struct {
			Name    string `json:"name"`
			Options []struct {
				Name  string `json:"name"`
				Value string `json:"value"`
			} `json:"options"`
		} `json:"data"`
	}
	if err = json.Unmarshal(body, &v); err != nil {
		http.Error(w, "bad JSON", 400)
		return
	}
	if v.Type == 1 {
		write(w, map[string]int{"type": 1})
		return
	}
	user := v.User.ID
	if user == "" {
		user = v.Member.User.ID
	}
	if user == "" || user != s.Engine.Config.DiscordUser {
		http.Error(w, "user not allowed", 403)
		return
	}
	if v.Type != 2 || v.Data.Name != "coldflow" {
		http.Error(w, "unsupported interaction", 400)
		return
	}
	{
		text := "status"
		agent := "Coordinator"
		for _, o := range v.Data.Options {
			if o.Name == "text" {
				text = o.Value
			}
			if o.Name == "agent" {
				agent = o.Value
			}
		}
		if err = s.Engine.Incoming(v.ID, "discord", agent+": "+text); err != nil {
			http.Error(w, "unable to queue message", 503)
			return
		}
	}
	write(w, map[string]any{"type": 4, "data": map[string]any{"content": "Queued. Coldflow will respond in your configured private channel.", "flags": 64}})
}
func (s *Server) message(text, channel string) error {
	agent := "Coordinator"
	if prefix, rest, ok := strings.Cut(text, ":"); ok {
		for _, a := range domain.Agents {
			if strings.EqualFold(strings.TrimSpace(prefix), a.Name) {
				agent = a.Name
				text = strings.TrimSpace(rest)
				break
			}
		}
	}
	switch strings.ToLower(strings.TrimSpace(text)) {
	case "pause":
		return s.Engine.Store.Set("paused", "true")
	case "resume":
		return s.Engine.Store.Set("paused", "false")
	}
	return s.Engine.Chat(agent, text, channel)
}
