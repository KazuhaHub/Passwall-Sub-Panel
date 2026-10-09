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

## Consequences, danger colors and focus return

The subsequent S20 package expands the actual Chinese/English messages. Pause
names both policies and allowlists, preserves definitions, and explains that
allowlist membership does not grant more nodes. Resume names both mechanisms.
Policy deletion preserves historical hits under the deleted-policy label;
unused-list deletion has no node impact. Pause, resume and policy deletion use
the current server ETA, rounded up to minutes, with the existing generic
few-minutes fallback when unavailable. Applying a version remains a prerequisite;
offline or failed nodes may retain old rules.

The shared host assigns destructive actions MUI's error color so the theme's
primary-button selector cannot override the danger color. Menu-origin actions
also pass their persistent trigger separately from the pure copy. After the
dialog exits, focus returns to that connected trigger; other callers retain
MUI's normal restoration. Policy/list deletion and header pause/resume are wired
to this path. A disappearing menu item no longer leaves keyboard focus on BODY.

Eleven consequence/interpolation cases, two real-theme color cases and three
menu-removal focus cases failed before their respective repairs. Page tests
verify all four menu entry points pass the correct trigger and canceled
deletions issue no DELETE. The final relevant suite passed 436 tests in 31 files
(including the four existing disabled-button theme checks). TypeScript,
changed-source lint, production build, fixture exclusion and all four production
smoke checks passed. The final typed focus test was checked again after replacing
its pre-API compatibility invocation with a direct call.

[Twenty native captures and measured bounds](access-control-acceptance/README.md#confirmation-consequences-and-focus)
cover these four dialogs across Chinese light/dark desktop/phone and English
light phone. Keyboard cancellation returns to the policy/list/header triggers;
Tab and Shift+Tab stay inside the restore dialog. These are isolated synthetic
UI checks, with no policy/list deletion performed. Complete S20, later group
dialogs, real candidate Node enforcement and owner acceptance remain open.

### Temporarily disabled direct actions

A subsequent native check found that canceling the editor's first-enable
confirmation preserved the draft but focused the dialog container instead of
Save. Its pending render disables Save before MUI records the origin. The host
now captures the active element synchronously in `confirm()`, before that
render, and restores it after exit. Menu callers still provide their persistent
trigger explicitly. Disconnected targets are ignored; calls without a usable
origin retain MUI's default restoration.

Two cancellation/Escape regressions failed before this change. All nine host
tests and the final 438-test relevant suite passed, along with TypeScript,
changed-source lint, production build, fixture exclusion and four production
smoke checks. Native Enter cancellation and Escape cancellation returned to
Save with the template intact and no created policy. The page's direct Resume
action also regained focus after cancellation. Three additional screenshots
are linked in the evidence index; the unchanged confirmation appearance uses
the preceding twenty-image matrix. These checks remain synthetic UI evidence.

### Exemption row-menu focus

The exemption sheet's cancellation menu also supplies its persistent row
button explicitly. Capturing the active menu item automatically cannot restore
it after unmounting. Before repair, native Enter cancellation preserved the
exemption but focused the drawer container. After repair, Enter cancellation
and Escape return to the row button; the confirmation remains an ordinary
action with unchanged server ETA and copy. Two caller-wiring regressions failed
before repair and verify both accepted and declined answers. Declining issues
no DELETE. Five additional language/theme/viewport captures and measured bounds
are linked in the evidence index. The final relevant suite passed 440 tests in
31 files, with TypeScript, changed-source lint, production build, fixture
exclusion and all four production smoke checks passing. No exemption was
actually canceled in the browser.

### Account Access cancellation

The account Access tab also calls `cancelExemptionCopy`. Its user-access
response does not include deployment ETA, so the shared unavailable-ETA phrase
is used without fabricating node counts or minutes. A real-language caller
regression verifies the catalog, ordinary action and declined/no-DELETE path;
native Enter on Cancel restores the 44px Cancel exemption button. Account
drawer language/theme/read-state evidence is linked in the acceptance index.
