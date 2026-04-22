package server

import (
	"crypto/ed25519"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"coldflow/internal/config"
	"coldflow/internal/engine"
	"coldflow/internal/store"
)

func TestWebhookSignaturesAndReplay(t *testing.T) {
	body := []byte(`{"type":1}`)
	secret := "test-secret"
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	signature := "sha256=" + hex.EncodeToString(mac.Sum(nil))
	if !WhatsAppSignature(secret, body, signature) || WhatsAppSignature(secret, []byte("tampered"), signature) || WhatsAppSignature("", body, signature) {
		t.Fatal("WhatsApp signature validation failed")
	}
	pub, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	ts := strconv.FormatInt(now.Unix(), 10)
	sig := hex.EncodeToString(ed25519.Sign(key, append([]byte(ts), body...)))
	if !DiscordSignature(hex.EncodeToString(pub), sig, ts, body, now) {
		t.Fatal("valid Discord signature rejected")
	}
	if DiscordSignature(hex.EncodeToString(pub), sig, ts, body, now.Add(10*time.Minute)) {
		t.Fatal("stale replay accepted")
	}
}
func TestAPIBearerAndApproval(t *testing.T) {
	c := config.Default(t.TempDir())
	c.Demo = true
	db, err := store.Open(c.DataDir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	e := engine.New(c, db)
	if err = e.SeedDemo(); err != nil {
		t.Fatal(err)
	}
	s, err := New(e)
	if err != nil {
		t.Fatal(err)
	}
	handler := s.Handler()
	r := httptest.NewRequest("GET", "/api/snapshot", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code != 401 {
		t.Fatal(w.Code)
	}
	r = httptest.NewRequest("GET", "/api/snapshot", nil)
	r.Header.Set("Authorization", "Bearer "+s.Token)
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "demo-campaign") {
		t.Fatal(w.Code, w.Body.String())
	}
	r = httptest.NewRequest("POST", "/api/action", strings.NewReader(`{"kind":"approve","id":"draft-ada","revision":99}`))
	r.Header.Set("Authorization", "Bearer "+s.Token)
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code != http.StatusBadRequest {
		t.Fatal("stale revision accepted", w.Code)
	}
}
