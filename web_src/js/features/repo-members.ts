import {confirmModal} from './comp/ConfirmModal.ts';
import {POST} from '../modules/fetch.ts';
import {attachSearchBox} from '../modules/search.ts';

type Preview = {requires_confirmation: boolean; token: string; impacts: {username: string; before: string; after: string; sources: {name: string; role: string; ceiling?: string; expired: boolean}[]}[]; visibility_notice: string; self_loses_management: boolean};

export function initRepoMembers() {
  const root = document.querySelector<HTMLElement>('#repo-members');
  if (!root) return;
  const search = document.querySelector<HTMLElement>('#repo-member-search');
  if (search) attachSearchBox<{data: {login: string}[]}>(search, `${window.config.appSubUrl}/api/v1/users/search?q={query}`, (r) => r.data.map((u) => ({title: u.login})));
  for (const form of document.querySelectorAll<HTMLFormElement>('.repo-member-change')) {
    const output = form.querySelector<HTMLElement>('.member-preview')!;
    const button = form.querySelector<HTMLButtonElement>('button[type="submit"], button:not([type])')!;
    let requiresConfirmation = false;
    let token = '';
    let snapshot = '';
    let busy = false;
    const clear = () => { token = ''; snapshot = ''; output.replaceChildren(); button.textContent = '预览影响' };
    form.addEventListener('input', clear);
    form.addEventListener('change', clear);
    // 弹窗复用时依据当前表单重新预览，不能复用上一位成员的确认。
    form.addEventListener('submit', async (event) => {
      event.preventDefault();
      if (busy) return;
      const data = new FormData(form);
      const text = (key: string) => data.get(key) as string || '';
      const number = (key: string) => Number(data.get(key) || 0);
      const expiry = text('expires');
      const original = number('expires_unix');
      let expires = expiry ? Date.parse(`${expiry}T00:00:00Z`) / 1000 + 86400 : 0;
      if (expiry && original && new Date((original - 1) * 1000).toISOString().slice(0, 10) === expiry) expires = original;
      const custom = form.querySelector<HTMLSelectElement>('[name="custom_role_id"]');
      const customBase = custom?.selectedOptions[0].getAttribute('data-base-role');
      const role = customBase ? Number(customBase) : number('role');
      const change = {kind: form.getAttribute('data-kind')!, remove: data.get('action') === 'remove', username: text('username'), group_path: text('group_path'), member: {user_id: number('user_id'), role, custom_role_id: number('custom_role_id'), expires_unix: expires, revision: number('revision'), preview_token: ''}, share: {group_id: number('group_id'), max_role: role, expires_unix: expires, revision: number('revision'), preview_token: ''}};
      const current = JSON.stringify(change);
      const commit = token !== '' && snapshot === current;
      if (commit) { change.member.preview_token = token; change.share.preview_token = token }
      busy = true;
      button.disabled = true;
      try {
        if (commit && requiresConfirmation && !form.closest('.modal') && !await confirmModal({header: '确认所有者权限变更', content: output.textContent, confirmButtonColor: 'red'})) return;
        const response = await POST(`${root.getAttribute('data-url')}/${commit ? 'change' : 'preview'}`, {data: change});
        const result = await response.json();
        if (!response.ok) throw new Error(result.message || `请求失败（${response.status}）`);
        if (commit) { window.location.assign(root.getAttribute('data-url')!); return }
        const preview = result as Preview;
        requiresConfirmation = preview.requires_confirmation;
        token = preview.token;
        snapshot = current;
        const lines = ['请核对实际影响，确认后再次点击提交。', ...preview.impacts.map((item) => `${item.username}：${item.before} → ${item.after}。剩余来源：${item.sources.filter((source) => !source.expired).map((source) => `${source.name}（${source.role}${source.ceiling ? `；共享上限：${source.ceiling}` : ''}）`).join('、') || '无'}`)];
        if (!preview.impacts.length) lines.push('没有有效成员权限变化。');
        if (preview.visibility_notice) lines.push(preview.visibility_notice);
        if (preview.self_loses_management) lines.push('注意：提交后你将失去本项目成员管理权限。');
        output.replaceChildren(...lines.map((line) => { const p = document.createElement('p'); p.textContent = line; return p }));
        button.textContent = '确认并提交变更';
      } catch (error) {
        token = '';
        output.textContent = error instanceof Error ? error.message : '操作失败，请重新预览';
        button.textContent = '重新预览';
      } finally {
        busy = false;
        button.disabled = false;
      }
    });
  }
  document.querySelector<HTMLSelectElement>('#member-custom')?.addEventListener('change', (event) => {
    const select = event.target as HTMLSelectElement;
    const base = select.selectedOptions[0].getAttribute('data-base-role');
    if (base) document.querySelector<HTMLSelectElement>('#member-role')!.value = base;
  });
  // 通用弹窗填充值在点击时同步完成，日期由精确时间戳生成。
  document.addEventListener('click', (event) => {
    if (!(event.target instanceof Element)) return;
    const modalTrigger = event.target.closest<HTMLElement>('[data-modal]');
    if (modalTrigger) {
      const selector = modalTrigger.getAttribute('data-modal')!;
      if (['#repo-member-modal', '#repo-share-modal', '#repo-share-remove-modal'].includes(selector)) {
        document.querySelector(`${selector} form`)!.dispatchEvent(new Event('change', {bubbles: true}));
      }
    }
    const trigger = event.target.closest<HTMLElement>('[data-modal="#repo-member-modal"]');
    if (!trigger) return;
    queueMicrotask(() => {
      const expiry = document.querySelector<HTMLInputElement>('#member-expiry')!;
      const original = Number(trigger.getAttribute('data-modal-member-expiry-unix') || 0);
      expiry.value = original ? new Date((original - 1) * 1000).toISOString().slice(0, 10) : '';
      expiry.dispatchEvent(new Event('change', {bubbles: true}));
    });
  });
}
