#!/bin/bash
#
# create-standalone-backport-pr.sh
#
# Extracts component diffs from a monorepo commit or commit range, creates a
# GitHub-signed commit on a topic branch in the bot fork with the push-forks
# App, and opens cross-repository Pull Requests with the push-prs App against
# the standalone repositories for legacy ART builds.
#
# Zero write permissions are required on the upstream standalone repositories.
#
# Usage:
#   create-standalone-backport-pr.sh [options]
#
# Options:
#   --branch NAME          Target base release branch (e.g. main, release-4.22, release-4.21) [required]
#   --commit SHA           Single monorepo commit SHA to mirror (default: HEAD)
#   --range FROM..TO       Commit range to mirror (e.g. HEAD~1..HEAD or origin/release-4.22..HEAD)
#   --components LIST      Comma-separated list of components: ptpop,lptpd,cep (default: auto-detect)
#   --fork-owner OWNER     Bot or user fork owner on GitHub (default: $SYNC_BOT_FORK_OWNER or ptp-monorepo-sync-bot)
#   --monorepo-repo REPO   Monorepo GitHub repository (default: openshift/ptp-network-operator)
#   --jira KEYS            Comma-separated Jira keys (e.g. OCPBUGS-123). Default: auto-detect from
#                          monorepo PR title / commit subject / linked monorepo PR. Reused on every standalone PR.
#   --monorepo-pr N        Monorepo PR number (stable topic branch sync/monorepo-<branch>-pr-N;
#                          required for Jira-safe open-before-merge bridging).
#   --pr-title TEXT        Monorepo PR title (preferred source for OCPBUGS + subject).
#   --dry-run              Generate patches and format PR without pushing or opening PRs
#   -h, --help             Show this help message
#
# Environment:
#   PUSH_FORKS_TOKEN       push-forks App installation token (required unless --dry-run)
#   GH_TOKEN               push-prs App installation token for OpenShift targets
#   PR_TOKEN_OPENSHIFT     optional override for OpenShift component PRs
#   PR_TOKEN_CEP           optional override for redhat-cne/cloud-event-proxy PRs
#
# OpenShift / Jira conventions:
#   - Titles follow openshift-cherrypick-robot style (no "[Monorepo Sync]" prefix):
#       main / master:  OCPBUGS-N: <subject>
#       release-X.Y:    [release-X.Y] OCPBUGS-N: <subject>
#   - Provenance uses label monorepo-sync + Monorepo-Commit trailer (not a title prefix).
#   - One Jira key from the monorepo PR is reused on all component PRs for that branch.
#     Backports use the per-release clone key already on the monorepo backport PR/commit.
#   - Does NOT apply cherry-pick-approved / backport-risk-assessed (humans / QE).
#   - CI must open/update standalone PRs on monorepo pull_request (not push-after-merge) so
#     jira-lifecycle-plugin links all PRs before any merge; OCPBUGS → MODIFIED only when all merge.
#
# Examples:
#   ./scripts/create-standalone-backport-pr.sh --branch release-4.22 --commit HEAD
#   ./scripts/create-standalone-backport-pr.sh --branch release-4.21 --range origin/release-4.21..HEAD
#   ./scripts/create-standalone-backport-pr.sh --branch release-4.22 --components lptpd,cep --dry-run
#   ./scripts/create-standalone-backport-pr.sh --branch main --commit HEAD --jira OCPBUGS-12345
#   ./scripts/create-standalone-backport-pr.sh --branch main --range BASE..HEAD --monorepo-pr 42 --pr-title "OCPBUGS-1: fix"
#

set -euo pipefail

export GH_PAGER=cat
export GIT_PAGER=cat

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
SIGNED_COMMIT_HELPER="${SIGNED_COMMIT_HELPER:-${SCRIPT_DIR}/lib/github-signed-commit.py}"
if [ ! -f "$SIGNED_COMMIT_HELPER" ]; then
    SIGNED_COMMIT_HELPER="${SCRIPT_DIR}/github-signed-commit.py"
fi
PUSH_FORKS_TOKEN="${PUSH_FORKS_TOKEN:-}"
DEFAULT_GH_TOKEN="${GH_TOKEN:-}"

