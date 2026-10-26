import { useEffect } from 'react';
import { useQueryClient, type QueryClient } from '@tanstack/react-query';
import { useLocation, useNavigate } from 'react-router-dom';
import { APIError } from '../../api/error';

// Match the BFF's return-path rules; never replay query-driven actions.
export function loginReturnPath(path: string | null) {
 return path && path.length <= 256 && /^\/app\/[A-Za-z0-9/_-]*$/.test(path) && !path.includes('//') ? path : '/app/instances';
}

export function loginPath(path: string) {
 return '/login?return_to=' + encodeURIComponent(loginReturnPath(path));
}

export function expireSession(cache: QueryClient) {
 cache.removeQueries({ predicate: query => query.queryKey[0] !== 'platform-status' });
 cache.setQueryData(['identity'], null);
}

export function useExpiredSession(error: unknown) {
 const cache = useQueryClient(); const navigate = useNavigate(); const { pathname } = useLocation();
 useEffect(() => {
  if (!(error instanceof APIError) || error.status !== 401) return;
  expireSession(cache);
  navigate(loginPath(pathname), { replace: true });
 }, [error, cache, navigate, pathname]);
}
