const { test, expect } = require('@playwright/test');

async function ensureLoggedIn(page) {
  await page.goto('/tutor');
  if (!/\/login\b/.test(page.url())) return;

  const invite = process.env.E2E_INVITE_CODE || process.env.APP_PASSWORD || '';
  test.skip(!invite, 'APP_PASSWORD/E2E_INVITE_CODE ausente para ambiente com login habilitado');

  const stamp = Date.now();
  const username = `e2e-${stamp}`;
  const password = `E2E-${stamp}-pass`;

  await page.goto('/register');
  await page.locator('#form-register input[name="username"]').fill(username);
  await page.locator('#form-register input[name="password"]').fill(password);
  await page.locator('#form-register input[name="invite"]').fill(invite);
  await Promise.all([
    page.waitForURL(url => !/\/register\b/.test(url.pathname), { timeout: 30000 }),
    page.locator('#form-register button.go').click()
  ]);
  await page.goto('/tutor');
}

async function openFirstTerminal(page) {
  await expect(page.locator('#terminal-container')).toBeVisible({ timeout: 120000 });
  await expect(page.locator('.xterm-helper-textarea').first()).toBeAttached({ timeout: 120000 });
  await page.locator('.xterm-helper-textarea').first().click();
}

async function expectTutorSession(page, prompt, cert, want) {
  const resp = await page.request.post('/api/tutor/chat', {
    data: { message: prompt, cert }
  });
  expect(resp.ok()).toBeTruthy();
  const body = await resp.json();
  expect(body.action?.type, body.reply).toBe('session');
  expect(body.action.cert, body.reply).toBe(want.cert);
  expect(body.action.topic, body.reply).toBe(want.topic);
  expect(body.action.first, body.reply).toMatch(/custom-|tfgen|ansgen|lab-/);
  expect(body.action.quality || 0, body.reply).toBeGreaterThanOrEqual(75);
  for (const dep of want.dependencies || []) {
    const deps = (body.action.dependencies || []).join(' ').toLowerCase();
    expect(deps, body.reply).toContain(dep.toLowerCase());
  }
}

test('golden eval cobre prompts criticos do tutor', async ({ page }) => {
  await ensureLoggedIn(page);

  const resp = await page.request.get('/api/tutor/eval');
  expect(resp.ok()).toBeTruthy();
  const body = await resp.json();
  expect(body.total).toBeGreaterThanOrEqual(4);
  expect(body.score).toBeGreaterThanOrEqual(75);
  expect(typeof body.regression_total).toBe('number');
  expect(body.quality).toBeTruthy();
  expect((body.cases || []).map(c => c.name)).toEqual(expect.arrayContaining([
    'CKA Static Pods action',
    'CKA HPA',
    'AWS SQS',
    'CAPA ArgoCD Sync',
    'Terraform Variables Outputs'
  ]));

  const quality = await page.request.get('/api/tutor/quality');
  expect(quality.ok()).toBeTruthy();
  const qualityBody = await quality.json();
  expect(typeof qualityBody.total).toBe('number');
});

test('tutor roteia labs por certificacao e topico exato', async ({ page }) => {
  await ensureLoggedIn(page);

  await expectTutorSession(page, 'criar questao da CKA de HPA nivel 3', 'CKA', {
    cert: 'CKA',
    topic: 'Autoscaling',
    dependencies: ['metrics-server']
  });
  await expectTutorSession(page, 'crie um lab de AWS para SQS', 'CKA', {
    cert: 'AWS',
    topic: 'AWS Messaging',
    dependencies: ['localstack']
  });
  await expectTutorSession(page, 'crie um lab da CAPA sobre ArgoCD sync', 'CKA', {
    cert: 'CAPA',
    topic: 'GitOps',
    dependencies: ['argocd']
  });
  await expectTutorSession(page, 'Criar 3 labs de pods estaticos nivel 2', 'CKA', {
    cert: 'CKA',
    topic: 'Static Pods'
  });
});

test('catalogo permite sessao customizada e labs mostram confianca', async ({ page }) => {
  await ensureLoggedIn(page);
  await page.goto('/lab');
  await expect(page.locator('#maker-topic')).toBeVisible();
  await expect(page.locator('#maker-source')).toBeVisible();
  await expect(page.getByRole('button', { name: 'ANALISAR TÓPICOS' })).toBeVisible();
  await expect(page.locator('#maker-count')).toHaveValue('5');
  await page.goto('/lab/cka-lab-001');
  await expect(page.locator('.lab-top-actions')).toContainText(/curado|verifica|valida|simula/i);
});

test('painel de desempenho mostra RAG, observabilidade e golden eval', async ({ page }) => {
  await ensureLoggedIn(page);

  await page.goto('/tutor');
  await expect(page.locator('#chat-text')).toBeVisible();
  await page.locator('#chat-text').fill('como esta meu desempenho?');
  await page.locator('#send-btn').click();

  await expect(page.locator('.rag-status')).toBeVisible({ timeout: 90000 });
  await expect(page.locator('.eval-card')).toBeVisible({ timeout: 90000 });
  await expect(page.locator('.quality-card')).toBeVisible({ timeout: 90000 });
  await expect(page.locator('.eval-card')).toContainText(/golden eval IA/i);
  await expect(page.locator('.quality-card')).toContainText(/dataset real de prompts/i);
  await expect(page.locator('.eval-card')).toContainText(/CKA HPA|AWS SQS|CAPA ArgoCD/i, { timeout: 90000 });
});