TARGET_BRANCH=""
COMMIT_SHA=""
COMMIT_RANGE=""
COMPONENTS_RAW=""
FORK_OWNER="${SYNC_BOT_FORK_OWNER:-ptp-monorepo-sync-bot}"
MONOREPO_REPO="${MONOREPO_REPO:-openshift/ptp-network-operator}"
JIRA_KEYS_RAW=""
MONOREPO_PR=""
PR_TITLE_INPUT=""
DRY_RUN=false
# Provenance label applied to outbound PRs (title stays OpenShift/Jira compliant).
PROVENANCE_LABEL="${SYNC_PROVENANCE_LABEL:-monorepo-sync}"

usage() {
    sed -n '3,45p' "$0" | sed 's/^# \{0,1\}//'
}

die() {
    echo "Error: $*" >&2
    exit 1
}

need_cmd() {
    command -v "$1" >/dev/null 2>&1 || die "Required command not found: $1"
}

while [ $# -gt 0 ]; do
    case "$1" in
        --branch)
            [ $# -ge 2 ] || die "--branch requires an argument"
            TARGET_BRANCH="$2"
            shift 2
            ;;
        --commit)
            [ $# -ge 2 ] || die "--commit requires an argument"
            COMMIT_SHA="$2"
            shift 2
            ;;
        --range)
            [ $# -ge 2 ] || die "--range requires an argument"
            COMMIT_RANGE="$2"
            shift 2
            ;;
        --components)
            [ $# -ge 2 ] || die "--components requires an argument"
            COMPONENTS_RAW="$2"
            shift 2
            ;;
        --fork-owner)
            [ $# -ge 2 ] || die "--fork-owner requires an argument"
            FORK_OWNER="$2"
            shift 2
            ;;
        --monorepo-repo)
            [ $# -ge 2 ] || die "--monorepo-repo requires an argument"
            MONOREPO_REPO="$2"
            shift 2
            ;;
        --jira)
            [ $# -ge 2 ] || die "--jira requires an argument"
            JIRA_KEYS_RAW="$2"
            shift 2
            ;;
        --monorepo-pr)
            [ $# -ge 2 ] || die "--monorepo-pr requires an argument"
            MONOREPO_PR="$2"
            shift 2
            ;;
        --pr-title)
            [ $# -ge 2 ] || die "--pr-title requires an argument"
            PR_TITLE_INPUT="$2"
            shift 2
            ;;
        --dry-run)
            DRY_RUN=true
            shift
            ;;
        -h|--help)
            usage
            exit 0
            ;;
        *)
            die "Unknown option: $1"
            ;;
    esac
done

[ -n "$TARGET_BRANCH" ] || die "--branch <NAME> is required (e.g. --branch release-4.22)"

need_cmd git
if [ "$DRY_RUN" = false ]; then
    need_cmd gh
    need_cmd python3
    [ -f "$SIGNED_COMMIT_HELPER" ] || die "Signed-commit helper not found: $SIGNED_COMMIT_HELPER"
    [ -n "$PUSH_FORKS_TOKEN" ] || die "PUSH_FORKS_TOKEN is required (push-forks App installation token)"
fi

# Extract OCPBUGS-### (and comma-separated lists) from free text, unique, stable order.
extract_jira_keys() {
    local text="$1"
    # Portable unique CSV (macOS BSD paste differs from GNU paste -sd,)
    echo "$text" | grep -oE 'OCPBUGS-[0-9]+' | awk '!seen[$0]++' | awk 'BEGIN{ORS=""} {print (NR>1?",":"") $0}'
    echo
}

# Strip OpenShift / bridge prefixes and leading Jira keys → bare subject.
normalize_commit_subject() {
    local subject="$1"
    # Repeatedly strip known prefixes
    while true; do
        local next="$subject"
        next="$(echo "$next" | sed -E 's/^\[Monorepo Sync\][[:space:]]*//I')"
        next="$(echo "$next" | sed -E 's/^\[release-[0-9]+\.[0-9]+\][[:space:]]*//')"
        next="$(echo "$next" | sed -E 's/^OCPBUGS-[0-9]+([[:space:]]*,[[:space:]]*OCPBUGS-[0-9]+)*:[[:space:]]*//')"
        if [ "$next" = "$subject" ]; then
            break
        fi
        subject="$next"
    done
    echo "$subject" | sed -E 's/^[[:space:]]+|[[:space:]]+$//g'
}

