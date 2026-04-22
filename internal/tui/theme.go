package tui

import (
	"charm.land/huh/v2"
	"charm.land/lipgloss/v2"
)

// ASCIITheme keeps form controls readable in basic SSH terminals.
func ASCIITheme(dark bool) *huh.Styles {
	t := huh.ThemeBase(dark)
	border := lipgloss.Border{Left: "|", Right: "|", Top: "-", Bottom: "-", TopLeft: "+", TopRight: "+", BottomLeft: "+", BottomRight: "+"}
	t.Focused.Base = t.Focused.Base.BorderStyle(border)
	t.Focused.Card = t.Focused.Base
	t.Focused.NextIndicator = t.Focused.NextIndicator.SetString(">")
	t.Focused.PrevIndicator = t.Focused.PrevIndicator.SetString("<")
	t.Focused.SelectedPrefix = t.Focused.SelectedPrefix.SetString("[x] ")
	t.Blurred.SelectedPrefix = t.Blurred.SelectedPrefix.SetString("[x] ")
	return t
}
