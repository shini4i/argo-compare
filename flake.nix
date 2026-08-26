{
  description = "Development environment for argo-compare with Go tooling and pre-commit hooks";
  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";
    flake-utils.url = "github:numtide/flake-utils";
  };
  outputs = { self, nixpkgs, flake-utils }:
    flake-utils.lib.eachDefaultSystem (system:
      let
        pkgs = import nixpkgs {
          inherit system;
        };

        goToolchain = with pkgs; [
          go_1_26
          gopls
          gotools
          mockgen
          goreleaser
          gosec
          golangci-lint
          govulncheck
          go-junit-report
          go-task
        ];

        preCommitTools = with pkgs; [
          pre-commit
          hadolint
          git
        ];

        # Security scanners, mirroring the CI security workflow so they can be
        # run locally. gosec and govulncheck are part of goToolchain already.
        securityTools = with pkgs; [
          zizmor
          trivy
          trufflehog
        ];

        # Enough for `task lint` and `task phases` in test/e2e. The cluster tools
        # (kind, kubectl, helm) are deliberately absent: they are heavy, and the
        # lab is a per-release gate rather than part of the everyday shell.
        e2eTools = with pkgs; [
          argocd
          bats
          shellcheck
          yq-go
          jq
        ];
      in
      {
        devShells.default = pkgs.mkShell {
          packages = goToolchain ++ preCommitTools ++ securityTools ++ e2eTools;
          shellHook = ''
            export GOPATH="$PWD/.go"
            export GOMODCACHE="$PWD/.gomod"
            mkdir -p "$GOPATH" "$GOMODCACHE"
            export GO111MODULE=on
            # Install the pre-commit git hook so checks run on every commit.
            # Idempotent; hook environments are built lazily on first commit.
            pre-commit install >/dev/null
          '';
        };
      }
    );
}
