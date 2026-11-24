{
  description = "golinkdiff — what moved in go links since you last looked";

  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixpkgs-unstable";
    flake-utils.url = "github:numtide/flake-utils";
    flake-checks.url = "github:kradalby/flake-checks";
    flake-checks.inputs.nixpkgs.follows = "nixpkgs";
    flake-checks.inputs.flake-utils.follows = "flake-utils";
  };

  outputs =
    {
      self,
      nixpkgs,
      flake-utils,
      flake-checks,
    }:
    let
      hashes = builtins.fromJSON (builtins.readFile ./flakehashes.json);
      version = "0.1.0";
      # A dirty tree has no shortRev, and dirtyShortRev changes on every edit,
      # which would relink the binary for a README typo.
      rev = self.shortRev or "dev";
    in
    {
      overlays.default = _final: prev: {
        golinkdiff = self.packages.${prev.stdenv.hostPlatform.system}.default;
      };
    }
    # eachDefaultSystem still lists x86_64-darwin, which nixpkgs 26.11 dropped:
    # evaluating any output for it throws.
    // flake-utils.lib.eachSystem [ "x86_64-linux" "aarch64-linux" "aarch64-darwin" ] (
      system:
      let
        # Bare `pkgs.go` and `pkgs.buildGoModule` still track the previous Go
        # release in nixpkgs-unstable, so the latest attributes are named
        # explicitly. The Go dev tools are rebuilt against it as well:
        # goimports ships wrapped with a `go` on PATH, and if that `go` is older
        # than the go.mod directive, GOTOOLCHAIN=auto tries to fetch a toolchain
        # from inside the network-less treefmt sandbox.
        goLatestOverlay = _final: prev: {
          gotools = prev.gotools.override {
            buildGoModule = prev.buildGoLatestModule;
            go = prev.go_latest;
          };
          gofumpt = prev.gofumpt.override {
            buildGoModule = prev.buildGoLatestModule;
          };
        };
        pkgs = import nixpkgs {
          inherit system;
          overlays = [ goLatestOverlay ];
        };
        fc = flake-checks.lib;
        common = {
          inherit pkgs version;
          root = ./.;
          pname = "golinkdiff";
          vendorHash = hashes.vendorHash;
          goPkg = pkgs.go_latest;
          subPackages = [ "cmd/golinkdiff" ];
          env.CGO_ENABLED = "0";
          ldflags = [ "-X github.com/kradalby/golinkdiff/cli.Version=${version}-${rev}" ];
        };
        # meta.mainProgram changes the derivation, so the build check uses this
        # too: it builds exactly what ships instead of a meta-less twin.
        golinkdiff = (fc.goBuild common).overrideAttrs (_: {
          meta = {
            description = "Show what moved in go links since you last looked";
            homepage = "https://github.com/kradalby/golinkdiff";
            license = pkgs.lib.licenses.bsd3;
            mainProgram = "golinkdiff";
          };
        });
      in
      {
        packages.default = golinkdiff;
        formatter = fc.formatter common;
        devShells.default = pkgs.mkShell {
          packages = [
            pkgs.go_latest
            pkgs.gopls
            pkgs.golangci-lint
            pkgs.gofumpt
            # The treefmt wrapper, configured identically to the formatting check
            # (gofumpt + goimports -local + nixfmt). Run `treefmt` or `nix fmt`.
            (fc.formatter common)
            pkgs.prek
            # Not a flake check: it fetches the live vulnerability database.
            pkgs.govulncheck
            pkgs.gnumake
          ];
        };
        checks = {
          build = golinkdiff;
          gotest = fc.goTest (common // { goRace = true; });
          golangci-lint = fc.goLint common;
          formatting = fc.goFormat common;
        };
      }
    );
}
