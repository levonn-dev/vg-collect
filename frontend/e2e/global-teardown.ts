import { existsSync, readdirSync, rmSync } from 'node:fs'
import path from 'node:path'
import { AUTH_DIR } from './fixtures'

// Reaching teardown means the run completed and every fixture teardown
// had its chance, so this run's manifests (see global-setup.ts) are done;
// a crashed run never gets here, so its manifests survive for the next sweep.
export default function globalTeardown() {
  if (!existsSync(AUTH_DIR)) return
  for (const f of readdirSync(AUTH_DIR)) {
    if (/^minted-[a-z0-9]+\.log$/.test(f)) rmSync(path.join(AUTH_DIR, f))
  }
}
