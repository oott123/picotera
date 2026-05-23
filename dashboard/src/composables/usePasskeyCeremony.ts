import { ref } from 'vue'
import { ApiRequestError } from '@/api/client'
import { webauthnCreate, WebAuthnUserCancelled } from '@/api/webauthn'

export type CeremonyPhase = 'idle' | 'waiting' | 'success' | 'error'

export interface CeremonyConfig<TResult> {
  /** Server's /begin call — returns CreationOptionsJSON. */
  begin: () => Promise<unknown>
  /** Server's /complete call — receives attestation, returns whatever the server returns. */
  complete: (attestation: unknown) => Promise<TResult>
}

/**
 * Drives the standard WebAuthn registration ceremony as a four-phase state
 * machine. Callers render UI per phase; the same composable is used by
 * MeView's add-passkey dialog AND EnrollView's bootstrap/invite/reset forms.
 *
 * On `run()`: phase goes idle → waiting (during the entire begin + WebAuthn
 * UI + complete window) → success or error. Callers in 'success' state read
 * `result.value`; callers in 'error' state read `errorMessage.value`.
 *
 * Note: only one combined "waiting" phase covers the whole window from
 * /begin call through /complete call — the user perceives them as a single
 * "waiting on my authenticator" period.
 */
export function usePasskeyCeremony<TResult>(config: CeremonyConfig<TResult>) {
  const phase = ref<CeremonyPhase>('idle')
  const errorMessage = ref<string | null>(null)
  const result = ref<TResult | null>(null)

  async function run() {
    phase.value = 'waiting'
    errorMessage.value = null
    result.value = null
    try {
      const options = await config.begin()
      const attestation = await webauthnCreate(options as Parameters<typeof webauthnCreate>[0])
      result.value = await config.complete(attestation)
      phase.value = 'success'
    } catch (err) {
      if (err instanceof WebAuthnUserCancelled) {
        errorMessage.value = '取消或超时，请重试。'
      } else if (err instanceof ApiRequestError) {
        errorMessage.value = err.message
      } else if (err instanceof DOMException) {
        errorMessage.value = `注册失败：${err.name}: ${err.message}`
      } else if (err instanceof Error) {
        errorMessage.value = `注册失败：${err.message}`
      } else {
        errorMessage.value = '注册失败，请重试。'
      }
      phase.value = 'error'
    }
  }

  function reset() {
    phase.value = 'idle'
    errorMessage.value = null
    result.value = null
  }

  return { phase, errorMessage, result, run, reset }
}