# Build openshift-bot-compliant PR title. Reuses the same Jira key(s) on every component PR.
build_openshift_pr_title() {
    local branch="$1"
    local subject="$2"
    local jira_csv="$3"
    local title=""

    if [ -n "$jira_csv" ]; then
        # Prefer "OCPBUGS-1, OCPBUGS-2: subject" (space after comma matches common OpenShift style)
        local jira_fmt
        jira_fmt="$(echo "$jira_csv" | sed 's/,/, /g')"
        title="${jira_fmt}: ${subject}"
    else
        title="$subject"
    fi

    case "$branch" in
        release-*)
            title="[${branch}] ${title}"
            ;;
    esac
    echo "$title"
}

# Resolve Jira keys: --jira wins, else --pr-title, else commit subject, else linked monorepo PR title.
resolve_jira_keys() {
    local keys=""
    if [ -n "$JIRA_KEYS_RAW" ]; then
        keys="$(extract_jira_keys "$JIRA_KEYS_RAW")"
        [ -n "$keys" ] || die "--jira value contained no OCPBUGS-### keys: ${JIRA_KEYS_RAW}"
        echo "$keys"
        return
    fi

    if [ -n "$PR_TITLE_INPUT" ]; then
        keys="$(extract_jira_keys "$PR_TITLE_INPUT")"
        if [ -n "$keys" ]; then
            echo "$keys"
            return
        fi
    fi

    keys="$(extract_jira_keys "$COMMIT_TITLE")"
    if [ -n "$keys" ]; then
        echo "$keys"
        return
    fi

    # Try linked monorepo PR(s) for this commit (needs network / gh).
    if command -v gh >/dev/null 2>&1; then
        local pr_titles=""
        if [ -n "$MONOREPO_PR" ]; then
            pr_titles="$(gh pr view "$MONOREPO_PR" --repo "$MONOREPO_REPO" --json title -q .title 2>/dev/null || true)"
        fi
        if [ -z "$pr_titles" ]; then
            pr_titles="$(gh api "repos/${MONOREPO_REPO}/commits/${FULL_SHA}/pulls" \
                --jq '.[].title' 2>/dev/null || true)"
        fi
        if [ -n "$pr_titles" ]; then
            keys="$(extract_jira_keys "$pr_titles")"
            if [ -n "$keys" ]; then
                echo "$keys"
                return
            fi
        fi
    fi
    echo ""
}

# Refresh title/body on an existing standalone PR. Prefer REST over `gh pr edit`
# (cross-fork + long bodies are more reliable via pulls API; always log errors).
refresh_standalone_pr() {
    local pr_url="$1"
    local repo="$2"
    local title="$3"
    local body="$4"
    local num err_file payload_file body_file

    num="$(echo "$pr_url" | grep -Eo '/pull/[0-9]+' | grep -Eo '[0-9]+' | tail -n1 || true)"
    if [ -z "$num" ]; then
        echo "Warning: could not parse PR number from ${pr_url}" >&2
        return 1
    fi

    err_file="$(mktemp "${TMPDIR:-/tmp}/gh-edit-err.XXXXXX")"
    payload_file="$(mktemp "${TMPDIR:-/tmp}/gh-edit-payload.XXXXXX")"
    body_file="${payload_file}.body"
    cleanup_refresh_tmp() { rm -f "$err_file" "$payload_file" "$body_file"; }
    if ! command -v jq >/dev/null 2>&1; then
        # Fallback without jq: title-only via raw-field, then body-file via gh pr edit.
        if gh api -X PATCH "repos/${repo}/pulls/${num}" -f title="${title}" 2>"$err_file"; then
            echo "Refreshed title on ${pr_url}"
        else
            echo "Warning: could not refresh title on ${pr_url}:" >&2
            cat "$err_file" >&2 || true
            cleanup_refresh_tmp
            return 1
        fi
        printf '%s' "$body" > "$body_file"
        if gh pr edit "$num" --repo "$repo" --body-file "$body_file" 2>"$err_file"; then
            echo "Refreshed body on ${pr_url}"
            cleanup_refresh_tmp
            return 0
        fi
        echo "Warning: could not refresh body on ${pr_url}:" >&2
        cat "$err_file" >&2 || true
        cleanup_refresh_tmp
        return 1
    fi

    jq -n --arg title "$title" --arg body "$body" '{title: $title, body: $body}' > "$payload_file"
    if gh api -X PATCH "repos/${repo}/pulls/${num}" --input "$payload_file" 2>"$err_file"; then
        echo "Refreshed title/body on ${pr_url}"
        cleanup_refresh_tmp
        return 0
    fi
    echo "Warning: could not refresh title/body on ${pr_url}:" >&2
    cat "$err_file" >&2 || true

    # Last resort: title only (still unblocks Jira retitles).
    if gh api -X PATCH "repos/${repo}/pulls/${num}" -f title="${title}" 2>"$err_file"; then
        echo "Refreshed title only on ${pr_url}"
        cleanup_refresh_tmp
        return 0
    fi
    echo "Warning: title-only refresh also failed on ${pr_url}:" >&2
    cat "$err_file" >&2 || true
    cleanup_refresh_tmp
    return 1
}

