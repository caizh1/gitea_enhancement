import {initGovernanceInvitations} from './governance-invitations.ts';

const token = 'ab'.repeat(32);
const key = 'governance-invitation-123';

describe('邀请凭据传递', {concurrent: false}, () => {
  beforeEach(() => {
    sessionStorage.clear();
    window.history.replaceState(null, '', '/governance/invitations/123');
    document.body.innerHTML = '';
  });

  afterEach(() => {
    vi.restoreAllMocks();
    sessionStorage.clear();
    document.body.innerHTML = '';
  });

  test('邀请令牌通过登录保留，但从地址栏移除并仅提交在表单正文', () => {
    document.body.innerHTML = '<div data-governance-invitation="123"><a href="/user/login">登录</a></div>';
    window.history.replaceState(null, '', `/governance/invitations/123#token=${token}`);
    initGovernanceInvitations();
    expect(window.location.hash).toBe('');
    expect(window.location.search).toBe('');
    expect(sessionStorage.getItem(key)).toBe(token);
    expect(document.querySelector('a')?.getAttribute('href')).toBe('/user/login');
    document.body.innerHTML = '<div data-governance-invitation="123"><form data-invitation-preview method="post"><input name="token"></form></div>';
    const submit = vi.spyOn(HTMLFormElement.prototype, 'requestSubmit').mockImplementation(() => {});
    initGovernanceInvitations();
    expect(document.querySelector<HTMLInputElement>('input')?.value).toBe(token);
    expect(submit).toHaveBeenCalledTimes(1);
    expect(window.location.href).not.toContain(token);
  });

  test('接受或拒绝完成后清除本邀请的浏览器暂存凭据', () => {
    sessionStorage.setItem(key, token);
    sessionStorage.setItem('governance-invitation-456', token);
    document.body.innerHTML = '<div data-governance-invitation="123" data-completed="true"></div>';
    initGovernanceInvitations();
    expect(sessionStorage.getItem(key)).toBeNull();
    expect(sessionStorage.getItem('governance-invitation-456')).toBe(token);
  });

  test('无效令牌不自动提交表单', () => {
    document.body.innerHTML = '<div data-governance-invitation="123"><form data-invitation-preview><input name="token"></form></div>';
    window.history.replaceState(null, '', '/governance/invitations/123#token=invalid');
    const submit = vi.spyOn(HTMLFormElement.prototype, 'requestSubmit').mockImplementation(() => {});
    initGovernanceInvitations();
    expect(submit).not.toHaveBeenCalled();
  });
});
