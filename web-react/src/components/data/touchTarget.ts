// On a coarse pointer (a finger) every control needs a hit area of at least
// 32px, but the compact density draws many of them at 24-28px. This adds an
// invisible expanded hit area through a pseudo-element, on coarse pointers only,
// so the desktop look and layout do not change. It is the same technique, media
// query and size as TOUCH_TARGET in the shell package's IconButton; the two are
// to be unified after both merge.
export const TOUCH_TARGET =
  "relative pointer-coarse:after:absolute pointer-coarse:after:top-1/2 pointer-coarse:after:left-1/2 pointer-coarse:after:size-[max(100%,2rem)] pointer-coarse:after:-translate-1/2 pointer-coarse:after:content-['']"
