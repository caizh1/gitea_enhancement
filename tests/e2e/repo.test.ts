import {env} from 'node:process';
import {expect, test} from '@playwright/test';
import {login, randomString} from './utils.ts';

test('create a repository', async ({page}) => {
  const repoName = `e2e-repo-${randomString(8)}`;
  await login(page);
  await page.goto('/repo/create');
  await page.locator('input[name="repo_name"]').fill(repoName);
  await page.getByRole('button', {name: 'Create Repository'}).click();
  await page.waitForURL(new RegExp(`/${env.GITEA_TEST_E2E_USER}/${repoName}$`));
});

test('创建仓库的可见性对齐与名称联动', async ({page}) => {
  const errors: string[] = [];
  page.on('pageerror', (error) => {
    errors.push(error.message);
  });
  await login(page);
  await page.goto('/repo/create');
  await page.waitForFunction(() => window.config.frontendInited);
  const name = page.locator('input[name="repo_name"]');
  const visibility = page.locator('select[name="visibility"]');
  const dropdown = page.locator('.ui.dropdown').filter({has: visibility});
  for (const width of [1440, 768, 390]) {
    await page.setViewportSize({width, height: 1000});
    const nameBox = (await name.boundingBox())!;
    const visibilityBox = (await dropdown.boundingBox())!;
    expect(Math.abs(nameBox.x - visibilityBox.x)).toBeLessThan(2);
    expect(Math.abs(nameBox.width - visibilityBox.width)).toBeLessThan(2);
    expect(visibilityBox.x + visibilityBox.width).toBeLessThanOrEqual(width);
  }
  for (const [repoName, value] of [['.profile-private', '2'], ['.profile', '0']]) {
    await name.fill(repoName);
    await expect(visibility).toHaveValue(value);
    await expect(dropdown.locator('.text')).toHaveText(await visibility.locator(`option[value="${value}"]`).innerText());
  }
  await name.fill('normal-repository');
  await expect(visibility).toHaveValue('0');
  expect(errors).toEqual([]);
});
