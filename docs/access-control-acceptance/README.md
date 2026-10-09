# Access-control browser evidence

The earlier fixture screenshots below use real page/components and built-in
translations with mock API responses. Their temporary fixtures and development
servers were removed after checking. A persistent DEV adapter is described
below for subsequent reproducible checks. Real backend catalog captures are recorded
separately at the end of this file; neither set establishes Node enforcement or
completion of the entire stage-1c acceptance matrix.

## Reproducible DEV acceptance data

Run the local PSP and Vite, then open `/access-control-fixtures.html` on the
Vite origin. Choose the normal, empty, error, missing-catalog or failed-catalog
scenario and activate it. Policy-editor scenarios also isolate preview failure,
projected quota failure, a concurrent revision, a 30-second save and list/group
read failures. Sign in normally to the local PSP if needed. The tool
sets `psp_dev_fixtures=access` and reloads the normal product route; browser
storage failures are reported without navigation. Disable fixtures from the
same tool. Reloading resets all in-memory fixture mutations.

The DEV-only axios adapter intercepts destination, risk-user, group and server routes.
Server reads use the same twelve panel IDs as coverage; connection probes are
synthetic. Unsupported server writes, credential changes and diagnostics fail
inside the fixture adapter and cannot reach real servers.
Unknown operations in that scope fail with 501 instead of reaching the real
backend. Authentication and unrelated requests still use the local PSP. An
optional `PSP_DEV_PROXY_TARGET` sets Vite's local proxy target; it does not change
the production server. Fixture import is inside `import.meta.env.DEV` and the
production smoke test rejects fixture markers in every emitted script, HTML,
CSS and source map, including nested chunks.

The seed has eight policies, five lists, trial/enforcing group descriptors,
twelve nodes covering all ten current states and the fallback variants, and
300 future hit samples including a deleted policy and a long domain. The hit
samples are not exposed through an invented future API. The adapter models UI
states and limited synthetic interactions, not actual kernel enforcement.

| Capture | Language/theme/viewport | Check |
| --- | --- | --- |
| [Seed policies](dev-seed-policies-zh-CN-light-1440.jpg) | Chinese, light, 1440 × 900 | Normal product route with seeded policies and node warning |
| [Phone policies](dev-seed-policies-zh-CN-light-375.jpg) | Chinese, light, 375 × 812 | Document width equals viewport width |
| [Dark desktop policies](dev-seed-policies-zh-CN-dark-1440.jpg) | Chinese, dark, 1440 × 900 | Same normal route and seed |
| [Dark phone policies](dev-seed-policies-zh-CN-dark-375.jpg) | Chinese, dark, 375 × 812 | Same normal route and seed |
| [English phone policies](dev-seed-policies-en-US-dark-375.jpg) | English, dark, 375 × 812 | Translated controls and no horizontal overflow |
| [Empty policies](dev-seed-empty-en-US-dark-375.jpg) | English, dark, 375 × 812 | Template-driven empty state and zero quotas |
| [Read failure](dev-seed-error-en-US-dark-375.jpg) | English, dark, 375 × 812 | Synthetic server failure |
| [Phone nodes](dev-seed-nodes-zh-CN-light-375.jpg) | Chinese, light, 375 × 812 | Node matrix and KPI filters |
| [Finance entries](dev-seed-finance-zh-CN-light-375.jpg) | Chinese, light, 375 × 812 | Bounded 200-entry sample and full-content counts |
| [Finance report](dev-seed-finance-report-zh-CN-light-375.jpg) | Chinese, light, 375 × 812 | Synthetic 612-entry report and excluded `domain:hsbc` sample |

Dimensions above describe the measured browser viewport. The current screenshot
provider exports 811 image rows for an 812px phone viewport; the layout checks
use `innerWidth`, `innerHeight` and document width, without resizing the images.

These initial captures are not the complete §7.6 matrix. Failed refreshes now
retain independent content availability in the policy overview and editor.
Lists that have never downloaded usable entries still show the inactive warning.
Node, settings and global-exception close buttons are separate from their named
headings and have 44 × 44px touch targets.

| Capture | Language/theme/viewport | Check |
| --- | --- | --- |
| [Cached-list editor](cached-list-active-editor-en-US-dark-375.jpg) | English, dark, 375 × 812 | Usable cached list does not show an inactive warning |
| [Fallback coverage](coverage-fallback-en-US-dark-375.jpg) | English, dark, 375 × 812 | Problem filter includes rejected nodes and their fallback status |
| [Coverage title](coverage-title-en-US-dark-375.jpg) | English, dark, 375 × 812 | Dialog and heading names contain the title alone |
| [Settings title](settings-title-en-US-dark-375.jpg) | English, dark, 375 × 812 | Separate close button and unique labelled heading |
| [Exception title](exception-title-en-US-dark-375.jpg) | English, dark, 375 × 812 | Title-only accessible name, 44px close target and no horizontal overflow |

Five frontend regressions and six real HTTP-handler cases reproduced the
readiness/title issues before repair. The repaired package passed 279 focused
frontend tests, the HTTP-handler and destination-policy service suites,
TypeScript, changed-source lint, production build and four production browser
smoke checks. The final adapter/availability/style-guard run passed 13 tests.
An authenticated request to the restarted isolated real backend confirmed
`available: true` on both cached policy list references. Failure/first-download
branches are covered by handler regressions and DEV fixtures, rather than a
claimed live upstream outage. The complete matrix and owner approval remain
pending.

## Locale formatting and coverage matrix

Access-control measures and timestamps use `Intl` with the selected interface
language. Numeric plural inputs remain numbers; interpolation receives their
formatted strings, and account identifiers keep their raw form. Quota labels
and accessible values, list/type totals, reports and node KPIs use the same
formatter. Date/time inputs retain the browser's input format and timezone.

Three browser-language regressions failed before repair. Four formatting tests
now verify a non-builtin German locale, grouped report counts, numeric plural
selection/raw IDs and a Chinese-to-English date change outside the live region.
The complete access-control focused suite plus style guard passed 211 tests;
TypeScript, changed-view lint, production build, fixture-exclusion guard and four
production browser smoke checks also passed.

| Capture | Language/theme/viewport | Check |
| --- | --- | --- |
| [Light phone coverage](coverage-matrix-zh-CN-light-375.jpg) | Chinese, light, 375 × 812 | Named drawer, five KPI toggles and node status icons |
| [Light desktop coverage](coverage-matrix-zh-CN-light-1440.jpg) | Chinese, light, 1440 × 900 | Right-hand drawer and localized pending timestamp |
| [Dark phone coverage](coverage-matrix-zh-CN-dark-375.jpg) | Chinese, dark, 375 × 812 | Same node seed and bounded phone width |
| [Dark desktop coverage](coverage-matrix-zh-CN-dark-1440.jpg) | Chinese, dark, 1440 × 900 | Same drawer and status structure |
| [Chinese date](coverage-locale-zh-CN-dark-375.jpg) | Chinese, dark, 375 × 812 | Applied filter pressed and Chinese date/time |
| [English date](coverage-locale-en-US-dark-375.jpg) | English, dark, 375 × 812 | Same applied node/time rendered in English |
| [Coverage empty](coverage-matrix-empty-zh-CN-dark-375.jpg) | Chinese, dark, 375 × 812 | Zero KPI counts and explicit empty state |
| [Coverage unavailable](coverage-matrix-error-zh-CN-dark-375.jpg) | Chinese, dark, 375 × 812 | Cold coverage deep link after failed reads shows unknown counts and a 44px read-retry button |

The normal seed includes twelve nodes. Its pending node had passed the real
ten-minute threshold during these captures, so it appears in Problems rather
than Pending; this is expected age-based filtering. Browser interaction also
confirmed `aria-pressed` on the applied filter, Escape closing and focus
restoration to the coverage trigger. This fills the coverage screenshot variants
only; the full per-view/dialog matrix, remaining §7.5 checks, real enforcement
and owner approval are still outstanding.

Coverage reads now distinguish six skeleton rows during first load, a failed
first read with explicit retry, and a stale refresh with the last node rows
retained. Three page/query regressions failed before repair and now pass;
the complete access-control/style focused suite passed 214 tests. Read retries
preserve the cold sheet/filter and do not issue node-application POSTs. Browser
interaction in the error scenario confirmed the loading transition and return
to the same error sheet with a retry button, measured at 44px high. Clear-filter
and node-application retry controls also measured 44px high; normal-scene KPI
targets measured approximately 68px, and the drawer had no horizontal overflow.
Escape again restored focus to the coverage trigger. This does
not close the remaining S14 explanatory/action links or other screen gates.

Fixture data deliberately differs from defaults: regular-expression limit 128,
deployment delay 93 seconds and list refresh interval 17 hours. The policy help
shows 128 and 93; list help shows 17. Only policy and list tabs exist at this
stage. Privacy settings and group/record tabs retain their later-stage scope.

