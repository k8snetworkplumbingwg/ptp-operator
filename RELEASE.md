# Release Process

This document describes how ptp-operator and
[linuxptp-daemon](https://github.com/k8snetworkplumbingwg/linuxptp-daemon) are
released. Both projects are released together with the same version. Each
release consists of a tagged version, published images and release notes,
produced from `main` on a regular cadence.

## Versioning

Releases follow [semantic versioning](https://semver.org/): `vMAJOR.MINOR.PATCH`.

## Cadence

Releases are cut from `main` on a regular cadence agreed by the maintainers.

## Release artifacts

Each release `vX.Y.Z` produces:

| Artifact | Location |
|----------|----------|
| Git tag | `vX.Y.Z` on `main` in both repositories |
| Operator image | `ghcr.io/k8snetworkplumbingwg/ptp-operator:vX.Y.Z` |
| Bundle image | `ghcr.io/k8snetworkplumbingwg/ptp-operator-bundle:vX.Y.Z` |
| Daemon image | `ghcr.io/k8snetworkplumbingwg/linuxptp-daemon:vX.Y.Z` |
| Release notes | GitHub Release for `vX.Y.Z` in both repositories |

## Release checklist

linuxptp-daemon imports ptp-operator's API types through `go.mod`, so
ptp-operator is tagged first.

1. Make sure CI on `main` is green in both repositories.
2. In ptp-operator, create and push the tag:
   ```bash
   git checkout main && git pull
   git tag -s vX.Y.Z -m "vX.Y.Z"
   git push origin vX.Y.Z
   ```
3. Build and push the operator and bundle images:
   ```bash
   make docker-build docker-push bundle bundle-build bundle-push \
     VERSION=X.Y.Z BUNDLE_VERSION=X.Y.Z \
     IMG=ghcr.io/k8snetworkplumbingwg/ptp-operator:vX.Y.Z
   ```
4. In linuxptp-daemon, bump `github.com/k8snetworkplumbingwg/ptp-operator` to
   `vX.Y.Z` (`go get`, `go mod tidy`, `go mod vendor`) and merge the change.
5. In linuxptp-daemon, create and push the `vX.Y.Z` tag, then build and push
   the daemon image.
6. Create the GitHub Release for `vX.Y.Z` with generated release notes in both
   repositories.

## Release notes

Release notes are generated in each repository from merged PRs between the
previous and current tag (e.g. GitHub's
[automatically generated release notes](https://docs.github.com/en/repositories/releasing-projects-on-github/automatically-generated-release-notes)).
PRs should carry a kind label such as `kind/feature`, `kind/bug`,
`kind/api-change` or `kind/deprecation` so notes can be grouped.
