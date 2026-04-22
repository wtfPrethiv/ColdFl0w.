package engine

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/mail"
	"os"
	"strings"
	"text/template"
	"time"

	"coldflow/internal/ai"
	"coldflow/internal/domain"
	"coldflow/internal/store"
)

func (e *Engine) discover(ctx context.Context, id string) error {
	c, err := store.Get[domain.Campaign](e.Store, "campaign", id)
	if err != nil {
		return err
	}
	if c.Paused || e.Store.Value("paused") == "true" {
		return &Deferred{time.Now().Add(time.Minute)}
	}
	if e.Config.Demo {
		return nil
	}
	urls, err := e.Web.Discover(ctx, c.Interests+" "+c.Geography, c.Seeds)
	if err != nil {
		return err
	}
	all, _ := store.List[domain.Contact](e.Store, "contact")
	existing := map[string]bool{}
	for _, v := range all {
		existing[strings.ToLower(v.Email)] = true
	}
	added := 0
	failures := 0
	for _, u := range urls {
		if added >= 5 {
			break
		}
		e.event("Scout", "research", "Checking professor contact information at "+u)
		page, err := e.Web.Fetch(ctx, u)
		if err != nil {
			e.event("Scout", "source", err.Error())
			failures++
			continue
		}
		page = page[:min(len(page), 12000)]
		prompt := "Extract at most 3 professors from this page. Each must have a name, exact published email, institution, and research relevant to the campaign. Geography: " + c.Geography + "; exclusions: " + c.Exclusions + ". Return JSON array [{\"name\":\"\",\"email\":\"\",\"university\":\"\"}]. Return [] if insufficient evidence. Campaign interests: " + c.Interests + "\nPAGE:\n" + page
		raw, err := e.AI.Generate(ctx, "You are Scout. Extract facts only; do not infer email patterns. "+e.memory("Scout"), prompt)
		if err != nil {
			return err
		}
		candidates, err := ai.JSON[[]struct {
			Name       string `json:"name"`
			Email      string `json:"email"`
			University string `json:"university"`
		}](raw)
		if err != nil {
			return err
		}
		for _, candidate := range candidates {
			v := domain.Contact{Name: candidate.Name, Email: candidate.Email, University: candidate.University}
			v.Email = strings.TrimSpace(v.Email)
			addr, parseErr := mail.ParseAddress(v.Email)
			if parseErr != nil || addr.Address != v.Email || !strings.Contains(strings.ToLower(page), strings.ToLower(v.Email)) || !strings.Contains(strings.ToLower(page), strings.ToLower(v.Name)) || v.Name == "" || v.University == "" {
				continue
			}
			excluded := false
			for _, term := range strings.Split(c.Exclusions, ",") {
				term = strings.ToLower(strings.TrimSpace(term))
				if term != "" && strings.Contains(strings.ToLower(v.Name+" "+v.University+" "+v.Email), term) {
					excluded = true
				}
			}
			if excluded || existing[strings.ToLower(v.Email)] {
				continue
			}
			if added >= 5 {
				break
			}
			v.ID = domain.ID()
			v.CampaignID = c.ID
			v.URL = u
			v.Status = "discovered"
			v.UpdatedAt = domain.Now()
			if err = e.Store.Put("contact", v.ID, v); err != nil {
				return err
			}
			existing[strings.ToLower(v.Email)] = true
			added++
			if err = e.queue("match", v.ID); err != nil {
				return err
			}
			e.event("Scout", "discovery", "Found "+v.Name+" at "+v.University+" with a published contact address.")
		}
	}
	if failures == len(urls) && len(urls) > 0 {
		return errors.New("no discovery sources could be read; inspect source errors")
	}
	c.LastDiscovery = domain.Now()
	if err = e.Store.Put("campaign", c.ID, c); err != nil {
		return err
	}
	e.event("Scout", "complete", fmt.Sprintf("Discovery finished: %d new professors added.", added))
	return nil
}
func (e *Engine) match(ctx context.Context, id string) error {
	c, err := store.Get[domain.Contact](e.Store, "contact", id)
	if err != nil {
		return err
	}
	campaign, err := store.Get[domain.Campaign](e.Store, "campaign", c.CampaignID)
	if err != nil {
		return err
	}
	if stopped(c.Status) {
		return nil
	}
	if campaign.Paused {
		return &Deferred{time.Now().Add(time.Minute)}
	}
	papers, err := e.Web.Papers(ctx, c, campaign.Interests)
	if err != nil {
		return err
	}
	if len(papers) == 0 {
		e.event("Matcher", "review", "No paper with verified authorship and affiliation found for "+c.Name+". Add a paper manually in the contact screen.")
		return nil
	}
	shortlist := papers[:min(4, len(papers))]
	b, _ := json.Marshal(shortlist)
	raw, err := e.AI.Generate(ctx, "You are Matcher. Choose the paper best aligned with the interests. Return JSON {\"index\":0,\"reason\":\"...\"}. "+e.memory("Matcher"), "Interests: "+campaign.Interests+"\nVerified-author candidates:\n"+string(b))
	if err != nil {
		return err
	}
	choice, err := ai.JSON[struct {
		Index  int    `json:"index"`
		Reason string `json:"reason"`
	}](raw)
	if err != nil {
		return err
	}
	if choice.Index < 0 || choice.Index >= len(shortlist) {
		return errors.New("invalid paper selection")
	}
	p := shortlist[choice.Index]
	if p.PDFURL != "" {
		if text, fetchErr := e.Web.Fetch(ctx, p.PDFURL); fetchErr == nil && len(text) > 500 {
			p.Text = text
			p.FullText = true
		} else if fetchErr != nil {
			e.event("Matcher", "source", "Using abstract evidence for "+p.Title+": "+fetchErr.Error())
		}
	}
	if err = e.Store.Put("paper", p.ID, p); err != nil {
		return err
	}
	e.event("Matcher", "match", "Selected "+p.Title+" for "+c.Name+". "+choice.Reason)
	return e.queue("analyze", p.ID)
}
func (e *Engine) analyze(ctx context.Context, id string) error {
	p, err := store.Get[domain.Paper](e.Store, "paper", id)
	if err != nil {
		return err
	}
	source := p.Abstract
	location := "abstract"
	if p.FullText {
		source = p.Text
		location = "full text excerpt"
	}
	source = source[:min(18000, len(source))]
	if len(source) < 100 {
		return errors.New("paper has too little accessible text; supply a source excerpt")
	}
	c, err := store.Get[domain.Contact](e.Store, "contact", p.ContactID)
	if err != nil {
		return err
	}
	campaign, err := store.Get[domain.Campaign](e.Store, "campaign", c.CampaignID)
	if err != nil {
		return err
	}
	raw, err := e.AI.Generate(ctx, "You are Analyst. Extract up to 3 specific findings relevant to the user's interests. Each text must be an EXACT substring of SOURCE (20-500 characters); do not paraphrase. Return JSON [{\"text\":\"exact quotation\"}]. "+e.memory("Analyst"), "Interests: "+campaign.Interests+"\nPaper: "+p.Title+"\nSOURCE:\n"+source)
	if err != nil {
		return err
	}
	points, err := ai.JSON[[]domain.Evidence](raw)
	if err != nil {
		return err
	}
	p.Points = nil
	for _, v := range points {
		if len(v.Text) >= 20 && len(v.Text) <= 500 && strings.Contains(source, v.Text) {
			v.Source = p.URL
			v.Location = location
			p.Points = append(p.Points, v)
		}
		if len(p.Points) == 3 {
			break
		}
	}
	if len(p.Points) == 0 {
		return errors.New("no talking point could be verified against the paper text")
	}
	if err = e.Store.Put("paper", p.ID, p); err != nil {
		return err
	}
	e.event("Analyst", "evidence", fmt.Sprintf("Verified %d paper excerpts for %s (%s).", len(p.Points), c.Name, location))
	return e.queue("compose", p.ID)
}
func (e *Engine) compose(ctx context.Context, id string, followup bool, replace ...bool) error {
	var p domain.Paper
	var c domain.Contact
	var original domain.Draft
	var err error
	if followup {
		original, err = store.Get[domain.Draft](e.Store, "draft", id)
		if err != nil {
			return err
		}
		if original.Status != "sent" || original.Followup {
			return nil
		}
		p, err = store.Get[domain.Paper](e.Store, "paper", original.PaperID)
	} else {
		p, err = store.Get[domain.Paper](e.Store, "paper", id)
	}
	if err != nil {
		return err
	}
	c, err = store.Get[domain.Contact](e.Store, "contact", p.ContactID)
	if err != nil {
		return err
	}
	if stopped(c.Status) {
		return nil
	}
	campaign, err := store.Get[domain.Campaign](e.Store, "campaign", c.CampaignID)
	if err != nil {
		return err
	}
	if campaign.Paused || e.Store.Value("paused") == "true" {
		return &Deferred{time.Now().Add(time.Hour)}
	}
	drafts, _ := store.List[domain.Draft](e.Store, "draft")
	for _, d := range drafts {
		if followup && d.ContactID == c.ID && d.Followup && (len(replace) == 0 || !replace[0]) {
			return nil
		}
		if d.ContactID == c.ID && d.Followup == followup && d.Status != "skipped" && d.Status != "cancelled" {
			return nil
		}
	}
	profile, err := store.Get[domain.Profile](e.Store, "profile", "user")
	if err != nil || profile.Name == "" || profile.Background == "" || profile.Goal == "" {
		return errors.New("complete your profile before composing outreach")
	}
	if len(p.Points) == 0 {
		return errors.New("verified talking points are required")
	}
	data := map[string]string{"Professor": c.Name, "Name": profile.Name, "Background": profile.Background, "Interests": campaign.Interests, "Paper": p.Title, "Connection": p.Points[0].Text, "Goal": profile.Goal, "Signature": profile.Signature}
	tpl, err := template.New("email").Option("missingkey=error").Parse(campaign.Template)
	if err != nil {
		return err
	}
	var rendered bytes.Buffer
	if err = tpl.Execute(&rendered, data); err != nil {
		return err
	}
	contextData, _ := json.Marshal(map[string]any{"profile": profile, "paper_title": p.Title, "evidence": p.Points, "template": rendered.String(), "followup": followup})
	raw, err := e.AI.Generate(ctx, "You are Composer. Return JSON {\"subject\":\"...\",\"body\":\"...\"}. Write concise academic outreach using the template and supported facts only. Do not claim the user read the full paper. A follow-up must be a brief polite check-in, not a new introduction. "+e.memory("Composer"), string(contextData))
	if err != nil {
		return err
	}
	content, err := ai.JSON[struct {
		Subject string `json:"subject"`
		Body    string `json:"body"`
	}](raw)
	if err != nil {
		return err
	}
	if strings.TrimSpace(content.Subject) == "" || strings.TrimSpace(content.Body) == "" || len(content.Body) > 20000 || strings.ContainsAny(content.Subject, "\r\n") {
		return errors.New("invalid composed email")
	}
	verification, err := e.AI.Generate(ctx, "Check whether every factual assertion in EMAIL is supported by FACTS. Return JSON {\"supported\":true,\"reason\":\"...\"}. Reject invented qualifications, exaggerated research findings, and claims of having read full text when only quotations are provided.", "FACTS:\n"+string(contextData)+"\nEMAIL:\n"+content.Subject+"\n"+content.Body)
	if err != nil {
		return err
	}
	check, err := ai.JSON[struct {
		Supported bool   `json:"supported"`
		Reason    string `json:"reason"`
	}](verification)
	if err != nil {
		return err
	}
	d := domain.Draft{ID: domain.ID(), ContactID: c.ID, CampaignID: c.CampaignID, PaperID: p.ID, Subject: content.Subject, Body: content.Body, AttachmentIDs: campaign.AttachmentIDs, AttachmentHashes: map[string]string{}, Evidence: p.Points, Revision: 1, Status: "draft", Followup: followup, CreatedAt: domain.Now()}
	if followup {
		d.ThreadID = original.ThreadID
		d.InReplyTo = original.RFCMessageID
		d.Subject = original.Subject
	}
	if !check.Supported {
		d.BlockReason = "Factual review: " + check.Reason
	}
	if check.Supported && !p.FullText && campaign.Mode == "automatic" {
		d.BlockReason = "Abstract-only evidence requires manual approval"
	}
	for _, aid := range d.AttachmentIDs {
		a, err := store.Get[domain.Attachment](e.Store, "attachment", aid)
		if err != nil {
			return err
		}
		d.AttachmentHashes[aid] = a.SHA256
	}
	if err = e.Store.Put("draft", d.ID, d); err != nil {
		return err
	}
	e.gate.Lock()
	current, loadErr := store.Get[domain.Contact](e.Store, "contact", c.ID)
	if loadErr == nil && !stopped(current.Status) {
		if !followup {
			current.Status = "drafted"
		}
		current.UpdatedAt = domain.Now()
		err = e.Store.Put("contact", current.ID, current)
	}
	e.gate.Unlock()
	if err != nil {
		return err
	}
	e.event("Composer", "draft", "Prepared a draft for "+c.Name+". "+d.BlockReason)
	if campaign.Mode == "automatic" && d.BlockReason == "" {
		return e.queue("send", d.ID)
	}
	return nil
}
func (e *Engine) Approve(id string, revision int) error {
	e.gate.Lock()
	defer e.gate.Unlock()
	d, err := store.Get[domain.Draft](e.Store, "draft", id)
	if err != nil {
		return err
	}
	if d.Revision != revision {
		return errors.New("draft changed; refresh and review the new revision")
	}
	if d.Status != "draft" {
		return errors.New("draft is already queued, sent, skipped, or cancelled")
	}
	if strings.HasPrefix(d.BlockReason, "Factual review:") {
		return errors.New("edit or regenerate the draft to resolve factual review")
	}
	d.ApprovedRevision = revision
	d.Status = "approved"
	d.BlockReason = ""
	if err = e.Store.Put("draft", d.ID, d); err != nil {
		return err
	}
	e.event("Courier", "approval", "Approved draft "+d.ID+" for delivery.")
	return e.queue("send", d.ID)
}
func (e *Engine) EditDraft(id string, revision int, subject, body string, attachments []string) error {
	e.gate.Lock()
	defer e.gate.Unlock()
	d, err := store.Get[domain.Draft](e.Store, "draft", id)
	if err != nil {
		return err
	}
	if d.Revision != revision || d.Status != "draft" && d.Status != "approved" {
		return errors.New("draft is stale or no longer editable")
	}
	if len(subject) == 0 || len(body) == 0 || len(body) > 20000 || strings.ContainsAny(subject, "\r\n") {
		return errors.New("invalid subject or body")
	}
	hashes := map[string]string{}
	for _, id := range attachments {
		a, err := store.Get[domain.Attachment](e.Store, "attachment", id)
		if err != nil {
			return err
		}
		hashes[id] = a.SHA256
	}
	d.Subject = subject
	d.Body = body
	d.AttachmentIDs = attachments
	d.AttachmentHashes = hashes
	d.Revision++
	d.ApprovedRevision = 0
	d.Status = "draft"
	d.BlockReason = "Edited draft requires manual approval"
	return e.Store.Put("draft", id, d)
}
func (e *Engine) Skip(id string) error {
	e.gate.Lock()
	defer e.gate.Unlock()
	d, err := store.Get[domain.Draft](e.Store, "draft", id)
	if err != nil {
		return err
	}
	if d.Status != "draft" && d.Status != "approved" {
		return errors.New("only pending drafts can be skipped")
	}
	d.Status = "skipped"
	return e.Store.Put("draft", id, d)
}
func (e *Engine) Regenerate(id, instructions string) error {
	d, err := store.Get[domain.Draft](e.Store, "draft", id)
	if err != nil {
		return err
	}
	if err = e.Skip(id); err != nil {
		return err
	}
	if instructions != "" {
		if err = e.Remember("Composer", instructions, "draft regeneration request"); err != nil {
			return err
		}
	}
	kind := "compose"
	key := d.PaperID
	if d.Followup {
		all, _ := store.List[domain.Draft](e.Store, "draft")
		for _, o := range all {
			if o.ContactID == d.ContactID && !o.Followup && o.Status == "sent" {
				key = o.ID
				break
			}
		}
		kind = "followup"
	}
	return e.Store.Enqueue(kind, "regenerate:"+domain.ID(), Payload{ID: key}, time.Now())
}
func hashFile(path string) (string, error) {
	st, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	if st.Size() > 10<<20 {
		return "", errors.New("attachment too large")
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	if len(b) > 10<<20 {
		return "", errors.New("attachment too large")
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:]), nil
}
