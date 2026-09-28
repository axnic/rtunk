#!/usr/bin/env bash
# verify-renovate-e2e.sh -- manual, standalone proof that the Renovate regexManagers snippet
# `rtunk renovate config` prints, together with the "# renovate: ..." annotations `rtunk renovate
# enable` writes, actually work against a REAL Renovate run (not just a plausible-looking regex
# nobody ever executed). This is the literal "Done when" proof ROADMAP.md's v0.12 milestone
# requires: "a Renovate run using the printed configuration correctly proposes an update for at
# least one version-pinned entry in a sample project."
#
# NOT wired into `go test ./...` or CI: it needs network access, a real GitHub token, and
# downloads the real `renovate` npm package via `npx` (a few hundred MB, cached after the first
# run) -- a real external dependency with real latency that would make the default test suite
# flaky and slow for everyone. Run it manually:
#
#   GITHUB_COM_TOKEN="$(gh auth token)" ./scripts/verify-renovate-e2e.sh
#
set -euo pipefail

: "${GITHUB_COM_TOKEN:?set GITHUB_COM_TOKEN (a real GitHub token; e.g. \$(gh auth token) if the gh CLI is already authenticated) before running this script}"

repo_root="$(git -C "$(dirname "$0")" rev-parse --show-toplevel)"

tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

echo "==> building rtunk"
go build -C "$repo_root" -o "$tmp/rtunk" ./cmd/rtunk

scratch="$tmp/scratch"
mkdir -p "$scratch/.rtunk" "$scratch/pluginrepo/linters/fixture"
cd "$scratch"
git init -q
git config user.email "verify-renovate-e2e@example.com"
git config user.name "verify-renovate-e2e"

# Step 2: the real config snippet, straight from rtunk's own CLI -- `rtunk renovate config`'s
# whole output IS already the complete {"regexManagers": [...]} document, not a fragment to wrap.
echo "==> getting the real Renovate config snippet from rtunk"
"$tmp/rtunk" renovate config >renovate.json5

# Step 3: a real annotated fixture, built by rtunk's OWN resolve/annotate code -- not hand-typed
# YAML. This is the same real shape internal/cli/renovate_test.go's writeToolLinterFixture uses
# (a plugins.sources[] local plugin declaring a tools.definitions[] entry with a downloads:
# recipe, bridged through a lint.definitions[] entry), pointed at a real, long-lived public
# GitHub repo (koalaman/shellcheck) pinned at a deliberately old real tag so Renovate's own
# github-releases lookup reliably finds a newer release.
cat >.rtunk/rtunk.yaml <<'EOF'
version: "0.1"
plugins:
  sources:
    - id: local
      local: ../pluginrepo
lint:
  enabled:
    - shellcheck@v0.7.0
EOF

cat >pluginrepo/linters/fixture/plugin.yaml <<'EOF'
downloads:
  - name: shellcheck-download
    version: v0.7.0
    downloads:
      - os: { linux: linux, macos: macos, windows: windows }
        cpu: { x86_64: x86_64, arm_64: arm_64 }
        url: https://github.com/koalaman/shellcheck/releases/download/${version}/shellcheck-${version}.linux.x86_64.tar.xz
tools:
  definitions:
    - name: shellcheck
      download: shellcheck-download
      known_good_version: v0.7.0
lint:
  definitions:
    - name: shellcheck
      files: [ALL]
      tools: [shellcheck]
      description: fixture linter (verify-renovate-e2e.sh)
      commands:
        - name: lint
          run: echo unused
          output: xml
EOF

echo "==> annotating the fixture via rtunk's own CLI"
"$tmp/rtunk" renovate enable --config "$scratch/.rtunk/rtunk.yaml"

if ! grep -q '# renovate: datasource=github-releases depName=koalaman/shellcheck' .rtunk/rtunk.yaml; then
	echo "FAIL: rtunk renovate enable did not write the expected annotation" >&2
	cat .rtunk/rtunk.yaml >&2
	exit 1
fi
echo "    annotation confirmed:"
grep -A1 '# renovate:' .rtunk/rtunk.yaml

# Step 4: run the real Renovate CLI against it.
git add -A
git commit -q -m "scratch fixture"

echo "==> running the real Renovate CLI (npx renovate --dry-run=lookup)"
output="$(RENOVATE_ONBOARDING=false RENOVATE_REQUIRE_CONFIG=ignored LOG_LEVEL=debug \
	npx --yes renovate --platform=local --dry-run=lookup 2>&1)" || true

# Step 5: assert the proof. Checking for the literal substring `"updates":` alone is not enough
# -- it matches an EMPTY array ("updates": []) just as much as a real one, which would let a
# fixture with no actual update available silently pass. "newVersion": only ever appears inside a
# real update entry, never in an empty array, regardless of Renovate's own JSON formatting.
if echo "$output" | grep -q '"depName": "koalaman/shellcheck"' && echo "$output" | grep -q '"newVersion":'; then
	echo "==> PASS: Renovate found a real update for koalaman/shellcheck"
	echo "$output" | grep -A6 '"depName": "koalaman/shellcheck"' | head -20
	exit 0
fi

echo "FAIL: did not find a proposed update for koalaman/shellcheck in Renovate's debug output" >&2
echo "$output" >&2
exit 1
