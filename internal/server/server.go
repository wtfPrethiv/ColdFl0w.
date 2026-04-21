package server

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"coldflow/internal/config"
	"coldflow/internal/domain"
	"coldflow/internal/engine"
	"coldflow/internal/store"
)

type Server struct {
	Engine *engine.Engine
	Token  string
}

func Token(dir string) (string, error) {
	path := filepath.Join(dir, "api-token")
	b, err := os.ReadFile(path)
	if err == nil {
		return strings.TrimSpace(string(b)), nil
	}
	if !os.IsNotExist(err) {
		return "", err
	}
	var key [32]byte
	if _, err = rand.Read(key[:]); err != nil {
		return "", err
	}
	token := hex.EncodeToString(key[:])
	err = os.WriteFile(path, []byte(token), 0600)
	return token, err
}
func New(e *engine.Engine) (*Server, error) {
	token, err := Token(e.Config.DataDir)
	return &Server{e, token}, err
}
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) { write(w, map[string]string{"status": "running"}) })
	mux.HandleFunc("GET /webhooks/whatsapp", s.whatsappVerify)
	mux.HandleFunc("POST /webhooks/whatsapp", s.whatsapp)
	mux.HandleFunc("POST /webhooks/discord", s.discord)
	mux.Handle("/api/", s.authorize(http.HandlerFunc(s.api)))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Cache-Control", "no-store")
		r.Body = http.MaxBytesReader(w, r.Body, 16<<20)
		mux.ServeHTTP(w, r)
	})
}
func (s *Server) authorize(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if len(token) != len(s.Token) || subtle.ConstantTimeCompare([]byte(token), []byte(s.Token)) != 1 {
			http.Error(w, "unauthorized", 401)
			return
		}
		next.ServeHTTP(w, r)
	})
}
func write(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}
func decode(r *http.Request, v any) error {
	d := json.NewDecoder(io.LimitReader(r.Body, 2<<20))
	d.DisallowUnknownFields()
	return d.Decode(v)
}

type Action struct {
	Kind        string   `json:"kind"`
	ID          string   `json:"id"`
	Revision    int      `json:"revision"`
	Text        string   `json:"text"`
	Agent       string   `json:"agent"`
	Subject     string   `json:"subject"`
	Body        string   `json:"body"`
	Attachments []string `json:"attachments"`
	Status      string   `json:"status"`
	NextAction  string   `json:"next_action"`
	Mode        string   `json:"mode"`
	Paused      bool     `json:"paused"`
	Limit       int      `json:"limit"`
}

