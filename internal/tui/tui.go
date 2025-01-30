package tui

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
	"github.com/sergelogvinov/dnscli/internal/tui/dialog"
)

type (
	// KeyOp defines dbui specific hotkey operations.
	KeyOp int16
)

const (
	// KeyDomainsSearchOp is the operation corresponding to the activation of the Domains Search view.
	KeyDomainsSearchOp KeyOp = iota
	// KeyDomainsOp is the operation corresponding to the activation of the Domains view.
	KeyDomainsOp
	// KeyAccountsOp is the operation corresponding to the activation of the Accounts view.
	KeyAccountsOp
	// KeyRecordsOp is the operation corresponding to the activation of the Records view.
	KeyRecordsOp
)

var (
	// KeyMapping maps keyboard operations to their hotkeys. In the future, this part can be customized by the user configuration.
	KeyMapping = map[KeyOp]tcell.Key{
		KeyDomainsOp:  tcell.KeyCtrlD,
		KeyAccountsOp: tcell.KeyCtrlA,
		KeyRecordsOp:  tcell.KeyCtrlE,
	}

	// TitleDomainsView is the title for Domains view.
	TitleDomainsView = fmt.Sprintf(" Domains [ %s ] ", tcell.KeyNames[KeyMapping[KeyDomainsOp]])
	// TitleAccountsView is the title for Accounts view.
	TitleAccountsView = fmt.Sprintf(" Accounts [ %s ] ", tcell.KeyNames[KeyMapping[KeyAccountsOp]])
	// TitleRecordsView is the title for Records view.
	TitleRecordsView = fmt.Sprintf(" Records [ %s ] ", tcell.KeyNames[KeyMapping[KeyRecordsOp]])
	// TitleRecordRecord is the title for Record record.
	TitleRecordRecord = " Add or Edit record "
	// TitleFooterView is the title for Footer view.
	TitleFooterView = " Navigate [ Tab / Shift-Tab ] · Exit [ Ctrl-C ] \n Records specific: Add [ a ] · Edit [ enter ] · Delete [ d ]"

	// PageDomain is the page name for Domain list.
	PageDomain = "Domain"
	// PageRecordEditor is the page name for RecordEditor.
	PageRecordEditor = "RecordEditor"
	// PageHelp is the page name for Help.
	PageHelp = "Help"
)

type TUI struct {
	App   *tview.Application
	Pages *tview.Pages

	DomainsSearch *tview.InputField
	Domains       *tview.List
	Accounts      *tview.List

	DomainInfo   *tview.Table
	RecordsTable *tview.Table
	RecordEdit   *dialog.RecordEditor
	FooterText   *tview.TextView
	Help         *dialog.Help

	leftPanel  *tview.Flex
	rightPanel *tview.Flex
	layout     *tview.Flex

	domainFilter string
}

type RecordConfig struct {
	Type   string `json:"type"`
	Name   string `json:"name"`
	Target string `json:"target,omitempty"`
	TTL    uint32 `json:"ttl,omitempty"`
}

func (tui *TUI) queueUpdateDraw(f func()) {
	go func() {
		tui.App.QueueUpdateDraw(f)
	}()
}

var (
	AllDomains = map[string][]RecordConfig{
		"example.com": {
			{Type: "A", Name: "example.com", Target: "127.0.0.1", TTL: 300},
			{Type: "AAAA", Name: "example.com", Target: "::1", TTL: 300},
			{Type: "CNAME", Name: "www.example.com", Target: "example.com.", TTL: 300},
			{Type: "TXT", Name: "example.com", Target: "spf", TTL: 300},
		},
		"two.tv": {
			{Type: "A", Name: "two.tv", Target: "8.8.8.8", TTL: 300},
			{Type: "AAAA", Name: "two.tv", Target: "2001:4860:4860::8888", TTL: 300},
		},
	}
)

