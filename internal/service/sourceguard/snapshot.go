package sourceguard

import "github.com/KazuhaHub/passwall-sub-panel/internal/domain"

// BuiltinCloudflareDate and BuiltinCloudflare are the snapshot of Cloudflare's
// published ranges (domain.BuiltinCloudflare), what a CDN line is checked
// against until a fetch succeeds, or when fetching is turned off.
const BuiltinCloudflareDate = domain.BuiltinCloudflareDate

var BuiltinCloudflare = domain.BuiltinCloudflare
