// On a coarse pointer (a finger) every control needs a hit area of at least 32px,
// but the compact density draws many of them at 24-28px. The control itself grows
// to 2rem on coarse pointers only, so desktop is unchanged and nothing overlaps its
// neighbour. (An invisible expanded hit area was tried first: it overlapped the
// neighbouring control by a few px in a tight row, which is why the shell package
// abandoned it too.) The same media query and size as TOUCH_TARGET in the shell
// package's IconButton; the two are to be unified after both merge.
export const TOUCH_TARGET = 'pointer-coarse:min-h-[2rem] pointer-coarse:min-w-[2rem]'

// For controls that are wide but short (a text button, a segmented option): height only.
export const TOUCH_HEIGHT = 'pointer-coarse:min-h-[2rem]'
