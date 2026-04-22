package engine

import (
	"errors"
	"strings"
	"text/template"

	"coldflow/internal/domain"
	"coldflow/internal/research"
	"coldflow/internal/store"
)

func (e *Engine) EditCampaign(c domain.Campaign) error {
	if err := e.validateCampaign(c); err != nil {
		return err
	}
	e.gate.Lock()
	defer e.gate.Unlock()
	previous, err := store.Get[domain.Campaign](e.Store, "campaign", c.ID)
	if err != nil {
		return err
	}
	c.LastDiscovery = previous.LastDiscovery
	if err = e.Store.Put("campaign", c.ID, c); err != nil {
		return err
	}
	e.event("Coordinator", "campaign", "Updated "+c.Name+". Existing drafts keep their reviewed content and attachments.")
	return nil
}
func (e *Engine) validateCampaign(c domain.Campaign) error {
	if strings.TrimSpace(c.Name) == "" || strings.TrimSpace(c.Interests) == "" {
		return errors.New("campaign name and interests are required")
	}
	if c.Mode != "approval" && c.Mode != "automatic" {
		return errors.New("invalid sending mode")
	}
	if c.DailyLimit < 1 || c.DailyLimit > 100 || c.FollowupLimit < 1 || c.FollowupLimit > 100 {
		return errors.New("campaign daily limits must be 1-100")
	}
	if len(c.Template) > 20000 {
		return errors.New("template exceeds 20000 bytes")
	}
	if _, err := template.New("email").Parse(c.Template); err != nil {
		return err
	}
	for _, u := range c.Seeds {
		if err := research.ValidateURL(u); err != nil {
			return err
		}
	}
	for _, id := range c.AttachmentIDs {
		if _, err := store.Get[domain.Attachment](e.Store, "attachment", id); err != nil {
			return err
		}
	}
	return nil
}
