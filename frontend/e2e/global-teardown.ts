import { existsSync, readdirSync, rmSync } from 'node:fs'
import path from 'node:path'
import { AUTH_DIR } from './fixtures'

// The minted manifests list this run's accounts for the NEXT run's
// sweep (see global-setup.ts). Reaching global teardown means the
// runner completed and every per-fixture teardown had its chance, so
// the manifests' job is done - each worker process wrote its own, so
// all of them go. A crashed or interrupted run never gets here; its
// manifests survive for the next run's global setup to act on.
export default function globalTeardown() {
  if (!existsSync(AUTH_DIR)) return
  for (const f of readdirSync(AUTH_DIR)) {
    if (/^minted-[a-z0-9]+\.log$/.test(f)) rmSync(path.join(AUTH_DIR, f))
  }
}
