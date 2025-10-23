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
          go_1_24
          gopls
          gotools
        ];

        preCommitTools = with pkgs; [
          pre-commit
          hadolint
          git
        ];
      in
      {
        devShells.default = pkgs.mkShell {
          packages = goToolchain ++ preCommitTools;
          shellHook = ''
            export GOPATH="$PWD/.go"
            export GOMODCACHE="$PWD/.gomod"
            mkdir -p "$GOPATH" "$GOMODCACHE"
            export GO111MODULE=on
          '';
        };
      }
    );
}
