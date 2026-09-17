import {createApp} from 'vue';
import '../../css/features/governance-navigation.css';
import GroupNavigationPanel from '../components/GroupNavigationPanel.vue';

export function initGovernanceNavigation() {
  for (const element of document.querySelectorAll<HTMLElement>('.governance-navigation-panel')) {
    createApp(GroupNavigationPanel, {groupId: Number(element.getAttribute('data-group-id'))}).mount(element);
  }
  const memberForm = document.querySelector<HTMLDetailsElement>('#group-member-form');
  for (const link of document.querySelectorAll<HTMLAnchorElement>('a[href="#group-member-form"]')) {
    link.addEventListener('click', (event) => {
      if (!memberForm) return;
      event.preventDefault();
      memberForm.open = true;
      const expiry = Number(link.getAttribute('data-member-expiry') || 0);
      const values: Record<string, string> = {
        'member-name': link.getAttribute('data-member-name') || '',
        'member-role': link.getAttribute('data-member-role') || '20',
        'member-expiry': expiry ? new Date(expiry * 1000 - 1).toISOString().slice(0, 10) : '',
        'member-custom-role': link.getAttribute('data-member-custom-role') || '0',
      };
      for (const [id, value] of Object.entries(values)) {
        const input = document.querySelector<HTMLInputElement | HTMLSelectElement>(`#${id}`);
        if (input) input.value = value;
      }
      document.querySelector<HTMLInputElement>('#member-name')?.focus();
    });
  }
  const pathInput = document.querySelector<HTMLInputElement>('#group-path');
  const preview = document.querySelector<HTMLOutputElement>('#group-full-path-preview');
  if (pathInput && preview) {
    const update = () => { preview.textContent = `${pathInput.getAttribute('data-parent-path') || ''}${pathInput.value}` };
    pathInput.addEventListener('input', update);
    update();
  }
}
