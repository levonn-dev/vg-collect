import { appendFileSync, mkdirSync } from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'
import { type APIRequestContext, type Page, test as base, expect, request } from '@playwright/test'

// Identity model: every run mints its own dev-provider fixture users
// (the auth service accepts any e2e-* handle), so tests never share
// accounts with each other, with earlier runs, or with a human on the
// same stack. Each worker logs in once and reuses the session through
// storageState - the gateway budgets /api/auth/* per IP per minute
// (240 on this dev stack, sized for the suite's own login traffic;
// production keeps it far tighter), and a per-test login would spend
// that budget for nothing.
// Tests that mutate user-global state (preferences, handle, linking,
// deletion, aggregate totals) mint throwaway users via freshUser;
// tests on the shared worker user assert only against their own
// stamped objects, never against counts or aggregates.

export const BASE_URL = process.env.BFF_URL ?? 'http://localhost:8090'
const runStamp = Date.now().toString(36)
let mintCounter = 0
// This package is ESM ("type": "module"), where __dirname does not
// exist; this is the standard replacement so the worker storageState
// path resolves next to this file regardless of the process's cwd.
const __dirname = path.dirname(fileURLToPath(import.meta.url))
export const AUTH_DIR = path.join(__dirname, '.auth')
// Every minted name lands in a per-run manifest the moment it exists,
// so a run that dies before its teardowns leaves a readable list of
// what it created; the next run's global setup finishes the cleanup
// from that list (see global-setup.ts).
const mintedLog = path.join(AUTH_DIR, `minted-${runStamp}.log`)

export type TestUser = { name: string; id: string; api: APIRequestContext }

// One GET drives the dev provider's redirect chain and seals the
// session cookie; the cookie name is constant, so logging in again on
// the same page or context simply becomes the new user.
export async function loginAs(page: Page, name: string) {
  await page.goto(`/api/auth/login?provider=dev&user=${name}`)
  await expect(page.getByRole('navigation', { name: 'Primary' })).toBeVisible()
}

export async function logout(page: Page) {
  await page.context().clearCookies()
}

// Accept the next native dialog (confirm or prompt) once.
export function acceptNext(page: Page, promptText?: string) {
  page.once('dialog', (d) => void d.accept(promptText))
}

// The app lands on /entries/<id> after an add; parsing the id back out
// of the URL (absolute or path-only) saves a redundant fetch when a
// test needs it for cleanup or a later assert.
export function entryIdFromURL(url: string): string {
  return new URL(url, BASE_URL).pathname.split('/').pop()!
}

async function mintContext(name: string): Promise<{ ctx: APIRequestContext; id: string }> {
  const ctx = await request.newContext({ baseURL: BASE_URL })
  const login = await ctx.get(`/api/auth/login?provider=dev&user=${name}`)
  if (!login.ok()) throw new Error(`dev login for ${name} failed: ${login.status()}`)
  const me = await ctx.get('/api/me')
  if (!me.ok()) throw new Error(`/api/me for ${name} failed: ${me.status()}`)
  const id = ((await me.json()) as { id: string }).id
  mkdirSync(AUTH_DIR, { recursive: true })
  appendFileSync(mintedLog, `${name}\n`)
  return { ctx, id }
}

// Teardown re-logs the subject in before anything else: a test may
// have deleted or re-provisioned its account mid-flight (the account
// deletion round trip does both), leaving this context's session
// pointing at a ghost account whose delete "succeeds" idempotently
// while the name's real account survives. A fresh login always binds
// the name's CURRENT account, so the entries sweep and the account
// delete below target what actually exists. The sweep itself stays:
// entries hold product references, and a stranded reference blocks
// later product cleanup in the admin flows.
async function deleteUser(ctx: APIRequestContext, name: string) {
  try {
    const login = await ctx.get(`/api/auth/login?provider=dev&user=${name}`)
    if (!login.ok()) console.log(`teardown: relogin ${name} -> ${login.status()}`)
    const res = await ctx.get('/api/entries?limit=500')
    if (res.ok()) {
      const body = (await res.json()) as { entries?: { id: string }[] }
      for (const entry of body.entries ?? []) await ctx.delete(`/api/entries/${entry.id}`)
    }
    const del = await ctx.delete('/api/me')
    if (!del.ok()) console.log(`teardown: delete ${name} -> ${del.status()}`)
  } catch (err) {
    console.log(`teardown: delete ${name} skipped:`, err)
  } finally {
    await ctx.dispose()
  }
}

type WorkerUser = { name: string; id: string; statePath: string }

export const test = base.extend<
  { api: APIRequestContext; user: { name: string; id: string }; freshUser: () => Promise<TestUser> },
  { workerUser: WorkerUser }
>({
  workerUser: [
    async ({}, use, workerInfo) => {
      const name = `e2e-w${workerInfo.parallelIndex}-${runStamp}`
      const { ctx, id } = await mintContext(name)
      const statePath = path.join(AUTH_DIR, `${name}.json`)
      mkdirSync(path.dirname(statePath), { recursive: true })
      await ctx.storageState({ path: statePath })
      await use({ name, id, statePath })
      await deleteUser(ctx, name)
    },
    { scope: 'worker' },
  ],
  storageState: async ({ workerUser }, use) => {
    await use(workerUser.statePath)
  },
  api: async ({ workerUser }, use) => {
    const ctx = await request.newContext({ baseURL: BASE_URL, storageState: workerUser.statePath })
    await use(ctx)
    await ctx.dispose()
  },
  user: async ({ workerUser }, use) => {
    await use({ name: workerUser.name, id: workerUser.id })
  },
  freshUser: async ({}, use, testInfo) => {
    const minted: { name: string; ctx: APIRequestContext }[] = []
    await use(async () => {
      const name = `e2e-${runStamp}-w${testInfo.parallelIndex}-${++mintCounter}`
      const { ctx, id } = await mintContext(name)
      minted.push({ name, ctx })
      return { name, id, api: ctx }
    })
    for (const m of minted) await deleteUser(m.ctx, m.name)
  },
})

export { expect }