| Capture | Language/theme/viewport | Check |
| --- | --- | --- |
| [Header](header-en-US-dark-375.jpg) | English, dark, 375 × 812 | Test icon, accessible menu, account picker on its own full row, no horizontal overflow |
| [Policy help](help-policies-en-US-dark-375.jpg) | English, dark, 375 × 812 | Paragraphs, matching/bypass limits, actual quota/wait values, 16px side margins and scrollable height |
| [List help](help-lists-en-US-dark-375.jpg) | English, dark, 375 × 812 | Accepted formats, actual refresh interval, broad-entry exclusions and empty-content preservation |
| [Desktop header](header-zh-CN-light-1440.jpg) | Chinese, light, 1440 × 1000 | Desktop actions and policy/list tab help entry |
| [Collapsed conditions](conditions-collapsed-en-US-dark-375.jpg) | English, dark, 375 × 812 | Preserved inline summary and draft, full-width phone editor |
| [Field error](conditions-error-en-US-dark-375.jpg) | English, dark, 375 × 812 | A fixture's forced 400 port error reopens conditions and preserves the draft |
| [Phone editor controls](editor-controls-en-US-dark-375.jpg) | English, dark, 375 × 812 | Close on the left, one Save on the right, full-width action choices and no horizontal overflow |
| [Phone budget](editor-budget-en-US-dark-375.jpg) | English, dark, 375 × 812 | Counts below 4px bars, actual quota names and counts exposed to assistive technology |
| [Desktop editor controls](editor-controls-zh-CN-light-1440.jpg) | Chinese, light, 1440 × 1000 | Measured 720px dialog and network segmented choices beside ports |
| [Allow explanation](editor-allow-hint-en-US-dark-375.jpg) | English, dark, 375 × 812 | Template title and named explanation dialog within 16px screen margins |
| [Pending-list explanation](editor-list-hint-en-US-dark-375.jpg) | English, dark, 375 × 812 | Expandable explanation of partial execution and next-publication inclusion |
| [List choices](editor-list-options-zh-CN-light-1440.jpg) | Chinese, light, 1440 × 1000 | Attention badge for a pending list; disabled empty custom option with its reason |
| [Catalog unavailable](catalog-missing-zh-CN-light-1440.jpg) | Chinese, light, 1440 × 1000 | List-page notice and manual download above existing definitions |
| [Catalog download failure](catalog-failed-en-US-dark-375.jpg) | English, dark, 375 × 812 | Failed download retains the list card, selected problem filter and retry action |
| [Editor catalog failure](catalog-editor-en-US-dark-375.jpg) | English, dark, 375 × 812 | Same manual download notice, preserved draft and title-only heading |

Browser interaction also checked Chinese policy-help interpolation and pause
confirmation/cancellation. Cancellation leaves the existing execution verdict;
confirmation warns that offline or failed nodes can retain the old rules.
These captures are a partial increment, not the complete §7.6 matrix, owner
approval or completion of stage 1c.

The additional-condition fixture also checked Chinese blank-policy initial
collapse, BT/port summary and retained values. The phone fixture used an existing
policy; it forced the same port-field error twice, each time reopening a manually
collapsed section. Dialog and heading names contain only the title, with one
label target. A subsequent editor fixture checked desktop width at 720px,
keyboard selection from TCP to UDP to Any with matching focus, mobile top-bar
Save, and budget counts below their bars. Double-clicking Save issued one mock
PUT; controls were disabled while pending and success closed the dialog. These
checks do not establish live backend writes. A later fixture checks the template
origin title, allow/pending-list details, BT limits and list-choice readiness.
The shared hint previously measured 380px wide with a left edge of -21px on a
375px screen; after repair its measured edges are 16px and 359.33px. Named detail
dialogs close with Escape. Empty custom choices remain disabled and expose the
reason both visibly and on hover. Later group-mode hints and risk-threshold
settings retain their final-plan scope.

The catalog fixture also checked pending-button feedback and a successful
download removing the notice without clearing the selected problem filter.
Failed list-page/editor downloads retained definitions/draft and allowed retry;
the phone had no horizontal overflow. Actual download deduplication and query
invalidation are covered by request-count regressions. These mock downloads do
not establish live upstream availability or successful real list refresh.

## Coverage actions and page deep links

The shared node-policy row now provides fixed explanations, localized quota
names, last-report times, prolonged-deployment labels and sing-box's execution-only
badge. Its menu opens server-name search or exact agent diagnostics; sniffing
failures link by managed Node.ID to the existing inbound editor. Quota actions
open the complete list view, and the footer opens deployment settings with the
interval input focused. Stage 2c recording controls remain absent.

| Capture | Language/theme/viewport | Check |
| --- | --- | --- |
| [Phone coverage actions](coverage-actions-zh-CN-dark-375.jpg) | Chinese, dark, 375 × 812 | Problem filter, confirmed-vs-waiting fallback copy and next actions |
| [English actions](coverage-actions-en-US-dark-375.jpg) | English, dark, 375 × 812 | Same problem filter and translated explanations |
| [Desktop actions](coverage-actions-zh-CN-dark-1440.jpg) | Chinese, dark, 1440 × 900 | Same shared status rows in the right-hand drawer |
| [Server search](server-deep-search-zh-CN-dark-375.jpg) | Chinese, dark, 375 × 812 | Coverage menu reaches the real server page; search text and `q` survive blur |
| [Agent diagnostics](node-issues-deep-agent-zh-CN-dark-375.jpg) | Chinese, dark, 375 × 812 | Real node-issue page shows the agent identity; clearing removes its parameter |
| [Deployment interval](coverage-interval-focus-en-US-dark-375.jpg) | English, dark, 375 × 812 | Coverage footer focuses the policy deployment delay input |

Coverage buttons, menu triggers and links measured at least 44px in both
dimensions at 375px, with no horizontal overflow. Escape from a drawer opened
through its trigger restored focus to that trigger. The real backend has no
managed nodes: following a sniffing fixture link showed the missing/unauthorized
target warning, consumed the inbound parameter and opened no editor. This is
a fail-safe navigation check, not real inbound editing or kernel enforcement.

Eight deep-link tests and six coverage-action/category cases failed before
implementation. Eleven final deep-link tests cover async readiness, exact
managed-vs-upstream IDs, permission/capability gates, failed reads and retry,
open-editor preservation, URL parameter preservation and Back/Forward. Six
Chinese/English status-row cases check receipt-dependent fallback text/rule
counts, quotas, prolonged pending state and sing-box. The final focused suite
passed **342 tests in 30 files**; TypeScript, changed-source lint, production
build/fixture exclusion and four production browser smoke checks passed.
Existing unrelated lint warnings in the legacy server/node views remain.

