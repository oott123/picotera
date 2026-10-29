import type { InjectionKey, ComputedRef } from 'vue'

export const trDimmedKey: InjectionKey<ComputedRef<boolean>> = Symbol('trDimmed')
