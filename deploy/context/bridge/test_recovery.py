"""Failure injection for retaining the last usable document revision."""
import os
import tempfile
import unittest
os.environ['CONTEXT_DB']=tempfile.mktemp(suffix='.db')
import app
class Store:
    def __init__(self):self.points={};self.fail=False
    def delete(self, identity, version=None):
        self.points={k:v for k,v in self.points.items() if not (k[0]==identity and (version is None or k[1]==version))}
    def put(self,obj,chunks):
        if self.fail:raise RuntimeError('index failure')
        self.points[(obj['id'],obj['version'])]=obj
    def search(self,query,scope,limit):
        return [{'record_id':k[0],'version':k[1],'chunk':0,'score':1} for k,obj in self.points.items() if obj['scope']==scope]
class Recovery(unittest.TestCase):
    def test_failed_update_keeps_previous_document_and_scope(self):
        db=app.connect();target=Store();namespace='a'*64
        def inp(**kw):return app.Input(contract=app.CONTRACT,kind='knowledge',namespace=namespace,**kw)
        p=app.preview(db,'qdrant',inp(content='版本一：每周三发布',title='约定'))
        identity=p['items'][0]['id'];app.apply(db,'qdrant',inp(token=p['token'],mode='skip',idempotency_key='first'),target)
        p=app.preview(db,'qdrant',inp(record_id=identity,record_version=1,content='版本二：每周五发布',title='约定'))
        target.fail=True
        with self.assertRaises(RuntimeError):app.apply(db,'qdrant',inp(token=p['token'],mode='replace',idempotency_key='second'),target)
        result=app.dispatch(db,'qdrant','search',inp(query='何时发布'),target)
        self.assertEqual(result['items'][0]['status'],'STALE');self.assertIn('每周三',result['items'][0]['snippet'])
        self.assertEqual(app.dispatch(db,'qdrant','search',inp(query='何时发布',scope='workspace:11111111-1111-4111-8111-111111111111'),target)['items'],[])
        self.assertEqual(app.dispatch(db,'qdrant','get',inp(record_id=identity),target)['active_version'],1)
        target.fail=False;app.apply(db,'qdrant',inp(token=p['token'],mode='replace',idempotency_key='second'),target)
        result=app.dispatch(db,'qdrant','search',inp(query='何时发布'),target)
        self.assertEqual(len(result['items']),1);self.assertIn('每周五',result['items'][0]['snippet']);self.assertEqual(result['items'][0]['version'],2)
        app.dispatch(db,'qdrant','delete',inp(record_id=identity,record_version=2),target)
        self.assertEqual(app.dispatch(db,'qdrant','search',inp(query='发布'),target)['items'],[])
        db.close()
    def test_graph_package_preserves_source_and_fact_times_requires_loss_confirmation(self):
        namespace='b'*64;db=app.connect();target=Store()
        graph={'nodes':[{'id':'entity1','name':'Alice'}], 'edges':[{'source':'entity1','target':'entity2','fact':'maintains Atlas','valid_at':'2026-10-20T00:00:00Z','invalid_at':None,'episodes':['source1']}], 'sources':[{'id':'source1','content':'confirmed fact'}]}
        entry={'id':'portable','title':'约定','content':'Alice maintains Atlas','type':'note','source':'本人确认','created_at':'2026-10-20T00:00:00Z','updated_at':'2026-10-21T00:00:00Z','graph':graph}
        package={'schema':'hc-memory-v1','backend':'graphiti','exported_at':app.now(),'entries':[entry],'sha256':app.digest([entry])}
        inp=app.Input(contract=app.CONTRACT,kind='memory',namespace=namespace,package=package)
        staged=app.preview(db,'mem0',inp);self.assertTrue(staged['warnings']);self.assertEqual(staged['items'][0]['graph'],graph)
        with self.assertRaises(app.HTTPException):app.apply(db,'mem0',app.Input(contract=app.CONTRACT,kind='memory',namespace=namespace,token=staged['token'],mode='skip'),target)
        app.apply(db,'mem0',app.Input(contract=app.CONTRACT,kind='memory',namespace=namespace,token=staged['token'],mode='skip',accept_loss=True),target)
        exported=app.dispatch(db,'mem0','export',app.Input(contract=app.CONTRACT,kind='memory',namespace=namespace),target)
        self.assertEqual(exported['entries'][0]['graph'],graph);self.assertEqual(exported['entries'][0]['created_at'],entry['created_at']);db.close()
if __name__=='__main__':unittest.main()
