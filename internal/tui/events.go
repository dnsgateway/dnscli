package tui

import (
	"strconv"
	"strings"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

// func (tui *TUI) setAfterDrawFunc(screen tcell.Screen) {
// 	tui.queueUpdateDraw(func() {
// 		p := tui.App.GetFocus()

// 		tui.DomainsSearch.SetBorderColor(tcell.ColorWhite)
// 		tui.Domains.SetBorderColor(tcell.ColorWhite)
// 		tui.Accounts.SetBorderColor(tcell.ColorWhite)
// 		tui.RecordsTable.SetBorderColor(tcell.ColorWhite)

// 		switch p {
// 		case tui.DomainsSearch:
// 			tui.DomainsSearch.SetBorderColor(tcell.ColorGreen)
// 		case tui.Domains:
// 			tui.Domains.SetBorderColor(tcell.ColorGreen)
// 		case tui.Accounts:
// 			tui.Accounts.SetBorderColor(tcell.ColorGreen)
// 		case tui.RecordsTable:
// 			tui.RecordsTable.SetBorderColor(tcell.ColorGreen)
// 		}
// 	})
// }

func (tui *TUI) domainSearchChanged(text string) {
	tui.domainFilter = text
	tui.LoadData()
}

func (tui *TUI) domainSelected(index int, mainText string, secondaryText string, shortcut rune) {
	tui.RecordsTable.Clear()

	records, ok := AllDomains[mainText]
	if !ok {
		records = []RecordConfig{}
	}

	tui.DomainInfo.SetCell(0, 0, tview.NewTableCell("Domain flags:").SetTextColor(tcell.ColorWhite))

	domainChecks := []string{"DNSSec", "MX", "SPF", "DKIM", "DMARC", "AutoRenew", "PrivateWhois"}
	for c, check := range domainChecks {
		color := tcell.ColorWhite
		if c%2 == 0 {
			color = tcell.ColorGreen
		}

		tui.DomainInfo.SetCell(0, c+1, tview.NewTableCell(check).SetTextColor(color).SetText(check))
	}

	dnsTitle := strings.Split("Name Type Content TTL", " ")

	for c := 0; c < len(dnsTitle); c++ {
		cell := tview.NewTableCell(dnsTitle[c])
		cell.SetTextColor(tcell.ColorYellow).
			SetAlign(tview.AlignCenter)

		cell.NotSelectable = true
		tui.RecordsTable.SetCell(0, c, cell)
	}

	for r, rec := range records {
		i := r + 1
		tui.RecordsTable.SetCell(i, 0, tview.NewTableCell(rec.Name))
		tui.RecordsTable.SetCell(i, 1, tview.NewTableCell(rec.Type).SetMaxWidth(8))
		tui.RecordsTable.SetCell(i, 2, tview.NewTableCell(rec.Target).SetExpansion(2))
		tui.RecordsTable.SetCell(i, 3, tview.NewTableCell(strconv.Itoa(int(rec.TTL))).SetTextColor(tcell.ColorWhite).SetAlign(tview.AlignLeft))
	}

	tui.setFocus(tui.Domains)
}

func (tui *TUI) recordsTableSelected(row, column int) {
	tui.Pages.ShowPage(PageRecordEditor)
}

// func (tui *TUI) recordEditDone(buttonIndex int, buttonLabel string) {
// 	if buttonLabel == "Done" {
// 		tui.Pages.HidePage(PageRecordEditor)
// 	}

// 	if buttonLabel == "Cancel" {
// 		tui.Pages.HidePage(PageRecordEditor)
// 	}

// 	tui.setFocus(tui.RecordsTable)
// }

// func (tui *TUI) accountSelected(index int, mainText string, secondaryText string, shortcut rune) {
// 	tui.setFocus(tui.Accounts)
// }

// func (tui *TUI) recordsTableSelected(index int, mainText string, secondaryText string, shortcut rune) {
// 	records := []string{"one", "two", "three"}

// 	tui.queueUpdateDraw(func() {
// 		for _, record := range records {
// 			tui.RecordsTable.AddItem(record, mainText, 0, nil)
// 		}
// 	})

// 	tui.setFocus(tui.RecordsTable)
// }
