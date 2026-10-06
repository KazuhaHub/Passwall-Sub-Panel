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

Browser interaction also checked Chinese policy-help interpolation and pause
confirmation/cancellation. Cancellation leaves the existing execution verdict;
confirmation warns that offline or failed nodes can retain the old rules.
These captures are a partial increment, not the complete §7.6 matrix, owner
approval or completion of stage 1c.
