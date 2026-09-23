import {initRepoMembers} from './repo-members.ts';
import {confirmModal} from './comp/ConfirmModal.ts';
import {POST} from '../modules/fetch.ts';

vi.mock('./comp/ConfirmModal.ts', () => ({confirmModal: vi.fn().mockResolvedValue(false)}));
vi.mock('../modules/fetch.ts', () => ({POST: vi.fn()}));
vi.mock('../modules/search.ts', () => ({attachSearchBox: vi.fn()}));

describe('成员授权预览', {concurrent: false}, () => {
  test.each(['username', 'role', 'expires', 'custom_role_id'])('修改 %s 后必须重新预览', async (field) => {
    document.body.innerHTML = '<div id="repo-members" data-url="/sample/repo/collaborators"></div><form class="repo-member-change" data-kind="member"><input name="username" value="sample"><input name="role" value="30"><input name="expires" value="2030-01-01"><select name="custom_role_id"><option value="0">默认角色</option></select><div class="member-preview"></div><button>预览影响</button></form>';
    vi.mocked(POST).mockResolvedValue({ok: true, json: async () => ({token: '预览版本', impacts: []})} as Response);
    initRepoMembers();
    const form = document.querySelector('form')!;
    form.dispatchEvent(new Event('submit', {cancelable: true}));
    await vi.waitFor(() => expect(form.querySelector('button')!.textContent).toBe('确认并提交变更'));
    form.querySelector(`[name="${CSS.escape(field)}"]`)!.dispatchEvent(new Event('input', {bubbles: true}));
    form.dispatchEvent(new Event('submit', {cancelable: true}));
    await vi.waitFor(() => expect(POST).toHaveBeenCalledTimes(2));
    expect(POST).toHaveBeenLastCalledWith('/sample/repo/collaborators/preview', expect.objectContaining({data: expect.objectContaining({member: expect.objectContaining({preview_token: ''})})}));
    await vi.waitFor(() => expect(form.querySelector('button')!.disabled).toBe(false));
    document.body.replaceChildren();
    vi.clearAllMocks();
  });

  test('等待响应时重复提交只发送一次，异常保留输入并允许重新预览', async () => {
    document.body.innerHTML = '<div id="repo-members" data-url="/sample/repo/collaborators"></div><form class="repo-member-change" data-kind="member"><input name="username" value="sample"><input name="role" value="30"><div class="member-preview"></div><button>预览影响</button></form>';
    const pending = Promise.withResolvers<Response>();
    vi.mocked(POST).mockReturnValue(pending.promise);
    initRepoMembers();
    const form = document.querySelector('form')!;
    form.dispatchEvent(new Event('submit', {cancelable: true}));
    form.dispatchEvent(new Event('submit', {cancelable: true}));
    expect(POST).toHaveBeenCalledTimes(1);
    expect(form.querySelector('button')!.disabled).toBe(true);
    pending.reject(new Error('响应超时，请重新预览'));
    await vi.waitFor(() => expect(form.querySelector('button')!.textContent).toBe('重新预览'));
    expect(form.querySelector<HTMLInputElement>('[name="username"]')!.value).toBe('sample');
    expect(form.querySelector('button')!.disabled).toBe(false);
    document.body.replaceChildren();
    vi.clearAllMocks();
  });

  test('预览中的用户和来源名称作为文本显示，日期使用 UTC 截止时间', async () => {
    document.body.innerHTML = '<div id="repo-members" data-url="/sample/repo/collaborators"></div><form class="repo-member-change" data-kind="member"><input name="expires" value="2030-01-01"><div class="member-preview"></div><button>预览影响</button></form>';
    vi.mocked(POST).mockResolvedValue({ok: true, json: async () => ({token: '预览版本', impacts: [{username: '<img src=x onerror=alert(1)>', before: 'Guest', after: 'Reporter', sources: [{name: '<script>恶意名称</script>', role: 'Reporter'}]}]})} as Response);
    initRepoMembers();
    const form = document.querySelector('form')!;
    form.dispatchEvent(new Event('submit', {cancelable: true}));
    await vi.waitFor(() => expect(form.querySelector('button')!.textContent).toBe('确认并提交变更'));
    expect(form.querySelector('.member-preview')!.querySelectorAll('img, script')).toHaveLength(0);
    expect(form.textContent).toContain('<script>恶意名称</script>');
    expect(POST).toHaveBeenLastCalledWith('/sample/repo/collaborators/preview', expect.objectContaining({data: expect.objectContaining({member: expect.objectContaining({expires_unix: 1893542400})})}));
    document.body.replaceChildren();
    vi.clearAllMocks();
  });

  test('角色变更清除旧预览，保留输入并重新计算共享上限', async () => {
    document.body.innerHTML = '<div id="repo-members" data-url="/sample/repo/collaborators"></div><form class="repo-member-change" data-kind="share"><input name="group_path" value="sample/group"><input name="role" value="50"><input name="revision" value="3"><div class="member-preview"></div><button>预览影响</button></form>';
    vi.mocked(POST).mockResolvedValue({ok: true, json: async () => ({token: 'version-1', impacts: [{username: 'sample', before: 'Reporter', after: 'Owner', sources: [{name: '邀请群组', role: 'Owner', expired: false}]}]})} as Response);
    initRepoMembers();
    const form = document.querySelector('form')!;
    const button = form.querySelector('button')!;
    form.dispatchEvent(new Event('submit', {cancelable: true}));
    await vi.waitFor(() => expect(button.textContent).toBe('确认并提交变更'));
    expect(POST).toHaveBeenLastCalledWith('/sample/repo/collaborators/preview', expect.objectContaining({data: expect.objectContaining({share: expect.objectContaining({max_role: 50})})}));
    expect(form.textContent).toContain('剩余来源：邀请群组');
    const role = form.querySelector<HTMLInputElement>('[name="role"]')!;
    role.value = '30';
    role.dispatchEvent(new Event('change', {bubbles: true}));
    expect(button.textContent).toBe('预览影响');
    vi.mocked(POST).mockResolvedValue({ok: false, json: async () => ({message: '来源已经变化，请重新预览'})} as Response);
    form.dispatchEvent(new Event('submit', {cancelable: true}));
    await vi.waitFor(() => expect(button.textContent).toBe('重新预览'));
    expect(POST).toHaveBeenLastCalledWith('/sample/repo/collaborators/preview', expect.objectContaining({data: expect.objectContaining({share: expect.objectContaining({max_role: 30, preview_token: ''})})}));
    expect(role.value).toBe('30');
    expect(form.querySelector<HTMLInputElement>('[name="group_path"]')!.value).toBe('sample/group');
    expect(form.textContent).toContain('来源已经变化');
    document.body.replaceChildren();
    vi.clearAllMocks();
  });

  test('列表内 Owner 撤权取消确认后不提交', async () => {
    document.body.innerHTML = '<div id="repo-members" data-url="/sample/repo/collaborators"></div><form class="repo-member-change" data-kind="member"><input name="user_id" value="4"><input name="action" value="remove"><div class="member-preview"></div><button>预览影响</button></form>';
    vi.mocked(POST).mockResolvedValue({ok: true, json: async () => ({token: 'owner-version', requires_confirmation: true, impacts: [{username: 'sample', before: 'Owner', after: '无成员授权', sources: []}]})} as Response);
    initRepoMembers();
    const form = document.querySelector('form')!;
    form.dispatchEvent(new Event('submit', {cancelable: true}));
    await vi.waitFor(() => expect(form.querySelector('button')!.textContent).toBe('确认并提交变更'));
    form.dispatchEvent(new Event('submit', {cancelable: true}));
    await vi.waitFor(() => expect(confirmModal).toHaveBeenCalled());
    expect(POST).toHaveBeenCalledTimes(1);
    expect(form.querySelector('button')!.disabled).toBe(false);
    document.body.replaceChildren();
    vi.clearAllMocks();
  });
});
