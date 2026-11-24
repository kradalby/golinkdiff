// Copyright (c) 2026 Kristoffer Dalby
// SPDX-License-Identifier: BSD-3-Clause

package cli

import (
	"cmp"
	"slices"
	"strings"
	"time"
)

// Link is one go link as golink's /.export writes it. The field names are
// golink's, so a snapshot is the export itself.
type Link struct {
	Short    string
	Long     string
	Created  time.Time
	LastEdit time.Time
	Owner    string
}

// Edit is a link whose destination or owner changed. Old is nil when the change
// was found by timestamp alone (--since, first run): there is no old version.
type Edit struct {
	Old *Link `json:",omitempty"`
	New Link
}

// Changes is what moved in go links since a point in time.
type Changes struct {
	Since   time.Time
	New     []Link
	Edited  []Edit
	Deleted []Link
}

func (c Changes) count() int {
	return len(c.New) + len(c.Edited) + len(c.Deleted)
}

// diffSnapshot compares the current export against the previous snapshot. Only
// Long and Owner count as edits: a re-save to the same destination is not news.
func diffSnapshot(since time.Time, prev, cur []Link) Changes {
	old := make(map[string]Link, len(prev))
	for _, l := range prev {
		old[l.Short] = l
	}

	ch := newChanges(since)

	for _, l := range cur {
		o, seen := old[l.Short]
		delete(old, l.Short)

		switch {
		case !seen:
			ch.New = append(ch.New, l)
		case o.Long != l.Long || o.Owner != l.Owner:
			ch.Edited = append(ch.Edited, Edit{Old: &o, New: l})
		}
	}

	for _, l := range old {
		ch.Deleted = append(ch.Deleted, l)
	}

	ch.sort()

	return ch
}

// diffSince finds changes by timestamp, for when there is no snapshot to
// compare against. That also means it cannot see deletions.
func diffSince(since time.Time, cur []Link) Changes {
	ch := newChanges(since)

	for _, l := range cur {
		switch {
		case !l.Created.Before(since):
			ch.New = append(ch.New, l)
		case !l.LastEdit.Before(since):
			ch.Edited = append(ch.Edited, Edit{New: l})
		}
	}

	ch.sort()

	return ch
}

// newChanges starts with empty, non-nil slices so --json emits [] not null, and
// reports Since in UTC whichever window it came from.
func newChanges(since time.Time) Changes {
	return Changes{Since: since.UTC(), New: []Link{}, Edited: []Edit{}, Deleted: []Link{}}
}

func (c Changes) sort() {
	slices.SortFunc(c.New, newestFirst)
	slices.SortFunc(c.Edited, func(a, b Edit) int { return newestFirst(a.New, b.New) })
	slices.SortFunc(c.Deleted, func(a, b Link) int { return strings.Compare(a.Short, b.Short) })
}

func newestFirst(a, b Link) int {
	return cmp.Or(b.LastEdit.Compare(a.LastEdit), strings.Compare(a.Short, b.Short))
}
