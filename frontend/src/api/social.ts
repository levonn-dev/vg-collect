import type { components } from './schema'
import { getJSON, sendJSON } from './client'

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

export type FeedTab = 'following' | 'you'
export type ExploreSort = 'recent' | 'top'

export function fetchProfilePage(handle: string): Promise<ProfilePage> {
  return getJSON<ProfilePage>(`/api/profiles/${handle}`)
}

export function fetchShelfPage(handle: string, slug: string): Promise<ShelfPage> {
  return getJSON<ShelfPage>(`/api/profiles/${handle}/shelves/${slug}`)
}

export function fetchShelfEntries(shelfId: string, offset = 0): Promise<SharedEntryList> {
  const params = new URLSearchParams({ offset: String(offset) })
  return getJSON<SharedEntryList>(`/api/shelves/${shelfId}/entries?${params.toString()}`)
}

export function fetchShelfComments(shelfId: string, cursor?: string): Promise<CommentList> {
  const params = new URLSearchParams()
  if (cursor) params.set('cursor', cursor)
  const qs = params.toString()
  return getJSON<CommentList>(`/api/shelves/${shelfId}/comments${qs ? `?${qs}` : ''}`)
}

export function postComment(shelfId: string, body: string): Promise<Comment> {
  return sendJSON<Comment>('POST', `/api/shelves/${shelfId}/comments`, { body })
}

export function deleteComment(id: string): Promise<undefined> {
  return sendJSON<undefined>('DELETE', `/api/comments/${id}`)
}

export function follow(userId: string): Promise<void> {
  return sendJSON<void>('PUT', `/api/social/follows/${userId}`)
}

export function unfollow(userId: string): Promise<void> {
  return sendJSON<void>('DELETE', `/api/social/follows/${userId}`)
}

export function like(shelfId: string): Promise<void> {
  return sendJSON<void>('PUT', `/api/social/likes/${shelfId}`)
}

export function unlike(shelfId: string): Promise<void> {
  return sendJSON<void>('DELETE', `/api/social/likes/${shelfId}`)
}

// fetchFeed appends &cursor= only when the caller supplies one, so
// the first page of a tab never carries a stale or empty cursor.
export function fetchFeed(tab: FeedTab, cursor?: string): Promise<FeedPage> {
  const params = new URLSearchParams({ tab })
  if (cursor) params.set('cursor', cursor)
  return getJSON<FeedPage>(`/api/feed?${params.toString()}`)
}

export function fetchExplore(sort: ExploreSort, offset = 0): Promise<ExplorePage> {
  const params = new URLSearchParams({ sort, offset: String(offset) })
  return getJSON<ExplorePage>(`/api/explore?${params.toString()}`)
}

export async function searchUsers(q: string): Promise<ProfileCard[]> {
  const params = new URLSearchParams({ q })
  const body = await getJSON<{ profiles: ProfileCard[] }>(`/api/search/users?${params.toString()}`)
  return body.profiles
}
