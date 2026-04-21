package tui

import (
	"fmt"
	"math"
	"os"
	"strings"
	"time"

	"charm.land/bubbles/v2/help"
	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/glamour/v2"
	"charm.land/glamour/v2/styles"
	"charm.land/huh/v2"
	"charm.land/lipgloss/v2"
	"coldflow/internal/client"
	"coldflow/internal/domain"
	"coldflow/internal/engine"
	"coldflow/internal/integrations"
	"coldflow/internal/server"
	"github.com/charmbracelet/harmonica"
)

var tabs = []string{"Overview", "Agents", "Campaigns", "Drafts", "Inbox", "Contacts", "Memory", "Settings", "Jobs"}

type snapshotMsg struct {
	s   engine.Snapshot
	err error
}
type resultMsg struct {
	err  error
	text string
}
type tickMsg time.Time
type formData struct {
	kind        string
	id          string
	revision    int
	name        string
	interests   string
	geography   string
	exclusions  string
	seeds       string
	mode        string
	limit       string
	template    string
	subject     string
	body        string
	notes       string
	status      string
	next        string
	agent       string
	attachments []string
}
type Model struct {
	Client         *client.Client
	Snap           engine.Snapshot
	tab            int
	selected       [9]int
	width, height  int
	viewport       viewport.Model
	help           help.Model
	status         string
	form           *huh.Form
	formData       *formData
	busy           bool
	reduced        bool
	spring         harmonica.Spring
	load, velocity float64
	rendered       string
}

