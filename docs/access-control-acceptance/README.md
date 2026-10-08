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
scenario and activate it. Sign in normally to the local PSP if needed. The tool
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
is not a successful Linux reinstallation-gate run; the new candidate's remote
gate remains pending.

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
