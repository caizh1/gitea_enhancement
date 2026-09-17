// Copyright 2026 企业治理贡献者。
// SPDX-License-Identifier: MIT

export function initGovernanceInvitations() {
  const root = document.querySelector<HTMLElement>('[data-governance-invitation]');
  if (!root) return;
  const key = `governance-invitation-${root.getAttribute('data-governance-invitation')}`;
  if (root.getAttribute('data-completed') === 'true') {
    try { sessionStorage.removeItem(key) } catch { /* 浏览器禁用存储时仍保留正常完成页面。 */ }
    return;
  }
  let token = new URLSearchParams(window.location.hash.slice(1)).get('token') || '';
  if (/^[0-9a-f]{64}$/.test(token)) {
    try { sessionStorage.setItem(key, token) } catch { /* 无存储时当前页面仍可预览。 */ }
    // 清除地址栏令牌，登录跳转只传无秘密的邀请路径。
    window.history.replaceState(null, '', window.location.pathname + window.location.search);
  } else {
    try { token = sessionStorage.getItem(key) || '' } catch { token = '' }
  }
  const form = root.querySelector<HTMLFormElement>('form[data-invitation-preview]');
  const input = form?.querySelector<HTMLInputElement>('input[name="token"]');
  if (form && input && /^[0-9a-f]{64}$/.test(token)) {
    input.value = token;
    form.requestSubmit();
  }
}
