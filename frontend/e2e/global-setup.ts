import { existsSync, readFileSync, readdirSync, rmSync } from 'node:fs'
import path from 'node:path'
import { request } from '@playwright/test'
import { AUTH_DIR, BASE_URL } from './fixtures'

// Teardown cleaning is not transactional: a run that dies mid-flight
// (crash, interrupt, a failed fixture) strands its minted e2e-*
// accounts and whatever they created. Every mint appends its name to a
// per-run manifest under .auth/ (see fixtures.ts), so the NEXT run can
// finish the job here before any test starts. Each stale name gets a
// fresh dev login - binding whatever account currently answers to the
// name - and a DELETE /api/me, which cascades collection data (entries,
// tags, views, and submissions via the entry cascade), the social
// graph, the login identities, and the user row. Community products
// are shared rather than user-owned, so no cascade reaches them: the
// ones tests mint carry an "e2e " name prefix and are swept separately
// below, after the accounts (and with them every referencing entry)
// are gone. A clean state returns before any request is made.
export default async function globalSetup() {
  if (!existsSync(AUTH_DIR)) return
  const stale = readdirSync(AUTH_DIR).filter((f) => /^minted-[a-z0-9]+\.log$/.test(f))
  if (stale.length === 0) return
  const names = [
    ...new Set(
      stale.flatMap((f) =>
        readFileSync(path.join(AUTH_DIR, f), 'utf8')
          .split('\n')
          .map((line) => line.trim())
          .filter(Boolean),
      ),
    ),
  ]
  console.log(`sweep: ${names.length} stale e2e account(s) left by ${stale.length} earlier run(s)`)
  const ctx = await request.newContext({ baseURL: BASE_URL })
  let removed = 0
  for (const [i, name] of names.entries()) {
    // The gateway budgets logins per IP per minute; a large backlog
    // paces itself under that budget instead of tripping 429s.
    if (i > 0 && i % 150 === 0) await new Promise((resolve) => setTimeout(resolve, 60_000))
    const login = await ctx.get(`/api/auth/login?provider=dev&user=${name}`)
    if (!login.ok()) {
      console.log(`sweep: login ${name} -> ${login.status()}`)
      continue
    }
    const del = await ctx.delete('/api/me')
    if (del.ok()) removed += 1
    else console.log(`sweep: delete ${name} -> ${del.status()}`)
  }
  const admin = await ctx.get('/api/auth/login?provider=dev&user=admin')
  if (admin.ok()) {
    const list = await ctx.get('/api/admin/products/community?limit=500')
    if (list.ok()) {
      const body = (await list.json()) as { products?: { id: string; name: string }[] }
      for (const p of body.products ?? []) {
        if (!p.name.startsWith('e2e ')) continue
        const del = await ctx.delete(`/api/admin/products/${p.id}`)
        console.log(`sweep: product "${p.name}" -> ${del.status()}`)
      }
    } else if (list.status() === 403) {
      // A stack where the admin fixture has not been granted the role
      // yet (task e2e grants it; a bare playwright run may not have).
      console.log('sweep: admin role not granted; community products skipped')
    }
  } else {
    console.log(`sweep: admin login -> ${admin.status()}; community products skipped`)
  }
  for (const f of stale) rmSync(path.join(AUTH_DIR, f))
  await ctx.dispose()
  console.log(`sweep: removed ${removed}/${names.length} stale account(s)`)
}
