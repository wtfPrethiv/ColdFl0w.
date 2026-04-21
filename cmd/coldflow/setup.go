package main

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"time"

	"charm.land/huh/v2"
	"coldflow/internal/config"
	"coldflow/internal/domain"
	"coldflow/internal/integrations"
	gmail "coldflow/internal/mail"
	"coldflow/internal/server"
	"coldflow/internal/store"
	"golang.org/x/oauth2"
)

func initialize(dir string, demo, headless bool) error {
	if _, err := os.Stat(config.Path(dir)); err == nil {
		return errors.New("configuration already exists; use configure")
	}
	c := config.Default(dir)
	c.Demo = demo
	p := domain.Profile{}
	if !headless && !demo {
		form := huh.NewForm(huh.NewGroup(huh.NewInput().Title("Your name").Value(&p.Name), huh.NewText().Title("Your background (facts agents may use)").Value(&p.Background), huh.NewInput().Title("Research interests").Value(&p.Interests), huh.NewText().Title("What research opportunity are you asking for?").Value(&p.Goal), huh.NewText().Title("Email signature").Value(&p.Signature)), huh.NewGroup(huh.NewInput().Title("Timezone").Value(&c.Timezone), huh.NewSelect[string]().Title("AI provider").Options(huh.NewOption("OpenAI", "openai"), huh.NewOption("Anthropic", "anthropic"), huh.NewOption("OpenRouter", "openrouter"), huh.NewOption("Compatible API", "compatible")).Value(&c.AIProvider), huh.NewInput().Title("AI model ID from your provider").Value(&c.AIModel)))
		if err := form.Run(); err != nil {
			return err
		}
		if _, err := time.LoadLocation(c.Timezone); err != nil {
			return err
		}
	}
	if err := config.Save(c); err != nil {
		return err
	}
	s, err := store.Open(dir)
	if err != nil {
		return err
	}
	defer s.Close()
	if p.Name != "" {
		if err = s.Put("profile", "user", p); err != nil {
			return err
		}
	}
	if _, err = server.Token(dir); err != nil {
		return err
	}
	fmt.Println("Initialized Coldflow at", dir)
	if !demo {
		fmt.Println("Next: coldflow configure, coldflow auth gmail, then coldflow serve.")
	}
	return nil
}
func configure(c config.Config) error {
	fields := []huh.Field{
		huh.NewInput().Title("AI provider: openai, anthropic, openrouter, compatible").Value(&c.AIProvider), huh.NewInput().Title("AI model ID").Value(&c.AIModel), huh.NewInput().Title("Compatible API base URL (optional)").Value(&c.AIBaseURL), huh.NewInput().Title("Timezone").Value(&c.Timezone), huh.NewInput().Title("Gmail OAuth desktop client ID").Value(&c.GmailClientID), huh.NewInput().Title("Gmail Pub/Sub topic: projects/PROJECT/topics/TOPIC (optional)").Value(&c.GmailTopic), huh.NewInput().Title("Gmail Pub/Sub subscription: projects/PROJECT/subscriptions/SUB (optional)").Value(&c.GmailSubscription), huh.NewInput().Title("Discord private channel ID (optional)").Value(&c.DiscordChannel), huh.NewInput().Title("Discord allowed user ID").Value(&c.DiscordUser), huh.NewInput().Title("Discord application public key").Value(&c.DiscordPublicKey), huh.NewInput().Title("WhatsApp phone number ID (optional)").Value(&c.WhatsAppPhoneID), huh.NewInput().Title("WhatsApp allowed user number, international digits").Value(&c.WhatsAppUser), huh.NewInput().Title("WhatsApp approved notification template name").Value(&c.WhatsAppTemplate), huh.NewInput().Title("Notion data source ID (optional)").Value(&c.NotionDataSource), huh.NewInput().Title("Optional Wish SSH listen address (blank disables)").Value(&c.WishListen), huh.NewInput().Title("Wish authorized_keys file path").Value(&c.AuthorizedKeys),
	}
	if err := huh.NewForm(huh.NewGroup(fields...)).Run(); err != nil {
		return err
	}
	if _, err := time.LoadLocation(c.Timezone); err != nil {
		return err
	}
	if err := config.Save(c); err != nil {
		return err
	}
	fmt.Println("Saved. Restart the service to apply settings. Credentials are read from environment; see docs/SETUP.md.")
	return nil
}
func authGmail(c config.Config) error {
	if c.Demo {
		return errors.New("Gmail is disabled in demo mode")
	}
	if c.GmailClientID == "" || os.Getenv("COLDFLOW_GMAIL_CLIENT_SECRET") == "" {
		return errors.New("set gmail_client_id and COLDFLOW_GMAIL_CLIENT_SECRET first")
	}
	listener, err := net.Listen("tcp", "127.0.0.1:53682")
	if err != nil {
		return err
	}
	defer listener.Close()
	var bytes [32]byte
	if _, err = rand.Read(bytes[:]); err != nil {
		return err
	}
	state := hex.EncodeToString(bytes[:])
	verifier := oauth2.GenerateVerifier()
	cfg := gmail.OAuth(c, "http://127.0.0.1:53682/callback")
	codes := make(chan string, 1)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /callback", func(w http.ResponseWriter, r *http.Request) {
		if subtle.ConstantTimeCompare([]byte(state), []byte(r.URL.Query().Get("state"))) != 1 {
			http.Error(w, "invalid state", 400)
			return
		}
		code := r.URL.Query().Get("code")
		if code == "" {
			http.Error(w, "authorization declined", 400)
			return
		}
		select {
		case codes <- code:
			fmt.Fprintln(w, "Authorization received. You may close this tab and check the terminal.")
		default:
			http.Error(w, "already received", 409)
		}
	})
	srv := &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	defer srv.Close()
	go srv.Serve(listener)
	fmt.Println("On SSH, first forward the callback: ssh -L 53682:127.0.0.1:53682 USER@VPS")
	fmt.Println("Open this URL on your own computer:")
	fmt.Println(cfg.AuthCodeURL(state, oauth2.AccessTypeOffline, oauth2.SetAuthURLParam("prompt", "consent"), oauth2.S256ChallengeOption(verifier)))
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	select {
	case <-ctx.Done():
		return errors.New("Gmail authorization timed out")
	case code := <-codes:
		t, err := cfg.Exchange(ctx, code, oauth2.VerifierOption(verifier))
		if err != nil {
			return errors.New("OAuth exchange failed; check client configuration")
		}
		if t.RefreshToken == "" {
			return errors.New("no refresh token returned; revoke old access and reconnect with consent")
		}
		if err = gmail.SaveToken(c, t); err != nil {
			return err
		}
	}
	fmt.Println("Gmail connected. Restart or start the service.")
	return nil
}
func doctor(c config.Config) error {
	bad := false
	check := func(name string, ok bool, detail string, required bool) {
		mark := "OK"
		if !ok {
			mark = "--"
			if required {
				mark = "FAIL"
				bad = true
			}
		}
		fmt.Printf("[%-4s] %-18s %s\n", mark, name, detail)
	}
	check("data directory", true, c.DataDir, true)
	check("demo", true, fmt.Sprint(c.Demo), false)
	_, tzErr := time.LoadLocation(c.Timezone)
	check("timezone", tzErr == nil, c.Timezone, true)
	check("AI", c.Demo || os.Getenv("COLDFLOW_AI_KEY") != "" && c.AIModel != "", "model and COLDFLOW_AI_KEY", !c.Demo)
	_, tokenErr := os.Stat(c.DataDir + string(os.PathSeparator) + "gmail-token.json")
	check("Gmail OAuth", c.Demo || tokenErr == nil, "run auth gmail to connect", !c.Demo)
	_, pdfErr := exec.LookPath("pdftotext")
	check("paper PDFs", pdfErr == nil, "pdftotext (optional; abstract fallback)", false)
	check("web search", os.Getenv("BRAVE_API_KEY") != "", "BRAVE_API_KEY or campaign seed URLs", false)
	check("Discord", c.DiscordChannel != "" && os.Getenv("DISCORD_BOT_TOKEN") != "", "private channel + token + allowed user + interaction public key", false)
	check("WhatsApp", c.WhatsAppPhoneID != "" && c.WhatsAppUser != "" && os.Getenv("WHATSAPP_TOKEN") != "" && os.Getenv("WHATSAPP_APP_SECRET") != "", "Cloud API account, webhook, template, token", false)
	check("Notion", c.NotionDataSource != "" && os.Getenv("NOTION_TOKEN") != "", "data source shared with integration", false)
	if c.WishListen != "" {
		_, err := os.Stat(c.AuthorizedKeys)
		check("Wish allowlist", c.AuthorizedKeys != "" && err == nil, c.AuthorizedKeys, true)
	}
	if bad {
		return errors.New("required configuration is incomplete")
	}
	return nil
}
func registerDiscord(c config.Config) error {
	app := os.Getenv("DISCORD_APPLICATION_ID")
	token := os.Getenv("DISCORD_BOT_TOKEN")
	if app == "" || token == "" {
		return errors.New("set DISCORD_APPLICATION_ID and DISCORD_BOT_TOKEN")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	body := map[string]any{"name": "coldflow", "description": "Chat with your research outreach bots", "options": []any{map[string]any{"name": "text", "description": "Message, status, pause, resume, or remember ...", "type": 3, "required": true}, map[string]any{"name": "agent", "description": "Bot name, such as Scout or Composer", "type": 3, "required": false}}, "default_member_permissions": "0"}
	path := "https://discord.com/api/v10/applications/" + app + "/commands"
	if guild := os.Getenv("DISCORD_GUILD_ID"); guild != "" {
		path = "https://discord.com/api/v10/applications/" + app + "/guilds/" + guild + "/commands"
	}
	if err := integrations.NewHTTP().Do(ctx, "POST", path, map[string]string{"Authorization": "Bot " + token}, body, nil); err != nil {
		return err
	}
	fmt.Println("Registered /coldflow. Enable command access for your operator in Discord integration settings.")
	return nil
}
