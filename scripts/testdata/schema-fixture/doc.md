# Fixture doc

Stands in for docs/session-format.md when testing scripts/schema-scan.sh: the scanner
derives its "documented" vocabulary from a doc's backticked tokens, so this file names
some of the fixture's elements and deliberately omits others.

Documented: `assistant`, `user`, `ai-title`, `text`, `type`, `version`, `timestamp`,
`message`, `aiTitle`.

Names an element the fixture never produces, so the scan reports it as unobserved:
`ghost-entry`.
