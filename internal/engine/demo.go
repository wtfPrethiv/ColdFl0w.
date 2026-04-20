package engine

import (
	"coldflow/internal/domain"
	"time"
)

// SeedDemo never accesses external services or sends mail.
func (e *Engine) SeedDemo() error {
	if e.Store.Value("demo:seeded") != "" {
		return nil
	}
	profile := domain.Profile{Name: "Research Student", Background: "I am an undergraduate exploring efficient machine learning systems.", Interests: "efficient machine learning and representation learning", Goal: "I would appreciate the opportunity to discuss possible research work in your lab.", Signature: "Research Student"}
	if err := e.Store.Put("profile", "user", profile); err != nil {
		return err
	}
	campaign := domain.Campaign{ID: "demo-campaign", Name: "Efficient ML research", Interests: profile.Interests, Mode: "approval", DailyLimit: 10, FollowupLimit: 10, Template: domain.DefaultTemplate}
	if err := e.Store.Put("campaign", campaign.ID, campaign); err != nil {
		return err
	}
	for i, name := range []string{"Dr. Ada Example", "Dr. Lin Sample", "Dr. Mira Demo"} {
		id := []string{"ada", "lin", "mira"}[i]
		c := domain.Contact{ID: id, CampaignID: campaign.ID, Name: name, Email: id + "@example.invalid", University: "Example Institute", URL: "https://example.org/lab", Status: "drafted", UpdatedAt: domain.Now()}
		if err := e.Store.Put("contact", id, c); err != nil {
			return err
		}
		p := domain.Paper{ID: "paper-" + id, ContactID: id, Title: []string{"Learning with Compact Representations", "Adaptive Computation for Small Models", "Efficient Research Systems"}[i], URL: "https://example.org/papers/" + id, Abstract: "This fictional demonstration describes a compact model and its evaluation.", FullText: true, Points: []domain.Evidence{{Text: "The compact representation reduced the model's memory requirements in the reported experiments.", Source: "https://example.org/papers/" + id, Location: "fictional demo excerpt"}}}
		if err := e.Store.Put("paper", p.ID, p); err != nil {
			return err
		}
		d := domain.Draft{ID: "draft-" + id, ContactID: id, CampaignID: campaign.ID, PaperID: p.ID, Subject: "Research interest in " + p.Title, Body: "Dear " + name + ",\n\n" + profile.Background + "\n\nYour paper, \"" + p.Title + "\", caught my attention. I am interested in the relationship between compact representations and memory use.\n\n" + profile.Goal + "\n\nThank you,\n" + profile.Name, Revision: 1, Status: "draft", Evidence: p.Points, CreatedAt: time.Now().Add(-time.Duration(i) * time.Minute).UTC().Format(time.RFC3339)}
		if err := e.Store.Put("draft", d.ID, d); err != nil {
			return err
		}
	}
	_ = e.Remember("shared", "Keep outreach concise, specific, and respectful of professors' time.", "demo preference")
	e.event("Coordinator", "demo", "Demo ready. All contacts and paper findings here are fictional; Enter simulates delivery.")
	return e.Store.Set("demo:seeded", domain.Now())
}