func (s *Server) api(w http.ResponseWriter, r *http.Request) {
	var err error
	switch r.Method + " " + r.URL.Path {
	case "GET /api/snapshot":
		var snap engine.Snapshot
		snap, err = s.Engine.Snapshot()
		if err == nil {
			write(w, snap)
			return
		}
	case "GET /api/search":
		var found []string
		found, err = s.Engine.Store.Search(r.URL.Query().Get("q"))
		if err == nil {
			write(w, found)
			return
		}
	case "POST /api/campaign":
		var c domain.Campaign
		if err = decode(r, &c); err == nil {
			c, err = s.Engine.AddCampaign(c)
			if err == nil {
				write(w, c)
				return
			}
		}
	case "PUT /api/profile":
		var p domain.Profile
		if err = decode(r, &p); err == nil {
			if p.Name == "" || p.Interests == "" || p.Goal == "" {
				err = errors.New("name, interests and goal are required")
			} else {
				err = s.Engine.Store.Put("profile", "user", p)
			}
		}
	case "PUT /api/config":
		var c config.Config
		if err = decode(r, &c); err == nil {
			c.DataDir = s.Engine.Config.DataDir
			err = config.Save(c)
			if err == nil {
				write(w, map[string]string{"status": "saved; restart service to apply integration settings"})
				return
			}
		}
	case "POST /api/attachments":
		var a domain.Attachment
		a, err = s.Engine.AddAttachment(r.URL.Query().Get("name"), r.Body)
		if err == nil {
			write(w, a)
			return
		}
	case "POST /api/action":
		var a Action
		if err = decode(r, &a); err == nil {
			err = s.action(r.Context(), a)
		}
	default:
		http.Error(w, "not found", 404)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	write(w, map[string]bool{"ok": true})
}
func (s *Server) action(ctx context.Context, a Action) error {
	e := s.Engine
	switch a.Kind {
	case "approve":
		return e.Approve(a.ID, a.Revision)
	case "skip":
		return e.Skip(a.ID)
	case "edit-draft":
		return e.EditDraft(a.ID, a.Revision, a.Subject, a.Body, a.Attachments)
	case "regenerate":
		return e.Regenerate(a.ID, a.Text)
	case "resolve-delivery":
		return e.ResolveDelivery(ctx, a.ID, a.Status == "delivered")
	case "pause":
		return e.Store.Set("paused", "true")
	case "resume":
		return e.Store.Set("paused", "false")
	case "campaign":
		return e.SetCampaign(a.ID, a.Mode, a.Paused, a.Limit)
	case "contact":
		return e.UpdateContact(a.ID, a.Status, a.Text, a.NextAction)
	case "chat":
		return e.Chat(a.Agent, a.Text, "")
	case "remember":
		return e.Remember(a.Agent, a.Text, "explicit user instruction")
	case "edit-memory":
		m, err := store.Get[domain.Memory](e.Store, "memory", a.ID)
		if err != nil {
			return err
		}
		if a.Text == "" || len(a.Text) > 4000 {
			return errors.New("memory must contain 1-4000 bytes")
		}
		m.Text = a.Text
		m.Source = "explicit user edit"
		m.At = domain.Now()
		return e.Store.Put("memory", m.ID, m)
	case "forget":
		return e.Store.Delete("memory", a.ID)
	case "retry":
		return e.Store.Retry(a.ID)
	case "inbox-resync":
		return e.Store.Set("gmail:resync", "true")
	case "discover":
		return e.Store.Enqueue("discover", "manual-discovery:"+domain.ID(), engine.Payload{ID: a.ID}, time.Now())
	case "paper":
		c, err := store.Get[domain.Contact](e.Store, "contact", a.ID)
		if err != nil {
			return err
		}
		if len(a.Body) < 100 || a.Subject == "" || a.Text == "" {
			return errors.New("provide paper title, source URL and at least 100 bytes of source text")
		}
		p := domain.Paper{ID: domain.ID(), ContactID: c.ID, Title: a.Subject, URL: a.Text, Abstract: a.Body, FullText: false}
		if err = e.Store.Put("paper", p.ID, p); err != nil {
			return err
		}
		return e.Store.Enqueue("analyze", "analyze:"+p.ID, engine.Payload{ID: p.ID}, time.Now())
	case "classify":
		m, err := store.Get[domain.InboxMessage](e.Store, "inbox", a.ID)
		if err != nil {
			return err
		}
		c, err := store.Get[domain.Contact](e.Store, "contact", m.ContactID)
		if err != nil {
			return err
		}
		if err = e.UpdateContact(c.ID, a.Status, c.Notes, c.NextAction); err != nil {
			return err
		}
		m.Classification = a.Status
		m.Confidence = 1
		return e.Store.Put("inbox", m.ID, m)
	}
	return errors.New("unknown action")
}
func (s *Server) Run(ctx context.Context) error {
	listener, err := net.Listen("tcp", s.Engine.Config.Listen)
	if err != nil {
		return err
	}
	defer listener.Close()
	// Opening the listener before workers prevents a second process from serving this installation.
	child, cancel := context.WithCancel(ctx)
	defer cancel()
	done := make(chan struct{})
	go func() { defer close(done); s.Engine.Run(child) }()
	srv := &http.Server{Handler: s.Handler(), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 40 * time.Second, WriteTimeout: 120 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16 << 10}
	stopped := make(chan struct{})
	go func() {
		select {
		case <-child.Done():
			shutdown, stop := context.WithTimeout(context.Background(), 5*time.Second)
			defer stop()
			_ = srv.Shutdown(shutdown)
		case <-stopped:
		}
	}()
	fmt.Println("Coldflow service listening on", listener.Addr())
	err = srv.Serve(listener)
	close(stopped)
	cancel()
	<-done
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}
