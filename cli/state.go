// Copyright (c) 2026 Kristoffer Dalby
// SPDX-License-Identifier: BSD-3-Clause

package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"
)

// state is what one run leaves for the next.
type state struct {
	Checked time.Time
	Links   []Link
}

// statePath keeps one file per golink host, so two servers never diff against
// each other's snapshot.
func statePath(host string) (string, error) {
	dir := os.Getenv("XDG_STATE_HOME")
	// The XDG spec says to ignore a relative path.
	if !filepath.IsAbs(dir) {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("finding state directory: %w", err)
		}

		dir = filepath.Join(home, ".local", "state")
	}

	return filepath.Join(dir, "golinkdiff", host+".json"), nil
}

// loadState returns the zero state, not an error, when there is none yet.
func loadState(path string) (state, error) {
	var st state

	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return st, nil
	}

	if err != nil {
		return st, fmt.Errorf("reading state: %w", err)
	}

	err = json.Unmarshal(b, &st)
	if err != nil {
		return st, fmt.Errorf("reading state %s (delete it to start over): %w", path, err)
	}

	return st, nil
}

// saveState replaces the state file atomically: an interrupted or failed write
// leaves the previous snapshot in place, never a half-written one.
func saveState(path string, st state) error {
	b, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return fmt.Errorf("encoding state: %w", err)
	}

	dir := filepath.Dir(path)

	err = os.MkdirAll(dir, 0o700)
	if err != nil {
		return fmt.Errorf("creating state directory: %w", err)
	}

	// Same directory as the target, so the rename cannot cross filesystems.
	tmp, err := os.CreateTemp(dir, ".golinkdiff-*.tmp")
	if err != nil {
		return fmt.Errorf("writing state: %w", err)
	}
	defer os.Remove(tmp.Name()) //nolint:errcheck // already gone once renamed

	_, err = tmp.Write(append(b, '\n'))
	if err == nil {
		err = tmp.Sync()
	}

	if cerr := tmp.Close(); err == nil {
		err = cerr
	}

	if err == nil {
		err = os.Rename(tmp.Name(), path)
	}

	if err != nil {
		return fmt.Errorf("writing state: %w", err)
	}

	return nil
}
