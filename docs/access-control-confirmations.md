# Access-control confirmation catalog

Stage-1c plain-text confirmations are defined by pure functions in
`web-react/src/views/admin/accessControl/confirmCopy.ts`. Components decide when
to ask and own the mutation; the catalog supplies only the translated text,
interpolation values, action labels and destructive styling.

| Action | Catalog function or custom dialog | Destructive |
| --- | --- | --- |
| First enabled publication | `firstPublishCopy`, gated by `needsFirstPublishConfirm` | No |
| Delete policy | `deletePolicyCopy` | Yes |
| Delete unused list | `deleteListCopy` | Yes |
| List acquired references before deletion | `listInUseCopy` and `ListInUseDialog` with current server references | No; informational |
| Convert observation into blocking | `ConvertToBlockDialog` with explicit risk choice | No |
| Switch list type with existing source input | `switchListKindCopy` | No |
| Shorten retention | `shortenRetentionCopy` | Yes |
| Cancel account exemption | `cancelExemptionCopy` | No |
| Pause execution | `pauseExecutionCopy(t, true)` | Yes |
| Resume execution | `pauseExecutionCopy(t, false)` | No |
| Discard unsaved changes | `discardSettingsCopy` | No |

The later group-onboarding/stage dialogs (enable with no eligible nodes,
switch to enforcement, return to trial and close allowlist) remain outside
this stage's completed catalog. Their absence is not a claim of full S20
acceptance. First-publication tests cover the shared decision for switch,
editor, template and future allowlist entry points; the latter is decision
coverage, not an implemented group-onboarding screen.

The catalog consolidation retains existing translation keys, interpolation,
labels and behavior. It introduces no new confirmation on ordinary saves.
The existing component tests continue to exercise cancellation, mutation
admission, pause/resume, type switching and exemption removal. Five new catalog
cases failed before the missing factories were introduced. Additional checks
cover the retention day value, known/unknown ETA, and first-publication node
counts: unavailable status remains unknown, an available empty fleet is zero,
and offline, unsupported and third-party nodes do not enter the count.

The focused four-file suite passed 131 tests before the final four positive
catalog cases were added; the catalog itself then passed all 16 cases.
The final relevant suite passed 415 tests in 29 files. TypeScript, changed-source
lint, production build, fixture exclusion and four production smoke checks
passed. Remote CI results are recorded by commit SHA in the PR. This refactor adds no native screenshots;
[existing captures](access-control-acceptance/README.md) remain the visual
evidence, and the full S20 screenshot/interaction matrix is still open.
