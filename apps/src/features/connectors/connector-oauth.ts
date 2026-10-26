import type {CurrentUser} from '../../api/identity';
import type {Connector} from '../../api/connectors';
import {client} from './connector-shared';

export async function authorizeConnector(user:CurrentUser,connection:Connector){
 const result=await client.request<{authorization_url:string}>(user,`/${connection.id}/oauth`,{expected_version:connection.version});
 window.location.assign(result.authorization_url);
}
