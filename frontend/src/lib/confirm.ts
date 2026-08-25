// Shared ask-then-run shape for destructive actions; message is caller-specific.
export function confirmThen(message: string, run: () => void): void {
  if (window.confirm(message)) run()
}
