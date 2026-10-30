import {CancelledError} from '@tanstack/react-query';
import {describe,expect,it,vi} from 'vitest';
import {isCancelledRead,readDirectoryForTree} from './directory-read';

describe('directory tree reads',()=>{
 it('retries a cancelled shared query while the page remains mounted',async()=>{
  const read=vi.fn().mockRejectedValueOnce(new CancelledError()).mockResolvedValueOnce({entries:['folder']});
  await expect(readDirectoryForTree(read,()=>true)).resolves.toEqual({entries:['folder']});
  expect(read).toHaveBeenCalledTimes(2);
 });
 it('does not retry after leaving the page and keeps real errors visible',async()=>{
  const cancelled=new CancelledError();const read=vi.fn().mockRejectedValue(cancelled);
  await expect(readDirectoryForTree(read,()=>false)).rejects.toBe(cancelled);expect(read).toHaveBeenCalledTimes(1);
  const failure=new Error('数据目录不可用');const failed=vi.fn().mockRejectedValue(failure);
  await expect(readDirectoryForTree(failed,()=>true)).rejects.toBe(failure);expect(failed).toHaveBeenCalledTimes(1);
  expect(isCancelledRead(new DOMException('Aborted','AbortError'))).toBe(true);
  expect(isCancelledRead(failure)).toBe(false);
 });
});
