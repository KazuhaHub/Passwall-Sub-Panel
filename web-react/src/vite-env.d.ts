/// <reference types="vite/client" />

interface ImportMetaEnv {
  /** Compile-time flag: set to "0" to suppress the admin sidebar version
   *  badge. Default unset → badge shows. Surfaced as a build switch
   *  rather than a runtime preference because operators want this
   *  decision baked into the binary they ship, not toggleable by an
   *  admin clicking around in the UI. */
  readonly VITE_SHOW_VERSION?: string
}
