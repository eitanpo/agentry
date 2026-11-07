#!/usr/bin/env bash
# Runs `go test` with the caller's arguments (./... when given none), prints its
# output unchanged, exits with its status, and appends one JSON line per run to
# the record file: start, wall seconds, outcome, packages passed and failed, and
# each package's seconds as go test printed them. A slower or faster suite can
# then be judged against earlier runs rather than remembered.
#
# The record is .test-runs.jsonl at the repository root (gitignored), or the
# path in AGENTRY_TEST_RECORD.
set -u

record=${AGENTRY_TEST_RECORD:-"$(git -C "$(dirname "$0")" rev-parse --show-toplevel)/.test-runs.jsonl"}
[ $# -eq 0 ] && set -- ./...

output=$(mktemp)
trap 'rm -f "$output"' EXIT

start=$(date -u +%Y-%m-%dT%H:%M:%SZ)
began=$(perl -MTime::HiRes=time -e 'printf "%.3f", time')
go test "$@" 2>&1 | tee "$output"
status=${PIPESTATUS[0]}
ended=$(perl -MTime::HiRes=time -e 'printf "%.3f", time')

# go test ends each package with one tab-separated summary line: "ok  " or
# "FAIL", the import path, then "0.92s" or "(cached)". A package that failed to
# build prints its reason inside the path field and no third field. The bare
# "FAIL" closing a failed run and the "?" lines for packages with no tests are
# not packages that ran, so they are skipped.
awk -F'\t' '
  ($1 == "ok  " || $1 == "FAIL") && NF >= 2 {
    verdict = ($1 == "FAIL") ? "fail" : "pass"
    print verdict "\t" $2 "\t" $3
  }' "$output" |
jq -R -s -c \
  --arg start "$start" \
  --argjson wall "$(echo "$ended - $began" | bc)" \
  --argjson status "$status" \
  --arg args "$*" '
  [split("\n")[] | select(length > 0) | split("\t")
    | {package: .[1], verdict: .[0],
       seconds: (.[2] | if test("^[0-9.]+s$") then rtrimstr("s") | tonumber else null end),
       note: (.[2] | if test("^[0-9.]+s$") then null else . end)}] as $packages
  | {start: $start, wallSeconds: $wall, outcome: (if $status == 0 then "pass" else "fail" end),
     exitStatus: $status, args: $args,
     passed: ([$packages[] | select(.verdict == "pass")] | length),
     failed: ([$packages[] | select(.verdict == "fail")] | length),
     packages: $packages}' >> "$record"

exit "$status"