# Apply provenance label; create if missing. Never apply restricted backport labels.
apply_provenance_label() {
    local pr_url="$1"
    local repo="$2"
    local label="$3"
    [ -n "$label" ] || return 0

    if ! gh label list --repo "$repo" --search "$label" --json name \
        --jq '.[].name' 2>/dev/null | grep -qx "$label"; then
        echo "Creating label '${label}' on ${repo} (best-effort)..."
        gh label create "$label" --repo "$repo" \
            --description "Outbound PR mirrored from downstream PTP monorepo" \
            --color "0E8A16" 2>/dev/null \
            || echo "Warning: could not create label '${label}' on ${repo} (may need openshift/release allowlist)."
    fi

    if gh pr edit "$pr_url" --add-label "$label" 2>/dev/null; then
        echo "Applied label '${label}' on ${pr_url}"
    else
        echo "Warning: could not add label '${label}' on ${pr_url}. Provenance remains in title body + Monorepo-Commit trailer."
    fi
}

# Determine commit range
if [ -n "$COMMIT_RANGE" ]; then
    RANGE="$COMMIT_RANGE"
    FULL_SHA="$(git rev-parse "${RANGE#*..}")"
elif [ -n "$COMMIT_SHA" ]; then
    FULL_SHA="$(git rev-parse "$COMMIT_SHA")"
    RANGE="${FULL_SHA}~1..${FULL_SHA}"
else
    FULL_SHA="$(git rev-parse HEAD)"
    RANGE="${FULL_SHA}~1..${FULL_SHA}"
fi

SHORT_SHA="$(git rev-parse --short=8 "$FULL_SHA")"
COMMIT_TITLE="$(git log -1 --format='%s' "$FULL_SHA")"
COMMIT_BODY="$(git log -1 --format='%b' "$FULL_SHA")"
AUTHOR_NAME="$(git log -1 --format='%an' "$FULL_SHA")"
AUTHOR_EMAIL="$(git log -1 --format='%ae' "$FULL_SHA")"

JIRA_KEYS="$(resolve_jira_keys)"
# Also scan commit body if still empty
if [ -z "$JIRA_KEYS" ]; then
    JIRA_KEYS="$(extract_jira_keys "$COMMIT_BODY")"
fi
# Prefer subject from monorepo PR title when provided (OpenShift bots validate PR titles).
if [ -n "$PR_TITLE_INPUT" ]; then
    SUBJECT="$(normalize_commit_subject "$PR_TITLE_INPUT")"
else
    SUBJECT="$(normalize_commit_subject "$COMMIT_TITLE")"
fi
[ -n "$SUBJECT" ] || SUBJECT="monorepo sync ${SHORT_SHA}"
PR_TITLE="$(build_openshift_pr_title "$TARGET_BRANCH" "$SUBJECT" "$JIRA_KEYS")"

# Stable topic branch when bridging an open monorepo PR (updates force-push same standalone PRs).
if [ -n "$MONOREPO_PR" ]; then
    TOPIC_BRANCH_BASE="sync/monorepo-${TARGET_BRANCH}-pr-${MONOREPO_PR}"
else
    TOPIC_BRANCH_BASE="sync/monorepo-${TARGET_BRANCH}-${SHORT_SHA}"
fi

echo "=== Outbound Monorepo to Standalone PR Bridge ==="
echo "Monorepo Repo : ${MONOREPO_REPO}"
echo "Base Branch   : ${TARGET_BRANCH}"
echo "Commit Range  : ${RANGE}"
echo "Head SHA      : ${FULL_SHA} (${SHORT_SHA})"
echo "Monorepo PR   : ${MONOREPO_PR:-<none>}"
echo "Commit Title  : ${COMMIT_TITLE}"
echo "PR Title In   : ${PR_TITLE_INPUT:-<none>}"
echo "PR Title Out  : ${PR_TITLE}"
echo "Topic Branch  : ${TOPIC_BRANCH_BASE}"
echo "Jira Keys     : ${JIRA_KEYS:-<none — add OCPBUGS to monorepo commit/PR or pass --jira>}"
echo "Provenance    : label=${PROVENANCE_LABEL} (not a title prefix)"
echo "Author        : ${AUTHOR_NAME} <${AUTHOR_EMAIL}>"
echo "Fork Owner    : ${FORK_OWNER}"
echo "Dry Run       : ${DRY_RUN}"
echo "================================================"
if [ -z "$JIRA_KEYS" ]; then
    echo "Warning: no OCPBUGS key found. Standalone PRs may fail jira/valid-bug until retitled."
fi
if [ -z "$MONOREPO_PR" ]; then
    echo "Warning: no --monorepo-pr. Prefer CI pull_request trigger so standalone PRs open before monorepo merge (Jira MODIFIED gating)."
fi

# Get modified files in the range
CHANGED_FILES="$(git diff --name-only "$RANGE")"
if [ -z "$CHANGED_FILES" ]; then
    echo "No files modified in range ${RANGE}. Nothing to bridge."
    exit 0
fi

# Auto-detect components if not explicitly provided
DETECTED_COMPONENTS=""
if [ -n "$COMPONENTS_RAW" ]; then
    DETECTED_COMPONENTS="$COMPONENTS_RAW"
else
    COMP_ARRAY=()
    # Check linuxptp-daemon
    if echo "$CHANGED_FILES" | grep -q '^pkg/linuxptp-daemon/'; then
        COMP_ARRAY+=("lptpd")
    fi
    # Check cloud-event-proxy
    if echo "$CHANGED_FILES" | grep -q '^pkg/cloud-event-proxy/'; then
        COMP_ARRAY+=("cep")
    fi
    # Check operator root files (monorepo-only paths are not standalone content)
    if echo "$CHANGED_FILES" \
        | grep -v '^pkg/' \
        | grep -v '^\.github/' \
        | grep -v '^scripts/' \
        | grep -q .; then
        COMP_ARRAY+=("ptpop")
    fi
    DETECTED_COMPONENTS="$(IFS=,; echo "${COMP_ARRAY[*]}")"
fi

echo "Target Components: ${DETECTED_COMPONENTS:-none}"
if [ -z "$DETECTED_COMPONENTS" ]; then
    echo "No relevant component files touched in this commit range. Exiting."
    exit 0
fi

# Component configuration mapping function
get_component_info() {
    local code="$1"
    case "$code" in
        ptpop)
            UPSTREAM_REPO="openshift/ptp-operator"
            REPO_NAME="ptp-operator"
            COMPONENT_NAME="PTP Operator"
            SUBPATH=""
            ;;
        lptpd)
            UPSTREAM_REPO="openshift/linuxptp-daemon"
            REPO_NAME="linuxptp-daemon"
            COMPONENT_NAME="LinuxPTP Daemon"
            SUBPATH="pkg/linuxptp-daemon"
            ;;
        cep)
            UPSTREAM_REPO="redhat-cne/cloud-event-proxy"
            REPO_NAME="cloud-event-proxy"
            COMPONENT_NAME="Cloud Event Proxy"
            SUBPATH="pkg/cloud-event-proxy"
            ;;
        *)
            die "Unknown component code: $code"
            ;;
    esac
}

