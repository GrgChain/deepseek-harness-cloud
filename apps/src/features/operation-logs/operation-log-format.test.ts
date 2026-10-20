import { expect, test } from 'vitest';
import { operationLogChanges, operationLogSummary } from './operation-log-format';
import type { OperationLog } from '../../api/resources';
const base:OperationLog={id:'event',actor_id:null,actor_name:'',owner_id:null,action:'USER_QUOTA_UPDATE',outcome:'SUCCEEDED',timestamp:'2026-10-15T00:00:00Z',resource_id:null,resource_name:'',resource_type:'user',trace_id:'request',details:{}};
test('quota operation log changes retain native resource units and handle omitted old values',()=>{
 const event={...base,details:{before:{max_instances:1,cpu_millicores:500},after:{max_instances:2,cpu_millicores:2000,memory_bytes:4*2**30}}};
 expect(operationLogChanges(event)).toEqual([{key:'max_instances',label:'实例数量',before:'1 个',after:'2 个'},{key:'cpu_millicores',label:'CPU',before:'0.5 vCPU',after:'2 vCPU'},{key:'memory_bytes',label:'内存',before:'未记录',after:'4 GiB'}]);
 expect(operationLogSummary(event)).toContain('CPU 0.5 vCPU → 2 vCPU');
});
test('legacy events have no invented changes and retain unknown diagnostic codes',()=>{
 expect(operationLogChanges(base)).toEqual([]);expect(operationLogSummary(base)).toBe('');
 expect(operationLogSummary({...base,details:{reason_code:'NEW_FAILURE'}})).toBe('原因：NEW_FAILURE');
});
