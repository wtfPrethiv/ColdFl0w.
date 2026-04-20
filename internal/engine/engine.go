package engine

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"charm.land/log/v2"
	"coldflow/internal/ai"
	"coldflow/internal/config"
	"coldflow/internal/domain"
	"coldflow/internal/integrations"
	"coldflow/internal/mail"
	"coldflow/internal/research"
	"coldflow/internal/store"
)

type Engine struct {
	Config  config.Config
	Store   *store.Store
	AI      ai.Generator
	Web     research.Source
	Mail    mail.Mailer
	Notify  *integrations.Notifier
	Notion  *integrations.Notion
	gate    sync.Mutex
	stateMu sync.Mutex
	states  map[string]string
	wake    chan struct{}
}
type Payload struct {
	ID      string `json:"id"`
	Agent   string `json:"agent"`
	Text    string `json:"text"`
	Channel string `json:"channel"`
}
type Snapshot struct {
	Agents      []domain.Agent        `json:"agents"`
	Profile     domain.Profile        `json:"profile"`
	Campaigns   []domain.Campaign     `json:"campaigns"`
	Contacts    []domain.Contact      `json:"contacts"`
	Papers      []domain.Paper        `json:"papers"`
	Drafts      []domain.Draft        `json:"drafts"`
	Attachments []domain.Attachment   `json:"attachments"`
	Events      []domain.Event        `json:"events"`
	Memories    []domain.Memory       `json:"memories"`
	Chats       []domain.Chat         `json:"chats"`
	Inbox       []domain.InboxMessage `json:"inbox"`
	Jobs        []map[string]any      `json:"jobs"`
	Paused      bool                  `json:"paused"`
	Demo        bool                  `json:"demo"`
	Health      map[string]string     `json:"health"`
}

