import type { paths } from './schema'
import { getJSON, sendJSON } from './client'

export type Submission =
  paths['/api/entries/{entryId}/submission']['get']['responses']['200']['content']['application/json']

export function fetchSubmission(entryId: string): Promise<Submission> {
  return getJSON<Submission>(`/api/entries/${entryId}/submission`)
}

export function createSubmission(entryId: string): Promise<Submission> {
  return sendJSON<Submission>('POST', `/api/entries/${entryId}/submission`)
}

// Cancel is a status flip server-side; the row persists for the
// rolling creation cap.
export function cancelSubmission(entryId: string): Promise<void> {
  return sendJSON<void>('DELETE', `/api/entries/${entryId}/submission`)
}

// Ack is a status flip server-side (stamps resolution_ack_at); the
// approval banner stops reappearing after this resolves.
export function ackSubmissionResolution(entryId: string): Promise<void> {
  return sendJSON<void>('POST', `/api/entries/${entryId}/submission/ack`)
}
