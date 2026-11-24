// Copyright (c) 2026 Kristoffer Dalby
// SPDX-License-Identifier: BSD-3-Clause

package cli

import (
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
)

var t0 = time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)

func day(n int) time.Time { return t0.AddDate(0, 0, n) }

func link(short, long, owner string, edited time.Time) Link {
	return Link{Short: short, Long: long, Owner: owner, Created: edited, LastEdit: edited}
}

func TestDiffSnapshot(t *testing.T) {
	a := link("a", "https://a", "x@ts", day(0))
	b := link("b", "https://b", "x@ts", day(0))

	tests := []struct {
		name string
		prev []Link
		cur  []Link
		want Changes
	}{
		{
			name: "unchanged",
			prev: []Link{a, b},
			cur:  []Link{a, b},
			want: newChanges(t0),
		},
		{
			name: "new",
			prev: []Link{a},
			cur:  []Link{a, b},
			want: Changes{Since: t0, New: []Link{b}, Edited: []Edit{}, Deleted: []Link{}},
		},
		{
			name: "deleted",
			prev: []Link{a, b},
			cur:  []Link{b},
			want: Changes{Since: t0, New: []Link{}, Edited: []Edit{}, Deleted: []Link{a}},
		},
		{
			name: "long changed",
			prev: []Link{a},
			cur:  []Link{link("a", "https://a2", "x@ts", day(1))},
			want: Changes{
				Since:   t0,
				New:     []Link{},
				Edited:  []Edit{{Old: &a, New: link("a", "https://a2", "x@ts", day(1))}},
				Deleted: []Link{},
			},
		},
		{
			name: "owner changed",
			prev: []Link{a},
			cur:  []Link{link("a", "https://a", "y@ts", day(1))},
			want: Changes{
				Since:   t0,
				New:     []Link{},
				Edited:  []Edit{{Old: &a, New: link("a", "https://a", "y@ts", day(1))}},
				Deleted: []Link{},
			},
		},
		{
			name: "re-save with same content is not an edit",
			prev: []Link{a},
			cur:  []Link{link("a", "https://a", "x@ts", day(3))},
			want: newChanges(t0),
		},
		{
			name: "newest first, deletions by name",
			prev: []Link{link("z", "", "", day(0)), link("m", "", "", day(0))},
			cur: []Link{
				link("old", "", "", day(1)),
				link("newer", "", "", day(3)),
				link("tie-b", "", "", day(2)),
				link("tie-a", "", "", day(2)),
			},
			want: Changes{
				Since: t0,
				New: []Link{
					link("newer", "", "", day(3)),
					link("tie-a", "", "", day(2)),
					link("tie-b", "", "", day(2)),
					link("old", "", "", day(1)),
				},
				Edited:  []Edit{},
				Deleted: []Link{link("m", "", "", day(0)), link("z", "", "", day(0))},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := diffSnapshot(t0, tt.prev, tt.cur)
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("diffSnapshot (-want +got):\n%s", diff)
			}
		})
	}
}

func TestDiffSince(t *testing.T) {
	createdAtCutoff := link("created-at-cutoff", "", "", t0)
	editedAtCutoff := Link{Short: "edited-at-cutoff", Created: day(-30), LastEdit: t0}
	editedLater := Link{Short: "edited-later", Created: day(-30), LastEdit: day(2)}
	untouched := Link{Short: "untouched", Created: day(-30), LastEdit: t0.Add(-time.Second)}

	got := diffSince(t0, []Link{untouched, createdAtCutoff, editedAtCutoff, editedLater})
	want := Changes{
		Since:   t0,
		New:     []Link{createdAtCutoff},
		Edited:  []Edit{{New: editedLater}, {New: editedAtCutoff}},
		Deleted: []Link{},
	}

	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("diffSince (-want +got):\n%s", diff)
	}
}