func NewTUI() *TUI {
	tview.Styles.PrimitiveBackgroundColor = tcell.ColorBlack
	tview.Styles.ContrastBackgroundColor = tcell.ColorGray
	tview.Styles.MoreContrastBackgroundColor = tcell.ColorGray
	tview.Styles.BorderColor = tcell.ColorGray
	tview.Styles.TitleColor = tcell.ColorWhite
	tview.Styles.GraphicsColor = tcell.ColorWhite
	tview.Styles.PrimaryTextColor = tcell.ColorWhite
	tview.Styles.SecondaryTextColor = tcell.ColorYellow
	tview.Styles.TertiaryTextColor = tcell.ColorWhite
	tview.Styles.InverseTextColor = tcell.ColorWhite
	tview.Styles.ContrastSecondaryTextColor = tcell.ColorWhite

	t := TUI{}
	t.App = tview.NewApplication()

	// Setup view elements.
	t.DomainsSearch = tview.NewInputField().SetLabel("Domain ").SetFieldWidth(0).SetAcceptanceFunc(DomainInputFilter)
	t.Domains = tview.NewList().ShowSecondaryText(false)
	t.Accounts = tview.NewList().ShowSecondaryText(false).SetSelectedFocusOnly(true)
	t.DomainInfo = tview.NewTable().SetBorders(false)
	t.RecordsTable = tview.NewTable()
	t.FooterText = tview.NewTextView().SetTextAlign(tview.AlignCenter).SetText(TitleFooterView).SetTextColor(Styles.MoreContrastBackgroundColor)

	// Configure appearance.
	t.DomainsSearch.SetFieldTextColor(tcell.ColorWhite)
	t.Domains.SetTitle(TitleDomainsView).SetBorder(true)
	t.Accounts.SetTitle(TitleAccountsView).SetBorder(true)
	t.DomainInfo.SetTitleAlign(tview.AlignRight).SetBorderPadding(0, 0, 1, 1)
	t.RecordsTable.SetTitle(TitleRecordsView).SetBorder(true)

	// Configure input handlers.
	t.DomainsSearch.SetChangedFunc(t.domainSearchChanged)
	t.Domains.SetSelectedFunc(t.domainSelected)
	t.Domains.SetInputCapture(t.domainInputCapture)
	t.RecordsTable.SetSelectedFunc(t.recordsTableSelected)

	t.leftPanel = tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(t.DomainsSearch, 1, 0, true).
		AddItem(t.Domains, 0, 2, false).
		AddItem(t.Accounts, 0, 1, false)

	t.rightPanel = tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(t.DomainInfo, 1, 0, false).
		AddItem(t.RecordsTable, 0, 2, true)
		// AddItem(t.FooterText, 2, 0, false)

	t.layout = tview.NewFlex().
		AddItem(t.leftPanel, 0, 2, true).
		AddItem(t.rightPanel, 0, 8, false)

	t.Help = dialog.NewHelp(t.App, func(event *tcell.EventKey) *tcell.EventKey {
		switch event.Key() {
		case tcell.KeyEsc:
			t.Pages.HidePage(PageHelp)
			return nil
		}

		return event
	})

	t.RecordEdit = dialog.NewRecordEditor(t.App)

	t.Pages = tview.NewPages()
	t.Pages.AddPage(PageDomain, t.layout, true, true)
	t.Pages.AddPage(PageRecordEditor, t.RecordEdit, true, false)
	t.Pages.AddPage(PageHelp, t.Help, false, false)

	// t.App.SetAfterDrawFunc(t.setAfterDrawFunc)
	t.setupKeyboard()

	t.LoadData()

	// t.Pages.SwitchToPage(PageRecordEditor)
	t.Pages.ShowPage(PageRecordEditor)

	return &t
}

func (tui *TUI) Start() error {
	return tui.App.SetRoot(tui.Pages, true).EnableMouse(true).Run()
}

func (tui *TUI) setFocus(p tview.Primitive) {
	tui.queueUpdateDraw(func() {
		tui.App.SetFocus(p)
	})
}

func (tui *TUI) LoadData() {
	tui.Domains.Clear()
	tui.Accounts.Clear()
	tui.RecordsTable.Clear()

	tui.Domains.SetSelectedFocusOnly(true)

	domains := []string{"example.com", "two.tv", "three.io", "four.net", "five.org", "six.dev", "seven.app", "eight.co", "nine.xyz", "ten.space"}
	for _, domain := range domains {
		if tui.domainFilter == "" {
			tui.Domains.AddItem(domain, "", 0, nil)
			continue
		}

		if strings.Contains(domain, tui.domainFilter) {
			tui.Domains.AddItem(domain, "", 0, nil)
		}
	}

	accounts := []string{"cloudflare", "namecheap", "godaddy"}
	for _, account := range accounts {
		tui.Accounts.AddItem(account, "", 0, nil)
	}

	tui.RecordsTable.SetSelectable(true, false)

	records, ok := AllDomains["example.com"]
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
		// cell.Transparent = true
		// cell.SetExpansion(2)

		tui.RecordsTable.SetCell(0, c, cell)
	}

	for r, rec := range records {
		i := r + 1
		tui.RecordsTable.SetCell(i, 0, tview.NewTableCell(rec.Name))
		// tui.RecordsTable.SetCell(i, 0, tview.NewTableCell(rec.Name).SetSelectedStyle(tcell.Style{}.Foreground(tcell.ColorBlack).Background(tcell.ColorGray)))
		tui.RecordsTable.SetCell(i, 1, tview.NewTableCell(rec.Type).SetMaxWidth(8))
		tui.RecordsTable.SetCell(i, 2, tview.NewTableCell(rec.Target).SetExpansion(2))
		tui.RecordsTable.SetCell(i, 3, tview.NewTableCell(strconv.Itoa(int(rec.TTL))).SetTextColor(tcell.ColorWhite).SetAlign(tview.AlignLeft))
	}
}
