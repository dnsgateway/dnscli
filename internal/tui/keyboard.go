package tui

import (
	"strings"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

var (
	// DomainInputFilter is the input filter for the domain search input field.
	DomainInputFilter = func(text string, ch rune) bool {
		return strings.ContainsRune("abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789.-", ch)
	}
	SubDomainInputFilter = func(text string, ch rune) bool {
		return strings.ContainsRune("abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789.-@", ch)
	}
)

func (tui *TUI) domainInputCapture(event *tcell.EventKey) *tcell.EventKey {
	switch event.Key() {
	case tcell.KeyUp:
		if tui.Domains.GetCurrentItem() == 0 {
			tui.App.SetFocus(tui.DomainsSearch)
			return nil
		}
	case tcell.KeyDown:
		if tui.Domains.GetCurrentItem() == tui.Domains.GetItemCount()-1 {
			return nil
		}
	case tcell.KeyLeft:
		tui.Domains.SetCurrentItem(0)
		return nil
	}

	return event
}

func (tui *TUI) setupKeyboard() {
	focusMapping := map[tview.Primitive]struct{ next, prev, left, right tview.Primitive }{
		tui.DomainsSearch: {tui.Domains, tui.RecordsTable, nil, nil},
		tui.Domains:       {tui.RecordsTable, tui.DomainsSearch, nil, tui.RecordsTable},
		tui.Accounts:      {tui.RecordsTable, tui.Domains, nil, tui.RecordsTable},
		tui.RecordsTable:  {tui.Domains, tui.Domains, tui.Domains, nil},
	}

	tui.App.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		switch event.Key() {
		case KeyMapping[KeyDomainsSearchOp]:
			tui.App.SetFocus(tui.DomainsSearch)
		case KeyMapping[KeyDomainsOp]:
			tui.App.SetFocus(tui.Domains)
		case KeyMapping[KeyAccountsOp]:
			tui.setFocus(tui.Accounts)
		case KeyMapping[KeyRecordsOp]:
			tui.setFocus(tui.RecordsTable)

		case tcell.KeyDown:
			if tui.App.GetFocus() == tui.DomainsSearch {
				if focusMap, ok := focusMapping[tui.App.GetFocus()]; ok {
					tui.setFocus(focusMap.next)
					return nil
				}
			}

		case tcell.KeyLeft:
			if focusMap, ok := focusMapping[tui.App.GetFocus()]; ok && focusMap.left != nil {
				tui.setFocus(focusMap.left)
				return nil
			}
		case tcell.KeyRight:
			if focusMap, ok := focusMapping[tui.App.GetFocus()]; ok && focusMap.right != nil {
				tui.setFocus(focusMap.right)
				return nil
			}
		case tcell.KeyTab:
			if focusMap, ok := focusMapping[tui.App.GetFocus()]; ok {
				tui.setFocus(focusMap.next)
				return nil
			}
		case tcell.KeyBacktab:
			if focusMap, ok := focusMapping[tui.App.GetFocus()]; ok {
				tui.setFocus(focusMap.prev)
				return nil
			}
		case tcell.KeyEnter:
			if tui.App.GetFocus() == tui.DomainsSearch {
				if focusMap, ok := focusMapping[tui.App.GetFocus()]; ok {
					tui.setFocus(focusMap.next)
					return nil
				}
			}
		}

		switch event.Rune() {
		case 'q':
			if tui.App.GetFocus() == tui.Domains || tui.App.GetFocus() == tui.Accounts {
				tui.App.Stop()
			}
		case '?':
			tui.Pages.ShowPage(PageHelp)
		}

		return event
	})
}
