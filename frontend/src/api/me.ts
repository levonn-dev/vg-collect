import type { paths } from './schema'
import { api, unwrap } from './client'

export type Me =
  paths['/api/me']['get']['responses']['200']['content']['application/json']
type Providers =
  paths['/api/auth/providers']['get']['responses']['200']['content']['application/json']
export type Identity =
  paths['/api/me/identities']['get']['responses']['200']['content']['application/json']['identities'][number]
type Identities =
  paths['/api/me/identities']['get']['responses']['200']['content']['application/json']
type UpdateMeRequest = NonNullable<
  paths['/api/me']['patch']['requestBody']
>['content']['application/json']

export async function fetchMe(): Promise<Me> {
  return unwrap(await api.GET('/api/me'))
}

export async function fetchProviders(): Promise<string[]> {
  const body: Providers = await unwrap(await api.GET('/api/auth/providers'))
  return body.providers
}

export async function logout(): Promise<void> {
  return unwrap<void>(await api.POST('/api/auth/logout'))
}

export async function updateMe(body: UpdateMeRequest): Promise<Me> {
  return unwrap(await api.PATCH('/api/me', { body }))
}

export async function fetchIdentities(): Promise<Identity[]> {
  const body: Identities = await unwrap(await api.GET('/api/me/identities'))
  return body.identities
}

export async function unlinkIdentity(id: string): Promise<void> {
  return unwrap<void>(
    await api.DELETE('/api/me/identities/{identityId}', { params: { path: { identityId: id } } }),
  )
}

export async function deleteAccount(): Promise<void> {
  return unwrap<void>(await api.DELETE('/api/me'))
}
