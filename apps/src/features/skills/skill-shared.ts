import { SkillsClient } from '../../api/skills';

export const client = new SkillsClient();

export function size(bytes: number) {
  return bytes < 1024
    ? `${bytes} B`
    : `${new Intl.NumberFormat('zh-CN', { maximumFractionDigits: 1 }).format(bytes / 1024)} KiB`;
}

export function date(value: string | null) {
  return value ? new Date(value).toLocaleString('zh-CN', { hour12: false }) : '—';
}

export function download(blob: Blob, name: string) {
  const url = URL.createObjectURL(blob);
  const link = document.createElement('a');
  link.href = url;
  link.download = name;
  link.click();
  setTimeout(() => URL.revokeObjectURL(url), 30_000);
}
