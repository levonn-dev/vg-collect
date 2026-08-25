import { appendFileSync, mkdirSync, rmSync } from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'
import { type APIRequestContext, type Page, test as base, expect, request } from '@playwright/test'

// Each worker mints one e2e-* user, reused via storageState (gateway
// budgets /api/auth/* to 240/IP/min dev, far tighter in prod). Tests
// mutating user-global state mint throwaway users via freshUser instead.

export const BASE_URL = process.env.BFF_URL ?? 'http://localhost:8090'
const runStamp = Date.now().toString(36)
let mintCounter = 0
// ESM has no __dirname; resolves storageState paths next to this file.
const __dirname = path.dirname(fileURLToPath(import.meta.url))
export const AUTH_DIR = path.join(__dirname, '.auth')
// Minted names land in a per-run manifest immediately, so a crashed run
// leaves a list global-setup.ts can clean up.
const mintedLog = path.join(AUTH_DIR, `minted-${runStamp}.log`)

export type TestUser = { name: string; id: string; api: APIRequestContext }

// Cookie name is constant, so logging in again on the same context
// just becomes the new user.
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

// Parses the id from the post-add URL to avoid a redundant fetch.
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

// Re-logs in first: a mid-flight delete/re-provision can leave this
// session pointing at a ghost account whose delete silently no-ops.
// Entries are swept first since they hold product references that
// block later product cleanup.
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
      // Removes the now-stale snapshot; global-setup sweeps whatever a
      // crashed run skips.
      rmSync(statePath, { force: true })
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
