// Copyright (c) 2026 Kristoffer Dalby
// SPDX-License-Identifier: BSD-3-Clause

package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/stretchr/testify/require"
)

func TestParseSince(t *testing.T) {
	now := time.Date(2026, 11, 24, 15, 30, 0, 0, time.UTC)

	tests := []struct {
		in      string
		want    time.Time
		wantErr string
	}{
		{in: "36h", want: now.Add(-36 * time.Hour)},
		{in: "90m", want: now.Add(-90 * time.Minute)},
		{in: "7d", want: time.Date(2026, 11, 17, 15, 30, 0, 0, time.UTC)},
		{in: "2026-11-01", want: time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)},
		{in: "0d", wantErr: "in the past"},
		{in: "-3h", wantErr: "in the past"},
		{in: "2026-12-01", wantErr: "in the past"},
		{in: "1.5d", wantErr: "want a duration"},
		{in: "1w", wantErr: "want a duration"},
		{in: "yesterday", wantErr: "want a duration"},
	}

	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			got, err := parseSince(tt.in, now)
			if tt.wantErr != "" {
				require.ErrorContains(t, err, tt.wantErr)
				require.ErrorAs(t, err, new(usageError))

				return
			}

			require.NoError(t, err)
			require.True(t, tt.want.Equal(got), "got %v, want %v", got, tt.want)
		})
	}
}

func TestParseExport(t *testing.T) {
	line := `{"Short":"a","Long":"https://a","Created":"2026-11-20T12:00:00Z","LastEdit":"2026-11-20T12:00:00Z","Owner":"x@ts"}`

	tests := []struct {
		name    string
		body    string
		wantErr error
	}{
		{name: "ok", body: line + "\n" + strings.Replace(line, `"a"`, `"b"`, 1) + "\n"},
		{name: "empty", body: "", wantErr: errEmptyExport},
		{name: "cut mid-line", body: line + "\n" + line[:40], wantErr: errTruncated},
		{name: "cut before final newline", body: line, wantErr: errTruncated},
		{name: "login page", body: "<html>sign in</html>\n", wantErr: errBadLink},
		{name: "json but not a link", body: `{"error":"nope"}` + "\n", wantErr: errBadLink},
		{name: "blank line", body: line + "\n\n", wantErr: errBadLink},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			links, err := parseExport([]byte(tt.body))
			if tt.wantErr != nil {
				require.ErrorIs(t, err, tt.wantErr)

				return
			}

			require.NoError(t, err)
			require.Len(t, links, 2)
			require.Equal(t, link("a", "https://a", "x@ts", t0), links[0])
		})
	}
}

func TestFetch(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/.export" {
			http.NotFound(w, r)

			return
		}

		w.Write([]byte(`{"Short":"a","Long":"https://a"}` + "\n"))
	}))
	defer target.Close()

	// golink answers http://go with a redirect to its https FQDN.
	redirector := httptest.NewServer(http.RedirectHandler(target.URL+"/.export", http.StatusFound))
	defer redirector.Close()

	for _, base := range []string{redirector.URL, target.URL + "/"} {
		u, err := url.Parse(base)
		require.NoError(t, err)

		links, err := fetch(t.Context(), u)
		require.NoError(t, err, base)
		require.Equal(t, []Link{{Short: "a", Long: "https://a"}}, links)
	}

	failing := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer failing.Close()

	u, err := url.Parse(failing.URL)
	require.NoError(t, err)

	_, err = fetch(t.Context(), u)
	require.ErrorIs(t, err, errStatus)
}

// golinkServer serves a mutable export, like a golink whose links change
// between runs.
type golinkServer struct {
	*httptest.Server

	mu    sync.Mutex
	links []Link
}

func newGolinkServer(t *testing.T, links ...Link) *golinkServer {
	t.Helper()

	s := &golinkServer{links: links}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		s.mu.Lock()
		defer s.mu.Unlock()

		enc := json.NewEncoder(w)
		for _, l := range s.links {
			enc.Encode(l)
		}
	}))
	t.Cleanup(s.Close)

	return s
}

func (s *golinkServer) set(links ...Link) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.links = links
}

type result struct {
	code   int
	stdout string
	stderr string
}

func invoke(t *testing.T, args ...string) result {
	t.Helper()

	var stdout, stderr bytes.Buffer
	code := execute(t.Context(), args, &stdout, &stderr, 0)

	return result{code, stdout.String(), stderr.String()}
}

