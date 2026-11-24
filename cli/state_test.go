// Copyright (c) 2026 Kristoffer Dalby
// SPDX-License-Identifier: BSD-3-Clause

package cli

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/stretchr/testify/require"
)

func TestStatePath(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	tests := []struct {
		name string
		xdg  string
		want string
	}{
		{"xdg", "/xdg/state", "/xdg/state/golinkdiff/go.json"},
		{"unset falls back", "", filepath.Join(home, ".local/state/golinkdiff/go.json")},
		{"relative is ignored", "rel/state", filepath.Join(home, ".local/state/golinkdiff/go.json")},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("XDG_STATE_HOME", tt.xdg)

			got, err := statePath("go")
			require.NoError(t, err)
			require.Equal(t, tt.want, got)
		})
	}
}

func TestStateRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "golinkdiff", "go.json")

	missing, err := loadState(path)
	require.NoError(t, err)
	require.True(t, missing.Checked.IsZero(), "no file is a first run, not an error")

	want := state{Checked: t0, Links: []Link{link("a", "https://a?x=1&y=2", "x@ts", day(1))}}
	require.NoError(t, saveState(path, want))

	got, err := loadState(path)
	require.NoError(t, err)

	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("round trip (-want +got):\n%s", diff)
	}

	entries, err := os.ReadDir(filepath.Dir(path))
	require.NoError(t, err)
	require.Len(t, entries, 1, "temp file left behind")
}

func TestLoadStateCorrupt(t *testing.T) {
	path := filepath.Join(t.TempDir(), "go.json")
	require.NoError(t, os.WriteFile(path, []byte(`{"Checked":`), 0o600))

	_, err := loadState(path)
	require.ErrorContains(t, err, path)
}

func TestSaveStateFailureLeavesNoTemp(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "go.json")
	// A non-empty directory in the way makes the final rename fail after the
	// temp file is written.
	require.NoError(t, os.MkdirAll(filepath.Join(path, "x"), 0o700))

	require.Error(t, saveState(path, state{Checked: t0}))

	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	require.Len(t, entries, 1, "temp file left behind")
}