func New(c config.Config, s *store.Store) *Engine {
	h := integrations.NewHTTP()
	return &Engine{Config: c, Store: s, AI: &ai.Client{Config: c, Store: s}, Web: research.New(filepath.Join(c.DataDir, "cache")), Mail: &mail.Gmail{Config: c}, Notify: &integrations.Notifier{Config: c, HTTP: h}, Notion: &integrations.Notion{Config: c, HTTP: h}, states: map[string]string{}, wake: make(chan struct{}, 1)}
}
func (e *Engine) event(agent, kind, text string) {
	text = integrations.CleanText(text)
	_ = e.Store.Event(agent, kind, text)
	log.Info(text, "agent", agent, "kind", kind)
}
func (e *Engine) state(agent, state string) {
	e.stateMu.Lock()
	e.states[agent] = state
	e.stateMu.Unlock()
}
func (e *Engine) Snapshot() (Snapshot, error) {
	s := Snapshot{Paused: e.Store.Value("paused") == "true", Demo: e.Config.Demo, Health: map[string]string{}}
	var err error
	if s.Campaigns, err = store.List[domain.Campaign](e.Store, "campaign"); err != nil {
		return s, err
	}
	s.Contacts, err = store.List[domain.Contact](e.Store, "contact")
	if err != nil {
		return s, err
	}
	s.Papers, _ = store.List[domain.Paper](e.Store, "paper")
	for i := range s.Papers {
		s.Papers[i].Text = ""
	}
	s.Drafts, _ = store.List[domain.Draft](e.Store, "draft")
	s.Attachments, _ = store.List[domain.Attachment](e.Store, "attachment")
	s.Memories, _ = store.List[domain.Memory](e.Store, "memory")
	s.Chats, _ = store.List[domain.Chat](e.Store, "chat")
	if len(s.Chats) > 150 {
		s.Chats = s.Chats[:150]
	}
	s.Inbox, _ = store.List[domain.InboxMessage](e.Store, "inbox")
	s.Events, _ = e.Store.Events(120)
	s.Jobs, _ = e.Store.Jobs()
	s.Profile, _ = store.Get[domain.Profile](e.Store, "profile", "user")
	e.stateMu.Lock()
	for _, a := range domain.Agents {
		if v := e.states[a.Name]; v != "" {
			a.State = v
		}
		s.Agents = append(s.Agents, a)
	}
	e.stateMu.Unlock()
	for _, k := range []string{"inbox", "notion", "discord", "whatsapp", "pubsub"} {
		s.Health[k] = e.Store.Value("health:" + k)
	}
	s.Health["ai_tokens"] = e.Store.Value("tokens:" + time.Now().UTC().Format("2006-01-02"))
	return s, nil
}
func (e *Engine) Run(ctx context.Context) {
	var wg sync.WaitGroup
	wg.Add(1)
	go func() { defer wg.Done(); e.inboxLoop(ctx) }()
	if !e.Config.Demo && e.Config.GmailSubscription != "" {
		wg.Add(1)
		go func() { defer wg.Done(); e.pubsubLoop(ctx) }()
	}
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	maintenance := time.NewTicker(time.Minute)
	defer maintenance.Stop()
	e.maintenance(ctx)
	for {
		select {
		case <-ctx.Done():
			wg.Wait()
			return
		case <-maintenance.C:
			e.maintenance(ctx)
		case <-ticker.C:
			j, err := e.Store.Claim()
			if err != nil {
				continue
			}
			agent := agentFor(j.Kind)
			e.state(agent, "working: "+j.Kind)
			child, cancel := context.WithTimeout(ctx, 8*time.Minute)
			err = e.work(child, j)
			cancel()
			e.state(agent, "idle")
			var later *Deferred
			if errors.As(err, &later) {
				_ = e.Store.Defer(j, later.At)
				continue
			}
			var rate *integrations.RateLimit
			if errors.As(err, &rate) {
				_ = e.Store.Defer(j, time.Now().Add(rate.After))
				continue
			}
			if err != nil {
				e.event(agent, "error", j.Kind+" needs attention: "+err.Error())
				if j.Attempts >= 5 {
					e.alert("A " + j.Kind + " task needs attention. Open Jobs in Coldflow to retry.")
				}
			}
			_ = e.Store.Finish(j, err)
		}
	}
}
func agentFor(kind string) string {
	switch kind {
	case "discover":
		return "Scout"
	case "match":
		return "Matcher"
	case "analyze":
		return "Analyst"
	case "compose", "followup":
		return "Composer"
	case "send":
		return "Courier"
	case "classify":
		return "Inbox"
	case "notion", "digest", "notify":
		return "Reporter"
	default:
		return "Coordinator"
	}
}
func (e *Engine) work(ctx context.Context, j domain.Job) error {
	var p Payload
	if err := json.Unmarshal([]byte(j.Payload), &p); err != nil {
		return err
	}
	switch j.Kind {
	case "discover":
		return e.discover(ctx, p.ID)
	case "match":
		return e.match(ctx, p.ID)
	case "analyze":
		return e.analyze(ctx, p.ID)
	case "compose":
		return e.compose(ctx, p.ID, false)
	case "followup":
		return e.compose(ctx, p.ID, true)
	case "send":
		return e.send(ctx, p.ID)
	case "chat":
		return e.chat(ctx, p)
	case "classify":
		return e.classify(ctx, p.ID)
	case "notion":
		return e.syncNotion(ctx)
	case "digest":
		return e.digest(ctx)
	case "notify":
		var err error
		if p.Channel == "discord" {
			err = e.Notify.Discord(ctx, p.Text)
		} else {
			last, _ := time.Parse(time.RFC3339, e.Store.Value("whatsapp:last_user"))
			err = e.Notify.WhatsApp(ctx, p.Text, time.Since(last) < 23*time.Hour)
		}
		health := "connected"
		if err != nil {
			health = err.Error()
		}
		_ = e.Store.Set("health:"+p.Channel, health)
		return err
	}
	return errors.New("unknown job kind")
}

type Deferred struct{ At time.Time }