func TestExecute(t *testing.T) {
	stateHome := t.TempDir()
	t.Setenv("XDG_STATE_HOME", stateHome)

	now := time.Now().UTC()
	recent := link("recent", "https://recent", "a@ts", now.Add(-time.Hour))
	edited := Link{Short: "edited", Long: "https://edited", Owner: "a@ts", Created: now.AddDate(-1, 0, 0), LastEdit: now.Add(-2 * time.Hour)}
	old := link("old", "https://old", "a@ts", now.AddDate(-1, 0, 0))
	gone := link("gone", "https://gone", "b@ts", now.AddDate(-1, 0, 0))

	srv := newGolinkServer(t, recent, edited, old, gone)
	u, err := url.Parse(srv.URL)
	require.NoError(t, err)

	path := filepath.Join(stateHome, "golinkdiff", u.Host+".json")
	readState := func() []byte {
		b, err := os.ReadFile(path)
		require.NoError(t, err)

		return b
	}

	// The URL comes from the environment, as a dotfiles install would set it.
	t.Setenv("GOLINKDIFF_URL", srv.URL)

	t.Run("first run shows the last week and saves", func(t *testing.T) {
		r := invoke(t)
		require.Equal(t, 0, r.code, r.stderr)
		require.Contains(t, r.stdout, "+ go/recent")
		require.Contains(t, r.stdout, "~ go/edited")
		require.NotContains(t, r.stdout, "go/old")
		require.Contains(t, r.stdout, "2 changes in the last 7 days (1 new, 1 edited) · first run, snapshot saved")
		require.FileExists(t, path)
	})

	t.Run("second run sees nothing", func(t *testing.T) {
		r := invoke(t)
		require.Equal(t, 0, r.code, r.stderr)
		require.True(t, strings.HasPrefix(r.stdout, "Nothing moved in go/ since "), r.stdout)
	})

	moved := link("edited", "https://edited/v2", "a@ts", now)
	added := link("added", "https://added", "c@ts", now)
	srv.set(recent, moved, old, added)

	t.Run("no-update peeks without advancing", func(t *testing.T) {
		before := readState()

		r := invoke(t, "--no-update")
		require.Equal(t, 0, r.code, r.stderr)
		require.Contains(t, r.stdout, "3 changes since")
		require.Contains(t, r.stdout, "state not updated")
		require.Equal(t, before, readState())
	})

	t.Run("json", func(t *testing.T) {
		r := invoke(t, "--json", "--no-update")
		require.Equal(t, 0, r.code, r.stderr)

		var got Changes
		require.NoError(t, json.Unmarshal([]byte(r.stdout), &got))

		want := Changes{
			New:     []Link{added},
			Edited:  []Edit{{Old: &edited, New: moved}},
			Deleted: []Link{gone},
		}
		if diff := cmp.Diff(want, got, cmp.FilterPath(func(p cmp.Path) bool { return p.String() == "Since" }, cmp.Ignore())); diff != "" {
			t.Errorf("--json (-want +got):\n%s", diff)
		}
	})

	t.Run("since never touches state", func(t *testing.T) {
		before := readState()

		r := invoke(t, "--since", "90m")
		require.Equal(t, 0, r.code, r.stderr)
		require.Contains(t, r.stdout, "+ go/recent")
		require.Contains(t, r.stdout, "+ go/added")
		require.NotContains(t, r.stdout, "go/gone", "since cannot see deletions")
		require.Equal(t, before, readState())
	})

	t.Run("a bad export leaves state alone", func(t *testing.T) {
		before := readState()
		srv.set()

		r := invoke(t)
		require.Equal(t, 1, r.code)
		require.Contains(t, r.stderr, "error: ")
		require.Contains(t, r.stderr, errEmptyExport.Error())
		require.Empty(t, r.stdout)
		require.Equal(t, before, readState())

		srv.set(recent, moved, old, added)
	})

	t.Run("normal run reports and advances", func(t *testing.T) {
		r := invoke(t)
		require.Equal(t, 0, r.code, r.stderr)
		require.Contains(t, r.stdout, "+ go/added")
		require.Contains(t, r.stdout, "~ go/edited")
		require.Contains(t, r.stdout, "was https://edited\n")
		require.Contains(t, r.stdout, "- go/gone")

		r = invoke(t)
		require.Contains(t, r.stdout, "Nothing moved")
	})
}

func TestExecuteExitCodes(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())

	for _, tt := range []struct {
		name string
		args []string
		want int
	}{
		{"help", []string{"--help"}, 2},
		{"unknown flag", []string{"--frobnicate"}, 2},
		{"bad color", []string{"--color", "sometimes"}, 2},
		{"bad since", []string{"--since", "lately"}, 2},
		{"bad url", []string{"--url", "go"}, 2},
		{"stray argument", []string{"go/foo"}, 2},
		{"version", []string{"--version"}, 0},
		{"unreachable server", []string{"--url", "http://127.0.0.1:1"}, 1},
	} {
		t.Run(tt.name, func(t *testing.T) {
			r := invoke(t, tt.args...)
			require.Equal(t, tt.want, r.code, r.stderr)
		})
	}

	t.Run("interrupt", func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		cancel()

		var stdout, stderr bytes.Buffer
		require.Equal(t, 130, execute(ctx, []string{"--url", "http://127.0.0.1:1"}, &stdout, &stderr, 0))
	})
}

func TestUseColor(t *testing.T) {
	for _, tt := range []struct {
		mode    string
		tty     bool
		noColor string
		term    string
		want    bool
	}{
		{mode: "auto", tty: true, term: "xterm", want: true},
		{mode: "auto", tty: false, term: "xterm", want: false},
		{mode: "auto", tty: true, noColor: "1", term: "xterm", want: false},
		{mode: "auto", tty: true, term: "dumb", want: false},
		{mode: "always", tty: false, noColor: "1", want: true},
		{mode: "never", tty: true, term: "xterm", want: false},
	} {
		t.Setenv("NO_COLOR", tt.noColor)
		t.Setenv("TERM", tt.term)
		require.Equal(t, tt.want, useColor(tt.mode, tt.tty), "%+v", tt)
	}
}
