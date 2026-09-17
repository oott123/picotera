{
  description = "PicoTera — LLM API gateway (packages only)";

  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";

    # The three trees go.mod `replace`s — `third_party/go-sse`,
    # `third_party/axonhub/llm`, `third_party/quickjs` — are inputs rather than
    # git submodules, so a checkout that never ran `git submodule update` (a
    # GitHub tarball, say) still builds and `self.submodules = true` is no
    # longer needed. `flake = false` because none of the three is a flake; the
    # revisions live in `flake.lock`, bumped by `nix flake update`, and a
    # content change there invalidates `vendorHash`. `.gitmodules` keeps its
    # copies for the Dockerfile, which builds from a real checkout.
    axonhub = {
      url = "github:33forks/picotera-axonhub/unstable";
      flake = false;
    };
    go-sse = {
      url = "github:33forks/picotera-go-sse";
      flake = false;
    };
    quickjs = {
      url = "github:33forks/picotera-quickjs-go";
      flake = false;
    };
  };

  outputs =
    {
      self,
      nixpkgs,
      axonhub,
      go-sse,
      quickjs,
    }:
    let
      lib = nixpkgs.lib;

      systems = [
        "x86_64-linux"
        "aarch64-linux"
        "x86_64-darwin"
        "aarch64-darwin"
      ];

      version = "0-unstable-" + (self.shortRev or self.dirtyShortRev or "unknown");

      # The repo's own Go sources. The three `third_party/` trees the three
      # `replace` directives name are not part of the checkout any more; they
      # are laid out into this slice in `mkPackages`, from the inputs above.
      # `tools/` holds no Go files. A new `replace` must be added either here
      # (a directory in this repo) or to that assembly (an input), or the build
      # fails on a missing directory.
      goRepoSrc = lib.fileset.toSource {
        root = ./.;
        fileset = lib.fileset.unions [
          ./cmd
          ./pkg
          ./db
          ./go.mod
          ./go.sum
          ./LICENSE
          ./THIRD_PARTY_NOTICES.md
        ];
      };

      # The other source slice: a dashboard edit must not rebuild Go, and a Go
      # edit must not rebuild the dashboard.
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

          # go.mod points the three `replace` directives into `third_party/`,
          # which the checkout no longer carries, so they are pasted in from
          # the inputs. Only `llm/` of the axonhub input is reachable (its own
          # go.mod, docs and frontend are not) — the copy is what slices it.
          goSrc = pkgs.runCommand "picotera-go-src" { } ''
            mkdir -p $out/third_party/axonhub
            cp -r ${goRepoSrc}/. $out/
            cp -r --no-preserve=mode ${go-sse} $out/third_party/go-sse
            cp -r --no-preserve=mode ${axonhub}/llm $out/third_party/axonhub/llm
            cp -r --no-preserve=mode ${quickjs} $out/third_party/quickjs
          '';

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
              fetcherVersion = 4;
              hash = "sha256-7LBJ1l4roSH2+g9KtiiMwRI3GB7f6N1axORvkllsTjw=";
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
