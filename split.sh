#!/usr/bin/env bash
set -euo pipefail

# Split the demoparser subtree, preserving its history, into a standalone repo.
prefix="misc/demoparser"
target_dir="${1:-$HOME/workspace/etlegacy-demosummaries}"

source_root="$(git rev-parse --show-toplevel)"
cd "$source_root"

if ! git diff --quiet || ! git diff --cached --quiet; then
	echo "source repository has uncommitted changes; commit or stash them first" >&2
	exit 1
fi

mkdir -p "$(dirname "$target_dir")"

if [ -e "$target_dir" ] && [ ! -d "$target_dir" ]; then
	echo "target path exists and is not a directory: $target_dir" >&2
	exit 1
fi

if [ -d "$target_dir/.git" ]; then
	if ! git -C "$target_dir" diff --quiet || ! git -C "$target_dir" diff --cached --quiet; then
		echo "target repository has uncommitted changes: $target_dir" >&2
		exit 1
	fi
else
	if [ -d "$target_dir" ] && find "$target_dir" -mindepth 1 -maxdepth 1 -print -quit | grep -q .; then
		echo "target directory exists but is not an initialized git repository: $target_dir" >&2
		exit 1
	fi

	git init "$target_dir" >/dev/null
fi

split_commit="$(git subtree split --prefix="$prefix")"

# Fetch the split commit directly from the source repo so the target repo
# retains the subtree history without needing a temporary push.
git -C "$target_dir" fetch --force "$source_root" "$split_commit" >/dev/null
git -C "$target_dir" checkout -B main FETCH_HEAD >/dev/null

printf "split %s at %s into %s\n" "$prefix" "$split_commit" "$target_dir"
