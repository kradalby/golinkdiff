// Copyright (c) 2026 Kristoffer Dalby
// SPDX-License-Identifier: BSD-3-Clause

package cli

import (
	"bytes"
	"encoding/json"
	"regexp"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/google/go-cmp/cmp"
	"github.com/stretchr/testify/require"
)

func sampleChanges() Changes {
	oncall := link("oncall", "https://old.example/oncall", "bradfitz@example.com", day(-10))
	team := link("team", "https://team.example/", "danderson@example.com", day(-10))

	return Changes{
		Since: day(-1),
		New:   []Link{link("tsnix", "https://github.com/kradalby/tsnix", "kradalby@example.com", day(3))},
		Edited: []Edit{
			{Old: &oncall, New: link("oncall", "https://new.example/oncall", "bradfitz@example.com", day(2))},
			{Old: &team, New: link("team", "https://team.example/", "kradalby@example.com", day(1))},
		},
		Deleted: []Link{link("oldthing", "https://gone.example/", "danderson@example.com", day(-40))},
	}
}

func plainView() view {
	return view{loc: time.UTC, window: "since 2026-11-19 12:00"}
}

func TestRenderText(t *testing.T) {
	var buf bytes.Buffer
	require.NoError(t, renderText(&buf, sampleChanges(), plainView()))

	want := strings.Join([]string{
		"+ go/tsnix     kradalby@   2026-11-23  https://github.com/kradalby/tsnix",
		"~ go/oncall    bradfitz@   2026-11-22  https://new.example/oncall",
		"                                       was https://old.example/oncall",
		"~ go/team      kradalby@   2026-11-21  https://team.example/",
		"                                       was owned by danderson@",
		"- go/oldthing  danderson@  (deleted)   https://gone.example/",
		"",
		"4 changes since 2026-11-19 12:00 (1 new, 2 edited, 1 deleted)",
		"",
	}, "\n")

	if diff := cmp.Diff(want, buf.String()); diff != "" {
		t.Errorf("renderText (-want +got):\n%s", diff)
	}
}

var ansiRE = regexp.MustCompile("\x1b\\[[0-9;]*m")

func TestRenderTextColor(t *testing.T) {
	var plain, colored bytes.Buffer

	v := plainView()
	require.NoError(t, renderText(&plain, sampleChanges(), v))

	v.color = true
	require.NoError(t, renderText(&colored, sampleChanges(), v))

	out := colored.String()
	for _, marker := range []string{"\x1b[1;32m+\x1b[0m", "\x1b[1;33m~\x1b[0m", "\x1b[1;31m-\x1b[0m"} {
		require.Contains(t, out, marker)
	}

	// Padding is computed on the visible text, so colour must not shift columns.
	require.Equal(t, plain.String(), ansiRE.ReplaceAllString(out, ""))
}

func TestRenderTextClip(t *testing.T) {
	for _, tt := range []struct {
		name    string
		width   int
		clipped bool
	}{
		{"fits a line", 60, true},
		{"too narrow to be worth clipping", 50, false},
		{"not a terminal", 0, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			v := plainView()
			v.width = tt.width

			var buf bytes.Buffer
			require.NoError(t, renderText(&buf, sampleChanges(), v))

			out := buf.String()
			require.Equal(t, tt.clipped, strings.Contains(out, "…"), out)
			require.Equal(t, tt.clipped, !strings.Contains(out, "https://github.com/kradalby/tsnix"))

			if tt.clipped {
				rows, _, _ := strings.Cut(out, "\n\n") // the summary is prose; let it wrap
				for line := range strings.Lines(rows) {
					require.LessOrEqual(t, utf8.RuneCountInString(strings.TrimSuffix(line, "\n")), tt.width, line)
				}
			}
		})
	}
}

func TestRenderTextSummary(t *testing.T) {
	tests := []struct {
		name string
		ch   Changes
		note string
		want string
	}{
		{
			name: "nothing",
			ch:   newChanges(t0),
			want: "Nothing moved in go/ since 2026-11-19 12:00.\n",
		},
		{
			name: "one",
			ch:   Changes{New: []Link{link("a", "https://a", "x@ts", t0)}},
			note: "first run, snapshot saved",
			want: "1 change since 2026-11-19 12:00 (1 new) · first run, snapshot saved\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v := plainView()
			v.note = tt.note

			var buf bytes.Buffer
			require.NoError(t, renderText(&buf, tt.ch, v))

			lines := strings.Split(strings.TrimSuffix(buf.String(), "\n"), "\n")
			require.Equal(t, tt.want, lines[len(lines)-1]+"\n")
		})
	}
}

func TestRenderTextSanitizes(t *testing.T) {
	ch := Changes{New: []Link{link("x", "https://evil/\x1b]0;pwned\x07", "x@ts", t0)}}

	var buf bytes.Buffer
	require.NoError(t, renderText(&buf, ch, plainView()))
	require.NotContains(t, buf.String(), "\x1b")
	require.NotContains(t, buf.String(), "\x07")
}

func TestRenderJSON(t *testing.T) {
	ch := sampleChanges()
	ch.New[0].Long = "https://a?x=1&y=2"
	ch.Edited = append(ch.Edited, Edit{New: link("since-mode", "https://s", "x@ts", t0)})

	var buf bytes.Buffer
	require.NoError(t, renderJSON(&buf, ch))
	require.Contains(t, buf.String(), "x=1&y=2", "& must not be HTML-escaped")

	var got Changes
	require.NoError(t, json.Unmarshal(buf.Bytes(), &got))

	if diff := cmp.Diff(ch, got); diff != "" {
		t.Errorf("JSON round trip (-want +got):\n%s", diff)
	}

	var raw struct{ Edited []map[string]json.RawMessage }
	require.NoError(t, json.Unmarshal(buf.Bytes(), &raw))
	require.NotContains(t, raw.Edited[2], "Old", "an edit without a previous version omits Old")

	buf.Reset()
	require.NoError(t, renderJSON(&buf, newChanges(t0)))
	require.NotContains(t, buf.String(), "null", "empty lists are [], not null")
}
