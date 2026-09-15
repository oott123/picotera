import type { App } from 'vue'
import createClient from 'openapi-fetch'
import type { paths } from '@/openapi-types'
import { useImpersonationStore } from '@/stores/impersonation'

export type ApiClient = ReturnType<typeof createClient<paths>>

const LOGIN_URL_HEADER = 'X-PicoTera-Login-Url'

export function createApi(baseURL = '/') {
  const client = createClient<paths>({ baseUrl: baseURL })
  // Inject the impersonation header on every management request when an admin
  // is impersonating. Requests happen after app mount, so pinia is active.
  // The raw-`fetch` test requests don't go through this client, so they stay
  // on the real identity.
  client.use({
    onRequest({ request }) {
      const store = useImpersonationStore()
      if (store.target) {
        request.headers.set('X-PicoTera-Impersonation-User-Id', String(store.target.userId))
      }
      return request
    },
  })
  // In oidc mode a 401 carries the login url. Sending the browser there is the
  // only way to recover, and the header is absent in the other auth modes, so
  // this cannot turn into a redirect loop.
  client.use({
    onResponse({ response }) {
      if (response.status === 401) {
        const loginUrl = response.headers.get(LOGIN_URL_HEADER)
        if (loginUrl) {
          const back = window.location.pathname + window.location.search
          window.location.assign(`${loginUrl}?redirect_to=${encodeURIComponent(back)}`)
        }
      }
      return response
    },
  })
  return client
}

export const api = createApi()

export const apiPlugin = {
  install(app: App, options?: { baseURL?: string }) {
    app.provide('api', options?.baseURL ? createApi(options.baseURL) : api)
  },
}
