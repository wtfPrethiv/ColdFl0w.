package engine

import (
	"context"

	"coldflow/internal/domain"
	"coldflow/internal/store"
)

// Sync changed remote pages and at most 20 dirty local contacts per pass. No
// per-contact polling or repeated whole-dataset queries are needed.
func (e *Engine) syncNotion(ctx context.Context) error {
	started := domain.Now()
	remote, err := e.Notion.Changes(ctx, e.Store.Value("notion:watermark"))
	if err != nil {
		_ = e.Store.Set("health:notion", err.Error())
		return err
	}
	contacts, err := store.List[domain.Contact](e.Store, "contact")
	if err != nil {
		return err
	}
	byID := map[string]domain.Contact{}
	byPage := map[string]domain.Contact{}
	for _, c := range contacts {
		byID[c.ID] = c
		if c.NotionID != "" {
			byPage[c.NotionID] = c
		}
	}
	for _, p := range remote {
		c, ok := byID[p.Text("Coldflow ID")]
		if !ok {
			c, ok = byPage[p.ID]
		}
		if !ok || p.Edited == c.NotionEdited {
			continue
		}
		if c.NotionID != "" && c.NotionID != p.ID {
			e.event("Reporter", "conflict", "Multiple Notion pages have the Coldflow ID for "+c.Name+"; keeping the linked page.")
			continue
		}
		synced := e.Store.Value("notion:local:" + c.ID)
		dirty := synced != "" && c.UpdatedAt != synced
		status := p.Status()
		changed := status != c.Status || p.Text("Notes") != c.Notes || p.Next() != c.NextAction
		if dirty && changed {
			e.event("Reporter", "conflict", "Notion and Coldflow both changed "+c.Name+". Local fields retained; stop statuses take priority.")
		}
		imported := false
		if changed && (!dirty || stopped(status)) && allowedStatus(status) {
			if err = e.UpdateContact(c.ID, status, p.Text("Notes"), p.Next()); err != nil {
				return err
			}
			imported = true
		}
		e.gate.Lock()
		latest, loadErr := store.Get[domain.Contact](e.Store, "contact", c.ID)
		if loadErr == nil {
			latest.NotionID = p.ID
			latest.NotionEdited = p.Edited
			loadErr = e.Store.Put("contact", latest.ID, latest)
		}
		e.gate.Unlock()
		if loadErr != nil {
			return loadErr
		}
		if imported && !dirty {
			if err = e.Store.Set("notion:local:"+c.ID, latest.UpdatedAt); err != nil {
				return err
			}
		}
	}
	contacts, err = store.List[domain.Contact](e.Store, "contact")
	if err != nil {
		return err
	}
	campaigns, _ := store.List[domain.Campaign](e.Store, "campaign")
	names := map[string]string{}
	for _, c := range campaigns {
		names[c.ID] = c.Name
	}
	papers, _ := store.List[domain.Paper](e.Store, "paper")
	paperByContact := map[string]domain.Paper{}
	for _, p := range papers {
		if _, ok := paperByContact[p.ContactID]; !ok {
			paperByContact[p.ContactID] = p
		}
	}
	drafts, _ := store.List[domain.Draft](e.Store, "draft")
	sent := map[string]string{}
	for _, d := range drafts {
		if d.Status == "sent" && sent[d.ContactID] == "" {
			sent[d.ContactID] = d.SentAt
		}
	}
	messages, _ := store.List[domain.InboxMessage](e.Store, "inbox")
	replied := map[string]string{}
	for _, m := range messages {
		if replied[m.ContactID] == "" {
			replied[m.ContactID] = m.At
		}
	}
	pushed := 0
	for _, c := range contacts {
		if c.NotionID != "" && c.UpdatedAt == e.Store.Value("notion:local:"+c.ID) {
			continue
		}
		if pushed >= 20 {
			break
		}
		if c.NotionID == "" {
			existing, err := e.Notion.Find(ctx, c.ID)
			if err != nil {
				return err
			}
			if existing != nil {
				c.NotionID = existing.ID
			}
		}
		p, err := e.Notion.Push(ctx, c, names[c.CampaignID], paperByContact[c.ID], sent[c.ID], replied[c.ID])
		if err != nil {
			_ = e.Store.Set("health:notion", err.Error())
			return err
		}
		e.gate.Lock()
		latest, loadErr := store.Get[domain.Contact](e.Store, "contact", c.ID)
		if loadErr == nil {
			latest.NotionID = p.ID
			latest.NotionEdited = p.Edited
			loadErr = e.Store.Put("contact", latest.ID, latest)
		}
		e.gate.Unlock()
		if loadErr != nil {
			return loadErr
		}
		if err = e.Store.Set("notion:local:"+c.ID, c.UpdatedAt); err != nil {
			return err
		}
		pushed++
	}
	if err = e.Store.Set("notion:watermark", started); err != nil {
		return err
	}
	return e.Store.Set("health:notion", "connected; "+domain.Now())
}
