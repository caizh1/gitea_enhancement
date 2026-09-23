// 面包屑专项验收：只连接本机导航隔离实例，变更仅限本次新建样本。
// 凭据从既有隔离样本读取，不写入结果、截图或终端。
const fs = require('node:fs');
const path = require('node:path');
const assert = require('node:assert/strict');
const {chromium} = require(process.env.PLAYWRIGHT_MODULE || '/Users/archer/.cache/codex-runtimes/codex-primary-runtime/dependencies/node/node_modules/playwright');
const root = path.resolve(__dirname, '..');
const out = path.join(root, 'docs/evidence/breadcrumb-review');
const source = process.env.NAV_FIXTURES || '/Users/archer/Work/gitea-governance/work/navigation-review';
const fixtures = JSON.parse(fs.readFileSync(path.join(source, 'fixtures.json')));
const boundary = JSON.parse(fs.readFileSync(path.join(source, 'boundary-fixtures.json')));
const base = 'http://127.0.0.1:3510';
const credentials = {'管理员': ['nav-admin', fs.readFileSync(path.join(source, 'admin-password'), 'utf8').trim()]};
for (const [name, user] of Object.entries(fixtures.users)) credentials[name] = [name, user.password];
const results = [];
const selected = new Set((process.env.TEST_CASES || '').split(',').filter(Boolean));
const resultFile = path.join(out, selected.size ? 'supplement-results.json' : 'results.json');
const environment = {时间: new Date().toISOString(), 地址: base, 范围: '本机隔离实例；非生产验收'};
fs.mkdirSync(out, {recursive: true});

async function api(method, route, actor = '管理员', data) {
  const headers = {'Content-Type': 'application/json'};
  if (actor) headers.Authorization = 'Basic ' + Buffer.from(credentials[actor].join(':')).toString('base64');
  const response = await fetch(base + '/api/v1' + route, {method, headers, body: data === undefined ? undefined : JSON.stringify(data)});
  const text = await response.text();
  let body;
  try { body = JSON.parse(text); } catch { body = text; }
  return {状态码: response.status, 内容: body};
}
async function expectApi(method, route, actor, data, status = 200) {
  const result = await api(method, route, actor, data);
  assert.equal(result.状态码, status, `${method} ${route}：预期 ${status}，实际 ${result.状态码}`);
  return result.内容;
}
async function test(id, area, name, expected, run) {
  if (selected.size && !selected.has(id)) return;
  const entry = {编号: id, 分类: area, 用例: name, 预期: expected};
  try { entry.实测 = await run() || '符合预期'; entry.结果 = '通过'; }
  catch (error) { entry.结果 = '失败'; entry.实测 = error.message; }
  results.push(entry);
  fs.writeFileSync(resultFile, JSON.stringify({环境: environment, 用例: results}, null, 2) + '\n');
  console.log(`${id} ${entry.结果}：${name}`);
}
async function grant(group, actor, role) {
  const current = await expectApi('GET', `/governance/groups/${group}`, '管理员');
  await expectApi('PUT', `/governance/groups/${group}/members/${fixtures.users[actor].id}`, '管理员', {role, revision: current.revision}, 204);
}

