import { describe, expect, it } from 'vitest';
import { parameterErrors, toolParameters } from './tool-parameters';

describe('toolParameters and parameterErrors', () => {
  it('extracts flat primitive parameters and rejects composite or unsupported JSON schemas', () => {
    const params = toolParameters({
      type: 'object',
      required: ['query', 'limit'],
      properties: {
        query: { type: 'string', title: '搜索词', description: '关键词' },
        limit: { type: 'integer', minimum: 1, maximum: 50 },
        mode: { type: 'string', enum: ['fast', 'accurate'] },
        verbose: { type: 'boolean' },
      },
    });
    expect(params).toHaveLength(4);
    expect(params?.[0]).toMatchObject({
      name: 'query',
      label: '搜索词',
      type: 'string',
      required: true,
    });

    expect(toolParameters({ type: 'array' })).toBeNull();
    expect(toolParameters({ type: 'object', oneOf: [] })).toBeNull();
    expect(
      toolParameters({
        type: 'object',
        required: ['missing'],
        properties: { a: { type: 'string' } },
      }),
    ).toBeNull();
    expect(
      toolParameters({
        type: 'object',
        properties: { nested: { type: 'object' } },
      }),
    ).toBeNull();
  });

  it('validates required, integer, type, and enum constraints', () => {
    const params = toolParameters({
      type: 'object',
      required: ['query', 'count'],
      properties: {
        query: { type: 'string', title: '查询' },
        count: { type: 'integer', title: '数量' },
        mode: { type: 'string', title: '模式', enum: ['a', 'b'] },
      },
    })!;
    expect(parameterErrors(params, { count: 1.5, mode: 'c' })).toEqual({
      query: '请填写 查询',
      count: '数量 的类型或取值不符合要求',
      mode: '模式 的类型或取值不符合要求',
    });
    expect(parameterErrors(params, { query: 'ok', count: 3, mode: 'a' })).toEqual({});
  });
});
