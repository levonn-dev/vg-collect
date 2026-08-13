// confirmThen wraps the browser's blocking confirm dialog around a
// mutation trigger: every destructive or hard-to-reverse action across
// the admin, account, and entry surfaces (delete, remove, unlink,
// clear, hold, promote) asks the same yes/no question first and only
// runs the follow-up when the user accepts. The message is caller-
// supplied because each site's question is specific to what it is
// about to do; only the ask-then-run shape is shared.
export function confirmThen(message: string, run: () => void): void {
  if (window.confirm(message)) run()
}
