#!/usr/bin/env python3
import argparse
import base64
import json
import os
import secrets
import subprocess
import sys
import urllib.error
import urllib.parse
import urllib.request

MUTATION = """
mutation CreateSignedCommit($input: CreateCommitOnBranchInput!) {
  createCommitOnBranch(input: $input) {
    commit {
      oid
      signature {
        isValid
        wasSignedByGitHub
        reason
      }
    }
  }
}
"""


def fail(message):
    print(f"Error: {message}", file=sys.stderr)
    sys.exit(1)


def git(*args):
    try:
        return subprocess.check_output(["git", *args], stderr=subprocess.PIPE)
    except subprocess.CalledProcessError as exc:
        fail(f"git {' '.join(args)} failed: {exc.stderr.decode().strip()}")


def api(method, path, token, payload=None, allow_404=False):
    base = os.environ.get("GITHUB_API_URL", "https://api.github.com").rstrip("/")
    url = path if path.startswith("http") else base + path
    data = None
    headers = {
        "Accept": "application/vnd.github+json",
        "Authorization": f"Bearer {token}",
        "X-GitHub-Api-Version": "2022-11-28",
    }
    if payload is not None:
        data = json.dumps(payload).encode()
        headers["Content-Type"] = "application/json"
    request = urllib.request.Request(url, data=data, headers=headers, method=method)
    try:
        with urllib.request.urlopen(request, timeout=60) as response:
            body = response.read().decode()
            return json.loads(body) if body else {}
    except urllib.error.HTTPError as exc:
        body = exc.read().decode()
        if allow_404 and exc.code == 404:
            return None
        fail(f"{method} {path} returned {exc.code}: {body}")


def ref_path(branch):
    return urllib.parse.quote(branch, safe="/")


def staged_changes(base):
    raw = git("diff", "--cached", "--raw", "-z", "--no-renames", base, "--")
    fields = raw.decode().split("\0")
    additions = []
    deletions = []

    if fields and fields[-1] == "":
        fields.pop()
    if len(fields) % 2 != 0:
        fail("unexpected git diff --raw output")

    for index in range(0, len(fields), 2):
        metadata = fields[index].split()
        path = fields[index + 1]
        if len(metadata) != 5:
            fail(f"unexpected git diff metadata for {path}")

        old_mode, new_mode, status = metadata[0].lstrip(":"), metadata[1], metadata[4]
        for mode in (old_mode, new_mode):
            if mode not in ("000000", "100644"):
                fail(
                    f"unsupported file mode for {path} ({old_mode} -> {new_mode}); "
                    "only regular non-executable files are supported"
                )

        if status == "D":
            deletions.append({"path": path})
        elif status in ("A", "M"):
            additions.append(
                {
                    "path": path,
                    "contents": base64.b64encode(git("cat-file", "blob", f":{path}")).decode(),
                }
            )
        else:
            fail(f"unsupported git change {status!r} for {path}")

    return additions, deletions


def require_verified(repo, oid, token):
    data = api("GET", f"/repos/{repo}/commits/{oid}", token)
    verified = ((data.get("commit") or {}).get("verification") or {}).get("verified")
    if verified is not True:
        fail(f"GitHub did not report commit {oid} as verified")


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--write-repo", required=True)
    parser.add_argument("--base", required=True)
    parser.add_argument("--branch", required=True)
    parser.add_argument("--headline", required=True)
    parser.add_argument("--body", default="")
    parser.add_argument("--token", default=os.environ.get("SIGNED_COMMIT_TOKEN", ""))
    parser.add_argument("--dry-run", action="store_true")
    args = parser.parse_args()

    base_sha = git("rev-parse", f"{args.base}^{{commit}}").decode().strip()
    additions, deletions = staged_changes(base_sha)
    if not additions and not deletions:
        fail("no staged changes relative to the base commit")

    if args.dry_run:
        print(
            json.dumps(
                {
                    "write_repo": args.write_repo,
                    "base": base_sha,
                    "branch": args.branch,
                    "additions": [item["path"] for item in additions],
                    "deletions": [item["path"] for item in deletions],
                },
                indent=2,
            )
        )
        return

    if not args.token:
        fail("no token provided (set SIGNED_COMMIT_TOKEN or pass --token)")

    temporary = f"tmp-sign-{secrets.token_hex(8)}"
    temporary_ref = f"refs/heads/{temporary}"
    created = False
    try:
        api(
            "POST",
            f"/repos/{args.write_repo}/git/refs",
            args.token,
            {"ref": temporary_ref, "sha": base_sha},
        )
        created = True

        payload = {
            "query": MUTATION,
            "variables": {
                "input": {
                    "branch": {
                        "repositoryNameWithOwner": args.write_repo,
                        "branchName": temporary,
                    },
                    "expectedHeadOid": base_sha,
                    "message": {"headline": args.headline, "body": args.body},
                    "fileChanges": {"additions": additions, "deletions": deletions},
                }
            },
        }
        graphql_url = os.environ.get("GITHUB_GRAPHQL_URL", "")
        response = api("POST", graphql_url or "/graphql", args.token, payload)
        if response.get("errors"):
            fail(f"createCommitOnBranch failed: {response['errors']}")

        commit = ((response.get("data") or {}).get("createCommitOnBranch") or {}).get("commit") or {}
        oid = commit.get("oid")
        signature = commit.get("signature") or {}
        if not oid:
            fail("createCommitOnBranch returned no commit oid")
        if signature.get("isValid") is not True:
            fail(f"GitHub returned an unverified signature: {signature}")
        require_verified(args.write_repo, oid, args.token)

        branch_path = f"/repos/{args.write_repo}/git/refs/heads/{ref_path(args.branch)}"
        if api("PATCH", branch_path, args.token, {"sha": oid, "force": True}, allow_404=True) is None:
            api(
                "POST",
                f"/repos/{args.write_repo}/git/refs",
                args.token,
                {"ref": f"refs/heads/{args.branch}", "sha": oid},
            )

        print(oid)
    finally:
        if created:
            api(
                "DELETE",
                f"/repos/{args.write_repo}/git/refs/heads/{ref_path(temporary)}",
                args.token,
                allow_404=True,
            )


if __name__ == "__main__":
    main()
