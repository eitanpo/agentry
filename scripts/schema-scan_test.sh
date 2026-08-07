#!/usr/bin/env bash
# Pin the four schema-scan.sh behaviors that fail silently rather than loudly:
# a malformed line must be skipped and counted, a version-less entry must inherit
# its file's build, an element the doc omits must read NEW, and a row whose
# timestamp is absent must keep its remaining columns in place.
#
# Usage: scripts/schema-scan_test.sh      Exit: 0 all pass · 1 a check failed.
set -uo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
out=$("$here/schema-scan.sh" \
	--root "$here/testdata/schema-fixture" \
	--doc "$here/testdata/schema-fixture/doc.md" 2>&1) || {
	echo "FAIL scanner exited non-zero"
	echo "$out"
	exit 1
}

fails=0
check() { # check <description> <awk-condition-over-the-row>
	local what="$1" cond="$2"
	if awk "$cond { found = 1 } END { exit !found }" <<<"$out"; then
		echo "ok   $what"
	else
		echo "FAIL $what"
		fails=$((fails + 1))
	fi
}

# The fixture's fourth line is not JSON. jq reading the file as a stream would abort
# there and drop line five; the count proves it was skipped rather than fatal.
check "malformed line skipped and counted" '$1 == "scan" && $2 == "malformed-lines" && $3 == 1'

# Line five is the only entry after the malformed one. Seeing its subtype at all is
# what proves the file was read past the bad line.
check "entries after a malformed line still scanned" '$1 == "subtype" && $2 == "brand_new"'

# The ai-title line carries no version of its own; it must inherit 2.1.200, the
# highest its file carries, rather than reporting as undated.
check "version-less entry inherits its file build" '$1 == "type" && $2 == "ai-title" && $5 == "2.1.200"'

# Same row has no timestamp either. LAST SEEN must render as a placeholder and the
# STATUS column must still land in field 7 — an empty field that collapsed would
# shift every later column left and put the DOC verdict under STATUS.
check "absent timestamp keeps later columns aligned" '$1 == "type" && $2 == "ai-title" && $6 == "-" && $7 == "current"'

# doc.md names ai-title and omits widget, so the two must sort into opposite verdicts.
check "documented element reads documented" '$1 == "type" && $2 == "ai-title" && $8 == "documented"'
check "undocumented element reads NEW" '$1 == "block" && $2 == "widget" && $8 == "NEW"'

# The user entry appears only under 2.1.100 while the file's newest build is 2.1.200,
# so its status must name the last build that wrote it instead of reading current.
check "element absent from the newest build is dated" '$1 == "type" && $2 == "user" && $7 == "last@2.1.100"'

# A token the doc names but the corpus never produced has to surface somewhere, or a
# removed element stays documented forever.
check "documented-but-unobserved token reported" '/ghost-entry/'

[ "$fails" -eq 0 ] || {
	echo
	echo "$fails check(s) failed; scanner output was:"
	echo "$out"
	exit 1
}
echo "all checks passed"
