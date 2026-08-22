import type { paths } from './schema'
import { api, unwrap } from './client'

export type Submission =
  paths['/api/entries/{entryId}/submission']['get']['responses']['200']['content']['application/json']

export async function fetchSubmission(entryId: string): Promise<Submission> {
  return unwrap(
    await api.GET('/api/entries/{entryId}/submission', { params: { path: { entryId } } }),
  )
}

export async function createSubmission(entryId: string): Promise<Submission> {
  return unwrap(
    await api.POST('/api/entries/{entryId}/submission', { params: { path: { entryId } } }),
  )
}

// Cancel is a status flip server-side; the row persists for the
// rolling creation cap.
export async function cancelSubmission(entryId: string): Promise<void> {
  return unwrap<void>(
    await api.DELETE('/api/entries/{entryId}/submission', { params: { path: { entryId } } }),
  )
}

// Ack is a status flip server-side (stamps resolution_ack_at); the
// approval banner stops reappearing after this resolves.
export async function ackSubmissionResolution(entryId: string): Promise<void> {
  return unwrap<void>(
    await api.POST('/api/entries/{entryId}/submission/ack', { params: { path: { entryId } } }),
  )
}
