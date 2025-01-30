package dialog

import (
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

type Help struct {
	*tview.Modal
	app *tview.Application
}

func NewHelp(app *tview.Application, capture func(event *tcell.EventKey) *tcell.EventKey) *Help {
	f := Help{
		Modal: tview.NewModal(),
		app:   app,
	}

	f.Modal.SetBorder(true)
	f.Modal.SetInputCapture(capture)

	f.Modal.SetText(" Navigate [ Tab / Shift-Tab ] · Exit [ Ctrl-C ] \n Records specific: Add [ a ] · Edit [ enter ] · Delete [ d ] ")

	return &f
}