func (d *Deferred) Error() string { return "deferred until " + d.At.Format(time.RFC3339) }
func (e *Engine) queue(kind, id string) error {
	key := kind + ":" + id
	if kind == "send" {
		if d, err := store.Get[domain.Draft](e.Store, "draft", id); err == nil {
			key += ":" + strconv.Itoa(d.Revision)
		}
	}
	return e.Store.Enqueue(kind, key, Payload{ID: id}, time.Now())
}
func (e *Engine) maintenance(ctx context.Context) {
	loc, _ := time.LoadLocation(e.Config.Timezone)
	now := time.Now().In(loc)
	campaigns, _ := store.List[domain.Campaign](e.Store, "campaign")
	if e.Store.Value("paused") != "true" {
		for _, c := range campaigns {
			if !c.Paused {
				_ = e.Store.Enqueue("discover", "discover:"+c.ID+":"+now.Format("2006-01-02"), Payload{ID: c.ID}, time.Now())
			}
		}
	}
	if !e.Config.Demo && e.Config.NotionDataSource != "" {
		_ = e.Store.Enqueue("notion", "notion:"+strconv.FormatInt(time.Now().Unix()/300, 10), Payload{}, time.Now())
	}
	if now.Hour() >= 20 {
		_ = e.Store.Enqueue("digest", "digest:"+now.Format("2006-01-02"), Payload{}, time.Now())
	}
	contacts, _ := store.List[domain.Contact](e.Store, "contact")
	for _, c := range contacts {
		at, err := time.Parse(time.RFC3339, c.NextAction)
		if err != nil {
			at, err = time.Parse("2006-01-02", c.NextAction)
		}
		if err == nil && at.Before(time.Now()) {
			key := "reminded:" + c.ID + ":" + c.NextAction
			if e.Store.Value(key) == "" {
				e.alert("Reminder: " + c.Name + " (" + c.Status + "). " + c.Notes)
				_ = e.Store.Set(key, domain.Now())
			}
		}
	}
	_ = e.Store.Prune()
	if w, ok := e.Web.(*research.Web); ok {
		w.Prune()
	}
}
func (e *Engine) alert(text string) {
	e.event("Reporter", "notification", text)
	if e.Config.Demo {
		return
	}
	for channel, enabled := range map[string]bool{"discord": e.Config.DiscordChannel != "", "whatsapp": e.Config.WhatsAppPhoneID != ""} {
		if enabled {
			_ = e.Store.Enqueue("notify", "notify:"+channel+":"+domain.ID(), Payload{Text: text, Channel: channel}, time.Now())
		}
	}
}
func (e *Engine) AddCampaign(c domain.Campaign) (domain.Campaign, error) {
	if strings.TrimSpace(c.Name) == "" || strings.TrimSpace(c.Interests) == "" {
		return c, errors.New("campaign name and interests are required")
	}
	if c.ID != "" {
		return c, errors.New("use campaign settings to edit an existing campaign")
	}
	c.ID = domain.ID()
	if c.Mode == "" {
		c.Mode = "approval"
	}
	if c.Mode != "approval" && c.Mode != "automatic" {
		return c, errors.New("mode must be approval or automatic")
	}
	if c.DailyLimit <= 0 {
		c.DailyLimit = 10
	}
	if c.FollowupLimit <= 0 {
		c.FollowupLimit = 10
	}
	if c.Template == "" {
		c.Template = domain.DefaultTemplate
	}
	for _, id := range c.AttachmentIDs {
		if _, err := store.Get[domain.Attachment](e.Store, "attachment", id); err != nil {
			return c, errors.New("unknown campaign attachment")
		}
	}
	err := e.Store.Put("campaign", c.ID, c)
	if err == nil {
		e.event("Coordinator", "campaign", "Created campaign "+c.Name)
		_ = e.queue("discover", c.ID+"")
	}
	return c, err
}
func (e *Engine) Remember(agent, text, source string) error {
	if !validAgent(agent) && agent != "shared" {
		return errors.New("unknown agent")
	}
	text = strings.TrimSpace(text)
	if text == "" || len(text) > 4000 {
		return errors.New("memory must contain 1-4000 bytes")
	}
	m := domain.Memory{ID: domain.ID(), Agent: agent, Text: text, Source: source, At: domain.Now()}
	return e.Store.Put("memory", m.ID, m)
}
func validAgent(name string) bool {
	for _, a := range domain.Agents {
		if a.Name == name {
			return true
		}
	}
	return false
}
func (e *Engine) Chat(agent, text, channel string) error {
	if !validAgent(agent) {
		return errors.New("unknown agent")
	}
	if len(text) > 4000 || strings.TrimSpace(text) == "" {
		return errors.New("chat must contain 1-4000 bytes")
	}
	c := domain.Chat{ID: domain.ID(), Agent: agent, Role: "user", Text: text, At: domain.Now()}
	if err := e.Store.Put("chat", c.ID, c); err != nil {
		return err
	}
	return e.Store.Enqueue("chat", "chat:"+c.ID, Payload{ID: c.ID, Agent: agent, Text: text, Channel: channel}, time.Now())
}
func (e *Engine) chat(ctx context.Context, p Payload) error {
	answer := ""
	if strings.HasPrefix(strings.ToLower(p.Text), "remember ") {
		err := e.Remember(p.Agent, strings.TrimSpace(p.Text[9:]), "explicit user correction")
		if err != nil {
			return err
		}
		answer = "I saved that preference to my memory. You can edit or forget it in Memory."
	} else if strings.EqualFold(strings.TrimSpace(p.Text), "status") {
		contacts, _ := store.List[domain.Contact](e.Store, "contact")
		drafts, _ := store.List[domain.Draft](e.Store, "draft")
		answer = fmt.Sprintf("Tracking %d contacts and %d drafts. Global pause: %s.", len(contacts), len(drafts), e.Store.Value("paused"))
	} else if e.Config.Demo {
		answer = "Demo mode: I can show status and save preferences with 'remember ...'. Connect an AI provider for research conversations."
	} else {
		history, _ := store.List[domain.Chat](e.Store, "chat")
		var contextText strings.Builder
		for i := len(history) - 1; i >= 0; i-- {
			c := history[i]
			if c.Agent == p.Agent && c.ID != p.ID && contextText.Len() < 6000 {
				contextText.WriteString(c.Role + ": " + c.Text + "\n")
			}
		}
		answerRaw, err := e.AI.Generate(ctx, "You are "+p.Agent+". Answer the user's question about outreach. Memory:\n"+e.memory(p.Agent), "Recent conversation:\n"+contextText.String()+"\nCurrent request:\n"+p.Text)
		if err != nil {
			return err
		}
		answer = answerRaw
	}
	c := domain.Chat{ID: domain.ID(), Agent: p.Agent, Role: "assistant", Text: answer, At: domain.Now()}
	if err := e.Store.Put("chat", c.ID, c); err != nil {
		return err
	}
	if p.Channel != "" {
		return e.Store.Enqueue("notify", "chat-reply:"+p.ID, Payload{Channel: p.Channel, Text: p.Agent + ": " + answer}, time.Now())
	}
	return nil
}
func (e *Engine) memory(agent string) string {
	all, _ := store.List[domain.Memory](e.Store, "memory")
	var b strings.Builder
	for _, m := range all {
		if m.Agent == agent || m.Agent == "shared" {
			if b.Len()+len(m.Text) > 4000 {
				break
			}
			b.WriteString(m.Text + "\n")
		}
	}
	return b.String()
}
func (e *Engine) AddAttachment(name string, r io.Reader) (domain.Attachment, error) {
	a := domain.Attachment{ID: domain.ID(), Name: filepath.Base(name)}
	if a.Name == "." || a.Name == "" || strings.ContainsAny(a.Name, "\r\n") {
		return a, errors.New("invalid attachment name")
	}
	dir := filepath.Join(e.Config.DataDir, "attachments")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return a, err
	}
	a.Path = filepath.Join(dir, a.ID)
	f, err := os.OpenFile(a.Path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return a, err
	}
	h := sha256.New()
	a.Size, err = io.Copy(io.MultiWriter(f, h), io.LimitReader(r, (10<<20)+1))
	closeErr := f.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil || a.Size > 10<<20 {
		os.Remove(a.Path)
		return a, errors.New("attachment failed or exceeds 10 MiB")
	}
	a.SHA256 = hex.EncodeToString(h.Sum(nil))
	if err = e.Store.Put("attachment", a.ID, a); err != nil {
		os.Remove(a.Path)
	}
	return a, err
}
