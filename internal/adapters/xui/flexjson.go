package xui

import (
	"bytes"
)

// flexJSON captures an inbound list-payload field (settings / streamSettings /
// sniffing / allocate) as the canonical unescaped JSON text downstream
// parsers (xrayspec.ParseSettings, extractSSMethod, ...) expect.
//
// PSP requires 3X-UI ≥ 3.1.0, where these fields are returned as nested JSON
// objects/arrays (or null). Older 3X-UI returned them as escaped strings;
// against such a panel json.Unmarshal would store the outer-quoted+escaped
// literal here, which downstream parsers reject — failing fast surfaces the
// version mismatch immediately rather than silently breaking traffic-poll.
type flexJSON string

func (f *flexJSON) UnmarshalJSON(b []byte) error {
	if bytes.Equal(bytes.TrimSpace(b), []byte("null")) {
		*f = ""
		return nil
	}
	*f = flexJSON(b)
	return nil
}