test('tutor oferece conversas persistentes, modos e anexos acessiveis', async ({ page }) => {
  await ensureLoggedIn(page);
  await page.goto('/tutor');
  await expect(page.locator('.conversation-rail')).toBeVisible();
  await expect(page.locator('#response-mode')).toHaveValue(/auto|didactic|short|deep|diagnostic|exam/);
  await page.getByRole('button', { name: 'Adicionar contexto' }).click();
  await expect(page.locator('button', { hasText: 'Anexar arquivo' })).toBeVisible();
  await expect(page.locator('button', { hasText: 'Verificar certificacao' })).toBeVisible();
  await page.getByRole('button', { name: 'Selecionar certificacao' }).click();
  await expect(page.locator('#cert-row')).toHaveClass(/open/);
  await expect(page.locator('#cert-active-label')).toHaveText('CKA');
  await expect(page.locator('.tutor-tab')).toHaveCount(4);
  await page.locator('.tutor-tab', { hasText: 'Progresso' }).click();
  await expect(page.locator('#painel')).toHaveClass(/active/);
  await expect(page.locator('.tutor-tab', { hasText: 'Progresso' })).toHaveAttribute('aria-selected', 'true');
  await page.locator('.tutor-tab', { hasText: 'Fontes' }).click();
  await expect(page.locator('#sources-pane')).toHaveClass(/active/);
  await page.locator('.tutor-tab', { hasText: 'Sistema' }).click();
  await expect(page.locator('#system-pane')).toHaveClass(/active/);
  await expect(page.locator('#tutor-plan')).toContainText('orquestração pedagógica');
  await expect(page.locator('body')).not.toContainText(/cache hit|cache miss/i);
  const before = await page.locator('.conversation-item').count();
  await page.locator('.new-chat').click();
  await expect.poll(() => page.locator('.conversation-item').count()).toBeGreaterThanOrEqual(before + 1);
});

test('pedido vago de certificacao pede dominio e competencia', async ({ page }) => {
  await ensureLoggedIn(page);

  const broad = await page.request.post('/api/tutor/chat', { data: { message: 'Crie um lab para CKA', cert: 'CKA', mode: 'auto' } });
  expect(broad.ok()).toBeTruthy();
  const broadBody = await broad.json();
  expect(broadBody.action?.type).toBe('choices');
  expect(broadBody.action?.options).toHaveLength(5);
  expect(broadBody.action?.first).toBeFalsy();

  const domain = await page.request.post('/api/tutor/chat', { data: { message: 'Quero criar um lab de CKA no dominio Cluster Architecture, Installation & Configuration', cert: 'CKA', mode: 'auto' } });
  const domainBody = await domain.json();
  expect(domainBody.action?.type).toBe('choices');
  expect(domainBody.action?.options).toHaveLength(8);
  expect(domainBody.action.options[0].label).toContain('RBAC');
  expect(domainBody.action.options[1].available).toBe(false);
  expect(domainBody.action.options[1].researchable).toBe(true);
  expect(domainBody.action.options[1].prompt).toMatch(/Pesquise a documentacao oficial/i);

  await page.goto('/tutor');
  await page.getByRole('button', { name: 'Adicionar contexto' }).click();
  await expect(page.getByRole('button', { name: 'Verificar certificacao' })).toBeVisible();
  await page.getByRole('button', { name: 'Verificar certificacao' }).click();
  await expect(page.locator('#curriculum-panel')).toHaveClass(/open/);
  await expect(page.locator('#verify-cert')).toHaveValue('CKA');
});

test('checkpoint pedagogico aparece como card interativo', async ({ page }) => {
  await ensureLoggedIn(page);
  await page.goto('/tutor');
  await expect(page.locator('.tutor-tabbar')).toBeVisible();
  await expect(page.locator('#chat-text')).toBeVisible();
  const css = await page.locator('.chat-input-zone').evaluate(el => getComputedStyle(el).position);
  expect(css).toBe('absolute');
  const layout = await page.locator('.tutor-cols').boundingBox();
  const viewport = page.viewportSize();
  expect(layout.width).toBeGreaterThan(viewport.width * 0.8);
  const sidebarWidth = await page.locator('.sidebar').evaluate(el => Math.round(getComputedStyle(el).width.replace('px','')));
  expect(sidebarWidth).toBeLessThanOrEqual(72);
});

test('login, cria lab pelo tutor, abre terminal e valida comando real', async ({ page }) => {
  await ensureLoggedIn(page);

  await expect(page.locator('#chat-text')).toBeVisible();
  await page.locator('#chat-text').fill('criar questao da CKA de HPA nivel 3');
  await page.locator('#send-btn').click();

  const startLab = page.getByRole('link', { name: /Come[cç]ar/i }).first();
  await expect(startLab).toBeVisible({ timeout: 90000 });
  const generatedHref = await startLab.getAttribute('href');
  expect(generatedHref || '').toContain('/lab/');

  await startLab.click();
  await expect(page).toHaveURL(/\/lab\//, { timeout: 30000 });
  await openFirstTerminal(page);

  await page.goto('/lab/cka-lab-001');
  await openFirstTerminal(page);
  await page.keyboard.type('kubectl delete pod nginx-pod --ignore-not-found=true --wait=false; kubectl run nginx-pod --image=nginx:1.21');
  await page.keyboard.press('Enter');

  await expect(page.locator('.xterm-rows').first()).toContainText(/nginx-pod|created|configured|AlreadyExists/i, {
    timeout: 120000
  });

  const validation = await page.request.get('/lab/cka-lab-001/validate?goal=0');
  expect(validation.ok()).toBeTruthy();
  const body = await validation.json();
  expect(body.success, body.output).toBeTruthy();
});
