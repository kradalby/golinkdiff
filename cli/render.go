// Copyright (c) 2026 Kristoffer Dalby
// SPDX-License-Identifier: BSD-3-Clause

package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// SGR parameters; plain ANSI covers every terminal this runs in.
const (
	sgrBold   = "1"
	sgrDim    = "2"
	sgrRed    = "1;31"
	sgrGreen  = "1;32"
	sgrYellow = "1;33"
)

const (
	colGap = "  "
	// Narrower than this, clipping leaves too little to recognise a URL; let
	// the terminal wrap instead.
	minClip = 20
)

// view is how to present one set of changes.
type view struct {
	color  bool
	width  int // terminal columns to clip to; 0 never clips
	loc    *time.Location
	window string // completes "3 changes …", e.g. "since 2026-11-23 14:05"
	note   string // appended to the summary, e.g. "first run, snapshot saved"
}

type row struct {
	mark    string
	sgr     string
	link    Link
	was     string
	deleted bool
}

func renderText(w io.Writer, ch Changes, v view) error {
	rows := make([]row, 0, ch.count())
	for _, l := range ch.New {
		rows = append(rows, row{mark: "+", sgr: sgrGreen, link: l})
	}

	for _, e := range ch.Edited {
		rows = append(rows, row{mark: "~", sgr: sgrYellow, link: e.New, was: was(e)})
	}

	slices.SortStableFunc(rows, func(a, b row) int { return newestFirst(a.link, b.link) })

	for _, l := range ch.Deleted {
		rows = append(rows, row{mark: "-", sgr: sgrRed, link: l, deleted: true})
	}

	var shortW, ownerW int
	for _, r := range rows {
		shortW = max(shortW, width("go/"+r.link.Short))
		ownerW = max(ownerW, width(shortOwner(r.link.Owner)))
	}

	dateW := len(time.DateOnly)
	destCol := len("+ ") + shortW + len(colGap) + ownerW + len(colGap) + dateW + len(colGap)
	indent := strings.Repeat(" ", destCol)

	var b strings.Builder
	for _, r := range rows {
		short, owner, date, dest := sgrBold, "", sgrDim, ""
		dateText := r.link.LastEdit.In(v.loc).Format(time.DateOnly)

		if r.deleted {
			short, owner, dest, dateText = sgrDim, sgrDim, sgrDim, "(deleted)"
		}

		b.WriteString(v.paint(r.sgr, r.mark) + " ")
		b.WriteString(v.paint(sgrDim, "go/") + v.paint(short, sanitize(r.link.Short)))
		b.WriteString(pad("go/"+r.link.Short, shortW) + colGap)
		b.WriteString(v.paint(owner, shortOwner(r.link.Owner)) + pad(shortOwner(r.link.Owner), ownerW) + colGap)
		b.WriteString(v.paint(date, dateText) + pad(dateText, dateW) + colGap)
		b.WriteString(v.paint(dest, v.clip(sanitize(r.link.Long), destCol)) + "\n")

		if r.was != "" {
			b.WriteString(indent + v.paint(sgrDim, v.clip("was "+r.was, destCol)) + "\n")
		}
	}

	if len(rows) > 0 {
		b.WriteString("\n")
	}

	b.WriteString(v.summary(ch))

	_, err := io.WriteString(w, b.String())

	return err
}

func (v view) summary(ch Changes) string {
	var s string

	if n := ch.count(); n == 0 {
		s = "Nothing moved in go/ " + v.window + "."
	} else {
		var parts []string
		for _, p := range []struct {
			n   int
			sgr string
			say string
		}{
			{len(ch.New), sgrGreen, "new"},
			{len(ch.Edited), sgrYellow, "edited"},
			{len(ch.Deleted), sgrRed, "deleted"},
		} {
			if p.n > 0 {
				parts = append(parts, v.paint(p.sgr, fmt.Sprintf("%d %s", p.n, p.say)))
			}
		}

		noun := "changes"
		if n == 1 {
			noun = "change"
		}

		s = fmt.Sprintf("%s %s (%s)", v.paint(sgrBold, fmt.Sprintf("%d %s", n, noun)), v.window, strings.Join(parts, ", "))
	}

	if v.note != "" {
		s += v.paint(sgrDim, " · "+v.note)
	}

	return s + "\n"
}

func renderJSON(w io.Writer, ch Changes) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	// Destinations are full of &; keep them readable.
	enc.SetEscapeHTML(false)

	return enc.Encode(ch)
}

// was describes what an edit replaced; empty when there is no old version.
func was(e Edit) string {
	if e.Old == nil {
		return ""
	}

	var parts []string
	if e.Old.Long != e.New.Long {
		parts = append(parts, sanitize(e.Old.Long))
	}

	if e.Old.Owner != e.New.Owner {
		parts = append(parts, "owned by "+shortOwner(e.Old.Owner))
	}

	return strings.Join(parts, ", ")
}

func (v view) paint(sgr, text string) string {
	if !v.color || sgr == "" || text == "" {
		return text
	}

	return "\x1b[" + sgr + "m" + text + "\x1b[0m"
}

// clip keeps a destination on one terminal line; a single long URL would
// otherwise wrap and break the columns for every row after it.
func (v view) clip(s string, col int) string {
	avail := v.width - col
	if v.width == 0 || avail < minClip || width(s) <= avail {
		return s
	}

	r := []rune(s)

	return string(r[:avail-1]) + "…"
}

// shortOwner shows owners the way tailscale does, as "user@".
func shortOwner(owner string) string {
	if user, _, ok := strings.Cut(owner, "@"); ok {
		return sanitize(user) + "@"
	}

	return sanitize(owner)
}

// sanitize stops link text, which anyone on the tailnet can set, from sending
// escape sequences to the terminal.
func sanitize(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return utf8.RuneError
		}

		return r
	}, s)
}

func width(s string) int { return utf8.RuneCountInString(s) }

func pad(s string, w int) string { return strings.Repeat(" ", max(0, w-width(s))) }
