# git-pull
A small CLI tool to safely run git pull across multiple local Git repositories, skipping those with local modifications.

## Installation

```bash
go install github.com/mathiasdonoso/git-pull/cmd/gp@latest
```

## Usage

Run the tool in a directory containing multiple Git repositories, or pass a directory as the first argument.

```bash
gp
```
