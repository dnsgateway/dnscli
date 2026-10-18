// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.

package tui

import (
	"encoding/json"

	"github.com/charmbracelet/lipgloss"

	"github.com/sergelogvinov/dnscli/internal/dns"
)

// extraColumn is a Provider-specific Record extra the Records pane shows
// in a column of its own: a flag, or one of a few short values. Free
// text, such as a note or a comment, is not listed: it does not fit a
// column.
type extraColumn struct {
	// key is the Record extra.
	key string
	// label heads the column.
	label string
	// flag is true for an extra that is "true" or "false", shown as
	// "yes" or "no".
	flag bool
}

// extraColumns are the extras the Records pane may show, in column order.
// A column is shown only for a Zone with a Record that has its extra.
var extraColumns = []extraColumn{
	// Cloudflare's proxy flag.
	{key: proxiedExtra, label: "Proxied", flag: true},
	// DNSimple's mark of a Record the service manages.
	{key: "system_record", label: "System", flag: true},
}

// text is a Record's value in the column; empty when it has no such extra.
func (c extraColumn) text(r dns.Record) string {
	v, ok := r.Extras[c.key]
	if !ok {
		return ""
	}

	if c.flag {
		switch v {
		case "true":
			return "yes"
		case "false":
			return "no"
		}
	}

	return v
}

// shownExtraColumns are the extra columns of rows, each with its width:
// that of its label or its widest value. Every row counts, not only the
// ones a search leaves, so the columns do not come and go as it narrows.
func shownExtraColumns(rows []recordRow) ([]extraColumn, []int) {
	var (
		shown  []extraColumn
		widths []int
	)

	for _, c := range extraColumns {
		w, has := lipgloss.Width(c.label), false

		for _, row := range rows {
			if _, ok := row.rec.Extras[c.key]; ok {
				has = true
				w = max(w, lipgloss.Width(c.text(row.rec)))
			}
		}

		if has {
			shown, widths = append(shown, c), append(widths, w)
		}
	}

	return shown, widths
}

const (
	// commentExtra is the Record extra holding Cloudflare's comment on a
	// Record: free text, not served in DNS.
	commentExtra = "comment"
	// tagsExtra is the Record extra holding Cloudflare's tags on a Record,
	// as a JSON array of strings.
	tagsExtra = "tags"
)

// noteFields are the Record form's rows for the notes a Provider keeps on
// a Record beside its DNS data: its comment, wrapped, and its tags, one
// per row. They are shown, not edited, and an edit keeps them. A Record
// without them has none.
func noteFields(r dns.Record) []dialogField {
	var fields []dialogField

	// A comment of blanks only wraps to no lines, and is not shown.
	if lines := wrapNote(lipgloss.NewStyle(), r.Extras[commentExtra], maxValueWidth); len(lines) > 0 {
		fields = append(fields, listField("Comment", lines))
	}

	if tags := recordTags(r); len(tags) > 0 {
		fields = append(fields, listField("Tags", tags))
	}

	return fields
}

// recordTags are a Record's tags; the extra as it is when it is not a
// JSON array of strings.
func recordTags(r dns.Record) []string {
	raw, ok := r.Extras[tagsExtra]
	if !ok || raw == "" {
		return nil
	}

	var tags []string
	if err := json.Unmarshal([]byte(raw), &tags); err != nil {
		return []string{raw}
	}

	return tags
}
