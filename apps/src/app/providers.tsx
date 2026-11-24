import { useSyncExternalStore, type ReactNode } from 'react';
import { BrowserRouter } from 'react-router-dom';
import { MutationCache, QueryCache, QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { ConfigProvider } from 'antd';
import zhCN from 'antd/locale/zh_CN';
import { APIError } from '../api/error';
import { expireSession } from '../shared/hooks/use-expired-session';
import { portalTheme, uiColors } from './theme';

for (const [name, value] of Object.entries(uiColors)) {
  document.documentElement.style.setProperty(`--color-${name}`, value);
}

const queryClient = new QueryClient({
  queryCache: new QueryCache({
    onError: (error) => {
      if (error instanceof APIError && error.status === 401) expireSession(queryClient);
    },
  }),
  mutationCache: new MutationCache({
    onError: (error) => {
      if (error instanceof APIError && error.status === 401) expireSession(queryClient);
    },
  }),
  defaultOptions: {
    queries: { retry: false, refetchOnWindowFocus: true },
    mutations: { retry: false },
  },
});

const motionPreference = window.matchMedia('(prefers-reduced-motion: reduce)');
const subscribeToMotionPreference = (notify: () => void) => {
  motionPreference.addEventListener('change', notify);
  return () => motionPreference.removeEventListener('change', notify);
};

export function AppProviders({ children }: { children: ReactNode }) {
  const reducedMotion = useSyncExternalStore(
    subscribeToMotionPreference,
    () => motionPreference.matches,
  );
  return (
    <ConfigProvider
      locale={zhCN}
      theme={{ ...portalTheme, token: { ...portalTheme.token, motion: !reducedMotion } }}
    >
      <QueryClientProvider client={queryClient}>
        <BrowserRouter>{children}</BrowserRouter>
      </QueryClientProvider>
    </ConfigProvider>
  );
}
