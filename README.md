# golinkdiff

What moved in go links since you last looked.

Points at a [golink](https://github.com/tailscale/golink) server, diffs its
export against the snapshot from the previous run, and prints what is new,
edited and deleted:

```
+ go/tsnix     kradalby@   2026-11-23  https://github.com/kradalby/tsnix
~ go/oncall    bradfitz@   2026-11-22  https://new.example/oncall
                                       was https://old.example/oncall
- go/oldthing  danderson@  (deleted)   https://gone.example/

3 changes since 2026-11-21 09:12 (1 new, 1 edited, 1 deleted)
```

## How it works

- Fetches `GET <url>/.export` (JSONL, one link per line), following golink's
  redirect to its HTTPS name.
- Diffs it against the snapshot in `$XDG_STATE_HOME/golinkdiff/<host>.json`
  (default `~/.local/state`), one file per golink host. An edit is a changed
  destination or owner.
- Saves the export as the new snapshot via temp file + rename, so it is never
  half-written. A failed fetch leaves it untouched, and an export that is empty
  or cut off mid-line is refused rather than read as "everything was deleted".
- The first run has no snapshot, so it shows links created or edited in the
  last 7 days instead.

## Run

```sh
golinkdiff                    # what moved since the last run; remember now
golinkdiff --no-update        # peek: same, but keep the old snapshot
golinkdiff --since 36h        # by timestamp (also 7d, 2026-11-01); never touches state
golinkdiff --json | jq -r '.New[].Short'
golinkdiff --url https://go.example.ts.net
```

Configuration is via flags or `GOLINKDIFF_`-prefixed environment variables
(`GOLINKDIFF_URL`, …); see `golinkdiff --help`. `--color` is `auto` (only on a
terminal, and off under `NO_COLOR`), `always` or `never`. On a terminal, long
destinations are clipped to its width; piped output keeps them whole.

`--since` works from timestamps alone, so it cannot see deletions or show what an
edit replaced. `--json` prints `{"Since", "New", "Edited", "Deleted"}`, links in
golink's own export shape and edits as `{"Old", "New"}` (no `Old` under
`--since`).

Exit codes: 0 success, 1 failure, 2 usage or `--help`, 130 interrupted.

## Install

From a flake, e.g. dotfiles:

```nix
{
  inputs.golinkdiff = {
    url = "github:kradalby/golinkdiff";
    # go.mod tracks the newest Go, which only unstable carries.
    inputs.nixpkgs.follows = "nixpkgs-unstable";
    inputs.flake-utils.follows = "flake-utils";
  };

  # then
  nixpkgs.overlays = [ golinkdiff.overlays.default ];
  home.packages = [ pkgs.golinkdiff ];
}
```

## Develop

```sh
nix develop                      # go, gopls, golangci-lint, gofumpt, treefmt, prek
nix fmt                          # gofumpt + goimports + nixfmt via treefmt
nix flake check                  # build + race tests + lint + formatting
go test -race ./...
go run ./cmd/vendorhash update   # after changing go.mod
```
