import { existsSync, readFileSync, readdirSync, rmSync } from 'node:fs'
import path from 'node:path'
import { request } from '@playwright/test'
import { AUTH_DIR, BASE_URL } from './fixtures'

// Not transactional: a crashed run strands minted e2e-* accounts (.auth/
// manifest) and "e2e "-prefixed community products. DELETE /api/me
// cascades entries/tags/views/submissions and the social graph; products
// are swept separately below since they are not user-owned.
export default async function globalSetup() {
  if (!existsSync(AUTH_DIR)) return
  // Any e2e-w<idx>-<stamp>.json here predates this run (workers mint
  // their own after setup returns); orphaned by a crashed prior run,
  // swept unconditionally.
  const orphanedStates = readdirSync(AUTH_DIR).filter((f) => /^e2e-w\d+-[a-z0-9]+\.json$/.test(f))
  for (const f of orphanedStates) rmSync(path.join(AUTH_DIR, f))
  if (orphanedStates.length > 0) {
    console.log(`sweep: removed ${orphanedStates.length} orphaned worker session file(s)`)
  }

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
    // Gateway budgets logins per IP per minute; backlog paces itself to avoid 429s.
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
      // Admin role not granted yet (task e2e grants it; a bare playwright run may not).
      console.log('sweep: admin role not granted; community products skipped')
    }
  } else {
    console.log(`sweep: admin login -> ${admin.status()}; community products skipped`)
  }
  for (const f of stale) rmSync(path.join(AUTH_DIR, f))
  await ctx.dispose()
  console.log(`sweep: removed ${removed}/${names.length} stale account(s)`)
}
