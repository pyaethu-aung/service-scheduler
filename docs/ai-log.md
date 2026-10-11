# AI log

Gate failures, contract-test failures and P0/P1 review findings that the
implementation agent hit and fixed, one row per issue, appended after each
slice. Only issues that actually occurred are recorded.

| Date | Slice | What the agent produced | What was wrong | How it was caught | Fix |
|---|---|---|---|---|---|
| 2026-10-11 | 1: schema and seed | `internal/seed.Run` with a separate error return after every upsert (17 branches) and `cmd/seed` tested only on the happy path | Total coverage fell to 78.7%: none of the seed error branches were exercised, so a failing upsert or a rolled-back transaction was untested | Coverage gate (`make coverage`, 85% minimum) | Rewrote the seed as labelled steps run in one loop, with opening hours built from `hm()` instead of parsed strings, so the dead branches are gone. Added an integration test that seeds the unmigrated `postgres` database and asserts the rollback error. Coverage is now 86.2% |
