#!/usr/bin/env python3
"""主执行者亲自复核当前缺陷及 LFS 已签发请求在撤权后的行为。"""
import importlib.util
import json
import sys
import hashlib
import secrets
from pathlib import Path

def load(name,file):
    spec=importlib.util.spec_from_file_location(name,Path(__file__).with_name(file))
    m=importlib.util.module_from_spec(spec);spec.loader.exec_module(m);return m

a=load('a','user-permission-acceptance.py')
lfs=load('lfs','user-assets-acceptance.py')
cap=load('cap','user-capability-review.py')
sys.argv=[sys.argv[0],'personal-verification']
planner=a.S['用户']['role-15']; path='/repos/'+a.S['仓库']['sdk']['full_name']
status,body=a.api('GET',path+'/pulls',user=planner)
web_status,web_path,web_body=cap.web_status(planner,'/'+a.S['仓库']['sdk']['full_name']+'/pulls')
content_status,_=a.api('GET',path+'/contents/README.md',user=planner)
a.record('A04','主执行者复核Planner的PR API与网页差异',status==200,API状态=status,响应=body,网页HTTP状态=web_status,网页最终路径=web_path,代码内容API状态=content_status,最终评级='P2',评级理由='合法PR元数据API被误拒绝；网页可用且未证实数据泄漏、丢失或关键交付全面阻断，未满足本次P1严重影响门槛')
actor=lfs.S['用户'];repo=lfs.S['仓库']['lfs'];lfs.collaborator(repo,actor,True)
content=('主执行者真实LFS内容-'+secrets.token_hex(8)).encode();oid=hashlib.sha256(content).hexdigest()
status,body,_=lfs.lfs_batch(repo,actor,'upload',oid,len(content));assert status==200,body
upload=body['objects'][0]['actions']['upload'];us,_=lfs.action_request(upload,actor,content)
status,body,_=lfs.lfs_batch(repo,actor,'download',oid,len(content));assert status==200,body
download=body['objects'][0]['actions']['download'];ds,got=lfs.action_request(download,actor)
a.record('D10','主执行者LFS上传下载内容核对',us==200 and ds==200 and got==content,上传状态=us,下载状态=ds,OID=oid,内容SHA256=hashlib.sha256(got).hexdigest())
# 先取得新对象的上传能力，再撤权，验证已签发请求不能保留仓库权利。
nextcontent=('撤权后不可上传-'+secrets.token_hex(8)).encode();nextoid=hashlib.sha256(nextcontent).hexdigest()
status,body,_=lfs.lfs_batch(repo,actor,'upload',nextoid,len(nextcontent));assert status==200,body
old_upload=body['objects'][0]['actions']['upload']
lfs.collaborator(repo,actor,False)
try:
    status,body,_=lfs.lfs_batch(repo,actor,'download',oid,len(content))
    oldget,oldbody=lfs.action_request(download,actor)
    oldput,putbody=lfs.action_request(old_upload,actor,nextcontent)
    a.record('D10','撤权后新batch及先前签发LFS下载请求',status in [401,403,404] and oldget in [401,403,404],新batch状态=status,旧下载请求状态=oldget,返回字节数=len(oldbody),返回是否为私有内容=oldbody==content,OID=oid)
    checkstatus,checkbody,_=lfs.lfs_batch(repo,lfs.ADMIN,'download',nextoid,len(nextcontent))
    exists='actions' in checkbody.get('objects',[{}])[0]
    a.record('D10','撤权后先前签发LFS上传请求',oldput in [401,403,404] and not exists,旧上传请求状态=oldput,对象是否生成=exists,OID=nextoid,管理员独立见证状态=checkstatus)
finally:lfs.collaborator(repo,actor,True)
