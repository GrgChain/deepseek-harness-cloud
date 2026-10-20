import { useEffect, useState } from 'react';

// Each new notice gets its own lifetime, including repeated identical messages.
export function useTransientNotice(duration = 4000) {
  const [notice, setNotice] = useState<{ message: string; persistent: boolean } | null>(null);
  useEffect(() => {
    if (!notice || notice.persistent) return;
    const timer = window.setTimeout(() => setNotice(null), duration);
    return () => window.clearTimeout(timer);
  }, [notice, duration]);
  return [notice?.message ?? null, (message: string | null, persistent = false) => setNotice(message ? { message, persistent } : null)] as const;
}
