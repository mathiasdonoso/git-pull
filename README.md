# git-pull

Fast-forward every Git repository in a directory, in parallel, skipping anything that isn't safe to update.

## Install

```bash
go install github.com/mathiasdonoso/git-pull/cmd/gp@latest
```

## Usage

```bash
gp [directory]
```

Scans the immediate subdirectories of `directory` (default: `.`) and, for each one that is a Git repository, runs `git pull --ff-only`. Repositories are processed concurrently, with spawning rate-limited to ~250 requests/minute to stay under host SSH rate limits.

A repository is left untouched when it has local changes, an http/https remote (would prompt for credentials), or no fast-forwardable upstream.

## Output

One line per repository with its result:

| State | Meaning |
| --- | --- |
| `updated` | Fast-forwarded to the upstream. |
| `skipped (dirty)` | Has uncommitted changes. |
| `skipped (http remote)` | `origin` is http/https; skipped to avoid a credential prompt. Use SSH for these. |
| `pull failed` | `git pull --ff-only` failed (e.g. diverged history). |
| `rate limited` | Pull was throttled by the host. |
| `error` | Could not read the repository status. |

## Debug

```bash
DEBUG=1 gp
```
