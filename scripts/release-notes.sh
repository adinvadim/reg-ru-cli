#!/bin/sh
set -eu

if test "$#" -ne 1 || test -z "$1"; then
	printf 'usage: %s VERSION\n' "$0" >&2
	exit 2
fi

version=$1

awk -v version="$version" '
	BEGIN {
		header = "## " version
	}
	index($0, header) == 1 && (length($0) == length(header) || substr($0, length(header) + 1, 1) == " ") {
		capture = 1
		next
	}
	capture && /^## / {
		exit
	}
	capture {
		print
		if ($0 ~ /[^[:space:]]/) found = 1
	}
	END {
		if (!capture || !found) exit 1
	}
' CHANGELOG.md || {
	printf 'CHANGELOG.md has no non-empty "## %s" release section\n' "$version" >&2
	exit 1
}