TMP_BASE="$(mktemp -d "${TMPDIR:-/tmp}/ptp-sync-XXXXXX")"
cleanup() {
    rm -rf "$TMP_BASE"
}
trap cleanup EXIT

IFS=',' read -ra COMPS <<< "$DETECTED_COMPONENTS"

for COMP in "${COMPS[@]}"; do
    [ -n "$COMP" ] || continue
    get_component_info "$COMP"
    case "$UPSTREAM_REPO" in
        redhat-cne/*) COMPONENT_GH_TOKEN="${PR_TOKEN_CEP:-${DEFAULT_GH_TOKEN}}" ;;
        *) COMPONENT_GH_TOKEN="${PR_TOKEN_OPENSHIFT:-${DEFAULT_GH_TOKEN}}" ;;
    esac

    echo ""
    echo "--------------------------------------------------"
    echo "Processing Component: ${COMPONENT_NAME} (${COMP})"
    echo "Upstream Repo       : ${UPSTREAM_REPO}"
    echo "Target Fork         : ${FORK_OWNER}/${REPO_NAME}"
    echo "Target Branch       : ${TARGET_BRANCH}"
    echo "--------------------------------------------------"

    PATCH_DIR="${TMP_BASE}/patches_${COMP}"
    mkdir -p "$PATCH_DIR"

    # Extract component-specific patches (-o must precede pathspecs '--')
    if [ "$COMP" = "ptpop" ]; then
        git format-patch -o "$PATCH_DIR" "$RANGE" \
            -- . ":(exclude)pkg" ":(exclude).github" ":(exclude)scripts" >/dev/null || true
    else
        git format-patch -o "$PATCH_DIR" --relative="$SUBPATH" "$RANGE" >/dev/null || true
    fi

    PATCH_COUNT="$(find "$PATCH_DIR" -type f -name '*.patch' | wc -l | tr -d ' ')"
    if [ "$PATCH_COUNT" -eq 0 ]; then
        echo "No patch generated for ${COMPONENT_NAME}. Skipping."
        continue
    fi
    echo "Extracted ${PATCH_COUNT} patch file(s) for ${COMPONENT_NAME}."

    TOPIC_BRANCH="${TOPIC_BRANCH_BASE}"
    # Same OpenShift-compliant title (and Jira key) for every component PR from this monorepo commit.

    MONOREPO_PR_LINK=""
    if [ -n "$MONOREPO_PR" ]; then
        MONOREPO_PR_LINK="https://github.com/${MONOREPO_REPO}/pull/${MONOREPO_PR}"
    fi

    PR_BODY=$(cat <<EOF
## Automated Sync from Downstream Monorepo

Opened **while the monorepo PR is still open** so \`jira-lifecycle-plugin\` links this PR to the same OCPBUGS before any merge. The bug moves to MODIFIED only after the monorepo PR **and** all standalone PRs merge.

Reuses the monorepo Jira key(s) on every standalone component PR for this branch.

- **Jira:** ${JIRA_KEYS:-<none>}
- **Monorepo PR:** ${MONOREPO_PR_LINK:-n/a}
- **Monorepo Commit:** [\`${FULL_SHA}\`](https://github.com/${MONOREPO_REPO}/commit/${FULL_SHA})
- **Monorepo Commit Short SHA:** \`${SHORT_SHA}\`
- **Original Monorepo Repository:** [${MONOREPO_REPO}](https://github.com/${MONOREPO_REPO})
- **Original Author:** ${AUTHOR_NAME} <${AUTHOR_EMAIL}>
- **Target Component:** ${COMPONENT_NAME}
- **Release Branch:** ${TARGET_BRANCH}
- **Source Fork Branch:** \`${FORK_OWNER}:${TOPIC_BRANCH}\`
- **Provenance label:** \`${PROVENANCE_LABEL}\` (do not use a title prefix)

This PR was automatically generated to mirror changes from the downstream PTP monorepo for ART build consumption.

### Backport merge labels (humans / QE — not applied by the bridge)
On \`release-*\` PRs, apply \`cherry-pick-approved\` and \`backport-risk-assessed\` after review as usual. Do **not** use \`/cherry-pick\` on standalone repos; backport in the monorepo, then let this bridge open standalone PRs.
EOF
)

    PR_COMMENT=$(cat <<EOF
### Monorepo Provenance Traceability
- Source Monorepo: \`${MONOREPO_REPO}\`
- Monorepo PR: ${MONOREPO_PR_LINK:-n/a}
- Commit SHA: [\`${FULL_SHA}\`](https://github.com/${MONOREPO_REPO}/commit/${FULL_SHA})
- Monorepo Branch: \`${TARGET_BRANCH}\`
- Jira (shared across component PRs): \`${JIRA_KEYS:-none}\`
- Author: ${AUTHOR_NAME} <${AUTHOR_EMAIL}>
- Label: \`${PROVENANCE_LABEL}\`
EOF
)

    if [ "$DRY_RUN" = true ]; then
        echo "[DRY-RUN] Would clone https://github.com/${UPSTREAM_REPO}.git (branch: ${TARGET_BRANCH})"
        echo "[DRY-RUN] Would create topic branch: ${TOPIC_BRANCH}"
        echo "[DRY-RUN] Would apply ${PATCH_COUNT} patch(es) with git am -3"
        echo "[DRY-RUN] Would create a GitHub-signed commit in https://github.com/${FORK_OWNER}/${REPO_NAME}.git"
        echo "[DRY-RUN] Would update ${TOPIC_BRANCH} in the fork and verify the signature"
        echo "[DRY-RUN] Would open PR against ${UPSTREAM_REPO} (base: ${TARGET_BRANCH}, head: ${FORK_OWNER}:${TOPIC_BRANCH})"
        echo "[DRY-RUN] PR Title: ${PR_TITLE}"
        echo "[DRY-RUN] Would add label: ${PROVENANCE_LABEL}"
        echo "[DRY-RUN] PR Body:"
        echo "${PR_BODY}"
        echo "[DRY-RUN] Provenance PR Comment:"
        echo "${PR_COMMENT}"
        continue
    fi

    # Clone standalone repo
    STANDALONE_DIR="${TMP_BASE}/repo_${COMP}"
    echo "Cloning ${UPSTREAM_REPO} (branch: ${TARGET_BRANCH})..."
    TARGET_BASE="${TARGET_BRANCH}"
    if ! git clone --depth 50 --branch "${TARGET_BRANCH}" "https://github.com/${UPSTREAM_REPO}.git" "$STANDALONE_DIR" 2>/dev/null; then
        if [ "$TARGET_BRANCH" = "main" ] && git clone --depth 50 --branch "master" "https://github.com/${UPSTREAM_REPO}.git" "$STANDALONE_DIR" 2>/dev/null; then
            echo "Note: ${UPSTREAM_REPO} uses 'master' as its default development branch; using 'master' as base."
            TARGET_BASE="master"
        else
            echo "Error: Failed to clone ${UPSTREAM_REPO} at branch ${TARGET_BRANCH}" >&2
            continue
        fi
    fi
    BASE_SHA="$(git -C "$STANDALONE_DIR" rev-parse HEAD)"

    (
        cd "$STANDALONE_DIR"
        git checkout -b "$TOPIC_BRANCH"

        echo "Applying patches with git am -3..."
        for patch in "$PATCH_DIR"/*.patch; do
            [ -f "$patch" ] || continue
            if ! grep -q "Monorepo-Commit:" "$patch"; then
                sed -i.bak "/^---$/i\
Monorepo-Commit: ${FULL_SHA}\
" "$patch" && rm -f "${patch}.bak"
            fi
        done

        if ! git am -3 "$PATCH_DIR"/*.patch; then
            echo "Error: git am failed to apply patches cleanly for ${COMPONENT_NAME}." >&2
            echo "Aborting git am and skipping PR creation for this component." >&2
            git am --abort 2>/dev/null || true
            exit 1
        fi
        git add -A

        FORK_REPO="${FORK_OWNER}/${REPO_NAME}"
        FORK_URL="https://github.com/${FORK_REPO}.git"
        AUTH_HEADER="AUTHORIZATION: basic $(printf 'x-access-token:%s' "${PUSH_FORKS_TOKEN}" | base64 | tr -d '\n')"
        echo "Syncing fork ${FORK_REPO} ${TARGET_BASE} to the standalone base (fork is PR-head only)..."
        if ! git -c http.extraHeader="$AUTH_HEADER" push --force "$FORK_URL" "refs/heads/${TARGET_BASE}:refs/heads/${TARGET_BASE}"; then
            echo "Error: could not update fork ${TARGET_BASE} for ${FORK_REPO}." >&2
            echo "Grant the push-forks App Contents: Read and write and Workflows: Read and write." >&2
            exit 1
        fi

        echo "Creating a GitHub-signed commit on ${FORK_REPO}:${TOPIC_BRANCH}..."
        python3 "$SIGNED_COMMIT_HELPER" \
            --write-repo "$FORK_REPO" \
            --base "$BASE_SHA" \
            --branch "$TOPIC_BRANCH" \
            --headline "$PR_TITLE" \
            --body "$PR_BODY" \
            --token "$PUSH_FORKS_TOKEN"
    )

    export GH_TOKEN="$COMPONENT_GH_TOKEN"

    # Resolve existing cross-fork PR URL for this topic branch.
    # NOTE: `gh pr list --head owner:branch` returns [] for cross-fork PRs; use the REST
    # API head=owner:branch filter (or branch-only list) instead.
    echo "Checking for existing PR on ${UPSTREAM_REPO}..."
    EXISTING_URL="$(gh api "repos/${UPSTREAM_REPO}/pulls?head=${FORK_OWNER}:${TOPIC_BRANCH}&state=open" \
        --jq '.[0].html_url // empty' 2>/dev/null || true)"
    if [ -z "$EXISTING_URL" ]; then
        EXISTING_URL="$(gh api "repos/${UPSTREAM_REPO}/pulls?head=${FORK_OWNER}:${TOPIC_BRANCH}&state=all" \
            --jq '.[0].html_url // empty' 2>/dev/null || true)"
    fi
    if [ -z "$EXISTING_URL" ]; then
        # Fallback: branch-only match (works with gh pr list for cross-fork heads).
        EXISTING_URL="$(gh pr list --repo "${UPSTREAM_REPO}" --head "${TOPIC_BRANCH}" --state all \
            --json url -q '.[0].url // empty' 2>/dev/null || true)"
    fi

    if [ -n "$EXISTING_URL" ]; then
        echo "A Pull Request already exists for this topic branch: ${EXISTING_URL}"
        refresh_standalone_pr "${EXISTING_URL}" "${UPSTREAM_REPO}" "${PR_TITLE}" "${PR_BODY}" \
            || echo "Warning: standalone PR metadata refresh failed for ${EXISTING_URL}" >&2
        apply_provenance_label "${EXISTING_URL}" "${UPSTREAM_REPO}" "${PROVENANCE_LABEL}"
    else
        echo "Creating cross-repository Pull Request on ${UPSTREAM_REPO}..."
        set +e
        CREATE_OUT="$(gh pr create \
            --repo "${UPSTREAM_REPO}" \
            --base "${TARGET_BASE}" \
            --head "${FORK_OWNER}:${TOPIC_BRANCH}" \
            --title "${PR_TITLE}" \
            --body "${PR_BODY}" 2>&1)"
        CREATE_RC=$?
        set -e
        if [ "$CREATE_RC" -eq 0 ]; then
            CREATED_PR_URL="$(echo "$CREATE_OUT" | tail -n1)"
            echo "Pull Request created successfully: ${CREATED_PR_URL}"
            apply_provenance_label "${CREATED_PR_URL}" "${UPSTREAM_REPO}" "${PROVENANCE_LABEL}"
            echo "Posting provenance traceability comment..."
            gh pr comment "${CREATED_PR_URL}" --body "${PR_COMMENT}" || echo "Warning: failed to post comment on ${CREATED_PR_URL}"
        elif echo "$CREATE_OUT" | grep -q 'already exists'; then
            # Race / detection miss: recover URL and edit instead of failing the job.
            EXISTING_URL="$(echo "$CREATE_OUT" | grep -Eo 'https://github.com/[^[:space:]]+/pull/[0-9]+' | head -n1 || true)"
            echo "Create reported existing PR: ${EXISTING_URL:-unknown}"
            if [ -n "$EXISTING_URL" ]; then
                refresh_standalone_pr "${EXISTING_URL}" "${UPSTREAM_REPO}" "${PR_TITLE}" "${PR_BODY}" \
                    || echo "Warning: standalone PR metadata refresh failed for ${EXISTING_URL}" >&2
                apply_provenance_label "${EXISTING_URL}" "${UPSTREAM_REPO}" "${PROVENANCE_LABEL}"
            else
                echo "$CREATE_OUT" >&2
                die "Failed to create PR and could not parse existing PR URL"
            fi
        else
            echo "$CREATE_OUT" >&2
            die "Failed to create PR on ${UPSTREAM_REPO}"
        fi
    fi

done

echo ""
echo "=== Monorepo to Standalone PR Bridge Finished Successfully ==="
