import { describe, expect, it } from 'vitest';
import { APIError } from './error';

describe('APIError', () => {
  it('maps critical backend error codes to actionable Chinese messages', () => {
    const expected: Record<string, string> = {
      NAME_CONFLICT: '工作空间名称已被使用，请更换其他名称',
      STORAGE_MISSING: '原数据资产不可用，请联系管理员核实',
      NOT_FOUND: '请求的资源不存在或无权访问',
      INVALID_CURSOR: '分页游标无效或已过期，请刷新列表',
      MODEL_CATALOG_LIMIT: '已达到模型目录数量上限',
      PAYLOAD_TOO_LARGE: '上传内容超过大小限制',
      CONNECTOR_CONFLICT: '连接器存在冲突，请刷新后重试',
      NATIVE_AUTH_FAILED: '工作空间认证交接失败，请重试',
      UPSTREAM_UNAVAILABLE: '工作空间服务暂不可用，请稍后重试',
    };
    for (const [code, message] of Object.entries(expected)) {
      const err = new APIError(400, code);
      expect(err.message).toBe(message);
    }
  });

  it('falls back cleanly for unknown error codes', () => {
    const err = new APIError(500, 'SOME_FUTURE_ERROR');
    expect(err.message).toBe('请求未完成，请检查平台状态后重新加载');
  });
});
