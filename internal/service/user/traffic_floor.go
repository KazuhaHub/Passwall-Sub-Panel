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
