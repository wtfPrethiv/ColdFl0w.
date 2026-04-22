package mail

import (
	"coldflow/internal/domain"
	"encoding/base64"
	"io"
	"mime"
	"mime/multipart"
	"net/mail"
	"strings"
	"testing"
)

func TestMIMEHeadersAndBody(t *testing.T) {
	d := domain.Draft{From: "student@example.org", Subject: "Research in learning", Body: "Dear Professor,\nA specific finding.", RFCMessageID: "coldflow-test@coldflow.local", InReplyTo: "original@example.org"}
	c := domain.Contact{Email: "prof@example.edu"}
	b, err := MIME(d, c, nil)
	if err != nil {
		t.Fatal(err)
	}
	m, err := mail.ReadMessage(strings.NewReader(string(b)))
	if err != nil {
		t.Fatal(err)
	}
	if m.Header.Get("In-Reply-To") != "<original@example.org>" {
		t.Fatal(m.Header)
	}
	_, p, err := mime.ParseMediaType(m.Header.Get("Content-Type"))
	if err != nil {
		t.Fatal(err)
	}
	part, err := multipart.NewReader(m.Body, p["boundary"]).NextPart()
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(part)
	if err != nil || len(body) == 0 {
		t.Fatalf("%q %v", body, err)
	}
	d.Subject = "bad\r\nBcc: other@example.org"
	if _, err = MIME(d, c, nil); err == nil {
		t.Fatal("header injection accepted")
	}
}

func TestStructuredBounceRecipient(t *testing.T) {
	m := Message{}
	p := Part{MIMEType: "message/delivery-status"}
	p.Body.Data = base64.RawURLEncoding.EncodeToString([]byte("Reporting-MTA: dns; mx.example.org\r\n\r\nFinal-Recipient: rfc822; prof@example.edu\r\nAction: failed\r\n"))
	m.Payload.Parts = []Part{p}
	recipients := m.BounceRecipients()
	if len(recipients) != 1 || recipients[0] != "prof@example.edu" {
		t.Fatal(recipients)
	}
}
