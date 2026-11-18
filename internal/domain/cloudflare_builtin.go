package domain

// BuiltinCloudflareDate is when BuiltinCloudflare was copied from
// https://www.cloudflare.com/ips-v4 and /ips-v6. It is what a CDN line is
// checked against until a fetch succeeds, or when fetching is turned off.
const BuiltinCloudflareDate = "2026-11-18"

// BuiltinCloudflare is the snapshot of Cloudflare's published ranges.
var BuiltinCloudflare = []string{
	"103.21.244.0/22",
	"103.22.200.0/22",
	"103.31.4.0/22",
	"104.16.0.0/13",
	"104.24.0.0/14",
	"108.162.192.0/18",
	"131.0.72.0/22",
	"141.101.64.0/18",
	"162.158.0.0/15",
	"172.64.0.0/13",
	"173.245.48.0/20",
	"188.114.96.0/20",
	"190.93.240.0/20",
	"197.234.240.0/22",
	"198.41.128.0/17",
	"2400:cb00::/32",
	"2405:8100::/32",
	"2405:b500::/32",
	"2606:4700::/32",
	"2803:f800::/32",
	"2a06:98c0::/29",
	"2c0f:f248::/32",
}
