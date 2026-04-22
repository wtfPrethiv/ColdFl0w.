package domain

import (
	"crypto/rand"
	"encoding/hex"
	"time"
)

func ID() string {
	var b [12]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b[:])
}
func Now() string { return time.Now().UTC().Format(time.RFC3339Nano) }

type Profile struct {
	Name       string `json:"name"`
	Background string `json:"background"`
	Interests  string `json:"interests"`
	Goal       string `json:"goal"`
	Signature  string `json:"signature"`
}
type Campaign struct {
	ID            string   `json:"id"`
	Name          string   `json:"name"`
	Interests     string   `json:"interests"`
	Geography     string   `json:"geography"`
	Exclusions    string   `json:"exclusions"`
	Mode          string   `json:"mode"`
	Paused        bool     `json:"paused"`
	DailyLimit    int      `json:"daily_limit"`
	FollowupLimit int      `json:"followup_limit"`
	Template      string   `json:"template"`
	AttachmentIDs []string `json:"attachment_ids"`
	Seeds         []string `json:"seeds"`
	LastDiscovery string   `json:"last_discovery"`
}
type Contact struct {
	ID           string `json:"id"`
	CampaignID   string `json:"campaign_id"`
	Name         string `json:"name"`
	Email        string `json:"email"`
	University   string `json:"university"`
	URL          string `json:"url"`
	Status       string `json:"status"`
	Notes        string `json:"notes"`
	NextAction   string `json:"next_action"`
	UpdatedAt    string `json:"updated_at"`
	NotionID     string `json:"notion_id"`
	NotionEdited string `json:"notion_edited"`
}
type Evidence struct {
	Text     string `json:"text"`
	Source   string `json:"source"`
	Location string `json:"location"`
}
type Paper struct {
	ID        string     `json:"id"`
	ContactID string     `json:"contact_id"`
	Title     string     `json:"title"`
	URL       string     `json:"url"`
	PDFURL    string     `json:"pdf_url"`
	Authors   []string   `json:"authors"`
	Abstract  string     `json:"abstract"`
	Text      string     `json:"text,omitempty"`
	Year      int        `json:"year"`
	Score     float64    `json:"score"`
	FullText  bool       `json:"full_text"`
	Points    []Evidence `json:"points"`
}
type Draft struct {
	ID               string            `json:"id"`
	ContactID        string            `json:"contact_id"`
	CampaignID       string            `json:"campaign_id"`
	PaperID          string            `json:"paper_id"`
	Subject          string            `json:"subject"`
	From             string            `json:"from"`
	Body             string            `json:"body"`
	AttachmentIDs    []string          `json:"attachment_ids"`
	AttachmentHashes map[string]string `json:"attachment_hashes"`
	Evidence         []Evidence        `json:"evidence"`
	Revision         int               `json:"revision"`
	Status           string            `json:"status"`
	BlockReason      string            `json:"block_reason"`
	Followup         bool              `json:"followup"`
	ApprovedRevision int               `json:"approved_revision"`
	MessageID        string            `json:"message_id"`
	ThreadID         string            `json:"thread_id"`
	RFCMessageID     string            `json:"rfc_message_id"`
	InReplyTo        string            `json:"in_reply_to"`
	CreatedAt        string            `json:"created_at"`
	SentAt           string            `json:"sent_at"`
	AttemptedAt      string            `json:"attempted_at"`
}
type Attachment struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
}
type Event struct {
	ID    int64  `json:"id"`
	At    string `json:"at"`
	Agent string `json:"agent"`
	Kind  string `json:"kind"`
	Text  string `json:"text"`
}
type Memory struct {
	ID     string `json:"id"`
	Agent  string `json:"agent"`
	Text   string `json:"text"`
	Source string `json:"source"`
	At     string `json:"at"`
}
type Chat struct {
	ID    string `json:"id"`
	Agent string `json:"agent"`
	Role  string `json:"role"`
	Text  string `json:"text"`
	At    string `json:"at"`
}
type Job struct {
	ID       string `json:"id"`
	Kind     string `json:"kind"`
	Key      string `json:"key"`
	Payload  string `json:"payload"`
	Attempts int    `json:"attempts"`
	Due      string `json:"due"`
}
type InboxMessage struct {
	ID             string  `json:"id"`
	ThreadID       string  `json:"thread_id"`
	ContactID      string  `json:"contact_id"`
	From           string  `json:"from"`
	Subject        string  `json:"subject"`
	Text           string  `json:"text"`
	Classification string  `json:"classification"`
	Confidence     float64 `json:"confidence"`
	At             string  `json:"at"`
}
type Agent struct {
	Name  string `json:"name"`
	Job   string `json:"job"`
	State string `json:"state"`
}

var Agents = []Agent{{"Coordinator", "Plans and delegates campaign work", "idle"}, {"Scout", "Finds professors and verified contact sources", "idle"}, {"Matcher", "Finds papers related to your interests", "idle"}, {"Analyst", "Extracts supported research talking points", "idle"}, {"Composer", "Writes outreach and follow-ups", "idle"}, {"Courier", "Checks attachments and sends mail", "idle"}, {"Inbox", "Monitors replies and reminders", "idle"}, {"Reporter", "Summaries and Notion tracking", "idle"}}

const DefaultTemplate = `Dear {{.Professor}},

My name is {{.Name}}. {{.Background}}

I am interested in {{.Interests}}. Your paper, "{{.Paper}}", caught my attention: {{.Connection}}

{{.Goal}}

Thank you for your time.

{{.Signature}}`
