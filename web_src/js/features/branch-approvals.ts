import {POST} from '../modules/fetch.ts';
import '../../css/features/branch-approvals.css';

type Rule = {
  id: number; revision: number; name: string; required: number; scope_type: string; scope_id: number;
  user_ids: number[]; group_ids: number[]; team_ids: number[]; all_eligible: boolean; enabled: boolean;
  branch_mode: string; branches: string[]; protection_ids: number[]; native_protection_id: number;
  subjects_hidden?: boolean;
};
type Source = {value: boolean; locked: boolean; scope_type: string; scope_id: number};
type Configuration = {
  version: string; protection_id: number; can_reauthenticate: boolean; rules: Rule[]; settings_revision: number; can_manage: boolean;
  settings: {sources: Record<string, Source>};
  presentation: Record<number, {source: string; source_url?: string; notice?: string; scope?: string; editable: boolean; applicable: boolean; shared: boolean}>;
};
type Change = {rule: Rule; revision: number; remove: boolean};
const labels: Record<string, string> = {
  prevent_author: '禁止作者批准自己的 PR', prevent_committer: '禁止提交者批准',
  reset_on_change: '代码差异变化后重新批准', prevent_overrides: '禁止在 PR 中覆盖仓库审批规则',
  require_reauthentication: '批准前再次认证',
};

