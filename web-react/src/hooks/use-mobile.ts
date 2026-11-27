import { useMediaQuery } from "./useMediaQuery"

// shadcn's sidebar asks "is this a phone-width screen". The query machinery is
// useMediaQuery's (one subscription implementation, safe where matchMedia is missing);
// only the breakpoint is named here.
const MOBILE_BREAKPOINT = 768

export function useIsMobile() {
  return useMediaQuery(`(max-width: ${MOBILE_BREAKPOINT - 1}px)`)
}
