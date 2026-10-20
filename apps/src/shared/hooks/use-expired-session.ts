import { useEffect } from 'react';
import { useQueryClient, type QueryClient } from '@tanstack/react-query';
import { useNavigate } from 'react-router-dom';
import { APIError } from '../../api/error';

export function expireSession(cache: QueryClient) {
 cache.removeQueries({ predicate: query => query.queryKey[0] !== 'platform-status' });
 cache.setQueryData(['identity'], null);
}

export function useExpiredSession(error: unknown) {
 const cache = useQueryClient(); const navigate = useNavigate();
 useEffect(() => {
  if (!(error instanceof APIError) || error.status !== 401) return;
  expireSession(cache);
  navigate('/login', { replace: true });
 }, [error, cache, navigate]);
}