export function initBranchApprovals() {
  const root = document.querySelector<HTMLElement>('[data-approval-state]');
  if (!root) return;
  const form = root.closest('form')!;
  for (const control of form.querySelectorAll<HTMLSelectElement>('[data-protection-control]')) {
    const name = control.getAttribute('data-protection-control')!;
    const radios = form.querySelectorAll<HTMLInputElement>(`input[name="${CSS.escape(name)}"]`);
    control.addEventListener('change', () => {
      for (const radio of radios) radio.checked = radio.value === control.value;
      const chosen = Array.from(radios).find((radio) => radio.checked)!;
      chosen.dispatchEvent(new Event('change', {bubbles: true}));
      if (control.value === 'whitelist' || control.value === 'true') form.querySelector<HTMLDetailsElement>('[data-protection-advanced]')!.open = true;
    });
    for (const radio of radios) radio.addEventListener('change', () => { if (radio.checked) control.value = radio.value; });
  }

  const state: Configuration = JSON.parse(root.getAttribute('data-approval-state')!);
  const choices: Record<string, {ID: number; Name: string}[]> = JSON.parse(root.getAttribute('data-approval-choices')!);
  const repositoryMode = root.getAttribute('data-repository-mode') === 'true';
  const rows = root.querySelector<HTMLTableSectionElement>('[data-approval-rows]')!;
  const dialog = root.querySelector<HTMLDialogElement>('[data-approval-dialog]')!;
  const error = root.querySelector<HTMLElement>('[data-approval-error]')!;
  const changes = new Map<number, Change>();
  let rules: Rule[] = structuredClone(state.rules || []);
  let editing = 0;
  let nextDraftID = -1;
  let dirty = false;
  let submitting = false;
  const input = (key: string) => dialog.querySelector<HTMLInputElement>(`[data-rule-${CSS.escape(key)}]`)!;
  const select = (key: string) => dialog.querySelector<HTMLSelectElement>(`[data-rule-${CSS.escape(key)}]`)!;
  const relevant = (rule: Rule) => rule.id < 0 || repositoryMode || state.presentation[rule.id]?.applicable;
  const editable = (rule: Rule) => state.can_manage && (rule.id < 0 || state.presentation[rule.id]?.editable);
  const settings = root.querySelector<HTMLElement>('[data-approval-settings]')!;
  for (const [key, label] of Object.entries(labels)) {
    const source = state.settings.sources[key];
    const line = document.createElement('label');
    const box = document.createElement('input');
    box.type = 'checkbox'; box.checked = source.value; box.disabled = !state.can_manage || source.locked || (key === 'require_reauthentication' && !state.can_reauthenticate && !source.value);
    box.setAttribute('data-approval-setting', key);
    line.className = 'tw-flex tw-items-center tw-gap-2 tw-flex-wrap';
    line.append(box, document.createTextNode(label));
    if (key === 'require_reauthentication' && !state.can_reauthenticate) line.append(document.createTextNode('（当前账号认证方式不支持新启用此项）'));
    if (source.locked) line.append(document.createTextNode(`（继承锁定：${source.scope_type === 'instance' ? '实例' : '上级群组'}）`));
    settings.append(line);
  }
  const subjectNames = (rule: Rule) => {
    const names: string[] = [];
    if (rule.all_eligible) names.push('全部合格审批人');
    for (const [field, choice] of [['user_ids', 'Users'], ['group_ids', 'Groups']] as const) {
      for (const id of rule[field] || []) names.push((choices[choice] || []).find((item) => item.ID === id)?.Name || '不可用的历史主体');
    }
    if (rule.team_ids?.length) names.push('历史审批要求（兼容保留）');
    if (rule.subjects_hidden) names.push('部分主体不可见');
    return names.join('、') || '尚未指定';
  };
  const openEditor = (rule?: Rule) => {
    editing = rule ? rule.id : 0;
    input('name').value = rule ? rule.name : '';
    input('required').value = String(rule ? rule.required : 1);
    input('required').max = String(Math.max(100, rule ? rule.required : 100));
    input('all').checked = rule ? rule.all_eligible : false;
    input('enabled').checked = rule ? rule.enabled : true;
    input('enabled').disabled = Boolean(rule?.native_protection_id);
    for (const [key, field] of [['users', 'user_ids'], ['groups', 'group_ids']] as const) {
      for (const option of select(key).options) option.selected = Boolean(rule && (rule[field] || []).includes(Number(option.value)));
    }
    select('groups').disabled = Boolean(rule?.native_protection_id);
    select('scope').value = rule ? rule.branch_mode : 'all';
    for (const option of select('protections').options) option.selected = Boolean(rule?.protection_ids?.includes(Number(option.value)));
    dialog.querySelector<HTMLTextAreaElement>('[data-rule-branches]')!.value = rule ? (rule.branches || []).join('\n') : '';
    dialog.querySelector<HTMLElement>('[data-rule-scope-field]')!.hidden = !repositoryMode || Boolean(rule?.native_protection_id);
    dialog.querySelector<HTMLElement>('[data-rule-legacy]')!.hidden = !rule?.native_protection_id;
    dialog.querySelector<HTMLElement>('[data-rule-error]')!.textContent = '';
    dialog.querySelector<HTMLElement>('[data-approval-dialog-title]')!.textContent = rule ? '编辑审批规则' : '添加审批规则';
    dialog.showModal(); input('name').focus();
  };
  const render = () => {
    rows.replaceChildren();
    for (const rule of rules.filter(relevant)) {
      const row = document.createElement('tr'); row.setAttribute('data-rule-row', String(rule.id));
      const values = [rule.name, subjectNames(rule), rule.enabled ? (rule.required ? `${rule.required} 人` : '可选') : '已停用', state.presentation[rule.id]?.source || '本仓库'];
      for (const value of values) { const cell = document.createElement('td'); cell.textContent = value; row.append(cell) }
      for (const detail of [state.presentation[rule.id]?.scope, state.presentation[rule.id]?.notice]) {
        if (detail) { const note = document.createElement('div'); note.className = 'tw-text-xs tw-mt-2'; note.textContent = detail; row.firstElementChild!.append(note) }
      }
      const actions = document.createElement('td');
      const group = document.createElement('div'); group.className = 'tw-flex tw-items-center tw-gap-2 tw-flex-wrap';
      if (editable(rule)) {
        const edit = document.createElement('button'); edit.type = 'button'; edit.className = 'ui tiny basic button'; edit.textContent = '编辑'; edit.addEventListener('click', () => openEditor(rule)); group.append(edit);
        if (!rule.native_protection_id) {
          const remove = document.createElement('button'); remove.type = 'button'; remove.className = 'ui tiny basic red button'; remove.textContent = '删除';
          remove.addEventListener('click', () => { rules = rules.filter((item) => item.id !== rule.id); if (rule.id > 0) changes.set(rule.id, {rule, revision: rule.revision, remove: true}); else changes.delete(rule.id); dirty = true; render() });
          group.append(remove);
        }
      } else if (rule.scope_type === 'repository') {
        const link = document.createElement('a'); link.href = root.getAttribute('data-project-link')!; link.textContent = '影响多个分支 · 管理'; group.append(link);
      } else if (state.presentation[rule.id]?.source_url) {
        const link = document.createElement('a'); link.href = state.presentation[rule.id].source_url!; link.textContent = '查看来源'; group.append(link);
      } else { group.textContent = '继承锁定' }
      actions.append(group); row.append(actions); rows.append(row);
    }
    root.querySelector<HTMLElement>('[data-approval-empty]')!.hidden = rows.children.length !== 0;
  };
  root.querySelector('[data-approval-add]')?.addEventListener('click', () => openEditor());
  dialog.querySelector('[data-rule-cancel]')!.addEventListener('click', () => dialog.close());
  dialog.addEventListener('keydown', (event) => {
    if (event.key === 'Enter' && event.target instanceof HTMLInputElement) {event.preventDefault(); dialog.querySelector<HTMLButtonElement>('[data-rule-apply]')!.click()}
  });
  dialog.querySelector('[data-rule-apply]')!.addEventListener('click', () => {
    const previous = rules.find((rule) => rule.id === editing);
    const name = input('name').value.trim(); const required = Number(input('required').value);
    if (!name || !input('required').value.trim() || !Number.isInteger(required) || required < 0 || required > Math.max(100, previous ? previous.required : 100)) {
      dialog.querySelector<HTMLElement>('[data-rule-error]')!.textContent = '请填写规则名称和有效的审批人数。'; return;
    }
    const id = previous ? previous.id : nextDraftID--;
    const rule = {...previous, id, revision: previous ? previous.revision : 0, name, required, scope_type: 'repository', scope_id: previous ? previous.scope_id : 0,
      all_eligible: input('all').checked, enabled: input('enabled').checked,
      user_ids: Array.from(select('users').selectedOptions, (o) => Number(o.value)), group_ids: Array.from(select('groups').selectedOptions, (o) => Number(o.value)), team_ids: previous?.team_ids || [],
      branch_mode: previous?.native_protection_id ? 'protection_ids' : repositoryMode ? select('scope').value : 'protection_ids',
      branches: dialog.querySelector<HTMLTextAreaElement>('[data-rule-branches]')!.value.split('\n').map((s) => s.trim()).filter(Boolean),
      protection_ids: previous?.native_protection_id ? previous.protection_ids : repositoryMode ? Array.from(select('protections').selectedOptions, (o) => Number(o.value)) : [state.protection_id], native_protection_id: previous ? previous.native_protection_id : 0,
    };
    if (previous) rules = rules.map((item) => item.id === id ? rule : item); else rules.push(rule);
    changes.set(id, {rule, revision: rule.revision, remove: false}); dirty = true; render(); dialog.close();
  });
  form.addEventListener('input', () => { if (!dialog.open) dirty = true; });
  window.addEventListener('beforeunload', (event) => { if (dirty && !submitting) { event.preventDefault() } });
  form.addEventListener('submit', async (event) => {
    event.preventDefault(); if (submitting) return;
    const updatedSettings: Record<string, boolean | number> = {revision: state.settings_revision};
    let settingsChanged = false;
    for (const box of settings.querySelectorAll<HTMLInputElement>('[data-approval-setting]')) {
      const key = box.getAttribute('data-approval-setting')!; updatedSettings[key] = box.checked;
      settingsChanged ||= box.checked !== state.settings.sources[key].value;
    }
    const payload = {version: state.version, protection_id: state.protection_id, rules: Array.from(changes.values(), (change) => ({...change, rule: {...change.rule, id: Math.max(0, change.rule.id)}})), ...(settingsChanged && {settings: updatedSettings})};
    root.querySelector<HTMLInputElement>('[data-approval-payload]')!.value = JSON.stringify(payload);
    submitting = true; error.classList.add('tw-hidden');
    try {
      const response = await POST(form.action, {data: new FormData(form)});
      if (response.redirected) throw new Error('会话或页面状态已变化，请在保留草稿的情况下重新登录并核对。');
      const result = await response.json();
      if (!response.ok) {
        if (Number.isInteger(result.rule_index)) {
          const change = Array.from(changes.values())[result.rule_index];
          const row = change && rows.querySelector(`[data-rule-row="${CSS.escape(String(change.rule.id))}"]`);
          if (row) {
            row.classList.add('error');
            const message = document.createElement('p'); message.textContent = result.message; message.className = 'tw-text-red';
            row.firstElementChild!.append(message);
          }
        }
        throw new Error(result.message || '保存失败，草稿已保留。');
      }
      dirty = false; window.location.assign(result.redirect || window.location.href);
    } catch (errorValue) {
      error.textContent = errorValue instanceof TypeError ? '未能确认保存结果，草稿已保留。请在另一页面核对已保存配置，勿盲目重复提交。' : errorValue instanceof Error ? errorValue.message : '保存失败，草稿已保留。';
      error.classList.remove('tw-hidden'); submitting = false;
    }
  });
  render();
}
