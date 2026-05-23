/* eslint-disable @typescript-eslint/no-explicit-any */

/** Base64URL → ArrayBuffer */
function b64urlToBuf(s: string): ArrayBuffer {
  const pad = '='.repeat((4 - (s.length % 4)) % 4)
  const b64 = (s + pad).replace(/-/g, '+').replace(/_/g, '/')
  const bin = atob(b64)
  const buf = new ArrayBuffer(bin.length)
  const view = new Uint8Array(buf)
  for (let i = 0; i < bin.length; i++) view[i] = bin.charCodeAt(i)
  return buf
}

/** ArrayBuffer → Base64URL (no padding) */
function bufToB64url(buf: ArrayBuffer): string {
  const bytes = new Uint8Array(buf)
  let bin = ''
  for (let i = 0; i < bytes.length; i++) bin += String.fromCharCode(bytes[i]!)
  return btoa(bin).replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/, '')
}

/** Thrown when the user cancels the passkey prompt or it times out. */
export class WebAuthnUserCancelled extends Error {
  constructor() {
    super('Passkey ceremony cancelled or timed out')
    this.name = 'WebAuthnUserCancelled'
  }
}

/** Server response shape for create() options. */
type CreationOptionsJSON = {
  challenge: string
  rp: { name: string; id?: string }
  user: { id: string; name: string; displayName: string }
  pubKeyCredParams: { type: 'public-key'; alg: number }[]
  timeout?: number
  excludeCredentials?: { type: 'public-key'; id: string; transports?: string[] }[]
  authenticatorSelection?: PublicKeyCredentialCreationOptions['authenticatorSelection']
  attestation?: AttestationConveyancePreference
}

/** Server response shape for get() options. */
type RequestOptionsJSON = {
  challenge: string
  timeout?: number
  rpId?: string
  allowCredentials?: { type: 'public-key'; id: string; transports?: string[] }[]
  userVerification?: UserVerificationRequirement
}

function decodeCreationOptions(j: CreationOptionsJSON): PublicKeyCredentialCreationOptions {
  return {
    ...j,
    challenge: b64urlToBuf(j.challenge),
    user: {
      ...j.user,
      id: b64urlToBuf(j.user.id),
    },
    excludeCredentials: (j.excludeCredentials ?? []).map((c) => ({
      ...c,
      id: b64urlToBuf(c.id),
      transports: c.transports as AuthenticatorTransport[] | undefined,
    })),
  }
}

function decodeRequestOptions(j: RequestOptionsJSON): PublicKeyCredentialRequestOptions {
  return {
    ...j,
    challenge: b64urlToBuf(j.challenge),
    allowCredentials: (j.allowCredentials ?? []).map((c) => ({
      ...c,
      id: b64urlToBuf(c.id),
      transports: c.transports as AuthenticatorTransport[] | undefined,
    })),
  }
}

function encodeCreationResponse(c: PublicKeyCredential): any {
  const r = c.response as AuthenticatorAttestationResponse
  // getTransports() not present on older Safari — guard.
  const transports =
    typeof (r as any).getTransports === 'function' ? (r as any).getTransports() : []
  return {
    id: c.id,
    rawId: bufToB64url(c.rawId),
    type: c.type,
    response: {
      clientDataJSON: bufToB64url(r.clientDataJSON),
      attestationObject: bufToB64url(r.attestationObject),
      transports,
    },
    clientExtensionResults: c.getClientExtensionResults(),
    authenticatorAttachment: (c as any).authenticatorAttachment ?? null,
  }
}

function encodeAssertionResponse(c: PublicKeyCredential): any {
  const r = c.response as AuthenticatorAssertionResponse
  return {
    id: c.id,
    rawId: bufToB64url(c.rawId),
    type: c.type,
    response: {
      clientDataJSON: bufToB64url(r.clientDataJSON),
      authenticatorData: bufToB64url(r.authenticatorData),
      signature: bufToB64url(r.signature),
      userHandle: r.userHandle ? bufToB64url(r.userHandle) : null,
    },
    clientExtensionResults: c.getClientExtensionResults(),
  }
}

/** Run a WebAuthn registration ceremony. Returns the JSON the server expects. */
export async function webauthnCreate(
  optionsJSON: CreationOptionsJSON,
  signal?: AbortSignal,
): Promise<any> {
  const options = decodeCreationOptions(optionsJSON)
  let cred: PublicKeyCredential | null
  try {
    cred = (await navigator.credentials.create({
      publicKey: options,
      signal,
    })) as PublicKeyCredential | null
  } catch (e) {
    const err = e as DOMException
    if (err?.name === 'NotAllowedError' || err?.name === 'AbortError') {
      throw new WebAuthnUserCancelled()
    }
    throw e
  }
  if (!cred) throw new WebAuthnUserCancelled()
  return encodeCreationResponse(cred)
}

/** Run a WebAuthn discoverable-login ceremony. Returns the JSON the server expects. */
export async function webauthnGet(
  optionsJSON: RequestOptionsJSON,
  signal?: AbortSignal,
): Promise<any> {
  const options = decodeRequestOptions(optionsJSON)
  let cred: PublicKeyCredential | null
  try {
    cred = (await navigator.credentials.get({
      publicKey: options,
      signal,
    })) as PublicKeyCredential | null
  } catch (e) {
    const err = e as DOMException
    if (err?.name === 'NotAllowedError' || err?.name === 'AbortError') {
      throw new WebAuthnUserCancelled()
    }
    throw e
  }
  if (!cred) throw new WebAuthnUserCancelled()
  return encodeAssertionResponse(cred)
}
