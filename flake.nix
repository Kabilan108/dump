{
  description = "Dump files into LLM context windows";

  inputs.nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";

  outputs =
    { self, nixpkgs }:
    let
      systems = [ "x86_64-linux" ];
      forAllSystems = f: nixpkgs.lib.genAttrs systems (system: f system);
    in
    {
      packages = forAllSystems (
        system:
        let
          pkgs = import nixpkgs { inherit system; };
          lib = pkgs.lib;
        in
        {
          default = pkgs.buildGoModule rec {
            pname = "dump";
            version = "0.5.3";
            src = ./.;
            vendorHash = "sha256-A8PH2ITmJE8SD9KVTN76OyXZrmc/oq9JH8Vm0HFZWPw=";

            buildPhase = ''
              runHook preBuild
              make build VERSION=${version}
              runHook postBuild
            '';

            installPhase = ''
              runHook preInstall

              install -Dm755 build/dump $out/bin/dump

              # Shell completions (Cobra-generated)
              install -d $out/share/bash-completion/completions
              $out/bin/dump completion bash > $out/share/bash-completion/completions/dump

              install -d $out/share/zsh/site-functions
              $out/bin/dump completion zsh > $out/share/zsh/site-functions/_dump

              install -d $out/share/fish/vendor_completions.d
              $out/bin/dump completion fish > $out/share/fish/vendor_completions.d/dump.fish

              runHook postInstall
            '';

            # ensure tests run under nix
            doCheck = true;
            checkPhase = "make test";

            # ensure fully static build as your makefile requests
            env.CGO_ENABLED = 0;

            meta = with lib; {
              description = "Dump files into LLM context windows";
              homepage = "https://github.com/kabilan108/dump";
              license = licenses.mit;
              platforms = [ system ];
              mainProgram = "dump";
            };

            # toolchain for build/test (buildgomodule wires go itself, but make/useful tools live here)
            nativeBuildInputs = [ pkgs.makeWrapper ];
          };
        }
      );
      devShells = forAllSystems (
        system:
        let
          pkgs = import nixpkgs { inherit system; };
        in
        {
          default = pkgs.mkShell {
            buildInputs = with pkgs; [
              go
              gopls
              git
              gnumake
            ];
          };
        }
      );
    };
}
