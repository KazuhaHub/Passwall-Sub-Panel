package user

// TrafficFloorBytes returns the per-client traffic cap to push into 3X-UI's
// ClientSpec.TotalGB (bytes despite the name). Acts as a safety net so a
// long-offline panel can't be exploited to burn unlimited bandwidth: 3X-UI
// itself disables the client when its tracked usage crosses TotalGB.
//
// Encoding rules:
//
//   - limit <= 0           -> 0    (unlimited; 3X-UI side also has no cap)
//   - limit > 0, used < limit -> limit - used   (remaining bytes)
//   - limit > 0, used >= limit -> 1 (minimum non-zero)
//
// The "1" tail case matters: 3X-UI treats TotalGB == 0 as unlimited, so
// pushing 0 for a user who's already at their cap would defeat the floor
// entirely. Any non-zero value below current usage triggers immediate
// disable on the 3X-UI side on the next traffic tick, which is the
// behaviour we want.
//
// A consequence worth stating where the value is defined, because it has
// already tempted one optimisation: A QUOTA RESUME IS NOT AN ENABLE FLIP.
// Traffic periods are calendar-aligned (currentPeriodStart pins monthly to the
// 1st for everyone), so on the first poll after a rollover every suspended user
// resumes at once — and a rollover is precisely when this floor changes, since
// period_used resets and the floor goes from the exhausted sentinel back to the
// full quota. Anything that moves only the enable flag leaves the panel-side
// totalGB at last period's exhausted value, so Xray cuts the user off anyway:
// a silent quota failure hitting exactly the users a batching optimisation
// would be meant to help. The resume must push enable, expiry AND this floor
// together (pushClientConfigToAll -> syncSharedLifecycle).
func TrafficFloorBytes(limit, used int64) int64 {
	if limit <= 0 {
		return 0
	}
	rem := limit - used
	if rem <= 0 {
		return 1
	}
	return rem
}
