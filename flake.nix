{
  description = "File dumper for codebase analysis and LLM context";
  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";
  };
  outputs = { self, nixpkgs }: let
    system = "x86_64-linux";
    pkgs = import nixpkgs { inherit system; };
  in {
    packages.${system}.default = pkgs.stdenv.mkDerivation rec {
      pname = "dump";
      version = "0.2.3";
      src = pkgs.fetchurl {
        url = "https://github.com/Kabilan108/dump/releases/download/v${version}/dump-linux-amd64.tar.gz";
        sha256 = "sha256-Be/ppauxtHGbiH8HpYm07iomTOqsH0mqMp8kxB/GVe8=";
      };
      installPhase = ''
        mkdir -p $out/bin
        cp bin/dump $out/bin/
        chmod +x $out/bin/dump
      '';
    };
    devShells.${system}.default = pkgs.mkShell {
      buildInputs = with pkgs; [
        go
        gopls
        nodejs_20
      ];
      shellHook = ''
        export NPM_CONFIG_PREFIX="$HOME/.npm-global"
        export PATH="$HOME/.npm-global/bin:$PATH"
        if [ ! -f "$HOME/.npm-global/bin/claude" ]; then
          npm install -g @anthropic-ai/claude-code
        fi
      '';
    };
  };
}
