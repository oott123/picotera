{
  description = "PicoTera — LLM API gateway (packages only)";

  inputs = {
    # Nix >= 2.27 only. The build needs `third_party/go-sse`,
    # `third_party/axonhub/llm` and `third_party/quickjs` (three `replace`
    # directives in go.mod); older Nix snapshots the tree without them.
    self.submodules = true;
    nixpkgs.url = "github:NixOS/nixpkgs/nixos-25.11";
  };

  outputs =
    { self, nixpkgs }:
    let
      lib = nixpkgs.lib;

      systems = [
        "x86_64-linux"
        "aarch64-linux"
        "x86_64-darwin"
        "aarch64-darwin"
      ];

      version = "0-unstable-" + (self.shortRev or self.dirtyShortRev or "unknown");

      # Two source slices, so a dashboard edit does not rebuild Go and a Go edit
      # does not rebuild the dashboard. `tools/` holds no Go files; the rest of
      # `third_party/axonhub` (upstream's own go.mod, docs, frontend) is not
      # reachable through the `replace` in go.mod. A new `replace` must be added
      # here or the build fails on a missing directory.
      goSrc = lib.fileset.toSource {
        root = ./.;
        fileset = lib.fileset.unions [
          ./cmd
          ./pkg
          ./db
          ./go.mod
          ./go.sum
          ./LICENSE
          ./THIRD_PARTY_NOTICES.md
          ./third_party/go-sse
          ./third_party/axonhub/llm
          ./third_party/quickjs
        ];
      };

      webSrc = lib.fileset.toSource {
        root = ./.;
        fileset = lib.fileset.unions [
          ./dashboard
          ./pnpm-lock.yaml
          ./pnpm-workspace.yaml
        ];
      };

      # `storeDir` in `pnpm-workspace.yaml` outranks the `$HOME/.npmrc` that
      # `fetchPnpmDeps` and `pnpm.configHook` write through `pnpm config
      # set store-dir`, so the store would land in the build directory and the
      # fixed-output hash could never match. The repo's file is left untouched;
      # the line is dropped from the copy both pnpm derivations build from. It
      # takes no part in `--frozen-lockfile` validation.
      webPostPatch = "sed -i '/^storeDir:/d' pnpm-workspace.yaml";

      mkPackages =
        system:
        let
          pkgs = import nixpkgs { inherit system; };

          picotera-dashboard = pkgs.stdenv.mkDerivation (finalAttrs: {
            pname = "picotera-dashboard";
            inherit version;
            src = webSrc;

            nativeBuildInputs = [
              pkgs.nodejs_24
              pkgs.pnpm
              pkgs.pnpm.configHook
            ];

            pnpmDeps = pkgs.pnpm.fetchDeps {
              inherit (finalAttrs) pname version src;
              postPatch = webPostPatch;
              fetcherVersion = 3;
              hash = "sha256-e4QCm2GWbC9VAZmofUC6N+wD+7yxgbVjkC5oKcus4o8=";
            };

            postPatch = webPostPatch;

            # `build-only` is a bare `vite build`; `build` would prepend
            # `vue-tsc --build`, which belongs to the dev loop.
            buildPhase = ''
              runHook preBuild
              pnpm --dir dashboard build-only
              runHook postBuild
            '';

            installPhase = ''
              runHook preInstall
              mkdir -p $out
              cp -r dashboard/dist/. $out/
              runHook postInstall
            '';

            meta = {
              description = "PicoTera dashboard static assets";
              homepage = "https://github.com/oott123/picotera";
              license = lib.licenses.bsd3;
              platforms = systems;
            };
          });

          goModule =
            attrs:
            pkgs.buildGo126Module (
              {
                inherit version;
                src = goSrc;
                env.CGO_ENABLED = 0;
                ldflags = [
                  "-s"
                  "-w"
                ];
                doCheck = false;
                vendorHash = "sha256-miSoYdbmw3JPwYJ733xRSawDXReYGTXxBxsFh2fH0Iw=";
                meta = {
                  homepage = "https://github.com/oott123/picotera";
                  license = lib.licenses.bsd3;
                  platforms = systems;
                };
              }
              // attrs
            );

          picotera-core = goModule {
            pname = "picotera-core";
            subPackages = [ "cmd/picotera" ];

            # What the Dockerfile does with `dashboard/dist`: drop the
            # placeholder index.html and the dist/.gitignore, then embed for
            # real through `//go:embed all:dist`.
            preBuild = ''
              rm -rf pkg/server/static/dist
              mkdir -p pkg/server/static/dist
              cp -r ${picotera-dashboard}/. pkg/server/static/dist/
            '';

            postInstall = ''
              mkdir -p $out/share/doc/picotera
              cp LICENSE THIRD_PARTY_NOTICES.md $out/share/doc/picotera/
            '';

            meta.description = "PicoTera API gateway";
            meta.mainProgram = "picotera";
          };

          # No dashboard injection: the plugin does not import pkg/server, so a
          # frontend change never rebuilds it.
          picotera-llmbridge-plugin = goModule {
            pname = "picotera-llmbridge-plugin";
            subPackages = [ "cmd/picotera-llmbridge-plugin" ];

            # Links github.com/looplj/axonhub/llm (LGPL-3.0) — the notice must
            # ship with the binary.
            postInstall = ''
              mkdir -p $out/share/doc/picotera
              cp THIRD_PARTY_NOTICES.md $out/share/doc/picotera/
            '';

            meta.description = "PicoTera llmbridge cross-format converter (Hashicorp go-plugin)";
            meta.mainProgram = "picotera-llmbridge-plugin";
          };

          picotera = pkgs.symlinkJoin {
            name = "picotera-${version}";
            paths = [
              picotera-core
              picotera-llmbridge-plugin
            ];
            nativeBuildInputs = [ pkgs.makeWrapper ];
            # `--set-default`: a deployment can still point the gateway at a
            # different plugin binary. Mirrors the Dockerfile's
            # PICOTERA_LLMBRIDGE_PLUGIN_PATH.
            postBuild = ''
              wrapProgram $out/bin/picotera \
                --set-default PICOTERA_LLMBRIDGE_PLUGIN_PATH ${picotera-llmbridge-plugin}/bin/picotera-llmbridge-plugin
            '';
            meta = {
              description = "PicoTera API gateway with the llmbridge plugin wired up";
              homepage = "https://github.com/oott123/picotera";
              license = lib.licenses.bsd3;
              mainProgram = "picotera";
              platforms = systems;
            };
          };
        in
        {
          inherit
            picotera
            picotera-core
            picotera-dashboard
            picotera-llmbridge-plugin
            ;
          default = picotera;
        };
    in
    {
      packages = lib.genAttrs systems mkPackages;
    };
}
