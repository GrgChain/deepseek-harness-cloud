import {CancelledError} from '@tanstack/react-query';

export function isCancelledRead(error:unknown){return error instanceof CancelledError||(error instanceof DOMException&&error.name==='AbortError');}

export async function readDirectoryForTree<T>(read:()=>Promise<T>,isMounted:()=>boolean):Promise<T>{
 try{return await read();}catch(error){if(!isCancelledRead(error)||!isMounted())throw error;return read();}
}
