/** A browser that cannot run the WebAuthn ceremony has no PublicKeyCredential at all. */
export const webAuthnSupported = () => typeof window !== 'undefined' && typeof window.PublicKeyCredential !== 'undefined'

/** The user closed or dismissed the browser's passkey prompt: not a failure, and said in a calm sentence rather than an error. */
export const ceremonyCancelled = (err: unknown) => {
  const name = (err as { name?: string })?.name
  return name === 'NotAllowedError' || name === 'AbortError'
}