async function main() {
  environment.版本 = await expectApi('GET', '/version', null);
  assert.equal(environment.版本.version, '1.27.3+governance.demo.nav7', '实例版本变化，停止修改样本');
  const browser = await chromium.launch({headless: true});
  const contexts = {};
  async function session(actor) {
    if (contexts[actor || '匿名']) return contexts[actor || '匿名'];
    const context = await browser.newContext({viewport: {width: 1440, height: 1000}});
    const page = await context.newPage();
    page.setDefaultTimeout(15000);
    if (actor) {
      await page.goto(base + '/user/login');
      await page.locator('[name=user_name]').fill(credentials[actor][0]);
      await page.locator('[name=password]').fill(credentials[actor][1]);
      await Promise.all([page.waitForURL(url => !url.pathname.includes('/user/login')), page.locator('button').filter({hasText: /Sign In|登录/}).click()]);
    }
    return contexts[actor || '匿名'] = {context, page};
  }
  async function open(actor, route, status = 200) {
    const {page} = await session(actor);
    const response = await page.goto(base + route);
    assert.equal(response.status(), status, `${route}：预期 ${status}，实际 ${response.status()}`);
    return page;
  }
  const nav = id => `/governance/navigation/groups/${id}`;
  const group = id => `/governance/groups/${id}`;
  try {
    await test('P01', '权限', '仅子组授权：群组面包屑隐藏私有祖先链接', '两级祖先仅显示地址段，无链接，不泄露显示名称', async () => {
      const data = await expectApi('GET', nav(fixtures.firmware), 'nav-child');
      assert.deepEqual(data.breadcrumbs.slice(0, 2), [{name: 'rd', url: ''}, {name: 'storage', url: ''}]);
      const page = await open('nav-child', '/rd/storage/firmware');
      assert.equal(await page.locator('nav[aria-label="面包屑"] a[href="/rd"]').count(), 0);
      return data.breadcrumbs;
    });
    await test('P02', '权限', '仅子组授权：手输顶层和兄弟地址', '主页、设置、导航接口均返回404', async () => {
      for (const id of [fixtures.root, fixtures.validation]) {
        await expectApi('GET', nav(id), 'nav-child', undefined, 404);
        await open('nav-child', group(id) + '?tab=settings', 404);
      }
      await open('nav-child', '/rd', 404);
      await open('nav-child', '/org/rd/settings', 404);
    });
    await test('P03', '权限', '顶层Reporter沿面包屑进入顶层', '可浏览主页；设置页只读，无管理表单', async () => {
      const page = await open('nav-inherited', '/rd/storage/firmware');
      await page.locator('nav[aria-label="面包屑"] a[href="/rd"]').click();
      assert.equal(new URL(page.url()).pathname, '/rd');
      await open('nav-inherited', group(fixtures.root) + '?tab=settings');
      assert.equal(await page.locator('[role=main] form').count(), 0);
      await page.screenshot({path: path.join(out, 'reporter-settings.png'), fullPage: true});
      return '主页200；设置页200但无管理表单';
    });
    await test('P04', '权限', '顶层Reporter直接访问原生管理和审批页', '管理页拒绝访问', async () => {
      await open('nav-inherited', '/org/rd/settings', 404);
      await open('nav-inherited', '/org/rd/settings/approvals', 404);
    });
    await test('P05', '权限', '仅仓库授权：仓库面包屑和受限群组', '祖先无链接，直属组可进入受限视图，无设置入口', async () => {
      const page = await open('nav-repository', '/rd/storage/firmware/firmware-core');
      const links = await page.locator('.repo-header a').evaluateAll(es => es.map(e => e.getAttribute('href')));
      assert(!links.includes('/rd') && !links.includes('/rd/storage'));
      assert(links.includes('/rd/storage/firmware'));
      await page.locator('.repo-header a[href="/rd/storage/firmware"]').click();
      assert.equal(await page.locator('a[href$="?tab=settings"]').count(), 0);
      await open('nav-repository', group(fixtures.firmware) + '?tab=settings', 404);
      return '直属组受限页面可读；群组设置404';
    });
    await test('P06', '权限', '匿名访问私有祖先及仓库', '私有导航404；页面404或要求登录', async () => {
      await expectApi('GET', nav(fixtures.root), null, undefined, 404);
      const {context} = await session(null);
      for (const route of ['/rd', '/rd/storage/firmware/firmware-core']) {
        const response = await context.request.get(base + route, {maxRedirects: 0});
        assert([302, 303, 404].includes(response.status()));
        if (response.status() !== 404) assert(response.headers().location.includes('/user/login'));
      }
    });
    await test('P07', '权限', '公开群组匿名面包屑', '公开主页200；管理入口要求登录，不获得管理能力', async () => {
      const data = await expectApi('GET', nav(boundary['public-root']), null);
      assert.equal(data.allowed_actions.length, 0);
      await open(null, data.group.url);
      const {context} = await session(null);
      const response = await context.request.get(base + group(boundary['public-root']) + '?tab=settings', {maxRedirects: 0});
      assert.equal(response.status(), 303);
      assert(response.headers().location.includes('/user/login'));
    });
    await test('N01', '导航', '三级祖先顺序和链接目标', '根到当前组顺序正确，指向主页而非管理页', async () => {
      const data = await expectApi('GET', nav(fixtures.firmware), 'nav-inherited');
      assert.deepEqual(data.breadcrumbs.map(x => x.url), ['/rd', '/rd/storage', '/rd/storage/firmware']);
    });
    await test('N02', '导航', '旧稳定ID地址兼容跳转', '跳转到当前完整路径主页', async () => {
      const page = await open('nav-child', group(fixtures.firmware));
      assert.equal(new URL(page.url()).pathname, '/rd/storage/firmware');
    });
    await test('N03', '导航', '二十层面包屑', '二十项完整、顺序连续、每层链接返回200', async () => {
      const data = await expectApi('GET', nav(boundary['depth-20']), '管理员');
      assert.equal(data.breadcrumbs.length, 20);
      const {context} = await session('管理员');
      let previous = '';
      for (const crumb of data.breadcrumbs) {
        assert(crumb.url.startsWith(previous + '/'));
        const response = await context.request.get(base + crumb.url);
        assert.equal(response.status(), 200);
        previous = crumb.url;
      }
    });
    await test('N04', '导航', '同名子组路径消歧', '完整路径与稳定ID不同，不串组', async () => {
      const a = await expectApi('GET', nav(boundary['same-a']), '管理员');
      const b = await expectApi('GET', nav(boundary['same-b']), '管理员');
      assert.notEqual(a.group.full_path, b.group.full_path);
      assert.notEqual(a.breadcrumbs.at(-1).url, b.breadcrumbs.at(-1).url);
    });
    await test('N05', '导航', '刷新和浏览器后退', '上级与下级往返后路径和面包屑保持一致', async () => {
      const page = await open('nav-inherited', '/rd/storage/firmware');
      await page.locator('nav[aria-label="面包屑"] a[href="/rd/storage"]').click();
      await page.goBack();
      await page.reload();
      assert.equal(new URL(page.url()).pathname, '/rd/storage/firmware');
      assert.equal(await page.locator('nav[aria-label="面包屑"] a[href="/rd/storage/firmware"]').count(), 1);
    });
    await test('N06', '导航', '仓库与群组面包屑名称一致性', '同一节点在两种页面显示相同名称', async () => {
      const page = await open('nav-inherited', '/rd/storage/firmware');
      const groupName = await page.locator('nav[aria-label="面包屑"] a[href="/rd"]').innerText();
      await open('nav-inherited', '/rd/storage/firmware/firmware-core');
      const repoName = await page.locator('.repo-header a[href="/rd"]').innerText();
      assert.equal(repoName, groupName, `群组显示“${groupName}”，仓库显示“${repoName}”`);
    });
    await test('U01', '交互', '普通用户共享标签反馈', '无共享管理权限时隐藏标签或提供明确无权限说明', async () => {
      const page = await open('nav-inherited', group(fixtures.root) + '?tab=shares');
      await page.screenshot({path: path.join(out, 'reporter-shares.png'), fullPage: true});
      const links = await page.locator('a[href$="?tab=shares"]').count();
      const text = await page.locator('[role=main]').innerText();
      assert(links === 0 || /无权限|没有权限|仅.*管理员/.test(text), '共享标签可见，点击后只有页头，无内容或权限说明');
    });
    await test('U02', '无障碍', '面包屑当前页面标识', '当前项具有aria-current=page', async () => {
      const page = await open('nav-inherited', '/rd/storage/firmware');
      assert.equal(await page.locator('nav[aria-label="面包屑"] [aria-current=page]').count(), 1, '当前页面没有aria-current标记');
    });
    await test('U03', '无障碍', '键盘操作祖先链接', '祖先可获得焦点并通过Enter导航', async () => {
      const page = await open('nav-inherited', '/rd/storage/firmware');
      await page.locator('nav[aria-label="面包屑"] a').first().focus();
      await page.keyboard.press('Tab');
      assert.equal(await page.locator(':focus').getAttribute('href'), '/rd');
      await page.keyboard.press('Enter');
      await page.waitForURL(base + '/rd');
    });
    // 后续写操作都在新建专用群组；既有根、成员关系、仓库均不修改。
    const prefix = 'breadcrumb-check-' + Date.now();
    const create = async (slug, parent = 0, name = '面包屑专项验收', visibility = 2) => expectApi('POST', '/governance/groups', '管理员', {path: slug, parent_id: parent, name, visibility}, 201);
    const sample = await create(prefix);
    const child = await create('child', sample.id, '长名称' + 'x'.repeat(80));
    const sibling = await create('sibling', sample.id);
    environment.专用样本 = {根: sample.id, 子组: child.id, 兄弟: sibling.id, 路径: prefix};
    for (const [role, name] of [[5, 'Minimal Access'], [10, 'Guest'], [15, 'Planner'], [20, 'Reporter'], [30, 'Developer'], [40, 'Maintainer'], [50, 'Owner']]) {
      await test('R' + role, '角色矩阵', `仅子组${name}不能向上提权`, '祖先不可点击且404；只有Owner拥有本组管理能力', async () => {
        await grant(child.id, 'nav-child', role);
        const data = await expectApi('GET', nav(child.id), 'nav-child');
        assert.equal(data.breadcrumbs[0].url, '');
        assert.equal(data.allowed_actions.includes('manage_group'), role === 50);
        await expectApi('GET', nav(sample.id), 'nav-child', undefined, 404);
        await open('nav-child', group(sample.id) + '?tab=settings', 404);
      });
    }
    await grant(sample.id, 'nav-inherited', 20);
    await grant(child.id, 'nav-child', 30);
    const before = await expectApi('GET', group(sample.id), '管理员');
    for (const [id, name, method, suffix, body] of [
      ['W01', '移动群组', 'POST', '/move', {path: 'forbidden', parent_id: 0}],
      ['W02', '归档群组', 'PUT', '/archive', {archived: true}],
      ['W03', '删除群组', 'POST', '/deletion', {confirmation_path: before.full_path}],
      ['W04', '添加Owner成员', 'PUT', '/members/' + fixtures.users['nav-repository'].id, {role: 50}],
      ['W05', '添加共享', 'PUT', '/shares/' + sibling.id, {max_role: 50}],
      ['W06', '创建自定义角色', 'POST', '/roles', {name: '禁止创建', base_role: 20, abilities: ['manage_group']}],
    ]) {
      await test(id, '写入鉴权', 'Reporter直接请求' + name, '404拒绝；不修改群组', async () => {
        await expectApi(method, group(sample.id) + suffix, 'nav-inherited', {...body, revision: before.revision}, 404);
      });
    }
    await test('W07', '写入鉴权', '网页会话同源提交管理操作', '逐项404，不能以跨站请求拒绝替代权限验证', async () => {
      await open('nav-inherited', group(sample.id) + '?tab=settings');
      const {context} = await session('nav-inherited');
      for (const [suffix, data] of [
        ['/move', {action: 'move', path: 'forbidden', parent_id: '0'}],
        ['/archive', {archived: 'true'}],
        ['/deletion', {action: 'schedule', confirmation_path: before.full_path}],
        ['/members', {user_id: String(fixtures.users['nav-repository'].id), role: '50'}],
        ['/shares', {group_path: sibling.full_path, role: '50'}],
        ['/roles', {name: '禁止创建', role: '20'}],
        ['/share-restriction', {enabled: 'true'}],
      ]) {
        const response = await context.request.post(base + group(sample.id) + suffix, {headers: {Origin: base, 'Sec-Fetch-Site': 'same-origin'}, form: {revision: String(before.revision), ...data}, maxRedirects: 0});
        assert.equal(response.status(), 404, `${suffix}实际${response.status()}`);
      }
      const after = await expectApi('GET', group(sample.id), '管理员');
      assert.equal(after.revision, before.revision);
      assert.equal(after.full_path, before.full_path);
      return '7类网页写入均404；根群组修订号和路径不变';
    });
    await test('U04', '响应式', '长名称在390与320像素面包屑内换行', '面包屑节点不超出视口', async () => {
      const page = await open('nav-inherited', '/' + child.full_path);
      const measurements = [];
      for (const width of [390, 320]) {
        await page.setViewportSize({width, height: 844});
        await page.screenshot({path: path.join(out, `long-name-${width}.png`), fullPage: true});
        measurements.push(await page.locator('nav[aria-label="面包屑"]').evaluate(el => ({宽度: innerWidth, 超出节点: [...el.children].map(e => ({文本: e.textContent, 左: e.getBoundingClientRect().left, 右: e.getBoundingClientRect().right})).filter(e => e.左 < 0 || e.右 > innerWidth)})));
      }
      await page.setViewportSize({width: 1440, height: 1000});
      assert(measurements.every(x => !x.超出节点.length), JSON.stringify(measurements));
    });
    await test('L01', '生命周期', '改名转移后的面包屑及旧地址', '新祖先链正确；旧地址兼容访问，面包屑指向新规范地址，仍鉴权', async () => {
      const current = await expectApi('GET', group(child.id), '管理员');
      const moved = await expectApi('POST', group(child.id) + '/move', '管理员', {path: 'renamed', parent_id: sibling.id, revision: current.revision});
      environment.转移后路径 = moved.full_path;
      const data = await expectApi('GET', nav(child.id), 'nav-inherited');
      assert.deepEqual(data.breadcrumbs.map(x => x.url), ['/' + sample.full_path, '/' + sibling.full_path, '/' + moved.full_path]);
      const page = await open('nav-child', '/' + child.full_path);
      assert.equal(await page.locator('nav[aria-label="面包屑"] a[href="/' + moved.full_path + '"]').count(), 1);
      await expectApi('GET', nav(sample.id), 'nav-child', undefined, 404);
    });
    await test('L02', '生命周期', '撤权后的旧面包屑链接与旧别名', '重新请求主页、接口和别名均拒绝', async () => {
      const current = await expectApi('GET', group(child.id), '管理员');
      await expectApi('DELETE', group(child.id) + '/members/' + fixtures.users['nav-child'].id + '?revision=' + current.revision, '管理员', undefined, 204);
      await expectApi('GET', nav(child.id), 'nav-child', undefined, 404);
      await open('nav-child', '/' + child.full_path, 404);
      await open('nav-child', '/' + environment.转移后路径, 404);
    });
    await test('L03', '生命周期', '归档与恢复保持面包屑完整', '归档状态正确；链条仍可读，创建能力隐藏，恢复成功', async () => {
      let current = await expectApi('GET', group(child.id), '管理员');
      await expectApi('PUT', group(child.id) + '/archive', '管理员', {archived: true, revision: current.revision});
      try {
        const data = await expectApi('GET', nav(child.id), 'nav-inherited');
        assert.equal(data.group.state, 'archived');
        assert.equal(data.breadcrumbs.length, 3);
        const page = await open('管理员', '/' + environment.转移后路径);
        assert.equal(await page.locator('a[href$="?create=1"]').count(), 0);
      } finally {
        current = await expectApi('GET', group(child.id), '管理员');
        await expectApi('PUT', group(child.id) + '/archive', '管理员', {archived: false, revision: current.revision});
      }
    });
    await test('L04', '生命周期', '待删除与恢复的面包屑', '待删除可读且状态准确，恢复后路径不变', async () => {
      let current = await expectApi('GET', group(child.id), '管理员');
      await expectApi('POST', group(child.id) + '/deletion', '管理员', {confirmation_path: current.full_path, revision: current.revision});
      try {
        const data = await expectApi('GET', nav(child.id), 'nav-inherited');
        assert.equal(data.group.state, 'pending_deletion');
        assert.equal(data.breadcrumbs.length, 3);
      } finally {
        current = await expectApi('GET', group(child.id), '管理员');
        await expectApi('POST', group(child.id) + '/restore', '管理员', {confirmation_path: current.full_path, revision: current.revision});
      }
      const data = await expectApi('GET', nav(child.id), 'nav-inherited');
      assert.equal(data.group.state, 'active');
      assert.equal(data.group.full_path, environment.转移后路径);
    });
    await test('S01', '输入安全', '显示名称中的HTML和脚本', '以文本显示；无脚本执行和注入元素', async () => {
      const hostile = await create('escaped', sample.id, '<img src=x onerror=alert(1)>');
      const page = (await session('nav-inherited')).page;
      let dialog = false;
      page.on('dialog', async d => { dialog = true; await d.dismiss(); });
      await open('nav-inherited', '/' + hostile.full_path);
      const crumb = page.locator('nav[aria-label="面包屑"]');
      assert((await crumb.innerText()).includes('<img src=x onerror=alert(1)>'));
      assert.equal(await crumb.locator('img').count(), 0);
      assert.equal(dialog, false);
    });
    await test('P08', '权限', '内部群组的已登录非成员和匿名访问', '已登录可读但无管理权；匿名404', async () => {
      const internal = await create(prefix + '-internal', 0, '内部面包屑专项样本', 1);
      const data = await expectApi('GET', nav(internal.id), 'nav-repository');
      assert.equal(data.allowed_actions.length, 0);
      await expectApi('GET', nav(internal.id), null, undefined, 404);
      const page = await open('nav-repository', group(internal.id) + '?tab=settings');
      assert.equal(await page.locator('[role=main] form').count(), 0);
    });
    await test('P09', '权限', '共享Reporter和过期Owner不能获得管理权', '共享权限不向祖先扩散；过期高权限不覆盖有效只读共享', async () => {
      const invited = await create('invited', sample.id);
      await grant(invited.id, 'nav-repository', 20);
      let current = await expectApi('GET', group(child.id), '管理员');
      await expectApi('PUT', group(child.id) + '/shares/' + invited.id, '管理员', {max_role: 20, revision: current.revision}, 204);
      current = await expectApi('GET', group(child.id), '管理员');
      const expiry = Math.floor(Date.now() / 1000) + 6;
      await expectApi('PUT', group(child.id) + '/members/' + fixtures.users['nav-repository'].id, '管理员', {role: 50, expires_unix: expiry, revision: current.revision}, 204);
      const active = await expectApi('GET', nav(child.id), 'nav-repository');
      assert(active.allowed_actions.includes('manage_group'), '到期前高权限应实际生效');
      await new Promise(resolve => setTimeout(resolve, Math.max(0, expiry * 1000 - Date.now() + 1000)));
      const data = await expectApi('GET', nav(child.id), 'nav-repository');
      assert(!data.allowed_actions.includes('manage_group'));
      assert(data.breadcrumbs.slice(0, -1).every(x => !x.url));
      await expectApi('GET', nav(sample.id), 'nav-repository', undefined, 404);
      current = await expectApi('GET', group(child.id), '管理员');
      await expectApi('DELETE', group(child.id) + '/shares/' + invited.id + '?revision=' + current.revision, '管理员', undefined, 204);
      await expectApi('GET', nav(child.id), 'nav-repository', undefined, 404);
    });
    await test('U05', '响应式', '常规三级与二十层面包屑窄屏布局', '390像素下节点全部位于视口内', async () => {
      const page = (await session('管理员')).page;
      const deep = await expectApi('GET', nav(boundary['depth-20']), '管理员');
      await page.setViewportSize({width: 390, height: 844});
      try {
        for (const route of ['/rd/storage/firmware', deep.group.url]) {
          await open('管理员', route);
          assert(await page.locator('nav[aria-label="面包屑"]').evaluate(el => [...el.children].every(e => e.getBoundingClientRect().left >= 0 && e.getBoundingClientRect().right <= innerWidth)));
        }
        await page.screenshot({path: path.join(out, 'deep-hierarchy-390.png'), fullPage: true});
      } finally { await page.setViewportSize({width: 1440, height: 1000}); }
    });
  } finally {
    await browser.close();
    fs.writeFileSync(resultFile, JSON.stringify({环境: environment, 用例: results}, null, 2) + '\n');
  }
  console.log(JSON.stringify({通过: results.filter(x => x.结果 === '通过').length, 失败: results.filter(x => x.结果 === '失败').length}));
  process.exitCode = results.some(x => x.结果 === '失败') ? 1 : 0;
}
main().catch(error => { console.error('执行中止：' + error.message); process.exitCode = 2; });
