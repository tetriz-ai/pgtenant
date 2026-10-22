#!/bin/sh
# Copyright 2026 The pgtenant Authors
# SPDX-License-Identifier: MIT
#
# Runs every example under examples/ with exactly the command in the ```sh
# block of its README.md, from the repo root, and compares standard output with
# the last fenced block of that README. CI's Examples job runs this same
# script. It checks every example and exits non-zero if any of them failed.
# A folder that examples/README.md does not list stops the run before anything
# in examples/ is built or run, because the runner executes each README's
# commands. The examples need the compose Postgres (docker compose up -d). The
# duration at the end of a `go test` result line is rewritten to <time> before
# comparing.
set -eu

cd "$(dirname "$0")/.."

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
status=0

# The index lists every example folder and nothing else.
for dir in examples/*/; do
	basename "$dir"
done | sort > "$tmp/folders"
grep -o '](\([a-z0-9-]*\)/)' examples/README.md | sed 's|^](||; s|/)$||' | sort > "$tmp/index"
if ! diff -u "$tmp/folders" "$tmp/index"; then
	echo "FAIL examples/README.md: the index does not match the folders (- folder, + index); no example ran"
	exit 1
fi

go vet ./examples/...

for dir in examples/*/; do
	name=$(basename "$dir")
	readme="${dir}README.md"
	if [ ! -f "$readme" ]; then
		echo "FAIL $name: no README.md"
		status=1
		continue
	fi
	# The command a reader copies: the first ```sh block, run as written.
	awk '/^```sh$/ { on = 1; next } on && /^```$/ { exit } on' "$readme" > "$tmp/cmd"
	if [ ! -s "$tmp/cmd" ]; then
		echo "FAIL $name: README.md has no \`\`\`sh block with the run command"
		status=1
		continue
	fi
	# The expected output: the contents of the last fenced block.
	awk '/^```/ { if (on) { on = 0; last = cur } else { on = 1; cur = "" } next }
		on { cur = cur $0 "\n" }
		END { printf "%s", last }' "$readme" > "$tmp/want"
	if [ ! -s "$tmp/want" ]; then
		echo "FAIL $name: README.md has no output block after the run command"
		status=1
		continue
	fi
	# Standard output only is compared. Standard error still reaches the log.
	if ! sh -eu "$tmp/cmd" > "$tmp/raw"; then
		echo "FAIL $name: the README's run command failed: $(cat "$tmp/cmd")"
		status=1
		continue
	fi
	awk '{ sub(/\t[0-9]+(\.[0-9]+)?s$/, "\t<time>"); print }' "$tmp/raw" > "$tmp/got"
	if diff -u "$tmp/want" "$tmp/got"; then
		echo "ok   $name"
	else
		echo "FAIL $name: output differs from README.md (- README, + output)"
		status=1
	fi
done

exit "$status"