func New(c *client.Client, reduced bool) *Model {
	v := viewport.New()
	v.SetWidth(76)
	v.SetHeight(16)
	return &Model{Client: c, width: 80, height: 28, viewport: v, help: help.New(), status: "Connecting to Coldflow...", reduced: reduced, spring: harmonica.NewSpring(0.5, 6, 1)}
}
func (m *Model) Init() tea.Cmd { return tea.Batch(m.refresh(), tick()) }
func tick() tea.Cmd            { return tea.Tick(2*time.Second, func(t time.Time) tea.Msg { return tickMsg(t) }) }
func (m *Model) refresh() tea.Cmd {
	return func() tea.Msg { s, err := m.Client.Snapshot(); return snapshotMsg{s, err} }
}
func (m *Model) action(a server.Action) tea.Cmd {
	m.busy = true
	return func() tea.Msg { err := m.Client.Action(a); return resultMsg{err, "Saved. Refreshing..."} }
}
func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if size, ok := msg.(tea.WindowSizeMsg); ok {
		m.width = size.Width
		m.height = size.Height
		m.viewport.SetWidth(max(20, m.width-6))
		m.viewport.SetHeight(max(3, m.height-10))
		if m.form != nil {
			m.form.WithWidth(max(20, m.width-8)).WithHeight(max(8, m.height-8))
		}
		m.render()
		return m, nil
	}
	switch v := msg.(type) {
	case tickMsg:
		return m, tea.Batch(m.refresh(), tick())
	case snapshotMsg:
		if v.err != nil {
			m.status = v.err.Error()
		} else {
			selectedID := m.currentID()
			m.Snap = v.s
			m.restoreID(selectedID)
			m.status = strings.TrimPrefix(m.status, "Connecting to Coldflow...")
			busy := 0
			for _, a := range v.s.Agents {
				if a.State != "idle" {
					busy++
				}
			}
			if m.reduced {
				m.load = float64(busy)
			} else {
				m.load, m.velocity = m.spring.Update(m.load, m.velocity, float64(busy))
			}
			m.clamp()
			m.render()
		}
		return m, nil
	case resultMsg:
		m.busy = false
		if v.err != nil {
			m.status = v.err.Error()
		} else {
			m.status = v.text
		}
		return m, m.refresh()
	}
	if m.form != nil {
		if k, ok := msg.(tea.KeyPressMsg); ok && k.String() == "esc" {
			m.form = nil
			m.formData = nil
			return m, nil
		}
		updated, cmd := m.form.Update(msg)
		m.form = updated.(*huh.Form)
		if m.form.State == huh.StateCompleted {
			data := m.formData
			m.form = nil
			m.formData = nil
			return m, m.submit(data)
		}
		return m, cmd
	}
	if k, ok := msg.(tea.KeyPressMsg); ok {
		keystr := k.String()
		switch keystr {
		case "ctrl+c", "q":
			return m, tea.Quit
		case "tab":
			m.tab = (m.tab + 1) % len(tabs)
			m.viewport.GotoTop()
			m.render()
			return m, nil
		case "shift+tab":
			m.tab = (m.tab + len(tabs) - 1) % len(tabs)
			m.viewport.GotoTop()
			m.render()
			return m, nil
		case "1", "2", "3", "4", "5", "6", "7", "8", "9":
			m.tab = int(keystr[0] - '1')
			m.viewport.GotoTop()
			m.render()
			return m, nil
		case "p":
			if m.Snap.Paused {
				return m, m.action(server.Action{Kind: "resume"})
			}
			return m, m.action(server.Action{Kind: "pause"})
		case "left", "right":
			if keystr == "left" {
				m.selected[m.tab]--
			} else {
				m.selected[m.tab]++
			}
			m.clamp()
			m.viewport.GotoTop()
			m.render()
			return m, nil
		case "up", "down":
			if m.tab != 0 && m.tab != 3 && m.tab != 7 {
				if keystr == "up" {
					m.selected[m.tab]--
				} else {
					m.selected[m.tab]++
				}
				m.clamp()
				m.viewport.GotoTop()
				m.render()
				return m, nil
			}
		case "enter":
			if m.tab == 3 && !m.busy && len(m.Snap.Drafts) > 0 {
				d := m.Snap.Drafts[m.selected[3]]
				return m, m.action(server.Action{Kind: "approve", ID: d.ID, Revision: d.Revision})
			}
			if m.tab == 1 {
				return m, m.openForm("chat")
			}
		case "c":
			if m.tab == 1 {
				return m, m.openForm("chat")
			}
		case "n":
			switch m.tab {
			case 2:
				return m, m.openForm("campaign-new")
			case 6:
				return m, m.openForm("memory-new")
			}
		case "e":
			switch m.tab {
			case 2:
				return m, m.openForm("campaign-edit")
			case 3:
				return m, m.openForm("draft")
			case 4:
				return m, m.openForm("classification")
			case 5:
				return m, m.openForm("contact")
			case 6:
				return m, m.openForm("memory-edit")
			case 7:
				return m, m.openForm("profile")
			}
		case "a":
			if m.tab == 7 {
				return m, m.openForm("attachment")
			}
		case "t":
			if m.tab == 5 {
				return m, m.openForm("paper")
			}
		case "s":
			if m.tab == 3 && len(m.Snap.Drafts) > 0 {
				return m, m.action(server.Action{Kind: "skip", ID: m.Snap.Drafts[m.selected[3]].ID})
			}
		case "r":
			if m.tab == 3 {
				return m, m.openForm("regenerate")
			}
			if m.tab == 8 && len(m.Snap.Jobs) > 0 {
				id, _ := m.Snap.Jobs[m.selected[8]]["id"].(string)
				return m, m.action(server.Action{Kind: "retry", ID: id})
			}
		case "d":
			if m.tab == 2 && len(m.Snap.Campaigns) > 0 {
				return m, m.action(server.Action{Kind: "discover", ID: m.Snap.Campaigns[m.selected[2]].ID})
			}
		case "x":
			if m.tab == 6 && len(m.Snap.Memories) > 0 {
				return m, m.action(server.Action{Kind: "forget", ID: m.Snap.Memories[m.selected[6]].ID})
			}
		case "u":
			if m.tab == 3 && len(m.Snap.Drafts) > 0 {
				return m, m.action(server.Action{Kind: "resolve-delivery", ID: m.Snap.Drafts[m.selected[3]].ID, Status: "not-delivered"})
			}
		}
	}
	var cmd tea.Cmd
	m.viewport, cmd = m.viewport.Update(msg)
	return m, cmd
}
func (m *Model) counts() []int {
	return []int{1, len(m.Snap.Agents), len(m.Snap.Campaigns), len(m.Snap.Drafts), len(m.Snap.Inbox), len(m.Snap.Contacts), len(m.Snap.Memories), 1, len(m.Snap.Jobs)}
}
func (m *Model) clamp() {
	for i, n := range m.counts() {
		m.selected[i] = max(0, min(m.selected[i], n-1))
	}
}
func (m *Model) currentID() string {
	i := m.selected[m.tab]
	switch m.tab {
	case 3:
		if i < len(m.Snap.Drafts) {
			return m.Snap.Drafts[i].ID
		}
	case 5:
		if i < len(m.Snap.Contacts) {
			return m.Snap.Contacts[i].ID
		}
	case 6:
		if i < len(m.Snap.Memories) {
			return m.Snap.Memories[i].ID
		}
	}
	return ""
}
func (m *Model) restoreID(id string) {
	if id == "" {
		return
	}
	switch m.tab {
	case 3:
		for i, v := range m.Snap.Drafts {
			if v.ID == id {
				m.selected[3] = i
				break
			}
		}
	case 5:
		for i, v := range m.Snap.Contacts {
			if v.ID == id {
				m.selected[5] = i
				break
			}
		}
	case 6:
		for i, v := range m.Snap.Memories {
			if v.ID == id {
				m.selected[6] = i
				break
			}
		}
	}
}
func (m *Model) render() {
	text := integrations.CleanText(m.content())
	m.rendered = text
	m.viewport.SetContent(lipgloss.NewStyle().Width(max(20, m.width-8)).Render(text))
}
func (m *Model) content() string {
	var b strings.Builder
	i := m.selected[m.tab]
	switch m.tab {
	case 0:
		pending, sent := 0, 0
		for _, d := range m.Snap.Drafts {
			if d.Status == "draft" {
				pending++
			}
			if d.Status == "sent" {
				sent++
			}
		}
		fmt.Fprintf(&b, "CAMPAIGNS %d    CONTACTS %d    DRAFTS %d    SENT %d\n\n", len(m.Snap.Campaigns), len(m.Snap.Contacts), pending, sent)
		for _, a := range m.Snap.Agents {
			fmt.Fprintf(&b, "[%s] %-12s %s\n", stateSymbol(a.State), a.Name, a.State)
		}
		b.WriteString("\nACTIVITY / observable results\n" + strings.Repeat("-", min(65, m.width-10)) + "\n")
		for _, v := range m.Snap.Events {
			at := v.At
			if len(at) > 19 {
				at = at[11:19]
			}
			fmt.Fprintf(&b, "%s %-11s %s\n", at, v.Agent, v.Text)
		}
	case 1:
		if len(m.Snap.Agents) == 0 {
			break
		}
		for j, a := range m.Snap.Agents {
			marker := " "
			if i == j {
				marker = ">"
			}
			fmt.Fprintf(&b, "%s %-12s [%s]\n", marker, a.Name, a.State)
		}
		a := m.Snap.Agents[i]
		fmt.Fprintf(&b, "\n%s: %s\n\nCHAT (c / Enter) | 'remember ...' saves a preference\n", a.Name, a.Job)
		chats := []domain.Chat{}
		for _, c := range m.Snap.Chats {
			if c.Agent == a.Name {
				chats = append(chats, c)
			}
		}
		for j := min(len(chats), 20) - 1; j >= 0; j-- {
			c := chats[j]
			rendered := c.Text
			if c.Role == "assistant" {
				if r, err := glamour.NewTermRenderer(glamour.WithStyles(styles.ASCIIStyleConfig), glamour.WithWordWrap(max(20, m.width-12))); err == nil {
					if s, err := r.Render(integrations.CleanText(c.Text)); err == nil {
						rendered = s
					}
				}
			}
			fmt.Fprintf(&b, "\n%s:\n%s\n", c.Role, rendered)
		}
	case 2:
		if len(m.Snap.Campaigns) == 0 {
			return "No campaigns. Press n to create one."
		}
		for j, c := range m.Snap.Campaigns {
			marker := " "
			if i == j {
				marker = ">"
			}
			fmt.Fprintf(&b, "%s %s [%s] paused=%t\n", marker, c.Name, c.Mode, c.Paused)
		}
		c := m.Snap.Campaigns[i]
		fmt.Fprintf(&b, "\nInterests: %s\nGeography: %s\nExclusions: %s\nNew/day: %d | Follow-ups/day: %d\n\nSeeds:\n%s\n\nTemplate:\n%s\n", c.Interests, c.Geography, c.Exclusions, c.DailyLimit, c.FollowupLimit, strings.Join(c.Seeds, "\n"), c.Template)
	case 3:
		if len(m.Snap.Drafts) == 0 {
			return "No drafts yet. Add a campaign and let the research bots work."
		}
		d := m.Snap.Drafts[i]
		c := m.contact(d.ContactID)
		fmt.Fprintf(&b, "< PREVIOUS       DRAFT %d / %d       NEXT >\n\nTo: %s <%s>\nSubject: %s\nStatus: %s | Revision: %d | Follow-up: %t\n", i+1, len(m.Snap.Drafts), c.Name, c.Email, d.Subject, d.Status, d.Revision, d.Followup)
		if d.BlockReason != "" {
			fmt.Fprintf(&b, "Review: %s\n", d.BlockReason)
		}
		fmt.Fprintf(&b, "\n%s\n\nATTACHMENTS\n", d.Body)
		if len(d.AttachmentIDs) == 0 {
			b.WriteString("(none)\n")
		}
		for _, id := range d.AttachmentIDs {
			for _, a := range m.Snap.Attachments {
				if a.ID == id {
					fmt.Fprintf(&b, "- %s (%d bytes)\n", a.Name, a.Size)
				}
			}
		}
		b.WriteString("\nPAPER EVIDENCE\n")
		for _, v := range d.Evidence {
			fmt.Fprintf(&b, "- %s\n  %s | %s\n", v.Text, v.Source, v.Location)
		}
		b.WriteString("\nEnter approves & queues this revision. Delivery obeys campaign limits.\n'u' resolves uncertain delivery as not sent after Gmail reconciliation.")
	case 4:
		if len(m.Snap.Inbox) == 0 {
			return "No tracked replies yet. Inbox watches continuously while the service runs."
		}
		for j, v := range m.Snap.Inbox {
			mark := " "
			if i == j {
				mark = ">"
			}
			fmt.Fprintf(&b, "%s %s [%s %.0f%%]\n", mark, v.Subject, v.Classification, v.Confidence*100)
		}
		v := m.Snap.Inbox[i]
		fmt.Fprintf(&b, "\nFrom: %s\n%s\n\n%s", v.From, v.At, v.Text)
	case 5:
		if len(m.Snap.Contacts) == 0 {
			return "No contacts yet."
		}
		for j, c := range m.Snap.Contacts {
			mark := " "
			if j == i {
				mark = ">"
			}
			fmt.Fprintf(&b, "%s %-22s %-13s %s\n", mark, c.Name, c.Status, c.University)
		}
		c := m.Snap.Contacts[i]
		fmt.Fprintf(&b, "\n%s\nSource: %s\nNext action: %s\nNotes: %s\n\nMatched papers:\n", c.Email, c.URL, c.NextAction, c.Notes)
		for _, p := range m.Snap.Papers {
			if p.ContactID == c.ID {
				fmt.Fprintf(&b, "- %s (%d)\n  %s | full text=%t\n", p.Title, p.Year, p.URL, p.FullText)
			}
		}
	case 6:
		if len(m.Snap.Memories) == 0 {
			return "No saved preferences. Press n to add one, or tell a bot 'remember ...'."
		}
		for j, v := range m.Snap.Memories {
			mark := " "
			if j == i {
				mark = ">"
			}
			fmt.Fprintf(&b, "%s [%s] %s\n  Source: %s | %s\n\n", mark, v.Agent, v.Text, v.Source, v.At)
		}
	case 7:
		fmt.Fprintf(&b, "PROFILE\n%s\n%s\nInterests: %s\nGoal: %s\n\nINTEGRATIONS\n", m.Snap.Profile.Name, m.Snap.Profile.Background, m.Snap.Profile.Interests, m.Snap.Profile.Goal)
		for _, k := range []string{"inbox", "notion", "discord", "whatsapp", "pubsub", "ai_tokens"} {
			v := m.Snap.Health[k]
			if v == "" {
				v = "not connected / no activity"
			}
			fmt.Fprintf(&b, "%-12s %s\n", k, v)
		}
		b.WriteString("\nATTACHMENT LIBRARY (a to import a local file)\n")
		for _, a := range m.Snap.Attachments {
			fmt.Fprintf(&b, "- %s (%d bytes)\n", a.Name, a.Size)
		}
		b.WriteString("\nIntegration setup: coldflow configure\nGmail sign-in: coldflow auth gmail\nDiagnostics: coldflow doctor\nCredentials come from service environment; restart after changes.")
	case 8:
		if len(m.Snap.Jobs) == 0 {
			return "No queued or failed jobs."
		}
		for j, v := range m.Snap.Jobs {
			mark := " "
			if i == j {
				mark = ">"
			}
			fmt.Fprintf(&b, "%s %s [%s] attempts=%v\n  %v\n", mark, v["kind"], v["status"], v["attempts"], v["error"])
		}
	}
	return b.String()
}
func stateSymbol(s string) string {
	if s == "idle" {
		return "."
	}
	return "*"
}
func (m *Model) contact(id string) domain.Contact {
	for _, c := range m.Snap.Contacts {
		if c.ID == id {
			return c
		}
	}
	return domain.Contact{}
}

