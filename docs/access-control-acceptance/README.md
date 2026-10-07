# Access-control browser evidence

The fixture screenshots below use real page/components and built-in
translations with mock API responses. Their temporary fixtures and development
servers were removed after checking. Real backend catalog captures are recorded
separately at the end of this file; neither set establishes Node enforcement or
completion of the entire stage-1c acceptance matrix.

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
