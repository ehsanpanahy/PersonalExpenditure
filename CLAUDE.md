# Expense Tracker

Personal expense tracking app. See ARCHITECTURE.md for design,
scope, and key decisions. Read it before making structural changes.

## Commands
- Install: <e.g. npm install>
- Run: <command>
- Test: <command>  (run before declaring any task done)
- Lint/format: <command>
- Build: <command>

## Project structure
<Paste folder layout from ARCHITECTURE.md; one line per top-level
folder explaining its purpose.>

## Conventions
- Write tests for every new module or behavior; tests first when
  implementing a feature from requirements.
- Money: store amounts as integers in minor units (e.g. cents), never
  floats. Keep currency explicit. All rounding goes through one
  shared helper.
- Dates: store in UTC, convert to local time only at the UI layer.
- Keep functions small; no business logic in UI or storage layers.
- Don't add new dependencies without asking me first.
- Don't change the data model or public interfaces without updating
  ARCHITECTURE.md in the same change.

## Workflow
- Work on one feature or fix per session. Propose a plan before
  editing multiple files.
- Make small, focused commits with clear messages.
- Run tests and lint after changes and fix failures before finishing.
- If something is ambiguous, ask instead of guessing.

## Gotchas
<Add lines here as the agent makes repeated mistakes or you
discover quirks. Keep this section current.>

## Out of scope for v1
<Copy from ARCHITECTURE.md.>