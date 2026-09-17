#!/usr/bin/env python3
"""检查隔离镜像中审批主体表单，报告不包含登录凭据。"""
import urllib.request,urllib.parse,http.cookiejar,re,json,sys
from pathlib import Path
ROOT=Path(__file__).resolve().parents[1]
o=urllib.request.build_opener(urllib.request.ProxyHandler({}),urllib.request.HTTPCookieProcessor(http.cookiejar.CookieJar()))
b='http://127.0.0.1:3520'
html=o.open(b+'/user/login').read().decode()
csrf=""
p=(ROOT/'work/navigation-review/combined-upgrade/test-password').read_text().strip()
o.open(b+'/user/login',urllib.parse.urlencode({'_csrf':csrf,'user_name':'install-check','password':p}).encode()).read()
html=o.open(b+'/inherit-repro/child/private-code/settings/pulls').read().decode()
result={'原生团队选择器存在':'data-rule-teams' in html,'真实群组选择器存在':'data-rule-groups' in html,'子组完整路径存在':'inherit-repro/child' in html}
(ROOT/'work/group-access-regression'/('approval-'+sys.argv[1]+'.json')).write_text(json.dumps(result,ensure_ascii=False,indent=2))
print(json.dumps(result,ensure_ascii=False))
