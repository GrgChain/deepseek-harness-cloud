"""Failure injection for retaining the last usable document revision."""
import os
import tempfile
import unittest
from unittest.mock import patch
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
    def test_rebuild_preserves_previous_version_on_failure_and_is_retriable(self):
        db=app.connect();target=Store();namespace='5'*64
        def inp(**kw):return app.Input(contract=app.CONTRACT,kind='knowledge',namespace=namespace,**kw)
        staged=app.preview(db,'milvus',inp(title='报告',content='# 财务\n\n营业收入增长。'))
        identity=staged['items'][0]['id']
        app.apply(db,'milvus',inp(token=staged['token'],mode='skip',idempotency_key='original'),target)
        target.fail=True
        with self.assertRaises(RuntimeError):app.dispatch(db,'milvus','rebuild',inp(record_id=identity,record_version=1,idempotency_key='rebuild'),target)
        self.assertEqual(app.dispatch(db,'milvus','get',inp(record_id=identity),target)['active_version'],1)
        self.assertIn((identity,1),target.points)
        target.fail=False
        app.dispatch(db,'milvus','rebuild',inp(record_id=identity,record_version=1,idempotency_key='rebuild'),target)
        current=app.dispatch(db,'milvus','get',inp(record_id=identity),target)
        self.assertEqual((current['version'],current['active_version']),(2,2))
        self.assertEqual(app.dispatch(db,'milvus','rebuild',inp(record_id=identity,record_version=1,idempotency_key='rebuild'),target)['rebuilt'],True)
        with patch.object(target,'search',return_value=[]):
            hits=app.dispatch(db,'milvus','search',inp(query='财务',search_mode='keyword'),target)['items']
        self.assertEqual(hits[0]['section'],'财务')
        self.assertEqual(hits[0]['snippet'],'营业收入增长。')
        db.close()

    def test_hybrid_fuses_chinese_keyword_and_vector_results_with_threshold_and_scope(self):
        db=app.connect();target=Store();namespace='1'*64;other='2'*64
        def inp(ns=namespace,**kw):return app.Input(contract=app.CONTRACT,kind='knowledge',namespace=ns,**kw)
        def add(title,content,ns=namespace,scope='personal'):
            staged=app.preview(db,'qdrant',inp(ns,title=title,content=content,scope=scope))
            app.apply(db,'qdrant',inp(ns,token=staged['token'],mode='skip',idempotency_key=title,scope=scope),target)
            return staged['items'][0]['id']
        both=add('双路','国轩高科2026年营业收入增长')
        keyword=add('关键词','国轩高科营业收入数据')
        semantic=add('语义','公司经营业绩明显改善')
        add('其他范围','国轩高科营业收入',scope='workspace:11111111-1111-4111-8111-111111111111')
        add('其他连接','国轩高科营业收入',ns=other)
        matches=[{'record_id':semantic,'version':1,'chunk':0,'score':0.9},
                 {'record_id':both,'version':1,'chunk':0,'score':0.8},
                 {'record_id':keyword,'version':1,'chunk':0,'score':0.1}]
        keyword_rows=app.keyword_search.search(db,'qdrant',namespace,'personal','国轩高科收入情况',25)
        keyword_rank=next(i for i,row in enumerate(keyword_rows,1) if row['record_id']==both)
        with patch.object(target,'search',side_effect=AssertionError('full-text search must not call the vector service')):
            fulltext=app.dispatch(db,'qdrant','search',inp(query='国轩高科收入情况',search_mode='keyword'),target)['items']
        self.assertEqual({hit['id'] for hit in fulltext},{both,keyword})
        self.assertTrue(all('score' not in hit and 'fusion_score' not in hit for hit in fulltext))
        with patch.object(target,'search',return_value=matches):
            hits=app.dispatch(db,'qdrant','search',inp(query='国轩高科收入情况',search_mode='hybrid',min_similarity=0.5),target)['items']
        self.assertEqual(hits[0]['id'],both)
        self.assertEqual(set(hits[0]['match_methods']),{'keyword','semantic'})
        self.assertAlmostEqual(hits[0]['fusion_score'],1/(60+keyword_rank)+1/62)
        self.assertEqual({hit['id'] for hit in hits},{both,keyword,semantic})
        keyword_hit=next(hit for hit in hits if hit['id']==keyword)
        self.assertEqual(keyword_hit['match_methods'],['keyword'])
        self.assertNotIn('score',keyword_hit)
        self.assertEqual(next(hit for hit in hits if hit['id']==semantic)['match_methods'],['semantic'])
        db.close()

    def test_keyword_index_tracks_active_revision_rebuild_and_deletion(self):
        db=app.connect();target=Store();namespace='3'*64
        def inp(**kw):return app.Input(contract=app.CONTRACT,kind='knowledge',namespace=namespace,**kw)
        staged=app.preview(db,'milvus',inp(title='财报',content='旧版动力电池收入'))
        identity=staged['items'][0]['id']
        app.apply(db,'milvus',inp(token=staged['token'],mode='skip',idempotency_key='first'),target)
        def ids(query):
            with patch.object(target,'search',return_value=[]):
                return [hit['id'] for hit in app.dispatch(db,'milvus','search',inp(query=query,search_mode='hybrid'),target)['items']]
        self.assertEqual(ids('动力电池'),[identity])
        staged=app.preview(db,'milvus',inp(record_id=identity,record_version=1,title='财报',content='新版储能业务利润'))
        target.fail=True
        with self.assertRaises(RuntimeError):app.apply(db,'milvus',inp(token=staged['token'],mode='replace',idempotency_key='update'),target)
        self.assertEqual(ids('动力电池'),[identity])
        self.assertEqual(ids('储能'),[])
        target.fail=False
        app.dispatch(db,'milvus','rebuild',inp(record_id=identity,record_version=2),target)
        self.assertEqual(ids('动力电池'),[])
        self.assertEqual(ids('储能'),[identity])
        app.dispatch(db,'milvus','delete',inp(record_id=identity,record_version=2),target)
        self.assertEqual(ids('储能'),[])
        self.assertEqual(db.execute('SELECT count(*) FROM keyword_chunks WHERE backend=? AND namespace=?',('milvus',namespace)).fetchone()[0],0)
        db.close()

    def test_keyword_index_backfills_existing_active_documents_once(self):
        db=app.connect();target=Store();namespace='4'*64
        def inp(**kw):return app.Input(contract=app.CONTRACT,kind='knowledge',namespace=namespace,**kw)
        staged=app.preview(db,'qdrant',inp(title='历史文档',content='储能电池业务增长'))
        app.apply(db,'qdrant',inp(token=staged['token'],mode='skip',idempotency_key='old'),target)
        db.execute('DELETE FROM keyword_chunks');db.execute('DELETE FROM keyword_index_meta');db.commit()
        app.keyword_search.initialize(db,app.active,app.MEMORY)
        self.assertEqual(len(app.keyword_search.search(db,'qdrant',namespace,'personal','储能业务',10)),1)
        count=db.execute('SELECT count(*) FROM keyword_chunks').fetchone()[0]
        app.keyword_search.initialize(db,app.active,app.MEMORY)
        self.assertEqual(db.execute('SELECT count(*) FROM keyword_chunks').fetchone()[0],count)
        db.close()

    def test_similarity_threshold_filters_semantic_hits_but_preserves_exact_matches(self):
        db=app.connect();target=Store();namespace='9'*64
        def inp(**kw):return app.Input(contract=app.CONTRACT,kind='knowledge',namespace=namespace,**kw)
        for i,title in enumerate(('精确文档','高分文档','低分文档','缺失分数')):
            staged=app.preview(db,'milvus',inp(content=('关键字' if i==0 else '其他内容')+str(i),title=title))
            app.apply(db,'milvus',inp(token=staged['token'],mode='skip',idempotency_key=f'similarity-{i}'),target)
        rows=list(target.points)
        matches=[{'record_id':identity,'version':version,'chunk':0,**({} if i==3 else {'score':[0.1,0.8,0.3][i]})} for i,(identity,version) in enumerate(rows)]
        with patch.object(target,'search',return_value=matches):
            semantic=app.dispatch(db,'milvus','search',inp(query='关键字',search_mode='semantic',min_similarity=0.5),target)['items']
            self.assertEqual([hit['title'] for hit in semantic],['高分文档'])
            hybrid=app.dispatch(db,'milvus','search',inp(query='关键字',search_mode='hybrid',min_similarity=0.5),target)['items']
            self.assertEqual({hit['title'] for hit in hybrid},{'精确文档','高分文档'})
            self.assertNotIn('score',next(hit for hit in hybrid if hit['title']=='精确文档'))
            boundary=app.dispatch(db,'milvus','search',inp(query='关键字',search_mode='semantic',min_similarity=0.8),target)['items']
            self.assertEqual([hit['title'] for hit in boundary],['高分文档'])
        with patch.object(target,'search',return_value=matches[:3]):
            unfiltered=app.dispatch(db,'milvus','search',inp(query='关键字',search_mode='semantic'),target)['items']
            self.assertEqual(len(unfiltered),3)
        self.assertNotIn('min_similarity',app.operation_input(inp()))
        db.close()
    def test_knowledge_document_pages_search_all_records_and_isolate_scope(self):
        db=app.connect();namespace='d'*64;other='c'*64;backend='milvus'
        def add(ns,scope,identity,title,status='READY'):
            payload=app.canonical({'id':identity,'title':title,'content':'正文','type':'document','source':'测试','created_at':app.now(),'updated_at':app.now()}).decode()
            db.execute('INSERT INTO records (backend,namespace,id,scope,version,status,payload,chunks) VALUES (?,?,?,?,1,?,?,?)',(backend,ns,identity,scope,status,payload,'[]'))
        for i in range(1025):add(namespace,'personal',f'{i:04}',f'文档 {i:04}')
        add(namespace,'workspace:11111111-1111-4111-8111-111111111111','private','文档 私有')
        add(other,'personal','other','文档 其他用户')
        add(namespace,'personal','deleted','文档 删除','DELETED')
        db.commit()
        def inp(**kw):return app.Input(contract=app.CONTRACT,kind='knowledge',namespace=namespace,**kw)
        first=app.dispatch(db,backend,'list',inp(page=1,page_size=20),None)
        last=app.dispatch(db,backend,'list',inp(page=52,page_size=20),None)
        self.assertEqual((first['total'],len(first['items'])),(1025,20))
        self.assertEqual((last['total'],len(last['items'])),(1025,5))
        self.assertEqual(last['items'][-1]['title'],'文档 1024')
        found=app.dispatch(db,backend,'list',inp(page=1,page_size=10,query='1024'),None)
        self.assertEqual((found['total'],found['items'][0]['title']),(1,'文档 1024'))
        self.assertEqual(app.dispatch(db,backend,'list',inp(page=1,page_size=10,query='私有'),None)['total'],0)
        db.close()
    def test_search_limit_applies_to_every_knowledge_search_mode(self):
        db=app.connect();target=Store();namespace='e'*64
        def inp(**kw):return app.Input(contract=app.CONTRACT,kind='knowledge',namespace=namespace,**kw)
        for i in range(23):
            staged=app.preview(db,'qdrant',inp(content=f'星河项目内容 {i}',title=f'文档 {i}'))
            app.apply(db,'qdrant',inp(token=staged['token'],mode='skip',idempotency_key=f'limit-{i}'),target)
        for mode in ('semantic','exact','keyword','hybrid'):
            for limit in (5,10,20):
                with self.subTest(mode=mode,limit=limit):
                    hits=app.dispatch(db,'qdrant','search',inp(query='星河项目',search_mode=mode,limit=limit),target)['items']
                    self.assertEqual(len(hits),limit)
                    self.assertEqual(len({hit['id'] for hit in hits}),limit)
        db.close()

    def test_knowledge_search_modes_preserve_scope_and_active_version(self):
        db=app.connect();target=Store();namespace='f'*64
        def inp(**kw):return app.Input(contract=app.CONTRACT,kind='knowledge',namespace=namespace,**kw)
        first=app.preview(db,'qdrant',inp(content='星河项目每周三发布',title='发布'))
        app.apply(db,'qdrant',inp(token=first['token'],mode='skip',idempotency_key='search-first'),target)
        second=app.preview(db,'qdrant',inp(content='云端服务运行状态正常',title='状态'))
        app.apply(db,'qdrant',inp(token=second['token'],mode='skip',idempotency_key='search-second'),target)
        private=app.preview(db,'qdrant',inp(scope='workspace:11111111-1111-4111-8111-111111111111',content='星河项目其他范围',title='私有'))
        app.apply(db,'qdrant',inp(scope='workspace:11111111-1111-4111-8111-111111111111',token=private['token'],mode='skip',idempotency_key='search-private'),target)
        with patch.object(target,'search',side_effect=AssertionError('exact search must not call vector service')):
            exact=app.dispatch(db,'qdrant','search',inp(query='星河项目',search_mode='exact'),target)
        self.assertEqual([x['title'] for x in exact['items']],['发布'])
        hybrid=app.dispatch(db,'qdrant','search',inp(query='星河项目',search_mode='hybrid'),target)
        self.assertEqual(hybrid['items'][0]['title'],'发布')
        self.assertEqual({x['title'] for x in hybrid['items']},{'发布','状态'})
        self.assertEqual(len(hybrid['items']),2)
        self.assertEqual(hybrid['items'][0]['score'],1)
        limited=app.dispatch(db,'qdrant','search',inp(query='星河项目',search_mode='hybrid',limit=1),target)
        self.assertEqual(len(limited['items']),1)
        self.assertEqual(limited['items'][0]['score'],1)
        self.assertNotIn('score',exact['items'][0])
        updated=app.preview(db,'qdrant',inp(record_id=first['items'][0]['id'],record_version=1,content='星河项目新版',title='发布'))
        target.fail=True
        with self.assertRaises(RuntimeError):app.apply(db,'qdrant',inp(token=updated['token'],mode='replace',idempotency_key='search-update'),target)
        self.assertEqual(app.dispatch(db,'qdrant','search',inp(query='每周三',search_mode='exact'),target)['items'][0]['status'],'STALE')
        self.assertEqual(app.dispatch(db,'qdrant','search',inp(query='新版',search_mode='exact'),target)['items'],[])
        with self.assertRaises(app.HTTPException):app.dispatch(db,'qdrant','search',inp(query='星河',search_mode='unknown'),target)
        db.close()
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
