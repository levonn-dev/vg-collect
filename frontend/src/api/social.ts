import type { components, paths } from './schema'
import { api, unwrap } from './client'

export type ProfileCard = components['schemas']['ProfileCard']
export type ShelfCard = components['schemas']['ShelfCard']
export type ShelfPage = components['schemas']['ShelfPage']
export type ProfilePage = components['schemas']['ProfilePage']
export type FeedItem = components['schemas']['FeedItem']
export type Comment = components['schemas']['Comment']

type SharedEntryList = components['schemas']['SharedEntryList']
type CommentList = components['schemas']['CommentList']
type FeedPage = components['schemas']['FeedPage']
type ExplorePage = components['schemas']['ExplorePage']

export type FeedTab = paths['/api/feed']['get']['parameters']['query']['tab']
export type ExploreSort = paths['/api/explore']['get']['parameters']['query']['sort']

export async function fetchProfilePage(handle: string): Promise<ProfilePage> {
  return unwrap(await api.GET('/api/profiles/{handle}', { params: { path: { handle } } }))
}

export async function fetchShelfPage(handle: string, slug: string): Promise<ShelfPage> {
  return unwrap(
    await api.GET('/api/profiles/{handle}/shelves/{slug}', { params: { path: { handle, slug } } }),
  )
}

export async function fetchShelfEntries(shelfId: string, offset = 0): Promise<SharedEntryList> {
  const params = new URLSearchParams({ offset: String(offset) })
  return unwrap(
    await api.GET('/api/shelves/{shelfId}/entries', {
      params: { path: { shelfId } },
      querySerializer: () => params.toString(),
    }),
  )
}

export async function fetchShelfComments(shelfId: string, cursor?: string): Promise<CommentList> {
  const params = new URLSearchParams()
  if (cursor) params.set('cursor', cursor)
  return unwrap(
    await api.GET('/api/shelves/{shelfId}/comments', {
      params: { path: { shelfId } },
      querySerializer: () => params.toString(),
    }),
  )
}

export async function postComment(shelfId: string, body: string): Promise<Comment> {
  return unwrap(
    await api.POST('/api/shelves/{shelfId}/comments', {
      params: { path: { shelfId } },
      body: { body },
    }),
  )
}

// opts is an additive trailing bag (currently just keepalive, for a
// beacon-style commit on pagehide/unmount).
export async function deleteComment(
  id: string,
  opts?: { keepalive?: boolean },
): Promise<undefined> {
  return unwrap<undefined>(
    await api.DELETE('/api/comments/{commentId}', {
      params: { path: { commentId: id } },
      ...(opts?.keepalive === undefined ? {} : { keepalive: opts.keepalive }),
    }),
  )
}

export async function follow(userId: string): Promise<void> {
  return unwrap<void>(await api.PUT('/api/social/follows/{userId}', { params: { path: { userId } } }))
}

export async function unfollow(userId: string): Promise<void> {
  return unwrap<void>(await api.DELETE('/api/social/follows/{userId}', { params: { path: { userId } } }))
}

export async function like(shelfId: string): Promise<void> {
  return unwrap<void>(await api.PUT('/api/social/likes/{shelfId}', { params: { path: { shelfId } } }))
}

export async function unlike(shelfId: string): Promise<void> {
  return unwrap<void>(await api.DELETE('/api/social/likes/{shelfId}', { params: { path: { shelfId } } }))
}

// Appends &cursor= only when supplied, so the first page never
// carries a stale cursor.
export async function fetchFeed(tab: FeedTab, cursor?: string): Promise<FeedPage> {
  const params = new URLSearchParams({ tab })
  if (cursor) params.set('cursor', cursor)
  return unwrap(
    await api.GET('/api/feed', {
      params: { query: { tab, cursor } },
      querySerializer: () => params.toString(),
    }),
  )
}

export async function fetchExplore(sort: ExploreSort, offset = 0): Promise<ExplorePage> {
  const params = new URLSearchParams({ sort, offset: String(offset) })
  return unwrap(
    await api.GET('/api/explore', {
      params: { query: { sort, offset } },
      querySerializer: () => params.toString(),
    }),
  )
}

export async function searchUsers(q: string): Promise<ProfileCard[]> {
  const params = new URLSearchParams({ q })
  const body = await unwrap(
    await api.GET('/api/search/users', {
      params: { query: { q } },
      querySerializer: () => params.toString(),
    }),
  )
  return body.profiles
}
