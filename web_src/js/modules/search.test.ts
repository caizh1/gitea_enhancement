import {attachSearchBox} from './search.ts';
import {GET} from './fetch.ts';

vi.mock('./fetch.ts', () => ({GET: vi.fn()}));

test('外部点击和失焦只取消待执行搜索，重新输入仍可搜索', async () => {
  vi.useFakeTimers();
  const box = document.createElement('div');
  box.innerHTML = '<input class="prompt">';
  document.body.append(box);
  const input = box.querySelector('input')!;
  vi.mocked(GET).mockResolvedValue({ok: true, json: async () => ({data: ['platform-team']})} as Response);
  attachSearchBox<{data: string[]}>(box, '/groups?q={query}', (response) => response.data.map((title) => ({title})));
  try {
    document.body.click();
    input.value = 'platform';
    input.dispatchEvent(new Event('input'));
    await vi.advanceTimersByTimeAsync(201);
    expect(GET).toHaveBeenCalledTimes(1);
    expect(box.querySelector('.result')!.textContent).toBe('platform-team');
    input.dispatchEvent(new Event('blur'));
    await vi.advanceTimersByTimeAsync(151);
    input.dispatchEvent(new Event('focus'));
    input.value = 'platform-team';
    input.dispatchEvent(new Event('input'));
    await vi.advanceTimersByTimeAsync(201);
    expect(GET).toHaveBeenCalledTimes(2);
    const result = box.querySelector('.result')!;
    result.dispatchEvent(new MouseEvent('mousedown', {bubbles: true}));
    expect(input.value).toBe('platform-team');
  } finally {
    box.remove();
    vi.useRealTimers();
    vi.clearAllMocks();
  }
});
