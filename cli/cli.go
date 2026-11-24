// Copyright (c) 2026 Kristoffer Dalby
// SPDX-License-Identifier: BSD-3-Clause

// Package cli implements golinkdiff, which shows what moved in a golink
// server's links since the last time it ran.
package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/peterbourgon/ff/v4"
	"github.com/peterbourgon/ff/v4/ffhelp"
	"golang.org/x/term"
)

// Version is set by the linker.
var Version = "dev"

const (
	firstRunDays = 7
	fetchTimeout = 30 * time.Second
	timeLayout   = "2006-01-02 15:04"
)

var (
	errStatus      = errors.New("unexpected HTTP status")
	errTruncated   = errors.New("export truncated")
	errEmptyExport = errors.New("export has no links")
	errBadLink     = errors.New("not a golink export")

	// Mistakes in how golinkdiff was invoked; returned inside a usageError.
	errStrayArg    = errors.New("unexpected argument")
	errBadURL      = errors.New("want an http(s) URL like http://go")
	errBadSince    = errors.New("want a duration like 36h or 7d, or a date like 2026-11-01")
	errFutureSince = errors.New("must be in the past")
)

// usageError marks a mistake in how golinkdiff was invoked (exit 2), as
// opposed to a failure doing the work (exit 1).
type usageError struct{ error }

func (e usageError) Unwrap() error { return e.error }

// Main executes the CLI and returns its process exit code.
func Main(args []string) int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	// Restore default handling once cancelled, so a second Ctrl-C kills outright.
	context.AfterFunc(ctx, stop)

	var cols int
	if fd := int(os.Stdout.Fd()); term.IsTerminal(fd) { //nolint:gosec // fds fit in an int
		cols, _, _ = term.GetSize(fd)
		cols = max(cols, 1) // still a terminal even if the size is unknown
	}

	return execute(ctx, args, os.Stdout, os.Stderr, cols)
}

// execute runs one invocation. cols is stdout's terminal width, or 0 when
// stdout is not a terminal.
func execute(ctx context.Context, args []string, stdout, stderr io.Writer, cols int) int {
	cmd := newCommand(stdout, cols)

	err := cmd.Parse(args, ff.WithEnvVarPrefix("GOLINKDIFF"))
	switch {
	case errors.Is(err, ff.ErrHelp):
		fmt.Fprintln(stderr, ffhelp.Command(cmd))

		return 2
	case err != nil:
		fmt.Fprintf(stderr, "error: %v\n", err)

		return 2
	}

	err = cmd.Run(ctx)
	switch {
	case err == nil:
		return 0
	case ctx.Err() != nil:
		fmt.Fprintln(stderr, "interrupted")

		return 130
	}

	fmt.Fprintf(stderr, "error: %v\n", err)

	if _, ok := errors.AsType[usageError](err); ok {
		return 2
	}

	return 1
}

type config struct {
	url      *string
	since    *string
	noUpdate *bool
	color    *string
	json     *bool
	version  *bool
}

func newCommand(stdout io.Writer, cols int) *ff.Command {
	fs := ff.NewFlagSet("golinkdiff")
	cfg := &config{
		url:      fs.StringLong("url", "http://go", "golink server to check"),
		since:    fs.StringLong("since", "", "instead of diffing, list links created or edited within a duration (36h, 7d) or since a date (2026-11-01); ignores saved state"),
		noUpdate: fs.BoolLong("no-update", "peek: show changes without advancing the saved snapshot"),
		color:    fs.StringEnumLong("color", "colour: auto (on a terminal, unless NO_COLOR is set), always or never", "auto", "always", "never"),
		json:     fs.BoolLong("json", "print the changes as JSON"),
		version:  fs.BoolLong("version", "print the version and exit"),
	}

	return &ff.Command{
		Name:      "golinkdiff",
		Usage:     "golinkdiff [FLAGS]",
		ShortHelp: "Show what moved in go links since the last run.",
		LongHelp:  "Every flag can also be set as GOLINKDIFF_<FLAG>, e.g. GOLINKDIFF_URL.",
		Flags:     fs,
		Exec: func(ctx context.Context, args []string) error {
			if len(args) > 0 {
				return usageError{fmt.Errorf("%w %q", errStrayArg, args[0])}
			}

			if *cfg.version {
				_, err := fmt.Fprintln(stdout, Version)

				return err
			}

			return run(ctx, cfg, stdout, cols)
		},
	}
}

