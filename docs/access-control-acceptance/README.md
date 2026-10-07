# Access-control browser evidence

These screenshots cover the current stage-1c header and help increment. They
use real page/components and built-in translations with mock API responses;
they are not live backend or Node execution acceptance. The temporary fixture
and development server were removed after checking.

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