type bindings struct{}

func (bindings) ShortHelp() []key.Binding {
	return []key.Binding{key.NewBinding(key.WithKeys("tab"), key.WithHelp("tab/1-9", "view")), key.NewBinding(key.WithKeys("left", "right"), key.WithHelp("</>", "select")), key.NewBinding(key.WithKeys("pgup", "pgdown"), key.WithHelp("pgup/dn", "scroll")), key.NewBinding(key.WithKeys("p"), key.WithHelp("p", "pause/resume")), key.NewBinding(key.WithKeys("q"), key.WithHelp("q", "detach"))}
}
func (b bindings) FullHelp() [][]key.Binding { return [][]key.Binding{b.ShortHelp()} }
func (m *Model) View() tea.View {
	mode := "LIVE"
	if m.Snap.Demo {
		mode = "DEMO: simulated mail"
	}
	if m.Snap.Paused {
		mode += " / PAUSED"
	}
	title := fmt.Sprintf(" COLDFLOW / %s ", mode)
	border := lipgloss.Border{Top: "-", Bottom: "-", Left: "|", Right: "|", TopLeft: "+", TopRight: "+", BottomLeft: "+", BottomRight: "+"}
	style := lipgloss.NewStyle().Border(border).Width(max(20, m.width-4)).Padding(0, 1)
	if os.Getenv("NO_COLOR") == "" {
		style = style.BorderForeground(lipgloss.Color("8"))
	}
	var nav []string
	for i, t := range tabs {
		label := fmt.Sprintf("%d:%s", i+1, t)
		if i == m.tab {
			label = "[" + label + "]"
		}
		nav = append(nav, label)
	}
	navText := strings.Join(nav, " ")
	if m.width < 110 {
		navText = fmt.Sprintf("[%d:%s]   Tab / Shift-Tab: switch views", m.tab+1, tabs[m.tab])
	}
	content := m.viewport.View()
	if m.form != nil {
		content = m.form.View()
	}
	footer := m.contextHelp()
	if m.busy {
		footer = "Saving... " + footer
	}
	status := integrations.CleanText(m.status)
	if len(status) > max(20, m.width-8) {
		status = status[:max(20, m.width-8)]
	}
	load := int(math.Round(math.Max(0, math.Min(8, m.load))))
	header := fmt.Sprintf("%s\n%s\nAgents: %s%s\n", title, navText, strings.Repeat("#", load), strings.Repeat(".", 8-load))
	v := tea.NewView(header + style.Render(content) + "\n" + footer + "\n" + status + "\n" + m.help.View(bindings{}))
	v.AltScreen = true
	return v
}
func (m *Model) contextHelp() string {
	switch m.tab {
	case 1:
		return "Up/Down: bot | c/Enter: chat | Esc: close form"
	case 2:
		return "n: new | e: mode/settings | d: discover now"
	case 3:
		return "Left/Right: draft | Enter: send | e: edit | r: regenerate | s: skip"
	case 4:
		return "Up/Down: reply | e: correct classification"
	case 5:
		return "Up/Down: contact | e: status/reminder | t: add paper excerpt"
	case 6:
		return "n: new | e: edit | x: forget"
	case 7:
		return "e: edit profile | a: import attachment"
	case 8:
		return "r: retry selected failed job"
	}
	return "Activity updates every 2 seconds. Service continues when you detach."
}
