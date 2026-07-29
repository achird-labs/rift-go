#!/usr/bin/env bash
#
# The rift-go-specific edit half of the dependency-bump loop, invoked by the reusable
# .github/workflows/dep-bump.yml. Keeps ALL knowledge of which files pin the engine version in one
# place, so the reusable workflow stays generic.
#
#   scripts/bump.sh --current        print the currently pinned engine version (bare, no leading v)
#   scripts/bump.sh <new-version>    rewrite every pin literal to <new-version> and self-verify
#
# RIFT_VERSION in .github/workflows/ci.yml is the source of truth: it selects the cdylib, the engine
# binary and the conformance corpus the SDK is tested against, and those three move together. The
# docs quote the same version in copy-pasteable commands, so they are bumped with it — a reader who
# pastes a stale `-version` gets a library that does not match the corpus the SDK was verified
# against, which is exactly the drift this loop exists to prevent.
#
# Versions are handled bare internally (X.Y.Z) and written with the leading `v` where the file
# expects one, because release tags carry it and the reusable workflow strips it.

set -euo pipefail

# Resolve everything against the repo root rather than the caller's cwd. The reusable workflow
# happens to invoke this from the root, but a human debugging a bump should not have to know that —
# and a relative path that silently reads the wrong repo's ci.yml is a confusing way to find out.
REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$REPO_ROOT"

CI_FILE=".github/workflows/ci.yml"

# Files quoting the version in prose or copy-pasteable commands. Non-fatal if a file stops
# mentioning it — docs get rewritten — but the pin file itself is mandatory.
DOC_FILES=(
  "README.md"
  "docs/index.md"
  "docs/natives.md"
  "docs/conformance.md"
  "docs/embedded.md"
)

fail() { echo "[bump] $*" >&2; exit 1; }

current_version() {
  [ -f "$CI_FILE" ] || fail "pin file not found: $CI_FILE"
  local v
  v="$(sed -n 's/^[[:space:]]*RIFT_VERSION:[[:space:]]*"\{0,1\}v\{0,1\}\([0-9][^"[:space:]]*\)"\{0,1\}[[:space:]]*$/\1/p' "$CI_FILE" | head -n1)"
  [ -n "$v" ] || fail "could not parse RIFT_VERSION from $CI_FILE"
  printf '%s\n' "$v"
}

if [ "${1:-}" = "--current" ]; then
  current_version
  exit 0
fi

NEW="${1:-}"
[ -n "$NEW" ] || fail "usage: bump.sh --current | bump.sh <new-version>"
NEW="${NEW#v}"
[[ "$NEW" =~ ^[0-9]+\.[0-9]+\.[0-9]+ ]] || fail "not a semver-looking version: $NEW"

OLD="$(current_version)"
if [ "$OLD" = "$NEW" ]; then
  echo "[bump] already at $NEW; nothing to do."
  exit 0
fi

echo "[bump] $OLD -> $NEW"

# The pin. Quoted and v-prefixed, matching how the workflow reads it.
sed -i.bak -E "s|^([[:space:]]*RIFT_VERSION:[[:space:]]*)\"?v?${OLD//./\\.}\"?[[:space:]]*$|\\1\"v${NEW}\"|" "$CI_FILE"
rm -f "$CI_FILE.bak"

# Nothing below rewrites this script, so its own examples must never quote a real version — they
# would rot silently and mislead the next person to read it.

# The drift-catchers. Two spellings appear: v-prefixed in commands and tarball names, and bare in
# the sample rift_build_info output.
#
# The v-prefixed substitution runs first, so by the time the second one runs every v-prefixed
# occurrence has already become the new version — which means the bare pass needs no lookbehind to
# avoid double-prefixing, and therefore still matches at the start of a line.
OLD_RE="${OLD//./\\.}"
for f in "${DOC_FILES[@]}"; do
  [ -f "$f" ] || continue
  sed -i.bak "s/v${OLD_RE}/v${NEW}/g; s/${OLD_RE}/${NEW}/g" "$f"
  rm -f "$f.bak"
done

# --- self-verification -------------------------------------------------------
#
# A silent partial rewrite is the failure mode that matters: the bump PR goes green because CI
# still uses the old pin it failed to update. Verify rather than trust sed.

VERIFIED="$(current_version)"
[ "$VERIFIED" = "$NEW" ] || fail "pin rewrite failed: $CI_FILE still reports $VERIFIED"

STALE=0
while IFS= read -r hit; do
  echo "[bump] stale reference: $hit" >&2
  STALE=1
done < <(grep -rn --fixed-strings "$OLD" "$CI_FILE" "${DOC_FILES[@]}" 2>/dev/null || true)
[ "$STALE" -eq 0 ] || fail "one or more files still reference $OLD"

echo "[bump] ok: every pin now reads $NEW"
