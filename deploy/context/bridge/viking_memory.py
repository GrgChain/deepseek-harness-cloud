"""Explicit memories stored/indexed by OpenViking; native root API stays internal."""
import hashlib
import os
import re
import json
import sqlite3
import secrets
import httpx

class VikingMemory:
    def __init__(self,namespace):
        self.namespace=namespace
        self.keys=sqlite3.connect('/data/viking-keys.db')
        self.keys.execute('CREATE TABLE IF NOT EXISTS tenant_keys(namespace TEXT PRIMARY KEY, key TEXT)')
        self.root_key=os.environ['HC_CONTEXT_TEST_KEY']
        saved=self.keys.execute('SELECT key FROM tenant_keys WHERE namespace=?',(namespace,)).fetchone()
        self.http=httpx.Client(base_url=os.environ['OPENVIKING_URL'],headers={'X-API-Key':saved[0] if saved else self.root_key,'X-OpenViking-Account':'hc_'+namespace,'X-OpenViking-User':'owner'},timeout=180,trust_env=False)
    def request(self,method,path,data=None,params=None,missing=False):
        response=self.http.request(method,path,json=data,params=params)
        if missing and response.status_code==404:return None
        response.raise_for_status();result=response.json()
        if result.get('status')!='ok':raise RuntimeError('OpenViking rejected operation')
        return result.get('result')
    def root(self,scope):
        return 'viking://user/owner/memories/harness_'+hashlib.sha256(scope.encode()).hexdigest()
    def uri(self,record):
        return self.root(record['scope'])+'/'+record['id']+'_v'+str(record['version'])+'.md'
    def verify(self):
        # Creating an account is administrative and remains inside this companion.
        if self.http.headers['X-API-Key'] != self.root_key:return '0.4.16'
        response=self.http.post('/api/v1/admin/accounts',json={'account_id':'hc_'+self.namespace,'admin_user_id':'owner','seed':self.namespace})
        if response.status_code not in (200,201,409):response.raise_for_status()
        result=response.json().get('result') or {}
        key=result.get('user_key')
        if not key:raise RuntimeError('OpenViking tenant credential unavailable')
        self.keys.execute('INSERT OR REPLACE INTO tenant_keys VALUES(?,?)',(self.namespace,key));self.keys.commit()
        self.http.headers['X-API-Key']=key
        return '0.4.16'
    def put(self,record,chunks,probe=False):
        response=self.http.post('/api/v1/fs/mkdir',json={'uri':self.root(record['scope'])})
        if response.status_code not in (200,201,409):response.raise_for_status()
        self.request('POST','/api/v1/content/write',{'uri':self.uri(record),'content':record['content'],'mode':'create','wait':True,'timeout':170,**({'processing_mode':'vectors_only'} if probe else {})})
    def search(self,query,scope,limit):
        result=self.request('POST','/api/v1/search/find',{'query':query,'target_uri':self.root(scope),'context_type':'memory','limit':limit,'level':2})
        hits=[]
        for hit in result.get('memories',[]):
            uri=hit.get('uri','');match=re.fullmatch(re.escape(self.root(scope))+r'/([a-zA-Z0-9_-]+)_v([0-9]+)\.md',uri)
            if match:hits.append({'record_id':match[1],'version':int(match[2]),'chunk':0,'score':hit.get('score',0)})
        return hits
    def delete(self,record_id,version=None):
        # List only the current namespace, never trust a caller-supplied native URI.
        tree=self.request('GET','/api/v1/fs/tree',params={'uri':'viking://user/owner/memories','level_limit':5},missing=True)
        def visit(value):
            if isinstance(value,dict):
                uri=value.get('uri','')
                if re.fullmatch(r'viking://user/owner/memories/harness_[a-f0-9]{64}/'+re.escape(record_id)+r'_v'+(str(version) if version is not None else '[0-9]+')+r'\.md',uri):
                    self.request('DELETE','/api/v1/fs',params={'uri':uri,'wait':'true','timeout':170},missing=True)
                for child in value.values():visit(child)
            elif isinstance(value,list):
                for child in value:visit(child)
        visit(tree)
    def close(self):
        self.keys.close()
        self.http.close()
