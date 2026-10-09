# UI-0 visual acceptance evidence

Captured on 2026-10-04 from implementation commit `49d956c3`, using the real PSP application and Vite on loopback (`127.0.0.1:8788` / `5174`). The panel uses a newly created, isolated SQLite database under the system temporary directory, with the bootstrap test administrator and shipped defaults. No production data or native agents are present.

These screenshots document partial acceptance; they do **not** satisfy the full final plan §7.6 gate. The access-control fixture adapter belongs to the following implementation stage. Populated/error states, remaining views and drawers, and owner approval are still pending.

| View | zh-CN light | zh-CN dark | en-US 375×812 |
|---|---|---|---|
| Diagnostics | [1440×900](diagnostics-zh-CN-light-1440.jpg), [375×812](diagnostics-zh-CN-light-375.jpg) | [1440×900](diagnostics-zh-CN-dark-1440.jpg), [375×812](diagnostics-zh-CN-dark-375.jpg) | [dark](diagnostics-en-US-dark-375.jpg) |
| Risk policy | [1440×900](risk-policy-zh-CN-light-1440.jpg), [375×812](risk-policy-zh-CN-light-375.jpg) | [1440×900](risk-policy-zh-CN-dark-1440.jpg), [375×812](risk-policy-zh-CN-dark-375.jpg) | [dark](risk-policy-en-US-dark-375.jpg) |

Additional captures: [empty risk queue](risk-queue-empty-zh-CN-light-375.jpg), [unsaved policy departure confirmation](risk-policy-leave-confirm-zh-CN-light-1440.jpg).

The browser measured `document.documentElement.scrollWidth === innerWidth === 375` on diagnostics and risk policy. A real policy-tab edit followed by navigation displayed the departure confirmation; cancelling kept the policy URL and edited value `2`. The draft was discarded afterwards without a server write.

Local validation on the final implementation: 151 frontend test files passed, one file skipped; 1,797 tests passed and one skipped. TypeScript, Vite production build and four production browser smoke checks passed. Existing tests were preserved apart from the plan-authorized import/history-marker changes. On Windows, the unchanged Go-source drift test requires the remote LF representation of `sharedclient.go`; Git's CRLF checkout was normalized for the test run and restored afterwards, without a content change.

PR CI, the complete screenshot matrix and owner approval are tracked in [PR #272](https://github.com/KazuhaHub/Passwall-Sub-Panel/pull/272).