The shared row also supplies the server-page integration described below. Full per-view
acceptance, C2/C5/C6 closure, true new-kernel Node validation and owner approval
remain pending. The earlier `a0e04318` passed both its
[Test workflow](https://github.com/KazuhaHub/Passwall-Sub-Panel/actions/runs/37676483029)
and [released-node systemd acceptance](https://github.com/KazuhaHub/Passwall-Sub-Panel/actions/runs/37676483279);
that CI result does not cover these later edits.

The coverage-actions commit `a90592ec` passed
[released-node systemd acceptance](https://github.com/KazuhaHub/Passwall-Sub-Panel/actions/runs/37679967903),
but its [Test workflow](https://github.com/KazuhaHub/Passwall-Sub-Panel/actions/runs/37679967889)
failed one frontend deep-link assertion: it checked parameter removal before
the asynchronous router commit. The assertion now waits for that commit while
retaining the failed-read, retry and no-write checks. The focused deep-link
suite passed all eleven cases after this repair.

## Server access details (S15)

Native server rows show a compact access-control status button when the fleet
has saved enabled policies, allowlist groups or a still-published policy.
Empty fleets, legacy servers, operators and failed destination reads omit the
line. The native menu also opens a named detail dialog; its shared block shows
the published receipt, problems and next actions. Unpublished saved changes
receive a separate notice. Recording/hit/group cards retain their later stages.

These captures use synthetic servers from the DEV adapter, including synthetic
connection probes. The earlier server-search capture above used the real empty
backend and does not establish native-server connectivity.

| Capture | Language/theme/viewport | Check |
| --- | --- | --- |
| [Status line](server-access-line-zh-CN-dark-1440.jpg) | Chinese, dark, 1440 × 900 | Compact line below connection status, visible keyboard focus |
| [Chinese dark desktop](server-access-dialog-zh-CN-dark-1440.jpg) | Chinese, dark, 1440 × 900 | Shared problem, impact and inbound action |
| [Chinese dark phone](server-access-dialog-zh-CN-dark-375.jpg) | Chinese, dark, 375 × 812 | Full-screen detail, 44px targets |
| [Chinese light desktop](server-access-dialog-zh-CN-light-1440.jpg) | Chinese, light, 1440 × 900 | 600px detail and published receipt metadata |
| [Chinese light phone](server-access-dialog-zh-CN-light-375.jpg) | Chinese, light, 375 × 812 | Full-screen translated detail |
| [English dark desktop](server-access-dialog-en-US-dark-1440.jpg) | English, dark, 1440 × 900 | Translated problem, impact and actions |
| [English dark phone](server-access-dialog-en-US-dark-375.jpg) | English, dark, 375 × 812 | Wrapped title and domain-rule limitation |
| [English light desktop](server-access-dialog-en-US-light-1440.jpg) | English, light, 1440 × 900 | Same shared block at desktop width |
| [English light phone](server-access-dialog-en-US-light-375.jpg) | English, light, 375 × 812 | Same full-screen detail |
| [Failed read](server-access-read-error-en-US-light-1440.jpg) | English, light, 1440 × 900 | Server list remains usable; detail offers GET retry |

Keyboard Tab exposed a 2px focus outline on the status line. Enter opened the
detail; Escape closed it and restored focus to the line. Phone details had no
horizontal overflow, used the full-screen dialog class and exposed six targets
measuring at least 44px in both dimensions. The failed-read scene also displayed
the loading skeleton during a retry before returning to its failure message.
Request-count tests establish that read retry does not post an application.

Nine server-integration cases failed before implementation. Two malformed-200
cases exposed render failures before API response guards were added. The final
seventeen server-access cases cover fleet expectations, native/legacy/operator
gates, session changes, stale/cold/failed reads, unpublished receipts and single
application admission with shared status invalidation. The DEV adapter also
checks matching server IDs, synthetic probes, blocked writes and isolated
destination failure. Full UI/Node acceptance and owner approval remain pending.

The final complete frontend run passed **2147 tests, with one existing skipped
test, in 181 files** (180 passed, one skipped). TypeScript, changed-source lint,
production build, fixture-exclusion guard and four production browser smoke
checks passed. Lint retains three existing warnings in the legacy ServersView.
This run includes the deep-link CI assertion repair and all seventeen S15 cases.

## Exemption management (S5)

The exemption drawer and add/edit dialogs now have title-only accessible names
and separate 44px close buttons. Drawer actions, row menus, expiry choices,
account-picker indicators and dialog footer actions meet the 44px minimum.
The shared discard confirmation also uses 44px actions, including destructive
confirmation. These captures use synthetic destination data; account lookup
uses the isolated local backend. No real exemption was granted or changed.

| Language/theme/viewport | Drawer | New draft | Edit |
| --- | --- | --- | --- |
| Chinese, dark, 1440 × 900 | [Drawer](exemptions-zh-CN-dark-1440.jpg) | [Add](exemption-add-zh-CN-dark-1440.jpg) | [Edit](exemption-edit-zh-CN-dark-1440.jpg) |
| Chinese, dark, 375 × 812 | [Drawer](exemptions-zh-CN-dark-375.jpg) | [Add](exemption-add-zh-CN-dark-375.jpg) | [Edit](exemption-edit-zh-CN-dark-375.jpg) |
| Chinese, light, 1440 × 900 | [Drawer](exemptions-zh-CN-light-1440.jpg) | [Add](exemption-add-zh-CN-light-1440.jpg) | [Edit](exemption-edit-zh-CN-light-1440.jpg) |
| Chinese, light, 375 × 812 | [Drawer](exemptions-zh-CN-light-375.jpg) | [Add](exemption-add-zh-CN-light-375.jpg) | [Edit](exemption-edit-zh-CN-light-375.jpg) |
| English, dark, 375 × 812 | [Drawer](exemptions-en-US-dark-375.jpg) | [Add](exemption-add-en-US-dark-375.jpg) | [Edit](exemption-edit-en-US-dark-375.jpg) |

| English dark phone state | Check |
| --- | --- |
| [Discard confirmation](exemption-discard-en-US-dark-375.jpg) | Cancel preserves the reason draft; discard closes the editor |
| [Duplicate account](exemption-add-conflict-en-US-dark-375.jpg) | Synthetic POST conflict retains the account/reason and disables resubmission |
| [Expired edit](exemption-edit-invalid-en-US-dark-375.jpg) | Past stored expiry has an invalid field and disabled Save |
| [Empty drawer](exemptions-empty-en-US-dark-375.jpg) | Explicit empty copy and Add action |
| [Failed read](exemptions-read-error-en-US-dark-375.jpg) | Explicit failure and 44px GET-retry action |

Phone captures have no horizontal overflow. Escape from a clean new draft
returns focus to Add; Escape from a clean edit returns focus to its row menu.
Canceling the actual mounted discard dialog preserves the dirty reason; a
subsequent discard returns focus to the row menu. Relative expiry now uses at
least the successful query-read time immediately, so a newly read 24-hour
exemption does not briefly display 25 hours. The minute timer remains outside
live announcements. Automated read-retry checks prohibit mutation requests.

Ten regressions failed before their repairs: four exemption accessibility
cases, two shared confirmation cases, one delayed-read expiry case and three
DEV adapter contract cases. The adapter now refuses duplicate creates and
missing edits/deletes, validates fields, preserves creation attribution on edit
and leaves generation unchanged for no-op edits or rejected mutations. Those
synthetic contracts are checked against the existing service/storage/HTTP
behavior; this browser work does not establish real backend writes or Node
enforcement.

The complete frontend run passed **2157 tests, with one existing skip, across
182 files** (181 passed, one skipped). TypeScript, changed-source lint,
production build, fixture-exclusion guard and four production browser smoke
checks passed. The preceding `50b8061f` passed
[released-node systemd acceptance](https://github.com/KazuhaHub/Passwall-Sub-Panel/actions/runs/37683145894),
but its [Test workflow](https://github.com/KazuhaHub/Passwall-Sub-Panel/actions/runs/37683145881)
was canceled while installing pinned Playwright Chromium; frontend unit tests
and production browser smoke were skipped in that run. It is not counted as
a successful full CI result. The new candidate's remote CI, remaining per-view
acceptance, C2/C5/C6, real Node validation and owner approval remain pending.

## List entries drawer (S6)

The entries surface now uses the default page-drawer layer (1200), with a
560px desktop width and full phone width, instead of a 640px modal dialog.
Its real list editor remains at layer 1300. Canceling that editor returns focus
to Edit; opening a list with Enter and closing with Escape returns focus to the
list-name button when the layout is unchanged. Type filters expose
`aria-pressed` and toggle without refreshing content. Close, filters, entry
tests, edit/refresh and failed/stale-read retry actions have 44px minimums.

| Capture | Language/theme/viewport | Check |
| --- | --- | --- |
| [Chinese dark desktop](list-entries-zh-CN-dark-1440.jpg) | Chinese, dark, 1440 × 900 | 560px drawer and complete-count report |
| [Chinese dark phone](list-entries-zh-CN-dark-375.jpg) | Chinese, dark, 375 × 812 | Wrapped type filters and report |
| [Chinese light desktop](list-entries-zh-CN-light-1440.jpg) | Chinese, light, 1440 × 900 | Same drawer and report |
| [Chinese light phone](list-entries-zh-CN-light-375.jpg) | Chinese, light, 375 × 812 | Full phone width and readable report |
| [English phone](list-entries-en-US-dark-375.jpg) | English, dark, 375 × 812 | Full-content totals, bounded samples and entry-test actions |
| [Empty entries](list-entries-empty-zh-CN-light-375.jpg) | Chinese, light, 375 × 812 | Never-downloaded list: zero entries and no parse report |
| [Failed read](list-entries-read-error-zh-CN-light-375.jpg) | Chinese, light, 375 × 812 | Explicit read failure and 44px retry |

These destination reads use the DEV adapter. Chinese report captures filter
the accepted sample with `hsbc` to show the excluded broad entry in the report;
the search stays out of the URL. Phone views have no horizontal overflow.
Five cases failed before repair, including the real drawer-to-editor layering
integration. The repaired page tests passed 86 cases; the wider relevant UI,
adapter, shared confirmation, risk drawer, style and contrast checks passed
343 cases in 25 files. TypeScript, changed-source lint, production build,
fixture exclusion and four production browser smoke checks passed. This fills
the entries-drawer variants, not the entire lists tab/editor acceptance.

## Server reinstallation fixture contract

Commit `01595a2f` passed
[released-node systemd acceptance](https://github.com/KazuhaHub/Passwall-Sub-Panel/actions/runs/37851013928).
Its [Test workflow](https://github.com/KazuhaHub/Passwall-Sub-Panel/actions/runs/37851013910)
passed the frontend unit/build/smoke steps and backend/database checks, but the
Linux Chromium reinstallation gate failed: the fixture did not recognize the
native server row's GET `/api/admin/dest/status` and `/api/admin/dest/policies`.

The fixture now answers only those exact reads with empty, unpublished fleet
state. Destination writes still fail, and the Linux browser gate asserts both
read requests and absence of destination mutations. Local HTTP verification
changed both reads from 500 to 200 and confirmed a destination publish POST
still returns 500. The real built-SPA
[server-page capture](server-reinstall-empty-access-fixture-zh-CN.jpg)
(Chinese, default viewport 1531 × 840) shows the three synthetic server rows
without an access-status line. Syntax validation passed. This Windows preview
is not itself a successful Linux reinstallation-gate run. Commit `8dfb38d7`
subsequently passed both the actual Linux Chromium gate in
[Test CI](https://github.com/KazuhaHub/Passwall-Sub-Panel/actions/runs/37852527819)
and [released-node systemd acceptance](https://github.com/KazuhaHub/Passwall-Sub-Panel/actions/runs/37852527807).
The third-party live adapter jobs were skipped; these runs do not establish
new-kernel destination enforcement.

## Destination-test drawer (S13)

Each test now carries its own optional native-node execution snapshot. The
drawer uses that result for the S14 explanations, fallback confirmation and
rule counts, without replacing it with a later fleet read. A matching,
nonempty candidate digest and applied receipt are required to expose a count;
unknown, offline, exhausted or unconfirmed execution retains a null count.
Confirmed empty/paused execution reports zero and uses the stop receipt's
timestamp. Unsupported node versions omit historical execution metadata.
Older responses without the optional snapshot retain the state explanation.

Close, TCP/UDP, account indicators, submit, explicit retry, publication,
node expansion and navigation actions have 44px minimum targets. The native
account and node menus initially measured only 36px on desktop, despite the
node-option jsdom test passing its mobile CSS default. After repair, both
actual desktop menus measured 44px. Account-option sizing is opt-in for this
drawer, and node options can wrap long names. Desktop drawers measure 560px;
phones use the full 375px width, with equal drawer visible/content widths.
Failure text stays localized and exposes an explicit retry.

| Capture | Language/theme/viewport | Check |
| --- | --- | --- |
| [Desktop result](test-sheet-result-zh-CN-light-1440.jpg) | Chinese, light, 1440 × 900 | Published-policy result and 560px drawer |
| [Dark desktop result](test-sheet-result-zh-CN-dark-1440.jpg) | Chinese, dark, 1440 × 900 | Translated result and trace |
| [Phone result](test-sheet-result-zh-CN-light-375.jpg) | Chinese, light, 375 × 812 | Enter submission, full width and wrapped text |
| [Dark phone result](test-sheet-result-zh-CN-dark-375.jpg) | Chinese, dark, 375 × 812 | Result with no horizontal overflow |
| [English URL result](test-sheet-url-en-US-dark-375.jpg) | English, dark, 375 × 812 | Host-only normalization notice; target stays out of URL |
| [Initial desktop](test-sheet-initial-zh-CN-light-1440.jpg) | Chinese, light, 1440 × 900 | Required destination, default port and optional selectors |
| [Initial phone](test-sheet-initial-zh-CN-light-375.jpg) | Chinese, light, 375 × 812 | Initial instruction and full-width submit |
| [Invalid port](test-sheet-invalid-port-zh-CN-light-375.jpg) | Chinese, light, 375 × 812 | 65536 is rejected and the previous result is cleared |
| [Fallback waiting](test-sheet-fallback-waiting-zh-CN-light-375.jpg) | Chinese, light, 375 × 812 | No borrowed rule count while confirmation is pending |
| [Confirmed fallback and stopping](test-sheet-fallback-confirmed-zh-CN-light-375.jpg) | Chinese, light, 375 × 812 | Confirmed count, new-member limitation and pending stop remain distinct |
| [Account options](test-sheet-account-options-zh-CN-dark-1440.jpg) | Chinese, dark, 1440 × 900 | Real local account lookup; option and popup control measure 44px |
| [Node options](test-sheet-node-options-zh-CN-dark-1440.jpg) | Chinese, dark, 1440 × 900 | All thirteen native menu choices measure 44px |
| [Selected fallback](test-sheet-selected-fallback-zh-CN-dark-1440.jpg) | Chinese, dark, 1440 × 900 | Selected-node warning and receipt explanation |
| [English selected fallback](test-sheet-selected-fallback-en-US-dark-375.jpg) | English, dark, 375 × 812 | Long fallback/member text wraps within drawer |
| [Open matched policy](test-sheet-open-policy-en-US-dark-375.jpg) | English, dark, 375 × 812 | Test drawer closes and the existing policy editor opens without saving |
| [Empty fleet](test-sheet-empty-en-US-dark-375.jpg) | English, dark, 375 × 812 | Logical result contains no fabricated node receipts |
| [Test failure](test-sheet-error-en-US-dark-375.jpg) | English, dark, 375 × 812 | Synthetic failure, unavailable choices and explicit 44px retry |

These seventeen captures use the DEV adapter for destination data and were
visually checked. The adapter models limited matching and UI receipts; its
trace and counts do not establish Protocol matching or live Node enforcement.
Account search uses the isolated backend. Enter submitted the test, URL input
became `example.test` while the address stayed `?sheet=test`, and the matched
policy action opened its actual editor. No policy or exception was saved.
Language, automatic theme, normal fixture scenario and viewport were restored.

Nine initial backend snapshot cases, two receipt-edge cases, six drawer cases
and the DEV receipt contract failed before repair. The expanded backend table
also checks empty digests and missing timestamps. The relevant frontend suite
passed 383 cases in 28 files, followed by 19 targeted cases after the final
menu/error presentation changes. Destination-policy and HTTP-handler suites,
TypeScript, changed-source lint, production build, fixture exclusion and four
production browser smoke checks passed. Local Go was 1.26.8 with automatic
toolchain download disabled; remote CI uses the project's configured toolchain.
S13 commit `5a680eff92b4a724e547d649cdbab308349861eb` passed
[complete Test CI](https://github.com/KazuhaHub/Passwall-Sub-Panel/actions/runs/37858669757)
and [released-node systemd acceptance](https://github.com/KazuhaHub/Passwall-Sub-Panel/actions/runs/37858669634).
Third-party live adapter jobs were skipped. Full C2/C5/C6, other view matrices
and new-kernel Node acceptance remain open.

## Policy editor (S3)

Close, help, autocomplete indicators, list/group tag deletion, labels and menu
options now have 44px minimum targets. Actual desktop list/group/scope options
measured at least 44px, and tag deletion targets measured 44px. The editor
measures 720px on desktop and fills the phone viewport; the 375px dialog's
visible/content widths agree. The phone keeps Save in its header and a readable
summary in its fixed footer. English help text fits the phone popover.

The scope selector explicitly disables during a save: disabling its enclosing
fieldset alone did not disable the div-based combobox. The slow-save scene
confirmed disabled name/list/group/scope controls, close and cancel, a spinner
and normal closure after settlement. Tests cancel that scene before mutation
and verify that no timers remain and no request falls through to live transport.

Selected allowlist groups expose the final-plan ordering explanation. A failed
list catalog read preserves names, IDs and availability from the policy receipt;
it no longer falsely reports an available list as inactive. Explicit conflict
reload also refreshes that metadata. Pending downloaded lists still expose the
inactive-list explanation. Group-owned lists are absent from the policy picker.

Failed or disabled previews show unavailable values as `—`, with static bars
and no borrowed counts or loading animation. Valid pending previews retain
`…`. Preview failure allows a valid write and explicit retry; over-limit
previews retain editable fields, show the full rejection explanation and disable
Save. Concurrent writes preserve the local draft until Load latest is selected.
The safe discard action reads Continue editing and preserves the draft.

All 29 captures below were visually inspected. They use the synthetic DEV
transport and actual product components; they establish UI behavior, not real
kernel enforcement, upstream failures or complete later-stage acceptance.

| Capture | Check |
| --- | --- |
| [Chinese light desktop](policy-editor-zh-CN-light-1440.jpg) | 720px editor, selected list/group tags and allowlist summary |
| [Chinese dark desktop](policy-editor-zh-CN-dark-1440.jpg) | Dark-theme text and controls |
| [Chinese light phone](policy-editor-zh-CN-light-375.jpg) | Header Save, full width and fixed summary |
| [Chinese dark phone](policy-editor-zh-CN-dark-375.jpg) | Dark phone layout |
| [English phone](policy-editor-en-US-light-375.jpg) | Long English labels and summary fit |
| [Chinese light phone budgets](policy-editor-quota-zh-CN-light-375.jpg) | Numbers below bars and fixed footer |
| [Chinese dark phone budgets](policy-editor-quota-zh-CN-dark-375.jpg) | Dark quota presentation |
| [English phone budgets](policy-editor-quota-en-US-light-375.jpg) | Complete budget labels |
| [Chinese group explanation](policy-editor-group-hint-zh-CN-light-1440.jpg) | Block-before/observe-after ordering |
| [English group explanation](policy-editor-group-hint-en-US-light-375.jpg) | Full explanation fits the phone |
| [Keyboard action change](policy-editor-keyboard-allow-en-US-light-375.jpg) | ArrowRight selects Allow, moves to step one and shows restart guidance |
| [Discard confirmation](policy-editor-discard-en-US-light-375.jpg) | Continue editing retains the draft |
| [Unavailable preview](policy-editor-preview-failure-en-US-light-375.jpg) | `—`, explicit retry and enabled Save |
| [Saved after failed preview](policy-editor-preview-failure-saved-en-US-light-375.jpg) | Successful fixture write closes the dialog and retains the new name |
| [Over-limit preview](policy-editor-over-quota-en-US-light-375.jpg) | True 50,001/50,000 count, full warning and disabled Save |
| [Concurrent revision](policy-editor-conflict-en-US-light-375.jpg) | Local draft remains unsaved and Load latest is explicit |
| [Loaded revision](policy-editor-reloaded-en-US-light-375.jpg) | Latest name replaces the draft only after explicit reload |
| [Saving desktop](policy-editor-saving-en-US-light-1440.jpg) | Disabled scope, fields, close and cancel |
| [Saving phone](policy-editor-saving-en-US-light-375.jpg) | Header spinner and disabled phone controls |
| [List read failure](policy-editor-lists-read-failure-en-US-light-375.jpg) | Receipt name/availability retained with a clear read error |
| [Group read failure](policy-editor-groups-read-failure-en-US-light-375.jpg) | Selected ID retained with explicit retry |
| [Blank policy](policy-editor-blank-en-US-light-375.jpg) | Required name/match, collapsed conditions and disabled Save |
| [Invalid port](policy-editor-invalid-conditions-en-US-light-375.jpg) | Range guidance and disabled Save |
| [Invalid CIDR line](policy-editor-invalid-cidr-en-US-light-375.jpg) | Actual CodeMirror line two and its error are visible |
| [BT split](policy-editor-bt-split-en-US-light-375.jpg) | OR destinations, AND port/network and complete two-rule explanation |
| [Pending list](policy-editor-pending-list-en-US-light-1440.jpg) | Actual pending entries retain the inactive explanation |
| [List menu](policy-editor-list-options-en-US-light-1440.jpg) | Counts, state hints, 44px options and no group-owned list |
| [Scope menu](policy-editor-scope-options-en-US-light-1440.jpg) | Both actual options measure 44px |
| [Group menu](policy-editor-group-options-en-US-light-1440.jpg) | Required group selection and 44px options |

Touch targets, save-time scope mutation, three action-specific allowlist hints,
fixture failure/conflict contracts, unavailable previews, list receipt fallback
and disabled-preview presentation failed before repair. Relevant frontend
coverage passed 382 tests in 27 files after the final change. TypeScript,
changed-source lint, production build, fixture exclusion and four production
browser smoke checks passed. English, automatic theme, normal fixture data and
the default viewport were restored. S3 commit `d31353f5` passed the frontend,
database, race, build, compatibility and Node systemd jobs, but its
[Test workflow](https://github.com/KazuhaHub/Passwall-Sub-Panel/actions/runs/37886239945)
failed vulnerability scanning after the October 8 Go security advisories.
The separate repair selects Go 1.27.2 in both the preferred toolchain and Docker
builder, and upgrades `golang.org/x/net` to v0.60.0. Module verification and build
baseline tests passed with Go 1.27.2; govulncheck v1.8.0 found zero reachable
vulnerabilities for Linux, macOS and Windows. Six advisories in required modules
remain unreachable in these scans. Repair head `1988c38f` passed Node systemd,
frontend, databases, race, Docker and compatibility checks, but its
[Test workflow](https://github.com/KazuhaHub/Passwall-Sub-Panel/actions/runs/37887597788)
revealed that Staticcheck v0.8.1's original export reader rejects Go 1.27.2's
format 5. The follow-up builds the same analyzer from an isolated, checksum-pinned
tool module using PSP's existing `x/tools v0.50.0`; all checks stay enabled.
Its build, module verification, binary dependency metadata, build-baseline tests
and three focused workflow guards passed locally. Windows Application Control
prevented local execution of the newly built analyzer. Follow-up `d32236ae`
passed the complete [Test workflow](https://github.com/KazuhaHub/Passwall-Sub-Panel/actions/runs/37888539176),
including full analyzer execution and all three target vulnerability scans,
and [Node systemd acceptance](https://github.com/KazuhaHub/Passwall-Sub-Panel/actions/runs/37888539201).
The third-party live-panel job was skipped;
complete C2/C5/C6, remaining per-view matrices, owner approval and real Node
acceptance remain open.

## Templates and observation conversion (S4)

Template cards, menu triggers/items, quota explanations, finance-dialog actions
and conversion controls now have at least 44px targets. The conversion heading
contains only its title; Close is a separate button. The menu's explicit
download is one async action rather than a clickable item containing another
click handler. It participates in MUI menu focus navigation: Home and four Down
keys reach Download, skipping unavailable category choices, and Enter activates
it once. A keyboard regression test failed before registering the async action
as an actual MenuItem. No automatic download or nested download button is added.

The empty grid keeps three columns at 1440px and one at 375px, with no horizontal
overflow. The menu remains available after a policy is added. The category
templates default to observation; BT/mail block and count as risk, while private
addresses block without risk inclusion. The finance explanation opens list
creation with `tab=lists`, without creating a financial policy.

| Language/theme/viewport | Template grid | Template menu | Conversion |
| --- | --- | --- | --- |
| Chinese, light, 1440 × 900 | [Grid](templates-cn-light-1440.jpg) | [Menu](template-menu-cn-light-1440.jpg) | [Dialog](promotion-cn-light-1440.jpg) |
| Chinese, dark, 1440 × 900 | [Grid](templates-cn-dark-1440.jpg) | [Menu with failed download](template-menu-cn-dark-1440.jpg) | [Dialog](promotion-cn-dark-1440.jpg) |
| Chinese, light, 375 × 812 | [Rules](templates-cn-light-375-top.jpg), [categories](templates-cn-light-375-bottom.jpg) | [Keyboard download](template-menu-cn-light-375.jpg) | [Fullscreen](promotion-cn-light-375.jpg) |
| Chinese, dark, 375 × 812 | [Empty state](templates-cn-dark-375-top.jpg), [categories](templates-cn-dark-375-bottom.jpg) | [Menu with failed download](template-menu-cn-dark-375.jpg) | [Fullscreen](promotion-cn-dark-375.jpg) |
| English, light, 375 × 812 | [Rules](templates-en-light-375-top.jpg), [categories](templates-en-light-375-bottom.jpg) | [Wrapped menu](template-menu-en-light-375.jpg) | [Fullscreen](promotion-en-light-375.jpg) |

All 39 screenshots in this S4 package were captured from the native browser and
visually inspected. Additional checks:

| Capture | Check |
| --- | --- |
| [BT prefill](template-bt-prefill-en-light-375.jpg) | Block action and BT condition; risk inclusion checked in the actual form |
| [Mail prefill](template-mail-prefill-en-light-1440.jpg) | TCP and ports 25,465,587; risk inclusion checked |
| [Private-address prefill](template-private-prefill-en-light-1440.jpg) | Private condition selected, risk inclusion off |
| [Category prefill](template-crypto-prefill-en-light-1440.jpg) | Observe, cryptocurrency category, bounded parse summary and atomic list/policy explanation |
| [Added template](template-added-en-light-1440.jpg) | Successful synthetic creation retains the New policy menu and quiet Added badge |
| [Finance explanation](template-finance-explanation-en-light-1440.jpg), [New list](template-finance-new-list-en-light-1440.jpg) | Touch-sized explanation actions and native route to list creation |
| [Missing catalog](templates-catalog-missing-en-light-1440.jpg) | Category cards retain explicit download controls; inline templates remain available |
| [Downloading](template-download-pending-cn-light-375.jpg), [completed](template-download-completed-cn-light-375.jpg), [failed](template-download-failed-cn-light-375.jpg) | Keyboard submission has aria-busy/disabled feedback; success enables category choices; failure retains retry without a nested button |
| [Quota explanation](template-quota-hint-en-light-1440.jpg), [category draft](template-quota-editor-en-light-1440.jpg), [budget](template-quota-budget-en-light-1440.jpg) | Synthetic current 250/256 plus 18 category regexps produces 268/256; the draft stays editable, Save is disabled |
| [Discard confirmation](promotion-discard-cn-dark-375.jpg) | Continue editing retains the selected risk checkbox; confirmed discard closes without conversion |
| [Conflict](promotion-conflict-cn-dark-1440.jpg), [reloaded](promotion-reloaded-cn-dark-1440.jpg), [saved](promotion-saved-cn-dark-1440.jpg) | 409 retains current risk choice and disables submit; explicit reload resets risk and reads the new revision; success appends the row after three existing block policies |
| [Disabled observation](promotion-disabled-cn-dark-375.jpg) | Conversion explicitly retains disabled state |
| [Pending desktop](promotion-pending-cn-dark-1440.jpg), [pending phone](promotion-pending-cn-dark-375.jpg) | Thirty-second conversion shows a spinner, locks Close/Cancel/checkbox, ignores Escape, then succeeds and closes |

The two new DEV-only scenarios, `template-over-quota` and
`templates-catalog-missing`, keep catalog counts, preview counts and projected
quota consistent, reject over-limit paired creation before mutation, and use
the isolated adapter. Their contracts failed before implementation. These
fixtures do not prove kernel enforcement or real catalog sizes. Existing real
parser and backend acceptance evidence remains separate. Stage 1c conversion
reads no hit data and shows no statistical impact region; its PUT retains the
full policy and revision.

Download duplication, keyboard admission, undersized grid/menu/conversion and
finance controls, and nested conversion heading content all failed before
repair. Relevant frontend coverage passed 389 tests in 28 files after the final
source change. TypeScript, changed-source lint, production build, fixture
exclusion and four production browser smoke checks passed. English, automatic
theme, normal fixture data and the default viewport were restored. S4 commit
`62ee8429` passed its own complete
[Test workflow](https://github.com/KazuhaHub/Passwall-Sub-Panel/actions/runs/37890046260)
and [Node systemd acceptance](https://github.com/KazuhaHub/Passwall-Sub-Panel/actions/runs/37890046252).
Third-party live-panel checks were skipped. Complete C2/C5/C6, remaining per-view matrices,
owner approval and real candidate Node acceptance remain open.

## Lists overview and references (S6)

Creation, list names, row menus, usage triggers/linked references, sorting,
refresh-setting links and list-read retries meet the 44px minimum. Desktop
sort targets measure 52 × 44px and row menus 44 × 44px; phone menu choices
measure 48px high. Referenced lists keep Delete disabled. The table reserves
enough width for its row menu rather than placing the enlarged control across
the cell boundary.

| Capture | Language/theme/viewport | Check |
| --- | --- | --- |
| [Desktop table](list-overview-zh-CN-light-1440.jpg) | Chinese, light, 1440 × 900 | Names, usage, full totals and touch-sized sorting |
| [Dark table](list-overview-zh-CN-dark-1440.jpg) | Chinese, dark, 1440 × 900 | Table and semantic list states |
| [Phone cards](list-overview-zh-CN-light-375.jpg) | Chinese, light, 375 × 812 | Scrolled list cards with usable old remote content |
| [Dark phone cards](list-overview-zh-CN-dark-375.jpg) | Chinese, dark, 375 × 812 | Card layout and owned-list usage |
| [English cards](list-overview-en-US-light-375.jpg) | English, light, 375 × 812 | Translated states and usage actions |
| [Long English state](list-overview-long-status-en-US-light-375.jpg) | English, light, 375 × 812 | Never-downloaded state wraps within its card |
| [Usage popover](list-usage-en-US-light-375.jpg) | English, light, 375 × 812 | 343px popover with a 44px linked-policy action |
| [Row menu](list-menu-en-US-light-375.jpg) | English, light, 375 × 812 | Explicit edit/refresh choices and disabled Delete |
| [Problems filter](list-overview-problems-zh-CN-light-1440.jpg) | Chinese, light, 1440 × 900 | Pressed KPI, two problem lists and preserved URL filter; filtered finance remains healthy |
| [Empty lists](list-overview-empty-zh-CN-light-375.jpg) | Chinese, light, 375 × 812 | Zero budget usage, translated empty notice and New list action |
| [Failed read](list-overview-error-zh-CN-light-375.jpg) | Chinese, light, 375 × 812 | List-read failure has an explicit 44px retry; failure is not presented as empty |

These eleven captures use the DEV adapter and were visually checked. An initial
English-phone check found internal horizontal scrolling even though the
document itself did not overflow: the never-downloaded badge forced one line.
List states now opt into bounded wrapping in the shared badge; other callers
retain their existing one-line layout. At 375px the main scroll area changed
from 365px visible / 372px content to 365px / 365px. Desktop checks likewise
found equal visible/content widths. Screenshots were retaken after repair.
The actual name-sort click reversed table order, and selecting Problems kept
only the failed remote and referenced never-downloaded category, leaving the
filtered finance list out. Retry was explicit; it does not demonstrate upstream
recovery in the always-failing scenario. Language, theme and viewport were
restored afterward.

Four touch regressions and one wrapping regression failed before their fixes;
a sixth case also verifies referenced-list Delete remains disabled. The final
relevant suite passed 354 cases in 26 files. TypeScript, changed-source lint,
production build, fixture exclusion and four production browser smoke checks
passed. This is separate from the S7 commit `5b331830`, which passed both
[complete Test CI](https://github.com/KazuhaHub/Passwall-Sub-Panel/actions/runs/37854908718)
and [released-node systemd acceptance](https://github.com/KazuhaHub/Passwall-Sub-Panel/actions/runs/37854908770).
The overview commit `9f64f633` also passed
[complete Test CI](https://github.com/KazuhaHub/Passwall-Sub-Panel/actions/runs/37855921940)
and [released-node systemd acceptance](https://github.com/KazuhaHub/Passwall-Sub-Panel/actions/runs/37855921941).
Third-party live adapter jobs were skipped; full C2/C5/C6 and real Node
enforcement remain open.

## List editor (S7)

The existing custom, remote and community-category editors use a 900px desktop
dialog and a full-screen phone dialog. All fifteen normal captures below were
visually checked at their actual viewport dimensions; none has horizontal
document overflow. Existing-list types remain locked. The native CodeMirror
original-text editor measures 216px high with ten rows. Dialog names contain
only the title, the type group has a translated accessible name, and close,
type, fetch, refresh-setting, category/attribute indicator, download and footer
actions meet the 44px minimum. Selected attribute tags and their removal hit
areas also meet 44px; keyboard deletion preserves literal negative attributes.

| Language/theme/viewport | Custom | Remote | Community category |
| --- | --- | --- | --- |
| Chinese, dark, 375 × 812 | [Original text](list-editor-custom-zh-CN-dark-375.jpg) | [Explicit fetch](list-editor-remote-zh-CN-dark-375.jpg) | [Filtered report](list-editor-category-zh-CN-dark-375.jpg) |
| Chinese, dark, 1440 × 900 | [Original text](list-editor-custom-zh-CN-dark-1440.jpg) | [Explicit fetch](list-editor-remote-zh-CN-dark-1440.jpg) | [Filtered report](list-editor-category-zh-CN-dark-1440.jpg) |
| Chinese, light, 375 × 812 | [Original text](list-editor-custom-zh-CN-light-375.jpg) | [Explicit fetch](list-editor-remote-zh-CN-light-375.jpg) | [Filtered report](list-editor-category-zh-CN-light-375.jpg) |
| Chinese, light, 1440 × 900 | [Original text](list-editor-custom-zh-CN-light-1440.jpg) | [Explicit fetch](list-editor-remote-zh-CN-light-1440.jpg) | [Filtered report](list-editor-category-zh-CN-light-1440.jpg) |
| English, dark, 375 × 812 | [Original text](list-editor-custom-en-US-dark-375.jpg) | [Explicit fetch](list-editor-remote-en-US-dark-375.jpg) | [Filtered report](list-editor-category-en-US-dark-375.jpg) |

| Additional capture | Check |
| --- | --- |
| [Selected negative attribute](list-editor-category-attrs-en-US-dark-375.jpg) | `!cn` remains literal; selected tag and removal hit area measure 44px |
| [Discard confirmation](list-editor-discard-en-US-dark-375.jpg) | Shared confirmation appears; cancel preserves the selected attribute draft |
| [Remote fetch failure](list-editor-remote-fetch-error-en-US-dark-375.jpg) | Explicit synthetic fetch fails; cached four-entry report and old-content explanation remain |
| [Blank custom draft](list-editor-empty-zh-CN-light-375.jpg) | Real-backend empty preview accepts zero entries; Save stays disabled |
| [Real custom report](list-editor-live-custom-report-zh-CN-light-375.jpg) | Actual backend accepts two entries, normalizes two and ignores three, including one broad regexp |
| [Real report line jump](list-editor-live-line-jump-zh-CN-light-375.jpg) | Clicking line four focuses CodeMirror and selects `regexp:.*`; report line targets measure 44 × 44px |
| [Missing catalog](list-editor-catalog-missing-zh-CN-light-375.jpg) | Explicit 44px download action; no usable category and Save disabled |
| [Catalog download failure](list-editor-catalog-failed-zh-CN-light-375.jpg) | Synthetic queued download settles to a translated read error with explicit retry and Save disabled |

The normal matrix, attribute/discard, remote failure and missing/failed catalog
use the DEV adapter. Its generated preview digests are synthetic and do not
prove unchanged executable bytes or restart counts. The blank draft and two
custom-report captures instead use the actual isolated Go backend, with fixtures
disabled through their normal tool. The five original lines are a comment,
`*.Example.COM.`, an unsupported modifier rule, `regexp:.*` and `192.0.2.1/24`.
The accepted output is `domain:example.com` and `192.0.2.0/24`; the broad regexp
is excluded. The native DOM selection after line-four activation was exactly
`regexp:.*`, with focus on the original-text editor. Save was enabled after
valid parsing, but the draft was discarded without saving. Locale, automatic
theme and default viewport were restored after capture.

Five accessibility regressions failed before repair. The final relevant
access-control, query, fixture, shared-confirmation, risk drawer, style and
contrast suite passed 348 cases in 26 files. TypeScript, changed-source lint,
production build, fixture exclusion and four production browser smoke checks
passed. This fills the three editor variants and documented extra states;
original-read failure/reload is covered by the follow-up below. Complete C2/C5/C6, other per-view matrices, true Node
enforcement and owner acceptance remain pending. S7 commit `5b331830` passed
both remote workflows linked above, separately from the successful `8dfb38d7`
runs. Later overview changes require their own candidate validation.

### Original-read failure and explicit reload (S7 follow-up)

The first saved-list read now has its own translated failure explanation,
instead of suggesting that an unavailable source can be saved. A failed read
stops the skeleton animation and keeps Save disabled. Load latest is one async
action with busy/disabled feedback; a read does not label the footer as Saving.
Retry loads the preserved original text and name without a PUT. Cancellation
returns focus to the same list-actions trigger.

| Capture | Check |
| --- | --- |
| [English phone failure](list-editor-original-error-en-light-375.jpg), [reloaded](list-editor-original-reloaded-en-light-375.jpg) | No failed-read skeleton; explicit retry loads both original lines; Save remains disabled on the unchanged draft |
| [Chinese dark desktop failure](list-editor-original-error-cn-dark-1440.jpg), [reloaded](list-editor-original-reloaded-cn-dark-1440.jpg) | Translated read-specific message; 900px dialog, 44px actions and preserved group-owned source |

All four native captures were visually checked. At 375px the document width
equals the viewport width; the failed-read retry meets 44px. Native DOM checks
during both retries show Load latest busy and disabled, Close/Cancel disabled,
and a disabled Save with its normal label. After cancellation the focused
button is the originating list-actions trigger. The normal editor matrix
above remains the baseline; these synthetic scenes do not prove an actual
backend outage or node rule changes.

The DEV-only `list-original-read-error` scenario fails the first original-text
GET once per list and permits a later explicit retry, with no live transport
fallthrough or definition mutation. Its contract failed before implementation.
The editor regression also failed before the read-specific copy and async
feedback repairs. It now checks the disabled save, absence of automatic preview
before loading, exact preserved source/name, duplicate retry admission and no
PUT. Relevant frontend coverage passed 390 tests in 28 files after the final
source change. TypeScript, changed-source lint, production build, fixture
exclusion and four production browser smoke checks passed. English, automatic
theme, normal fixtures and the default viewport were restored. Follow-up
`c7ec4155` passed its own complete
[Test workflow](https://github.com/KazuhaHub/Passwall-Sub-Panel/actions/runs/37891075987)
and [Node systemd acceptance](https://github.com/KazuhaHub/Passwall-Sub-Panel/actions/runs/37891075899).
Third-party live-panel checks were skipped. Complete C2/C5/C6, remaining per-view matrices, real candidate
Node acceptance and owner approval remain open.

## Data and deployment settings (S18)

The settings dialog keeps its title separate from Close, with a 600px desktop
paper and a fullscreen phone layout. Reset, read-retry, footer and close actions
now meet 44px in both dimensions. Native checks found five 30px reset buttons
and 40px footer actions before repair. Async retry and saving expose progress;
short labels such as the Chinese retry action keep one line.

| Language/theme/viewport | Capture |
| --- | --- |
| Chinese, light, 1440 × 900 | [Desktop](settings-cn-light-1440.jpg) |
| Chinese, dark, 1440 × 900 | [Desktop](settings-cn-dark-1440.jpg) |
| Chinese, light, 375 × 812 | [Top](settings-cn-light-375-top.jpg), [bottom](settings-cn-light-375-bottom.jpg) |
| Chinese, dark, 375 × 812 | [Top](settings-cn-dark-375-top.jpg), [bottom](settings-cn-dark-375-bottom.jpg) |
| English, light, 375 × 812 | [Top](settings-en-light-375-top.jpg), [bottom](settings-en-light-375-bottom.jpg) |

| Additional capture | Check |
| --- | --- |
| [Default draft](settings-default-draft-cn-dark-375.jpg), [saved defaults](settings-default-saved-cn-dark-375.jpg) | Five reset actions clear actual input values and expose served defaults 30/7/7/17/93; synthetic save clears the changed count without changing effective retention |
| [Range error](settings-range-error-cn-dark-375.jpg) | Refresh value 5 shows the 6–168 integer range; Save stays disabled |
| [Discard confirmation](settings-discard-confirm-cn-dark-375.jpg) | Continue editing retains value 5; one confirmed discard closes settings without a write and returns focus to More |
| [Failed read](settings-read-error-cn-dark-375.jpg) | Error is not an empty editor; touch-sized explicit retry returns to the same synthetic failure |
| [Pending desktop](settings-pending-cn-dark-1440.jpg), [pending phone](settings-pending-cn-dark-375.jpg), [saved](settings-saved-cn-dark-375.jpg) | Separate thirty-second saves retain all fields, lock inputs/reset/Close/Discard/Save, show one spinner and ignore Escape; success keeps the editor open and resets the changed count |

All 16 native captures were visually checked. Phone document/content widths
match the viewport and footer actions remain within it. Closing an unchanged
dialog restores the persistent More trigger. Native dirty-close checks also
found that the confirmed discard triggered a second route-leave confirmation;
the editor now clears the discarded draft before closing. A shared admission
guard prevents two close callbacks from one pending confirmation. Cancellation
keeps the draft. This also preserves cold-link and existing field-focus behavior.

The pending desktop capture changes refresh 17 to 48; the independent phone
capture changes 48 to 72. Other fields retain 30/7/7/93. These use the DEV-only
`settings-save-pending` scenario. Its contract verifies cancellation before
mutation, timer cleanup and saving only the requested key without live
transport fallthrough. No screenshot proves actual retention cleanup, kernel
deployment or node enforcement.

Both slow saves settled with zero changed fields; the phone save retained
refresh 72 and other values 30/7/7/93. English, automatic theme, normal fixtures
and the default 1280 × 720 viewport were restored with no dialog left open.

Five regressions reproduced undersized actions/retry, immediate fixture
mutation, duplicate discard confirmation and duplicate close callbacks before
repair. The relevant frontend suite passed 396 cases in 28 files; after the
final retry-label layout adjustment, all 144 affected editor/view/fixture cases
in three files passed. TypeScript, changed-source lint, production build,
fixture exclusion and four production browser smoke checks passed. Head
`8526f9c8093c4515490669403b4ec4cda7e61ec7` passed its own
[complete Test CI](https://github.com/KazuhaHub/Passwall-Sub-Panel/actions/runs/37893775461)
and [Node systemd acceptance](https://github.com/KazuhaHub/Passwall-Sub-Panel/actions/runs/37893775469).
Third-party live-panel checks were skipped. Remaining per-view matrices,
C2/C5/C6, actual candidate Node acceptance and owner approval remain open.

## Concurrent list references (S20 follow-up)

A list that looked unused can acquire a reference before Delete reaches the
server. The 409 `dest_list_in_use` response already includes `used_by`; the UI
previously discarded that evidence and opened a generic confirmation. It now
opens an informational dialog with the returned references. Policy actions
open the existing editor without saving or repeating deletion. Group names
remain readable; group navigation awaits its later-stage route. Missing or
malformed references remain explicitly unknown, with no invented empty state.
Reference targets require positive safe-integer IDs, known kinds and string
names; duplicates are removed while order is preserved.

| Language/theme/viewport | Delete confirmation | Current references |
| --- | --- | --- |
| Chinese, light, 1440 × 900 | [Confirm](list-delete-confirm-cn-light-1440.jpg) | [References](list-in-use-cn-light-1440.jpg) |
| Chinese, dark, 1440 × 900 | [Confirm](list-delete-confirm-cn-dark-1440.jpg) | [References](list-in-use-cn-dark-1440.jpg) |
| Chinese, light, 375 × 812 | [Confirm](list-delete-confirm-cn-light-375.jpg) | [References](list-in-use-cn-light-375.jpg) |
| Chinese, dark, 375 × 812 | [Confirm](list-delete-confirm-cn-dark-375.jpg) | [References](list-in-use-cn-dark-375.jpg) |
| English, light, 375 × 812 | [Confirm](list-delete-confirm-en-light-375.jpg) | [References](list-in-use-en-light-375.jpg) |

[Opening the referenced original policy](list-in-use-policy-en-light-375.jpg)
retains its name, list, enabled state and disabled Save. All 11 captures were
visually checked. The informational dialog measures 480px on desktop and
343px at a 375px viewport, with 44px actions and no content overflow. Enter on
phone and Escape on desktop close it and restore the original list-actions
trigger. Its transition must finish before restoring focus; a regression
failed before that repair. The refreshed references then disable Delete.
Cancelling the initial delete confirmation leaves the list unused in the
synthetic scenario.

Two page regressions and the DEV fixture contract failed before the references
repair; the focus regression independently failed before repair. The fixture
reports an initially stale unused read, refuses deletion with actual seed
references, and keeps list content and policies unchanged without falling
through to live transport. Parser cases cover malformed and duplicate targets.
The final relevant frontend suite passed 406 tests in 29 files; TypeScript,
changed-source lint, production build, fixture exclusion and four production
browser smoke checks passed. Head `122683dfaec091be57ed6e257a8afcf3b2794652`
passed its own [complete Test CI](https://github.com/KazuhaHub/Passwall-Sub-Panel/actions/runs/37895731882)
and [Node systemd acceptance](https://github.com/KazuhaHub/Passwall-Sub-Panel/actions/runs/37895731853).
Third-party live-panel checks were skipped; released-node systemd does not prove
candidate Node enforcement.
English, automatic theme, normal fixtures and the default 1280 × 720 viewport
were restored with no open dialogs. The isolated backend was restarted with
its existing executable, config and database after the temporary processes
were cleared; no new backend runtime validation is claimed for this UI repair.
It does not complete S20's full catalog, C2/C5/C6 or real candidate Node acceptance.

The subsequent [confirmation catalog consolidation](../access-control-confirmations.md)
centralizes the remaining stage-1c plain-text copies without changing their
keys, labels or actions. Its final relevant suite passed 415 tests in 29 files.
The [C2 evidence index](../access-control-c2-evidence.md) records the separate
fixed-release, refresh-identity and application-reopen checks and their CI.

## Real backend catalog acceptance

These captures use the production frontend and actual Go backend, normal admin
login, a fresh isolated SQLite database and loopback-only HTTP. They are not
mock API downloads. Credentials, config secrets and bootstrap logs are excluded
from these artifacts. No nodes are configured in this environment.

| Capture | Check |
| --- | --- |
| [Downloaded catalog](catalog-live-downloaded-en-US.jpg) | Actual v2fly download completed, missing notice disappeared without manual reload, selected problem filter retained |
| [Finance preview](catalog-live-finance-report-en-US.jpg) | Actual `category-finance`: 612 accepted, one ignored broad entry; `domain:hsbc` explicitly excluded |
| [Restored list/report](catalog-live-restored-report-en-US.jpg) | After backend restart: title-only drawer heading, full-list type totals and persisted removal report; the entry-sample search is deliberately filtered to make the report visible |

The HTTP trace on 2026-10-07 UTC was POST 202 at 01:22:49.325, immediate GET 503
at 01:22:49.338, then automatic GET 200 at 01:22:54.342. One manual request
downloaded the 3,614,228-byte cache. Subsequent idle observation through list
creation did not issue another catalog read. The real list save returned 201.
A read-only SQLite check verified 612 full persisted entries, exclusion of the
exact `domain:hsbc` entry, one ignored-broad report entry and its persisted sample.
The stored content SHA-256 is
`114fec2e85ccbd0019a85123181cea120f404529ecab3e279cd80d70d7e77542`.

The pre-repair backend had written the same downloaded catalog while its page
continued to show the missing notice. Timed query regressions now verify delayed
success/failure after HTTP 202 and no additional polling after settlement.
Backend failure/cancellation and deduplication have service/HTTP regressions;
they are not claimed here as live upstream outage simulations. Complete C2/C6,
the full language/theme/screen matrix, real Node enforcement and owner acceptance
remain pending.

## Real backend template and simulation acceptance

These use the same isolated backend/database as the catalog captures above,
without Node fixtures. The template first-enable dialog was canceled once;
SQLite still contained one finance list and zero policies, and the renamed
template draft remained. Confirmation then returned 201 and atomically saved
the 235-entry cryptocurrency list and its enabled observation policy, with no
risk flag. A duplicate later remained disabled after a successful real save.

| Capture | Check |
| --- | --- |
| [Canceled template draft](template-live-canceled-draft-en-US.jpg) | Renamed draft and template origin survive canceling first enable; no policy/list write |
| [Unpublished result](template-live-unpublished-result-en-US.jpg) | `binance.com` simulation explicitly uses the published snapshot and warns about unpublished changes |
| [Published observation](template-live-published-observation-en-US.jpg) | Explicit rerun after publication matches observation at step four; header reports no executable Node |
| [Disabled duplicate](policy-live-disabled-copy-en-US.jpg) | Successful save keeps the copied policy disabled; original remains enabled |

Publication returned 200 and did not automatically repeat the audited test.
The explicit subsequent test reported the observation policy and
`domain:binance.com` match. This demonstrates backend snapshot selection and
matching, not native traffic enforcement or audit collection.

The first real policy save caused an unwanted preview POST returning 409 after
its 201. After query invalidation was repaired and the production build
restarted, the deliberate edited-draft preview returned 200 at 01:35:48.387 UTC;
the duplicate save returned 201 at 01:35:59.525 and normal reads followed with
no additional preview POST. Query regressions also cover exemption mutations,
preserved invalidation and session boundaries. Full C5/C6, responsive/language/
theme matrix and owner approval remain pending.

## Confirmation consequences and focus

The S20 consequence package adds twenty visually inspected native browser
captures. They use the normal and stale-unused-list DEV scenarios; the latter
changes reference metadata to expose the unused-list confirmation. Deletion was always
canceled: this does not establish successful deletion or live Node enforcement.
Pause/resume mutations changed only the isolated in-memory fixture.

| Confirmation | Chinese light desktop | Chinese dark desktop | Chinese light phone | Chinese dark phone | English light phone |
| --- | --- | --- | --- | --- | --- |
| Pause | [1440](s20-pause-zh-light-1440.jpg) | [1440](s20-pause-zh-dark-1440.jpg) | [375](s20-pause-zh-light-375.jpg) | [375](s20-pause-zh-dark-375.jpg) | [375](s20-pause-en-light-375.jpg) |
| Resume | [1440](s20-resume-zh-light-1440.jpg) | [1440](s20-resume-zh-dark-1440.jpg) | [375](s20-resume-zh-light-375.jpg) | [375](s20-resume-zh-dark-375.jpg) | [375](s20-resume-en-light-375.jpg) |
| Delete policy | [1440](s20-delete-policy-zh-light-1440.jpg) | [1440](s20-delete-policy-zh-dark-1440.jpg) | [375](s20-delete-policy-zh-light-375.jpg) | [375](s20-delete-policy-zh-dark-375.jpg) | [375](s20-delete-policy-en-light-375.jpg) |
| Delete unused list | [1440](s20-delete-list-zh-light-1440.jpg) | [1440](s20-delete-list-zh-dark-1440.jpg) | [375](s20-delete-list-zh-light-375.jpg) | [375](s20-delete-list-zh-dark-375.jpg) | [375](s20-delete-list-en-light-375.jpg) |

[DOM measurements](s20-confirmation-metrics.json) record each actual dialog's
text, viewport, rectangle, horizontal overflow and button sizes/colors.
Desktop CSS viewports were 1440×900; phones were 375×812. All twenty dialogs
fit their viewport without horizontal overflow, with 44px-high actions.
Danger buttons use the theme's error color in both modes; Resume uses primary.
The longest English pause text fits a 320px-wide dialog without clipping.

On the final source, Enter on Cancel preserved the policy/list and returned
focus to its persistent row-actions button. Escape on Pause and cancellation
of Resume returned focus to the header More button. Tab/Shift+Tab wrapped
between the two Resume actions inside the dialog. Fixture pause/resume was
confirmed using Enter, and the next menu offered the opposite action. Normal
fixtures, English, automatic theme and the default viewport were restored.

Eleven copy/ETA, two theme-color and three disappearing-menu focus regressions
failed before repair. The final relevant suite passed 436 tests in 31 files;
TypeScript, changed-source lint, production build, fixture exclusion and four
production smoke checks passed. The [catalog](../access-control-confirmations.md)
records the remaining dialogs and evidence boundaries. This package does not
close the complete S20/per-view matrices, C2/C5/C6, candidate Node enforcement,
owner acceptance or dependency/release gates.
