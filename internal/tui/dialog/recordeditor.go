package dialog

import (
	"strings"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

type RecordEditor struct {
	*tview.Flex
	app *tview.Application

	recordName   *tview.InputField
	recordType   *tview.DropDown
	recordTarget *tview.InputField
	recordTTL    *tview.DropDown
}

var records = []string{"example.com", "test", "dev", "staging", "prod"}

func NewRecordEditor(app *tview.Application) *RecordEditor {
	f := RecordEditor{
		Flex: tview.NewFlex(),
		app:  app,
	}

	width := 60

	form := tview.NewForm().SetFieldBackgroundColor(tcell.ColorGray).SetFieldTextColor(tcell.ColorWhite)

	f.recordName = tview.NewInputField().SetLabel("Name")
	f.recordName.SetAutocompleteFunc(func(currentText string) (entries []string) {
		if len(currentText) == 0 {
			return
		}
		for _, word := range records {
			if strings.Contains(strings.ToLower(word), strings.ToLower(currentText)) {
				entries = append(entries, word)
			}
		}
		if len(entries) <= 1 {
			entries = nil
		}
		return
	})
	form.AddFormItem(f.recordName)

	f.recordType = tview.NewDropDown().SetLabel("Type").SetOptions([]string{"A", "AAAA", "TXT", "MX"}, nil)
	f.recordType.SetCurrentOption(0)
	f.recordType.SetSelectedFunc(func(text string, index int) {
		app.SetFocus(f.recordTarget)
	})
	form.AddFormItem(f.recordType)

	f.recordTarget = tview.NewInputField().SetLabel("Target")
	form.AddFormItem(f.recordTarget)

	f.recordTTL = tview.NewDropDown().SetLabel("TTL").SetOptions([]string{"300", "600", "900", "1800", "3600", "7200", "14400", "28800", "57600", "86400"}, nil)
	f.recordTTL.SetCurrentOption(0)
	f.recordTTL.SetSelectedFunc(func(text string, index int) {
		app.SetFocus(form.GetButton(0))
	})
	form.AddFormItem(f.recordTTL)

	form.SetButtonsAlign(tview.AlignCenter)
	form.AddButton("Done", nil)
	form.AddButton("Cancel", nil)

	form.SetBorderPadding(1, 1, 2, 2)
	form.SetBackgroundColor(tcell.ColorBlack).SetBorder(true)

	f.SetBorder(false).SetBackgroundColor(tcell.ColorWhite)

	f.AddItem(nil, 0, 1, false).
		AddItem(tview.NewFlex().
			SetDirection(tview.FlexRow).
			AddItem(nil, 0, 1, false).
			AddItem(form, 14, 1, true).
			AddItem(nil, 0, 1, false), width, 1, true).
		AddItem(nil, 0, 1, false)

	return &f
}
