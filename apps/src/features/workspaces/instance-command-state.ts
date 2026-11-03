import { useQueryClient } from '@tanstack/react-query';
import { useLocation, useNavigate } from 'react-router-dom';
import { CommandsClient, type Acceptance, type CommandAction } from '../../api/commands';
import { APIError } from '../../api/error';
import { type CurrentUser } from '../../api/identity';
import { ProviderAccountsClient } from '../../api/provider-accounts';
import { expireSession, loginPath } from '../../shared/hooks/use-expired-session';

export const commands = new CommandsClient();

export const providerAccounts = new ProviderAccountsClient();

export function useCommandResult(user: CurrentUser) {
  const cache = useQueryClient();
  const navigate = useNavigate();
  const { pathname } = useLocation();
  return {
    onSuccess: (result: Acceptance) => {
      void cache.invalidateQueries({ queryKey: ['resources', user.id] });
      void cache.invalidateQueries({ queryKey: ['settings', user.id] });
      navigate('/app/instances/' + result.instance_id);
    },
    onError: (error: Error) => {
      if (error instanceof APIError && error.status === 401) {
        expireSession(cache);
        navigate(loginPath(pathname), { replace: true });
      }
    },
  };
}

export function pending(user: CurrentUser, action: CommandAction, id = '') {
  try {
    return commands.hasPending(user, action, id);
  } catch {
    return false;
  }
}
