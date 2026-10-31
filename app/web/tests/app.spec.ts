import { test, expect } from '@playwright/test';

test.describe('Auth Page', () => {
  test('renders server info and token generator', async ({ page }) => {
    await page.goto('/');

    // Should show Auth heading
    await expect(page.locator('h1')).toHaveText('Auth');

    // Should show server info table
    await expect(page.locator('text=Transport Port')).toBeVisible();
    await expect(page.locator('text=Protocols')).toBeVisible();
    await expect(page.locator('text=Version')).toBeVisible();

    // Should show token generator
    await expect(page.locator('h3')).toHaveText('Generate Node Token');
    await expect(page.locator('input[placeholder="Node ID"]')).toBeVisible();
    await expect(page.locator('button', { hasText: 'Generate' })).toBeVisible();
  });

  test('generates a node token', async ({ page }) => {
    await page.goto('/');

    // Fill node ID
    const input = page.locator('input[placeholder="Node ID"]');
    await input.fill('test-pw-node');

    // Click generate
    await page.locator('button', { hasText: 'Generate' }).click();

    // Should show token (JWT starting with ey)
    await expect(page.locator('text=Token:')).toBeVisible();
    // The token pre is after "Token:" label, skip the public key pre
    const tokenBlock = page.locator('label:has-text("Token:") + pre');
    await expect(tokenBlock).toContainText('eyJ');

    // Should show tai start command
    await expect(page.locator('text=tai start command:')).toBeVisible();
    const cmdBlock = page.locator('label:has-text("tai start command:") + pre');
    await expect(cmdBlock).toContainText('tai start --node-id test-pw-node');
  });
});

test.describe('Nodes Page', () => {
  test('renders empty node list', async ({ page }) => {
    await page.goto('/nodes');

    await expect(page.locator('h1')).toHaveText('Nodes');
    await expect(page.locator('text=Registered nodes: 0')).toBeVisible();
    await expect(page.locator('text=No nodes registered')).toBeVisible();
  });
});

test.describe('Sessions Page', () => {
  test('renders empty session list with create form', async ({ page }) => {
    await page.goto('/sessions');

    await expect(page.locator('h1')).toHaveText('Sessions');
    await expect(page.locator('h3')).toHaveText('Sessions');
    await expect(page.locator('text=No active sessions')).toBeVisible();

    // Create session button should exist
    await expect(page.locator('button', { hasText: 'Create Session' })).toBeVisible();
    await expect(page.locator('button', { hasText: 'Refresh' })).toBeVisible();
  });

  test('shows error when creating session without node', async ({ page }) => {
    await page.goto('/sessions');

    await page.locator('button', { hasText: 'Create Session' }).click();

    // Should show an error
    await expect(page.locator('text=No nodes available')).toBeVisible();
  });
});

test.describe('Docs Page', () => {
  test('renders doc navigation and content', async ({ page }) => {
    await page.goto('/docs');

    // Should show document navigation
    await expect(page.locator('h3', { hasText: 'Documentation' })).toBeVisible();

    // Should list documents in the nav sidebar
    const docNav = page.locator('h3:has-text("Documentation")').locator('..');
    await expect(docNav.locator('text=Quick Start')).toBeVisible();
    await expect(docNav.locator('text=Architecture')).toBeVisible();
    await expect(docNav.locator('text=API Reference')).toBeVisible();

    // First doc should be auto-loaded
    // Wait for markdown content to load
    await page.waitForTimeout(500);
    // The first doc (api-reference.md) should have some content rendered
    await expect(page.locator('main').locator('h1').first()).toBeVisible();
  });

  test('clicking a doc loads its content', async ({ page }) => {
    await page.goto('/docs');
    await page.waitForTimeout(300);

    // Click on "Quick Start"
    await page.locator('text=Quick Start').click();
    await page.waitForTimeout(500);

    // Should render quickstart content
    await expect(page.locator('main')).toContainText('Prerequisites');
  });
});

test.describe('Navigation', () => {
  test('sidebar navigation works', async ({ page }) => {
    await page.goto('/');

    // Verify sidebar links
    const nav = page.locator('nav');
    await expect(nav.locator('text=Auth')).toBeVisible();
    await expect(nav.locator('text=Nodes')).toBeVisible();
    await expect(nav.locator('text=Sessions')).toBeVisible();
    await expect(nav.locator('text=Docs')).toBeVisible();

    // Navigate to Nodes
    await nav.locator('text=Nodes').click();
    await expect(page.locator('h1')).toHaveText('Nodes');

    // Navigate to Sessions
    await nav.locator('text=Sessions').click();
    await expect(page.locator('h1')).toHaveText('Sessions');

    // Navigate to Docs
    await nav.locator('text=Docs').click();
    await expect(page.locator('h3', { hasText: 'Documentation' })).toBeVisible();

    // Navigate back to Auth
    await nav.locator('text=Auth').click();
    await expect(page.locator('h1')).toHaveText('Auth');
  });

  test('active nav item is highlighted', async ({ page }) => {
    await page.goto('/nodes');

    const nodesLink = page.locator('nav a', { hasText: 'Nodes' });
    // Active link should have blue background
    await expect(nodesLink).toHaveCSS('background-color', 'rgb(0, 102, 204)');
    await expect(nodesLink).toHaveCSS('color', 'rgb(255, 255, 255)');
  });
});
