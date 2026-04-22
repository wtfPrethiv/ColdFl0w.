package tui

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/huh/v2"
	"coldflow/internal/domain"
	"coldflow/internal/server"
)

func (m *Model) openForm(kind string) tea.Cmd {
	if m.busy {
		return nil
	}
	d := &formData{kind: kind, mode: "approval", limit: "10", template: domain.DefaultTemplate}
	var fields []huh.Field
	attachmentOptions := []huh.Option[string]{}
	for _, a := range m.Snap.Attachments {
		attachmentOptions = append(attachmentOptions, huh.NewOption(a.Name, a.ID))
	}
	switch kind {
	case "chat":
		if len(m.Snap.Agents) == 0 {
			return nil
		}
		d.agent = m.Snap.Agents[m.selected[1]].Name
		fields = []huh.Field{huh.NewText().Title("Chat with " + d.agent + " (use 'remember ...' to save a preference)").Value(&d.body).CharLimit(4000)}
	case "campaign-new":
		fields = []huh.Field{huh.NewInput().Title("Campaign name").Value(&d.name), huh.NewInput().Title("Research interests").Value(&d.interests), huh.NewInput().Title("Geography / countries (optional)").Value(&d.geography), huh.NewInput().Title("Exclusions, comma separated (optional)").Value(&d.exclusions), huh.NewText().Title("Professor or university URLs, one per line (optional with Brave search)").Value(&d.seeds), huh.NewSelect[string]().Title("Sending mode").Options(huh.NewOption("Review every draft", "approval"), huh.NewOption("Automatic within campaign rules", "automatic")).Value(&d.mode), huh.NewInput().Title("Maximum new emails per day").Value(&d.limit), huh.NewText().Title("Email template (leave default, or paste your own)").Value(&d.template)}
		if len(attachmentOptions) > 0 {
			fields = append(fields, huh.NewMultiSelect[string]().Title("Authorized campaign attachments").Options(attachmentOptions...).Value(&d.attachments))
		}
	case "campaign-edit":
		if len(m.Snap.Campaigns) == 0 {
			return nil
		}
		c := m.Snap.Campaigns[m.selected[2]]
		d.campaign = c
		d.name = c.Name
		d.interests = c.Interests
		d.geography = c.Geography
		d.exclusions = c.Exclusions
		d.seeds = strings.Join(c.Seeds, "\n")
		d.template = c.Template
		d.attachments = append([]string{}, c.AttachmentIDs...)
		d.id = c.ID
		d.mode = c.Mode
		d.limit = strconv.Itoa(c.DailyLimit)
		d.status = strconv.FormatBool(c.Paused)
		fields = []huh.Field{huh.NewSelect[string]().Title("Sending mode for "+c.Name).Options(huh.NewOption("Review drafts", "approval"), huh.NewOption("Automatic", "automatic")).Value(&d.mode), huh.NewSelect[string]().Title("Campaign state").Options(huh.NewOption("Running", "false"), huh.NewOption("Paused", "true")).Value(&d.status), huh.NewInput().Title("Maximum new emails per day").Value(&d.limit)}
		fields = append(fields, huh.NewInput().Title("Campaign name").Value(&d.name), huh.NewInput().Title("Research interests").Value(&d.interests), huh.NewInput().Title("Geography").Value(&d.geography), huh.NewInput().Title("Exclusions, comma separated").Value(&d.exclusions), huh.NewText().Title("Seed URLs, one per line").Value(&d.seeds), huh.NewText().Title("Email template for future drafts").Value(&d.template))
		if len(attachmentOptions) > 0 {
			fields = append(fields, huh.NewMultiSelect[string]().Title("Attachments for future drafts").Options(attachmentOptions...).Value(&d.attachments))
		}
	case "draft":
		if len(m.Snap.Drafts) == 0 {
			return nil
		}
		v := m.Snap.Drafts[m.selected[3]]
		d.id = v.ID
		d.revision = v.Revision
		d.subject = v.Subject
		d.body = v.Body
		d.attachments = append([]string{}, v.AttachmentIDs...)
		fields = []huh.Field{huh.NewInput().Title("Subject").Value(&d.subject), huh.NewText().Title("Email body (editing clears prior approval)").Value(&d.body)}
		if len(attachmentOptions) > 0 {
			fields = append(fields, huh.NewMultiSelect[string]().Title("Attachments").Options(attachmentOptions...).Value(&d.attachments))
		}
	case "regenerate":
		if len(m.Snap.Drafts) == 0 {
			return nil
		}
		d.id = m.Snap.Drafts[m.selected[3]].ID
		fields = []huh.Field{huh.NewText().Title("Instructions for Composer (saved as a preference)").Value(&d.body)}
	case "contact":
		if len(m.Snap.Contacts) == 0 {
			return nil
		}
		c := m.Snap.Contacts[m.selected[5]]
		d.id = c.ID
		d.status = c.Status
		d.notes = c.Notes
		d.next = c.NextAction
		fields = []huh.Field{statusField(&d.status), huh.NewText().Title("Notes").Value(&d.notes), huh.NewInput().Title("Next action (YYYY-MM-DD or RFC3339; blank clears)").Value(&d.next)}
	case "classification":
		if len(m.Snap.Inbox) == 0 {
			return nil
		}
		v := m.Snap.Inbox[m.selected[4]]
		d.id = v.ID
		d.status = "replied"
		fields = []huh.Field{statusField(&d.status)}
	case "memory-new", "memory-edit":
		d.agent = "shared"
		if kind == "memory-edit" {
			if len(m.Snap.Memories) == 0 {
				return nil
			}
			v := m.Snap.Memories[m.selected[6]]
			d.id = v.ID
			d.agent = v.Agent
			d.body = v.Text
		}
		opts := []huh.Option[string]{huh.NewOption("Shared profile", "shared")}
		for _, a := range domain.Agents {
			opts = append(opts, huh.NewOption(a.Name, a.Name))
		}
		fields = []huh.Field{huh.NewSelect[string]().Title("Memory owner").Options(opts...).Value(&d.agent), huh.NewText().Title("Preference or instruction").Value(&d.body).CharLimit(4000)}
	case "profile":
		p := m.Snap.Profile
		d.name = p.Name
		d.body = p.Background
		d.interests = p.Interests
		d.notes = p.Goal
		d.subject = p.Signature
		fields = []huh.Field{huh.NewInput().Title("Your name").Value(&d.name), huh.NewText().Title("Your background (facts agents may use)").Value(&d.body), huh.NewInput().Title("Research interests").Value(&d.interests), huh.NewText().Title("What are you asking professors for?").Value(&d.notes), huh.NewText().Title("Email signature").Value(&d.subject)}
	case "attachment":
		fields = []huh.Field{huh.NewInput().Title("Path to file on the computer running this dashboard").Value(&d.body)}
	case "paper":
		if len(m.Snap.Contacts) == 0 {
			return nil
		}
		d.id = m.Snap.Contacts[m.selected[5]].ID
		fields = []huh.Field{huh.NewInput().Title("Paper title").Value(&d.subject), huh.NewInput().Title("Paper source URL").Value(&d.notes), huh.NewText().Title("Source excerpt / abstract (at least 100 characters; requires approval)").Value(&d.body)}
	default:
		return nil
	}
	m.formData = d
	m.form = huh.NewForm(huh.NewGroup(fields...)).WithTheme(huh.ThemeFunc(ASCIITheme)).WithWidth(max(20, m.width-8)).WithHeight(max(8, m.height-8))
	return m.form.Init()
}
func statusField(value *string) huh.Field {
	options := []huh.Option[string]{}
	for _, s := range []string{"discovered", "drafted", "sent", "replied", "declined", "meeting", "meeting arranged", "bounced", "opt-out", "stop"} {
		options = append(options, huh.NewOption(s, s))
	}
	return huh.NewSelect[string]().Title("Tracking status (replies and stop states cancel pending outreach)").Options(options...).Value(value)
}
func (m *Model) submit(d *formData) tea.Cmd {
	m.busy = true
	return func() tea.Msg {
		var err error
		switch d.kind {
		case "chat":
			err = m.Client.Action(server.Action{Kind: "chat", Agent: d.agent, Text: d.body})
		case "campaign-new":
			limit, e := strconv.Atoi(d.limit)
			if e != nil || limit < 1 || limit > 100 {
				err = errors.New("daily limit must be 1-100")
				break
			}
			seeds := []string{}
			for _, s := range strings.Split(d.seeds, "\n") {
				if s = strings.TrimSpace(s); s != "" {
					seeds = append(seeds, s)
				}
			}
			err = m.Client.Request("POST", "/api/campaign", domain.Campaign{Name: d.name, Interests: d.interests, Geography: d.geography, Exclusions: d.exclusions, Mode: d.mode, DailyLimit: limit, Seeds: seeds, Template: d.template, AttachmentIDs: d.attachments}, nil)
		case "campaign-edit":
			limit, e := strconv.Atoi(d.limit)
			if e != nil {
				err = e
				break
			}
			c := d.campaign
			c.Name = d.name
			c.Interests = d.interests
			c.Geography = d.geography
			c.Exclusions = d.exclusions
			c.Template = d.template
			c.AttachmentIDs = d.attachments
			c.Seeds = nil
			for _, s := range strings.Split(d.seeds, "\n") {
				if s = strings.TrimSpace(s); s != "" {
					c.Seeds = append(c.Seeds, s)
				}
			}
			c.Mode = d.mode
			c.Paused = d.status == "true"
			c.DailyLimit = limit
			err = m.Client.Request("PUT", "/api/campaign", c, nil)
		case "draft":
			err = m.Client.Action(server.Action{Kind: "edit-draft", ID: d.id, Revision: d.revision, Subject: d.subject, Body: d.body, Attachments: d.attachments})
		case "regenerate":
			err = m.Client.Action(server.Action{Kind: "regenerate", ID: d.id, Text: d.body})
		case "contact":
			err = m.Client.Action(server.Action{Kind: "contact", ID: d.id, Status: d.status, Text: d.notes, NextAction: d.next})
		case "classification":
			err = m.Client.Action(server.Action{Kind: "classify", ID: d.id, Status: d.status})
		case "memory-new":
			err = m.Client.Action(server.Action{Kind: "remember", Agent: d.agent, Text: d.body})
		case "memory-edit":
			err = m.Client.Action(server.Action{Kind: "edit-memory", ID: d.id, Text: d.body, Agent: d.agent})
		case "profile":
			err = m.Client.Request("PUT", "/api/profile", domain.Profile{Name: d.name, Background: d.body, Interests: d.interests, Goal: d.notes, Signature: d.subject}, nil)
		case "attachment":
			f, e := os.Open(strings.Trim(strings.TrimSpace(d.body), "\""))
			if e != nil {
				err = e
				break
			}
			defer f.Close()
			err = m.Client.Raw("POST", "/api/attachments?name="+url.QueryEscape(filepath.Base(f.Name())), f, nil)
		case "paper":
			err = m.Client.Action(server.Action{Kind: "paper", ID: d.id, Subject: d.subject, Text: d.notes, Body: d.body})
		default:
			err = fmt.Errorf("unknown form %q", d.kind)
		}
		return resultMsg{err, "Saved. Refreshing..."}
	}
}
