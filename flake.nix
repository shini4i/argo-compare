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
      in
      {
        devShells.default = pkgs.mkShell {
          packages = goToolchain ++ preCommitTools ++ securityTools;
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
