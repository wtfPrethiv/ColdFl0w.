package engine

import (
	"context"
	"encoding/base64"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"coldflow/internal/config"
	"coldflow/internal/domain"
	gmail "coldflow/internal/mail"
	"coldflow/internal/store"
)

type fakeAI struct{ responses []string }

func (f *fakeAI) Generate(context.Context, string, string) (string, error) {
	if len(f.responses) == 0 {
		return "", errors.New("no fixture response")
	}
	v := f.responses[0]
	f.responses = f.responses[1:]
	return v, nil
}

type fakeMail struct {
	found   *gmail.Sent
	changes []gmail.Message
	sent    int
}

func (f *fakeMail) Send(context.Context, domain.Draft, domain.Contact, []domain.Attachment) (gmail.Sent, error) {
	f.sent++
	return gmail.Sent{ID: "sent", ThreadID: "thread"}, nil
}
func (f *fakeMail) Find(context.Context, string) (*gmail.Sent, error) { return f.found, nil }
func (f *fakeMail) Changes(context.Context, string) ([]gmail.Message, string, error) {
	return f.changes, "new-history", nil
}
func (f *fakeMail) Latest(context.Context) (string, error) { return "baseline", nil }

type fakeSource struct {
	paper domain.Paper
	page  string
}

func (f fakeSource) Fetch(context.Context, string) (string, error) { return f.page, nil }
func (f fakeSource) Discover(context.Context, string, []string) ([]string, error) {
	return []string{"https://example.edu/ada"}, nil
}
func (f fakeSource) Papers(context.Context, domain.Contact, string) ([]domain.Paper, error) {
	return []domain.Paper{f.paper}, nil
}
func fixture(t *testing.T) *Engine {
	t.Helper()
	c := config.Default(t.TempDir())
	c.Demo = true
	s, err := store.Open(c.DataDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	e := New(c, s)
	if err = e.SeedDemo(); err != nil {
		t.Fatal(err)
	}
	return e
}

func TestApprovalRevisionsAndDuplicateEnter(t *testing.T) {
	e := fixture(t)
	if err := e.Approve("draft-ada", 2); err == nil {
		t.Fatal("stale approval accepted")
	}
	var wg sync.WaitGroup
	success := make(chan bool, 2)
	for range 2 {
		wg.Add(1)
		go func() { defer wg.Done(); success <- e.Approve("draft-ada", 1) == nil }()
	}
	wg.Wait()
	close(success)
	count := 0
	for ok := range success {
		if ok {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("%d approvals accepted", count)
	}
	if err := e.EditDraft("draft-ada", 1, "Revised subject", "Revised content", nil); err != nil {
		t.Fatal(err)
	}
	d, _ := store.Get[domain.Draft](e.Store, "draft", "draft-ada")
	if d.Revision != 2 || d.ApprovedRevision != 0 {
		t.Fatal(d)
	}
	if err := e.Approve(d.ID, 1); err == nil {
		t.Fatal("old revision accepted")
	}
	if err := e.Approve(d.ID, 2); err != nil {
		t.Fatal(err)
	}
	jobs, _ := e.Store.Jobs()
	if len(jobs) != 2 {
		t.Fatalf("new revision did not get its own delivery job: %+v", jobs)
	}
}
func TestReplyCancelsAndBlocksOutreach(t *testing.T) {
	e := fixture(t)
	e.Config.Demo = false
	original := domain.Draft{ID: "original", ContactID: "ada", CampaignID: "demo-campaign", Status: "sent", ThreadID: "tracked", SentAt: domain.Now()}
	if err := e.Store.Put("draft", original.ID, original); err != nil {
		t.Fatal(err)
	}
	m := gmail.Message{ID: "reply", ThreadID: "tracked"}
	m.Payload.MIMEType = "text/plain"
	m.Payload.Body.Data = base64.RawURLEncoding.EncodeToString([]byte("Thanks, let us discuss your interests."))
	f := &fakeMail{changes: []gmail.Message{m}}
	e.Mail = f
	e.Store.Set("gmail:history", "old")
	if err := e.syncInbox(context.Background()); err != nil {
		t.Fatal(err)
	}
	c, _ := store.Get[domain.Contact](e.Store, "contact", "ada")
	if c.Status != "replied" {
		t.Fatal(c.Status)
	}
	d, _ := store.Get[domain.Draft](e.Store, "draft", "draft-ada")
	if d.Status != "cancelled" {
		t.Fatal(d.Status)
	}
	if err := e.send(context.Background(), d.ID); err != nil {
		t.Fatal(err)
	}
	if f.sent != 0 {
		t.Fatal("sent after reply")
	}
	if err := e.syncInbox(context.Background()); err != nil {
		t.Fatal(err)
	}
	messages, _ := store.List[domain.InboxMessage](e.Store, "inbox")
	if len(messages) != 1 {
		t.Fatal("duplicate reply inserted")
	}
}
func TestAmbiguousDeliveryReconcilesWithoutResending(t *testing.T) {
	for _, delivered := range []bool{false, true} {
		t.Run(map[bool]string{false: "not-found", true: "found"}[delivered], func(t *testing.T) {
			e := fixture(t)
			e.Config.Demo = false
			d, _ := store.Get[domain.Draft](e.Store, "draft", "draft-ada")
			d.AttemptedAt = domain.Now()
			d.RFCMessageID = "known@coldflow.local"
			d.Status = "sending"
			e.Store.Put("draft", d.ID, d)
			f := &fakeMail{}
			if delivered {
				f.found = &gmail.Sent{ID: "gmail-id", ThreadID: "gmail-thread"}
			}
			e.Mail = f
			if err := e.send(context.Background(), d.ID); err != nil {
				t.Fatal(err)
			}
			d, _ = store.Get[domain.Draft](e.Store, "draft", d.ID)
			want := "uncertain"
			if delivered {
				want = "sent"
			}
			if d.Status != want || f.sent != 0 {
				t.Fatalf("status=%s sends=%d", d.Status, f.sent)
			}
		})
	}
}
func TestEvidenceCannotBeInvented(t *testing.T) {
	e := fixture(t)
	p := domain.Paper{ID: "actual", ContactID: "ada", Title: "Compact learning", URL: "https://example.edu/paper", Abstract: strings.Repeat("The model requires less memory. ", 8)}
	e.Store.Put("paper", p.ID, p)
	e.AI = &fakeAI{responses: []string{`[{"text":"This invented result does not appear in the paper."}]`}}
	if err := e.analyze(context.Background(), p.ID); err == nil {
		t.Fatal("invented quotation accepted")
	}
}
func TestResearchPipelineAndAbstractReview(t *testing.T) {
	e := fixture(t)
	e.Config.Demo = false
	c, _ := store.Get[domain.Campaign](e.Store, "campaign", "demo-campaign")
	c.Mode = "automatic"
	e.Store.Put("campaign", c.ID, c)
	p := domain.Paper{ID: "real-paper", ContactID: "mira", Title: "Memory-efficient learning", URL: "https://example.edu/paper", Authors: []string{"Dr. Mira Demo"}, Abstract: strings.Repeat("The compact model used less memory in the experiment. ", 5)}
	e.Store.Delete("draft", "draft-mira")
	e.Web = fakeSource{paper: p}
	e.AI = &fakeAI{responses: []string{`{"index":0,"reason":"Related to efficient ML"}`, `[{"text":"The compact model used less memory in the experiment."}]`, `{"subject":"Research interest","body":"Dear Professor, I am interested in efficient learning. Could we discuss research opportunities?"}`, `{"supported":true,"reason":"Supported"}`}}
	if err := e.match(context.Background(), "mira"); err != nil {
		t.Fatal(err)
	}
	if err := e.analyze(context.Background(), p.ID); err != nil {
		t.Fatal(err)
	}
	if err := e.compose(context.Background(), p.ID, false); err != nil {
		t.Fatal(err)
	}
	drafts, _ := store.List[domain.Draft](e.Store, "draft")
	var d domain.Draft
	for _, v := range drafts {
		if v.PaperID == p.ID {
			d = v
		}
	}
	if d.ID == "" || d.BlockReason != "Abstract-only evidence requires manual approval" {
		t.Fatalf("%+v", d)
	}
	if err := e.Approve(d.ID, d.Revision); err != nil {
		t.Fatal(err)
	}
}
func TestWebhookAndMemoryIdempotence(t *testing.T) {
	e := fixture(t)
	for range 2 {
		if err := e.Incoming("same-id", "discord", "Scout: remember Be concise"); err != nil {
			t.Fatal(err)
		}
	}
	j, err := e.Store.Claim()
	if err != nil {
		t.Fatal(err)
	}
	if err = e.work(context.Background(), j); err != nil {
		t.Fatal(err)
	}
	e.Store.Finish(j, nil)
	j, err = e.Store.Claim()
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err = e.work(context.Background(), j); err != nil {
			t.Fatal(err)
		}
	}
	mem, _ := store.List[domain.Memory](e.Store, "memory")
	count := 0
	for _, m := range mem {
		if m.Agent == "Scout" {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("%d duplicate memories", count)
	}
}
func TestSendingWindow(t *testing.T) {
	for _, v := range []struct {
		at   string
		want bool
	}{{"2026-10-26T09:00:00Z", true}, {"2026-10-26T16:59:00Z", true}, {"2026-10-26T17:00:00Z", false}, {"2026-10-25T12:00:00Z", false}} {
		at, _ := time.Parse(time.RFC3339, v.at)
		if SendingWindow(at) != v.want {
			t.Fatal(v)
		}
	}
}

func TestChangedAttachmentBlocksDelivery(t *testing.T) {
	e := fixture(t)
	e.Config.Demo = false
	e.Now = func() time.Time { return time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC) }
	e.Config.Timezone = "UTC"
	e.Mail = &fakeMail{}
	a, err := e.AddAttachment("cv.txt", strings.NewReader("original CV"))
	if err != nil {
		t.Fatal(err)
	}
	d, _ := store.Get[domain.Draft](e.Store, "draft", "draft-ada")
	d.AttachmentIDs = []string{a.ID}
	d.AttachmentHashes = map[string]string{a.ID: a.SHA256}
	e.Store.Put("draft", d.ID, d)
	if err = e.Approve(d.ID, d.Revision); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(a.Path, []byte("modified CV"), 0600); err != nil {
		t.Fatal(err)
	}
	if err = e.send(context.Background(), d.ID); err == nil || !strings.Contains(err.Error(), "attachment changed") {
		t.Fatalf("%v", err)
	}
	if e.Mail.(*fakeMail).sent != 0 {
		t.Fatal("changed attachment was sent")
	}
}
func TestGlobalCapSpansCampaigns(t *testing.T) {
	e := fixture(t)
	e.Config.Demo = false
	e.Config.Timezone = "UTC"
	e.Now = func() time.Time { return time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC) }
	e.Config.DailyNewLimit = 1
	f := &fakeMail{}
	e.Mail = f
	sent := domain.Draft{ID: "other-campaign-sent", CampaignID: "other", Status: "sent", SentAt: "2026-10-26T10:00:00Z"}
	e.Store.Put("draft", sent.ID, sent)
	if err := e.Approve("draft-ada", 1); err != nil {
		t.Fatal(err)
	}
	var deferred *Deferred
	if err := e.send(context.Background(), "draft-ada"); !errors.As(err, &deferred) {
		t.Fatalf("expected global cap deferral, got %v", err)
	}
	if f.sent != 0 {
		t.Fatal("global cap ignored")
	}
}
