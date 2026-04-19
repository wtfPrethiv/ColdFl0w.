package config

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"
)

// Config contains preferences only. Credentials are resolved from environment.
type Config struct {
	DataDir           string `json:"data_dir"`
	Listen            string `json:"listen"`
	Timezone          string `json:"timezone"`
	Demo              bool   `json:"demo"`
	AIProvider        string `json:"ai_provider"`
	AIModel           string `json:"ai_model"`
	AIBaseURL         string `json:"ai_base_url"`
	DailyTokens       int    `json:"daily_tokens"`
	PollSeconds       int    `json:"poll_seconds"`
	GmailClientID     string `json:"gmail_client_id"`
	GmailProject      string `json:"gmail_project"`
	GmailTopic        string `json:"gmail_topic"`
	GmailSubscription string `json:"gmail_subscription"`
	DiscordChannel    string `json:"discord_channel"`
	DiscordUser       string `json:"discord_user"`
	DiscordPublicKey  string `json:"discord_public_key"`
	WhatsAppPhoneID   string `json:"whatsapp_phone_id"`
	WhatsAppUser      string `json:"whatsapp_user"`
	WhatsAppTemplate  string `json:"whatsapp_template"`
	WhatsAppLanguage  string `json:"whatsapp_language"`
	GraphVersion      string `json:"graph_version"`
	NotionDataSource  string `json:"notion_data_source"`
	WishListen        string `json:"wish_listen"`
	AuthorizedKeys    string `json:"authorized_keys"`
	ReducedMotion     bool   `json:"reduced_motion"`
}

func Default(dir string) Config {
	return Config{DataDir: dir, Listen: "127.0.0.1:8765", Timezone: "Asia/Kolkata", AIProvider: "openai", AIModel: "", DailyTokens: 50000, PollSeconds: 60, WhatsAppLanguage: "en", GraphVersion: "v23.0"}
}
func Path(dir string) string { return filepath.Join(dir, "config.json") }
func Load(dir string) (Config, error) {
	c := Default(dir)
	b, err := os.ReadFile(Path(dir))
	if err != nil {
		return c, err
	}
	if err = json.Unmarshal(b, &c); err != nil {
		return c, err
	}
	c.DataDir = dir
	if c.PollSeconds < 15 {
		c.PollSeconds = 15
	}
	if c.DailyTokens < 1000 {
		return c, errors.New("daily_tokens must be >= 1000")
	}
	if _, err = time.LoadLocation(c.Timezone); err != nil {
		return c, err
	}
	return c, nil
}
func Save(c Config) error {
	if err := os.MkdirAll(c.DataDir, 0700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(Path(c.DataDir), b, 0600)
}