func run(ctx context.Context, cfg *config, stdout io.Writer, cols int) error {
	// Sub-second precision is noise in the state file and --json.
	now := time.Now().Truncate(time.Second)

	u, err := parseURL(*cfg.url)
	if err != nil {
		return err
	}

	v := view{color: useColor(*cfg.color, cols > 0), width: cols, loc: now.Location()}

	if *cfg.since != "" {
		since, err := parseSince(*cfg.since, now)
		if err != nil {
			return err
		}

		links, err := fetch(ctx, u)
		if err != nil {
			return err
		}

		v.window = "since " + since.In(v.loc).Format(timeLayout)

		return emit(stdout, diffSince(since, links), v, *cfg.json)
	}

	path, err := statePath(strings.ToLower(u.Host))
	if err != nil {
		return err
	}

	// Before the fetch, so a broken state file fails without touching the network.
	prev, err := loadState(path)
	if err != nil {
		return err
	}

	links, err := fetch(ctx, u)
	if err != nil {
		return err
	}

	var ch Changes

	var notes []string

	if prev.Checked.IsZero() {
		ch = diffSince(now.AddDate(0, 0, -firstRunDays), links)
		v.window = fmt.Sprintf("in the last %d days", firstRunDays)
		notes = append(notes, "first run")
	} else {
		ch = diffSnapshot(prev.Checked, prev.Links, links)
		v.window = "since " + prev.Checked.In(v.loc).Format(timeLayout)
	}

	if *cfg.noUpdate {
		notes = append(notes, "state not updated")
	} else {
		err = saveState(path, state{Checked: now.UTC(), Links: links})
		if err != nil {
			return err
		}

		if prev.Checked.IsZero() {
			notes = append(notes, "snapshot saved")
		}
	}

	v.note = strings.Join(notes, ", ")

	return emit(stdout, ch, v, *cfg.json)
}

func emit(w io.Writer, ch Changes, v view, asJSON bool) error {
	if asJSON {
		return renderJSON(w, ch)
	}

	return renderText(w, ch, v)
}

// useColor resolves --color. NO_COLOR and TERM=dumb only veto auto: an explicit
// --color=always is the user overriding their own environment.
func useColor(mode string, tty bool) bool {
	switch mode {
	case "always":
		return true
	case "never":
		return false
	}

	return tty && os.Getenv("NO_COLOR") == "" && os.Getenv("TERM") != "dumb"
}

func parseURL(s string) (*url.URL, error) {
	u, err := url.Parse(s)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, usageError{fmt.Errorf("--url %q: %w", s, errBadURL)}
	}

	return u, nil
}

// parseSince turns --since into the start of the look-back window.
func parseSince(s string, now time.Time) (time.Time, error) {
	since := lookBack(s, now)

	switch {
	case since.IsZero():
		return since, usageError{fmt.Errorf("--since %q: %w", s, errBadSince)}
	case !since.Before(now):
		return since, usageError{fmt.Errorf("--since %q: %w", s, errFutureSince)}
	}

	return since, nil
}

// lookBack accepts days on top of Go durations because they are the unit
// people think in for "what did I miss"; a date means local midnight. It
// returns the zero time for anything else.
func lookBack(s string, now time.Time) time.Time {
	if days, ok := strings.CutSuffix(s, "d"); ok {
		n, err := strconv.Atoi(days)
		if err != nil {
			return time.Time{}
		}

		return now.AddDate(0, 0, -n)
	}

	d, err := time.ParseDuration(s)
	if err == nil {
		return now.Add(-d)
	}

	t, err := time.ParseInLocation(time.DateOnly, s, now.Location())
	if err == nil {
		return t
	}

	return time.Time{}
}

// fetch downloads and parses the export. It refuses anything that could be a
// partial export, since diffing one would report every missing link as deleted.
func fetch(ctx context.Context, u *url.URL) ([]Link, error) {
	export := u.JoinPath(".export").String()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, export, nil)
	if err != nil {
		return nil, err
	}

	req.Header.Set("User-Agent", "golinkdiff/"+Version)

	client := &http.Client{Timeout: fetchTimeout}

	// Do's errors already name the method and URL.
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetching %s: %w %s", export, errStatus, resp.Status)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("fetching %s: %w", export, err)
	}

	links, err := parseExport(body)
	if err != nil {
		return nil, fmt.Errorf("fetching %s: %w", export, err)
	}

	return links, nil
}

func parseExport(body []byte) ([]Link, error) {
	if len(body) == 0 {
		return nil, errEmptyExport
	}

	// golink ends every line with a newline; a body that stops mid-line was
	// cut off even if the connection closed cleanly.
	if body[len(body)-1] != '\n' {
		return nil, errTruncated
	}

	var links []Link

	n := 0
	for line := range bytes.Lines(body) {
		n++

		var l Link

		err := json.Unmarshal(line, &l)
		if err != nil {
			return nil, fmt.Errorf("%w: line %d: %w", errBadLink, n, err)
		}

		if l.Short == "" {
			return nil, fmt.Errorf("%w: line %d has no Short", errBadLink, n)
		}

		links = append(links, l)
	}

	return links, nil
}
