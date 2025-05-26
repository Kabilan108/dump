{
  description = "File dumper for codebase analysis and LLM context";
  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";
  };
  outputs = { self, nixpkgs }: let
    system = "x86_64-linux";
    pkgs = import nixpkgs { inherit system; };
  in {
    packages.${system}.default = pkgs.buildGoModule rec {
      pname = "dump";
      version = "latest";
      src = ./.;

      vendorHash = "sha256-A8PH2ITmJE8SD9KVTN76OyXZrmc/oq9JH8Vm0HFZWPw=";

      buildPhase = ''
        runHook preBuild
        make build
        runHook postBuild
      '';

      installPhase = ''
        runHook preInstall
        mkdir -p $out/bin
        cp build/dump $out/bin/
        runHook postInstall
      '';
    };
    devShells.${system}.default = pkgs.mkShell {
      buildInputs = with pkgs; [
        go
        gopls
        nodejs_20
        self.packages.${system}.default
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
