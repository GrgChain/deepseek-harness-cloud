import {
  BadgeCheck,
  Brain,
  GitBranch,
  Heading,
  Mic,
  Puzzle,
  Repeat2,
  ScanSearch,
  Search,
  SquareTerminal,
  Store,
  Terminal,
  Users,
  type LucideIcon,
} from 'lucide-react';
import { type CatalogPlugin } from './plugin-catalog';

export const pluginIcons: Record<string, LucideIcon> = {
  'session-search': Search,
  'openviking-memory': Brain,
  'mem0-memory': Brain,
  'terminal-bundle': Terminal,
  'dsh-ssh': SquareTerminal,
  'agent-team-profile': Users,
  'voice-input-bundle': Mic,
  dshmarket: Store,
  'session-titles-bundle': Heading,
  'tool-worktree': GitBranch,
  'ralph-bundle': Repeat2,
  'inspector-profile': ScanSearch,
  'badge-skill-bundle': BadgeCheck,
};

export const iconForPlugin = (plugin: CatalogPlugin) =>
  pluginIcons[plugin.id] ??
  (plugin.category === '会话与记忆' ? Brain : plugin.category === '开发工具' ? Terminal : Puzzle);
