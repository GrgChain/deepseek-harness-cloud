export function errorMessage(cause: unknown): string {
  return cause instanceof Error ? cause.message : '操作失败，请重试';
}
