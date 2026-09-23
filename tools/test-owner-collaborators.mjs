/** 隔离服务的成员验收：凭据仅从标准输入读取，报告只保留脱敏结果。 */
import assert from 'node:assert/strict';
import {readFileSync, writeFileSync, appendFileSync, mkdirSync, mkdtempSync, rmSync} from 'node:fs';
import {join} from 'node:path';
import {tmpdir} from 'node:os';
import {execFileSync} from 'node:child_process';
import {randomBytes} from 'node:crypto';
import {chromium, firefox, expect} from '@playwright/test';

const config = JSON.parse(readFileSync(0, 'utf8'));
const base = config.url.replace(/\/$/, '');
assert.ok(['localhost', '127.0.0.1'].includes(new URL(base).hostname), '只允许本机转发的隔离验收服务');
const run = `members-${Date.now()}`;
const output = join(config.output, run);
mkdirSync(output, {recursive:true, mode:0o700});
const temporary = mkdtempSync(join(tmpdir(), 'gitea-members-'));
const secrets = [config.admin.password];
const records = [];
const prefix = `m${Date.now().toString(36)}`;
const roles = [['guest',10],['planner',15],['reporter',20],['developer',30],['maintainer',40],['owner',50]];
const users = {};
let browser;
function clean(value) {
 let text = String(value);
 for (const secret of secrets) if (secret) text = text.replaceAll(secret, '[已脱敏]');
 return text.replace(/(https?:\/\/)[^/\s]+:[^/\s]+@/g, '$1[已脱敏]@');
}
async function check(ids, name, fn) {
 const start = Date.now();
 const row = {编号:ids.split(' '), 场景:name, 状态:'通过'};
 try {await fn();} catch (error) {row.状态='失败';row.原因=clean(error.message);}
 row.耗时毫秒=Date.now()-start;
 records.push(row);
 appendFileSync(join(output,'events.jsonl'),JSON.stringify(row)+'\n',{mode:0o600});
 console.log(`${ids} ${name}：${row.状态}${row.原因?'；'+row.原因:''}`);
 return row.状态==='通过';
}
function basic(user) {return 'Basic '+Buffer.from(user.username+':'+user.password).toString('base64');}
async function request(actor, method, path, body, expected=200) {
 const headers = {'Content-Type':'application/json'};
 if (actor) headers.Authorization=actor.token?'token '+actor.token:basic(actor);
 const response=await fetch(base+path,{method,headers,body:body===undefined?undefined:JSON.stringify(body),signal:AbortSignal.timeout(60000),redirect:'manual'});
 const text=await response.text();
 appendFileSync(join(output,'requests.jsonl'),JSON.stringify({方法:method,路径:path,预期状态:expected,实际状态:response.status})+'\n',{mode:0o600});
 assert.equal(response.status,expected,`${method} ${path}：预期 ${expected}，实际 ${response.status}；${clean(text.slice(0,450))}`);
 return text?JSON.parse(text):null;
}
const admin={...config.admin};
async function token(user) {
 const result=await request(user,'POST',`/api/v1/users/${user.username}/tokens`,{name:prefix,scopes:['all']},201);
 user.token=result.sha1;secrets.push(user.token);
}
async function createUser(name, extra={}) {
 const user={username:prefix+'-'+name,password:randomBytes(24).toString('base64url')};secrets.push(user.password);
 const row=await request(admin,'POST','/api/v1/admin/users',{username:user.username,email:user.username+'@example.invalid',password:user.password,must_change_password:false,...extra},201);
 user.id=row.id;await token(user);users[name]=user;return user;
}
async function view(actor,repo,query='') {return request(actor,'GET',`/api/v1/governance/repositories/${repo.id}/members/view${query}`);}
async function grant(repo,user,role,actor=users.creator,expires=0) {
 const current=await view(actor,repo);
 return request(actor,'PUT',`/api/v1/governance/repositories/${repo.id}/members/${user.id}`,{role,expires_unix:expires,revision:current.revision},204);
}
async function revoke(repo,user,actor=users.creator) {
 const current=await view(actor,repo);
 return request(actor,'DELETE',`/api/v1/governance/repositories/${repo.id}/members/${user.id}?revision=${current.revision}`,undefined,204);
}
async function createRepo(name,actor=users.creator,extra={}) {
 return request(actor,'POST','/api/v1/user/repos',{name:prefix+'-'+name,private:true,auto_init:true,...extra},201);
}
function repoAPI(repo) {return '/api/v1/repos/'+repo.full_name;}
async function login(context,user) {
 const response=await context.request.post(base+'/user/login',{form:{user_name:user.username,password:user.password},maxRedirects:0});
 assert.ok([302,303].includes(response.status()),'登录必须成功，实际 '+response.status());
}
function git(args,actor,ssh=false,expected=true) {
 const environment={...process.env,GIT_TRACE:'0',GIT_TRACE_CURL:'0',GIT_CURL_VERBOSE:'0',GIT_TERMINAL_PROMPT:'0',GIT_AUTHOR_NAME:'成员验收',GIT_AUTHOR_EMAIL:'acceptance@example.invalid',GIT_COMMITTER_NAME:'成员验收',GIT_COMMITTER_EMAIL:'acceptance@example.invalid'};
 if (actor) {
  environment.GIT_CONFIG_COUNT='1';environment.GIT_CONFIG_KEY_0='http.extraHeader';
  environment.GIT_CONFIG_VALUE_0='Authorization: Basic '+Buffer.from(actor.username+':'+actor.token).toString('base64');
  if (ssh) environment.GIT_SSH_COMMAND=`ssh -i '${actor.key}' -o IdentitiesOnly=yes -o BatchMode=yes -o UserKnownHostsFile='${temporary}/known_hosts'`;
 }
 try {
  const result=execFileSync('git',args,{env:environment,encoding:'utf8',timeout:60000,stdio:['ignore','pipe','pipe']});
  assert.ok(expected,'预期拒绝的 Git 操作实际成功');return result.trim();
 } catch(error) {
  if (expected || error.code==='ERR_ASSERTION') throw error;
  const message=String(error.stderr||error.message);
  assert.match(message,/permission denied|not found|not exist|403|401|not authorized|access denied/i,'必须是权限拒绝，不能把网络错误当作通过');
  return null;
 }
}
async function apiCases(repo) {
 for (const [name,role] of roles) {
  const user=users[name];await grant(repo,user,role);
  await check('VIEW-01 AUTH-01 AUTH-02 AUTH-05',`${name} 的安全成员视图与写权限`,async()=>{
   const current=await view(user,repo);assert.equal(current.can_manage,role>=40);assert.equal(current.can_own,role===50);
   const self=current.members.find(x=>x.user_id===user.id);assert.equal(self.role,role);assert.equal(self.sources.length,1);
   const option={role:30,revision:current.revision};
   await request(user,'PUT',`/api/v1/governance/repositories/${repo.id}/members/${users.target.id}`,option,role>=40?204:404);
   if (role>=40) await revoke(repo,users.target);
  });
 }
 await check('AUTH-03 AUTH-06','Maintainer 经新旧接口不能修改 Owner',async()=>{
  for (const change of [{role:30},{role:50,expires_unix:Math.floor(Date.now()/1000)+3600}]) {
   const current=await view(users.maintainer,repo);
   await request(users.maintainer,'PUT',`/api/v1/governance/repositories/${repo.id}/members/${users.owner.id}`,{...change,revision:current.revision},404);
  }
  const current=await view(users.maintainer,repo);
  await request(users.maintainer,'DELETE',`/api/v1/governance/repositories/${repo.id}/members/${users.owner.id}?revision=${current.revision}`,undefined,404);
  await request(users.maintainer,'PUT',repoAPI(repo)+'/collaborators/'+users.owner.username,{permission:'read'},404);
  assert.equal((await view(users.owner,repo)).can_own,true);
 });
 await check('AUTH-08 AUTH-09','项目 Owner 能归档恢复但不能管理兄弟项目',async()=>{
  await request(users.maintainer,'PATCH',repoAPI(repo),{archived:true},404);
  await request(users.owner,'PATCH',repoAPI(repo),{archived:true});
  await request(users.owner,'PATCH',repoAPI(repo),{archived:false});
  const sibling=await createRepo('sibling');
  await request(users.owner,'PATCH',repoAPI(sibling),{archived:true},404);
 });
 await check('SEC-01 SEC-04','匿名和无授权账号不能枚举私有成员',async()=>{
  const path=`/api/v1/governance/repositories/${repo.id}/members/view`;
  await request(null,'GET',path,undefined,404);await request(users.outsider,'GET',path,undefined,404);
  const safe=await view(users.guest,repo);
  assert.doesNotMatch(JSON.stringify(safe),/"email"|"password"|"token"/);
 });
 await check('AUTH-11','非法项目角色与组织账号不落库',async()=>{
  for (const role of [5,777]) {
   const before=await view(users.creator,repo);
   await request(users.creator,'PUT',`/api/v1/governance/repositories/${repo.id}/members/${users.target.id}`,{role,revision:before.revision},400);
   const after=await view(users.creator,repo);assert.equal(after.revision,before.revision);assert.ok(!after.members.some(x=>x.user_id===users.target.id));
  }
 });
 await check('SEC-05','不足范围的 Token 不能读取或修改治理成员',async()=>{
  const limited=await request({username:users.creator.username,password:users.creator.password},'POST',`/api/v1/users/${users.creator.username}/tokens`,{name:prefix+'-limited',scopes:['read:user']},201);
  secrets.push(limited.sha1);
  const actor={...users.creator,token:limited.sha1};
  await request(actor,'GET',`/api/v1/governance/repositories/${repo.id}/members/view`,undefined,403);
  await request(actor,'PUT',`/api/v1/governance/repositories/${repo.id}/members/${users.target.id}`,{role:50,revision:0},403);
 });
 await check('AUTH-10','Guest 和 Planner 不能读取代码；Planner 可以读取 PR',async()=>{
  for(const name of ['guest','planner']) await request(users[name],'GET',repoAPI(repo)+'/contents/',undefined,403);
  await request(users.planner,'GET',repoAPI(repo)+'/pulls');
 });
 await check('SEC-06','非法 JSON 被拒绝且不改变授权',async()=>{
  const before=await view(users.creator,repo);
  const response=await fetch(base+`/api/v1/governance/repositories/${repo.id}/members/${users.target.id}`,{method:'PUT',headers:{Authorization:'token '+users.creator.token,'Content-Type':'application/json'},body:'{"role":',signal:AbortSignal.timeout(10000)});
  assert.equal(response.status,422);
  assert.equal((await view(users.creator,repo)).revision,before.revision);
 });
 await check('PRE-01 PRE-03 PRE-04','预览不落库，其他来源变化后拒绝旧预览',async()=>{
  const before=await view(users.creator,repo);
  const change={kind:'member',member:{user_id:users.target.id,role:30,revision:before.revision}};
  const preview=await request(users.creator,'POST',`/api/v1/governance/repositories/${repo.id}/members/preview`,change);
  assert.equal((await view(users.creator,repo)).revision,before.revision);
  await grant(repo,users.outsider,20);
  const updated=await view(users.creator,repo);
  await request(users.creator,'PUT',`/api/v1/governance/repositories/${repo.id}/members/${users.target.id}`,{role:30,revision:updated.revision,preview_token:preview.token},409);
  await revoke(repo,users.outsider);
 });
 await check('PRE-02','预览绑定用户、角色和期限，不能提交不同的影响',async()=>{
  const before=await view(users.creator,repo);
  const preview=await request(users.creator,'POST',`/api/v1/governance/repositories/${repo.id}/members/preview`,{kind:'member',member:{user_id:users.target.id,role:30,revision:before.revision}});
  for(const option of [{user_id:users.target.id,role:50},{user_id:users.outsider.id,role:30},{user_id:users.target.id,role:30,expires_unix:Math.floor(Date.now()/1000)+3600}]) {
   await request(users.creator,'PUT',`/api/v1/governance/repositories/${repo.id}/members/${option.user_id}`,{...option,revision:before.revision,preview_token:preview.token},409);
  }
  assert.equal((await view(users.creator,repo)).revision,before.revision);
 });
 await check('OWN-03 OWN-04','唯一永久 Owner 不能停用或禁登，先接替后允许',async()=>{
  const custodian=await createUser('custodian');
  const owned=await createRepo('custodian-private',custodian);
  for(const option of [{active:false},{prohibit_login:true}]) await request(admin,'PATCH','/api/v1/admin/users/'+custodian.username,option,409);
  await grant(owned,users.owner,50,custodian);
  await request(admin,'PATCH','/api/v1/admin/users/'+custodian.username,{active:false});
  assert.equal((await view(users.owner,owned)).can_own,true);
  await request(admin,'PATCH','/api/v1/admin/users/'+custodian.username,{active:true});
 });
 const group=await request(users.creator,'POST','/api/v1/governance/groups',{name:'最低权限验收',path:prefix+'-minimal-group',visibility:2},201);
 const grouped=await request(users.creator,'POST',`/api/v1/orgs/${group.compatibility_name}/repos`,{name:prefix+'-grouped',private:true,auto_init:true},201);
 let state=await request(users.creator,'GET',`/api/v1/governance/groups/${group.id}`);
 await request(users.creator,'PUT',`/api/v1/governance/groups/${group.id}/members/${users.minimal.id}`,{role:5,revision:state.revision},204);
 await check('NAV-06','Minimal Access 拒绝、独立 Guest 允许、撤权后再次拒绝',async()=>{
  const path=`/api/v1/governance/repositories/${grouped.id}/members/view`;
  await request(users.minimal,'GET',path,undefined,404);
  await grant(grouped,users.minimal,10);await view(users.minimal,grouped);await revoke(grouped,users.minimal);
  await request(users.minimal,'GET',path,undefined,404);
 });
 await check('NAV-07 SRC-11 SEC-02','公开基础访问与私有共享成员隐私',async()=>{
  const publicRepo=await createRepo('public',users.creator,{private:false});
  state=await request(users.creator,'GET',`/api/v1/governance/groups/${group.id}`);
  await request(users.creator,'PUT',`/api/v1/governance/groups/${group.id}/members/${users.outsider.id}`,{role:30,revision:state.revision},204);
  const v=await view(users.creator,publicRepo);
  await request(users.creator,'PUT',`/api/v1/governance/repositories/${publicRepo.id}/shares/${group.id}`,{max_role:30,revision:v.revision},204);
  const anon=await view(null,publicRepo,'?q='+users.outsider.username);
  assert.equal(anon.members.length,0);assert.ok(!JSON.stringify(anon).includes(group.full_path));
  await view(users.minimal,publicRepo);
 });
 return grouped;
}
async function gitCases(repo) {
 const http=base+'/'+repo.full_name+'.git';
 const ssh=`ssh://git@127.0.0.1:${config.sshPort}/${repo.full_name}.git`;
 writeFileSync(join(temporary,'known_hosts'),execFileSync('ssh-keyscan',['-p',String(config.sshPort),'127.0.0.1'],{timeout:20000,stdio:['ignore','pipe','ignore']}),{mode:0o600});
 const working=join(temporary,'working');git(['clone',http,working],users.creator);
 git(['-C',working,'commit','--allow-empty','-m','成员权限验收'],users.creator);
 const expectedSHA=git(['-C',working,'rev-parse','HEAD'],users.creator);
 for (const [name,role] of roles) {
  const actor=users[name];actor.key=join(temporary,name+'-key');
  execFileSync('ssh-keygen',['-q','-t','ed25519','-N','','-f',actor.key]);
  await request(actor,'POST','/api/v1/user/keys',{title:'隔离成员验收',key:readFileSync(actor.key+'.pub','utf8')},201);
  for (const transport of ['http','ssh']) await check('FLOW-01 FLOW-02',`${name} ${transport} 真实克隆读取推送`,async()=>{
   const url=transport==='http'?http:ssh;const viaSSH=transport==='ssh';
   git(['ls-remote',url],actor,viaSSH,role>=20);
   git(['clone',url,join(temporary,name+'-'+transport)],actor,viaSSH,role>=20);
   const branch='accept-'+name+'-'+transport;
   git(['-C',working,'push',url,'HEAD:refs/heads/'+branch],actor,viaSSH,role>=30);
   const result=await request(users.creator,'GET',repoAPI(repo)+'/branches/'+branch,undefined,role>=30?200:404);
   if (role>=30) assert.equal(result.commit.id,expectedSHA);
  });
 }
 await check('FLOW-03','撤权后复用同一 PAT、SSH key 和本地克隆',async()=>{
  await revoke(repo,users.owner);
  for (const viaSSH of [false,true]) {
   git(['ls-remote',viaSSH?ssh:http],users.owner,viaSSH,false);
   git(['-C',working,'push',viaSSH?ssh:http,'HEAD:refs/heads/revoked'],users.owner,viaSSH,false);
  }
  await request(users.owner,'GET',`/api/v1/governance/repositories/${repo.id}/members/view`,undefined,404);
  await grant(repo,users.owner,50);
 });
 await check('FLOW-02 FLOW-04 SRC-02 SRC-06','共享 Owner 的管理、降级、独立授权保留及真实 Git',async()=>{
  const group=await request(users.creator,'POST','/api/v1/governance/groups',{name:'共享所有者验收',path:prefix+'-shared-group',visibility:2},201);
  await request(users.creator,'PUT',`/api/v1/governance/groups/${group.id}/members/${users.outsider.id}`,{role:50,revision:group.revision},204);
  async function share(role) {
   const current=await view(users.creator,repo);
   await request(users.creator,'PUT',`/api/v1/governance/repositories/${repo.id}/shares/${group.id}`,{max_role:role,revision:current.revision},204);
  }
  await share(50);assert.equal((await view(users.outsider,repo)).can_own,true);
  await request(users.outsider,'PATCH',repoAPI(repo),{archived:true});
  await request(users.outsider,'PATCH',repoAPI(repo),{archived:false});
  users.outsider.key=join(temporary,'shared-key');
  execFileSync('ssh-keygen',['-q','-t','ed25519','-N','','-f',users.outsider.key]);
  await request(users.outsider,'POST','/api/v1/user/keys',{title:'隔离共享验收',key:readFileSync(users.outsider.key+'.pub','utf8')},201);
  for(const viaSSH of [false,true])git(['-C',working,'push',viaSSH?ssh:http,'HEAD:refs/heads/shared-'+(viaSSH?'ssh':'http')],users.outsider,viaSSH);
  await share(30);assert.equal((await view(users.outsider,repo)).can_own,false);
  await request(users.outsider,'PATCH',repoAPI(repo),{archived:true},403);
  for(const viaSSH of [false,true])git(['-C',working,'push',viaSSH?ssh:http,'HEAD:refs/heads/downgraded-'+(viaSSH?'ssh':'http')],users.outsider,viaSSH);
  await grant(repo,users.outsider,30);
  const current=await view(users.creator,repo);
  await request(users.creator,'DELETE',`/api/v1/governance/repositories/${repo.id}/shares/${group.id}?revision=${current.revision}`,undefined,204);
  const remaining=await view(users.outsider,repo);assert.equal(remaining.members.find(x=>x.user_id===users.outsider.id).role,30);
  for(const viaSSH of [false,true])git(['-C',working,'push',viaSSH?ssh:http,'HEAD:refs/heads/remaining-'+(viaSSH?'ssh':'http')],users.outsider,viaSSH);
  await revoke(repo,users.outsider);
 });
 const branches=await request(users.creator,'GET',repoAPI(repo)+'/branches?limit=100');
 writeFileSync(join(output,'git-refs.json'),JSON.stringify({项目:repo.full_name,引用:branches.map(x=>({分支:x.name,提交:x.commit.id}))},null,2)+'\n',{mode:0o600});
}
async function browserCases(repo) {
 for (const engine of [chromium,firefox].filter(x=>!config.browsers||config.browsers.includes(x.name()))) {
  browser=await engine.launch({timeout:20000});
  for (const [name,role] of roles) {
   const context=await browser.newContext({baseURL:base,locale:'zh-CN',viewport:{width:1440,height:900}});
   await login(context,users[name]);const page=await context.newPage();page.setDefaultTimeout(20000);
   await check('NAV-01 NAV-02 NAV-04',`${engine.name()} ${name} 从首页进入协作者`,async()=>{
    await page.goto('/');
    const link=page.locator(`a[href="/${repo.full_name}"]`).first();await expect(link).toBeVisible({timeout:30000});await link.click();
    await page.getByRole('link',{name:'协作者',exact:true}).first().click();
    await expect(page.locator('#repo-members')).toBeVisible();
    await expect(page.locator('#repo-member-modal')).toHaveCount(role>=40?1:0);
    await expect(page.locator('#repo-share-modal')).toHaveCount(role===50?1:0);
   });
   await page.goto('/'+repo.full_name+'/collaborators');
   await check('VIEW-03 VIEW-04 VIEW-09 UX-02',`${engine.name()} ${name} 搜索、Owner 筛选和键盘来源展开`,async()=>{
    await page.locator('#member-q').fill(users[name].username);await page.getByRole('button',{name:'筛选',exact:true}).click();
    await expect(page.locator('.member-entry')).toHaveCount(1);
    const summary=page.locator('.member-entry > details > summary');await summary.focus();await page.keyboard.press('Enter');
    assert.equal(await page.locator('.member-entry > details').getAttribute('open'),'');
    await page.goto('/'+repo.full_name+'/collaborators');
    await page.getByRole('link',{name:'仅看 Owner',exact:true}).click();
    await expect(page.locator('.member-entry')).toHaveCount(2);
    const rows=await page.locator('.member-entry').allTextContents();assert.equal(rows.length,2);for(const row of rows)assert.ok(row.includes('Owner · 所有者'));
   });
   await context.close();
  }
  const context=await browser.newContext({baseURL:base,locale:'zh-CN'});await login(context,users.owner);
  const page=await context.newPage();page.setDefaultTimeout(20000);await page.goto('/'+repo.full_name+'/collaborators');
  await check('NAV-03',`${engine.name()} 设置与旧治理入口进入统一页面`,async()=>{
   for(const path of ['/'+repo.full_name+'/settings/collaboration',`/governance/repositories/${repo.id}/members`]) {
    await page.goto(path);await expect(page.locator('#repo-members')).toBeVisible();assert.equal(new URL(page.url()).pathname,'/'+repo.full_name+'/collaborators');
   }
  });
  await check('NAV-05',`${engine.name()} 仅代码单元的 Guest 仍可从首页进入协作者`,async()=>{
   const codeOnly=await createRepo('code-only-'+engine.name());
   await request(users.creator,'PATCH',repoAPI(codeOnly),{has_code:true,has_issues:false,has_pull_requests:false,has_wiki:false,has_projects:false,has_releases:false,has_packages:false,has_actions:false});
   await grant(codeOnly,users.guest,10);
   const guestContext=await browser.newContext({baseURL:base});await login(guestContext,users.guest);
   try {
    const guestPage=await guestContext.newPage();await guestPage.goto('/');
    const link=guestPage.locator(`a[href="/${codeOnly.full_name}"]`).first();await expect(link).toBeVisible({timeout:20000});await link.click();
    await guestPage.getByRole('link',{name:'协作者',exact:true}).first().click();await expect(guestPage.locator('#repo-members')).toBeVisible();
    await request(users.guest,'GET',repoAPI(codeOnly)+'/contents/',undefined,403);
   } finally {await guestContext.close();}
  });
  await check('SEC-06',`${engine.name()} 跨站 Cookie 写请求及无效请求被拒绝`,async()=>{
   const before=await view(users.owner,repo);
   const change={kind:'member',member:{user_id:users.target.id,role:30,revision:before.revision}};
   const preview=await request(users.owner,'POST',`/api/v1/governance/repositories/${repo.id}/members/preview`,change);
   const response=await context.request.post('/'+repo.full_name+'/collaborators/change',{headers:{Origin:'https://untrusted.example.invalid','Sec-Fetch-Site':'cross-site'},data:{...change,member:{...change.member,preview_token:preview.token}}});
   assert.equal(response.status(),403);
   for(const payload of [{...change,unexpected:true},{...change,username:'x'.repeat(20000)}]) {
    const rejected=await context.request.post('/'+repo.full_name+'/collaborators/preview',{data:payload});assert.equal(rejected.status(),400);
   }
   assert.equal((await view(users.owner,repo)).revision,before.revision);
  });
  await page.goto('/'+repo.full_name+'/collaborators');
  for (const [width,height] of [[1440,900],[768,1024],[390,844]]) await check('UX-01 UX-03',`${engine.name()} ${width} 像素布局`,async()=>{
   await page.setViewportSize({width,height});
   // 原生菜单在 ResizeObserver 后节流更新，等待可见布局稳定而非测量过渡帧。
   await expect.poll(()=>page.evaluate(()=>document.documentElement.scrollWidth-document.documentElement.clientWidth),{timeout:3000}).toBeLessThanOrEqual(0);
   const size=await page.evaluate(()=>({scroll:document.documentElement.scrollWidth,width:document.documentElement.clientWidth}));
   if(size.scroll>size.width) {
    const overflowing=await page.evaluate(()=>Array.from(document.querySelectorAll('body *')).filter(e=>e.getBoundingClientRect().right>innerWidth+2).slice(0,25).map(e=>({标签:e.tagName,标识:e.id,样式:e.className,右侧:e.getBoundingClientRect().right})));
    writeFileSync(join(output,engine.name()+'-'+width+'-overflow.json'),JSON.stringify(overflowing,null,2),{mode:0o600});
    await page.screenshot({path:join(output,engine.name()+'-'+width+'-overflow.png'),fullPage:true});
   }
   assert.ok(size.scroll<=size.width,JSON.stringify(size));
   await page.getByRole('button',{name:'添加协作者',exact:true}).click();await expect(page.locator('#member-role')).toHaveValue('30');
   await page.locator('#member-name').fill(users.target.username);
   await expect(page.locator('#repo-member-modal button[type="submit"]')).toBeInViewport();
   await page.locator('#repo-member-modal button.cancel').click();
   await page.screenshot({path:join(output,engine.name()+'-'+width+'.png'),fullPage:true});
  });
  await check('AUTH-01 PRE-05 FLOW-06',`${engine.name()} 页面添加 Developer 后目标账号可发现，页面撤权后拒绝`,async()=>{
   await page.getByRole('button',{name:'添加协作者',exact:true}).click();await page.locator('#member-name').fill(users.target.username);
   await page.locator('#repo-member-modal button[type="submit"]').click();await expect(page.locator('#repo-member-modal .member-preview')).toContainText('Developer');
   await Promise.all([page.waitForResponse(r=>r.url().endsWith('/collaborators/change')&&r.request().method()==='POST'),page.locator('#repo-member-modal button[type="submit"]').click()]);
   await expect(page.locator('.member-entry').filter({hasText:users.target.username})).toBeVisible();
   assert.equal((await view(users.target,repo)).members.find(x=>x.user_id===users.target.id).role,30);
   const targetContext=await browser.newContext({baseURL:base,locale:'zh-CN'});await login(targetContext,users.target);
   const targetPage=await targetContext.newPage();await targetPage.goto('/');await expect(targetPage.locator(`a[href="/${repo.full_name}"]`).first()).toBeVisible({timeout:30000});
   await page.goto('/'+repo.full_name+'/collaborators');
   const entry=page.locator('.member-entry').filter({hasText:users.target.username});
   await entry.getByRole('button',{name:'预览移除直接授权'}).click();
   await Promise.all([page.waitForResponse(r=>r.url().endsWith('/collaborators/change')&&r.request().method()==='POST'),entry.getByRole('button',{name:'确认并提交变更'}).click()]);
   const response=await targetPage.goto('/'+repo.full_name+'/collaborators');assert.equal(response.status(),404);
   await targetContext.close();
  });
  await context.close();await browser.close();browser=null;
 }
}
try {
 const version=await request(null,'GET','/api/v1/version');
 writeFileSync(join(output,'baseline.json'),JSON.stringify({程序版本:version,环境:config.baseline||'由执行方另附环境证据'},null,2)+'\n',{mode:0o600});
 await token(admin);
 for (const name of ['creator',...roles.map(x=>x[0]),'target','outsider','minimal']) await createUser(name);
 const repo=await createRepo('private');
 console.log('隔离固定样本已创建：'+run);
 const layers=config.layers||['api','git','browser'];
 if(layers.includes('api')) await apiCases(repo);
 else for(const [name,role] of roles) await grant(repo,users[name],role);
 if(layers.includes('git')) await gitCases(repo);
 if(layers.includes('browser')) await browserCases(repo);
} catch(error) {
 records.push({编号:[],场景:'验收准备或后续阶段',状态:'阻塞',原因:clean(error.stack)});console.log(clean(error.stack));
} finally {
 if(browser)await browser.close();rmSync(temporary,{recursive:true,force:true});
 const report={运行编号:run,说明:'隔离实例实际验收；仅下列层级与浏览器计入本次执行，凭据未写入报告。',执行层级:config.layers||['api','git','browser'],浏览器:config.browsers||['chromium','firefox'],结果:records};
 writeFileSync(join(output,'report.json'),JSON.stringify(report,null,2)+'\n',{mode:0o600});
 console.log('报告：'+join(output,'report.json'));
 process.exitCode=records.some(x=>x.状态!=='通过')?1:0;
}
