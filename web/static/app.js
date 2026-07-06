        // ── Ícones SVG (Lucide) — fonte única. icon(name,size) devolve o markup
        //    inline; usado no command palette e em qualquer render dinâmico. ──
        const ICON_PATHS = {
            home:     '<path d="M15 21v-8a1 1 0 0 0-1-1h-4a1 1 0 0 0-1 1v8"/><path d="M3 10a2 2 0 0 1 .709-1.528l7-6a2 2 0 0 1 2.582 0l7 6A2 2 0 0 1 21 10v9a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2z"/>',
            practice: '<rect width="8" height="4" x="8" y="2" rx="1"/><path d="M16 4h2a2 2 0 0 1 2 2v14a2 2 0 0 1-2 2H6a2 2 0 0 1-2-2V6a2 2 0 0 1 2-2h2"/><path d="M12 11h4"/><path d="M12 16h4"/><path d="M8 11h.01"/><path d="M8 16h.01"/>',
            labs:     '<path d="M14 2v6a2 2 0 0 0 .245.96l5.51 10.08A2 2 0 0 1 18 22H6a2 2 0 0 1-1.755-2.96l5.51-10.08A2 2 0 0 0 10 8V2"/><path d="M6.453 15h11.094"/><path d="M8.5 2h7"/>',
            tutor:    '<path d="M12 8V4H8"/><rect width="16" height="12" x="4" y="8" rx="2"/><path d="M2 14h2"/><path d="M20 14h2"/><path d="M15 13v2"/><path d="M9 13v2"/>',
            incident: '<path d="M7 18v-6a5 5 0 1 1 10 0v6"/><path d="M5 21a1 1 0 0 0 1 1h12a1 1 0 0 0 1-1v-1a1 1 0 0 0-1-1H6a1 1 0 0 0-1 1z"/><path d="M21 12h1"/><path d="M18.5 4.5 18 5"/><path d="M2 12h1"/><path d="M12 2v1"/><path d="M5.5 4.5 6 5"/>',
            exam:     '<path d="M6 9H4.5a2.5 2.5 0 0 1 0-5H6"/><path d="M18 9h1.5a2.5 2.5 0 0 0 0-5H18"/><path d="M4 22h16"/><path d="M10 14.66V17c0 .55-.47.98-.97 1.21C7.85 18.75 7 20.24 7 22"/><path d="M14 14.66V17c0 .55.47.98.97 1.21C16.15 18.75 17 20.24 17 22"/><path d="M18 2H6v7a6 6 0 0 0 12 0V2Z"/>',
            cloud:    '<path d="M17.5 19H9a7 7 0 1 1 6.71-9h1.79a4.5 4.5 0 1 1 0 9Z"/>',
            server:   '<rect width="20" height="8" x="2" y="2" rx="2"/><rect width="20" height="8" x="2" y="14" rx="2"/><path d="M6 6h.01"/><path d="M6 18h.01"/>',
            argocd:   '<line x1="6" x2="6" y1="3" y2="15"/><circle cx="18" cy="6" r="3"/><circle cx="6" cy="18" r="3"/><path d="M18 9a9 9 0 0 1-9 9"/>',
            tools:    '<path d="M14.7 6.3a1 1 0 0 0 0 1.4l1.6 1.6a1 1 0 0 0 1.4 0l3.77-3.77a6 6 0 0 1-7.94 7.94l-6.91 6.91a2.12 2.12 0 0 1-3-3l6.91-6.91a6 6 0 0 1 7.94-7.94l-3.76 3.76z"/>',
            docs:     '<path d="M12 7v14"/><path d="M3 18a1 1 0 0 1-1-1V4a1 1 0 0 1 1-1h5a4 4 0 0 1 4 4 4 4 0 0 1 4-4h5a1 1 0 0 1 1 1v13a1 1 0 0 1-1 1h-6a3 3 0 0 0-3 3 3 3 0 0 0-3-3z"/>',
            stats:    '<line x1="12" x2="12" y1="20" y2="10"/><line x1="18" x2="18" y1="20" y2="4"/><line x1="6" x2="6" y1="20" y2="16"/>',
            flame:    '<path d="M8.5 14.5A2.5 2.5 0 0 0 11 12c0-1.38-.5-2-1-3-1.072-2.143-.224-4.054 2-6 .5 2.5 2 4.9 4 6.5 2 1.6 3 3.5 3 5.5a7 7 0 1 1-14 0c0-1.153.433-2.294 1-3a2.5 2.5 0 0 0 2.5 2.5z"/>',
            zap:      '<path d="M4 14a1 1 0 0 1-.78-1.63l9.9-10.2a.5.5 0 0 1 .86.46l-1.92 6.02A1 1 0 0 0 13 10h7a1 1 0 0 1 .78 1.63l-9.9 10.2a.5.5 0 0 1-.86-.46l1.92-6.02A1 1 0 0 0 11 14z"/>',
            party:    '<path d="M5.8 11.3 2 22l10.7-3.79"/><path d="M4 3h.01"/><path d="M22 8h.01"/><path d="M15 2h.01"/><path d="M22 20h.01"/><path d="m22 2-2.24.75a2.9 2.9 0 0 0-1.96 3.12c.1.86-.57 1.63-1.45 1.63h-.38c-.86 0-1.6.6-1.76 1.44L14 10"/><path d="m22 13-.82-.33c-.86-.34-1.82.2-1.98 1.11-.11.7-.72 1.22-1.43 1.22H17"/><path d="m11 2 .33.82c.34.86-.2 1.82-1.11 1.98-.7.11-1.22.72-1.22 1.43V7"/><path d="M11 13c1.93 1.93 2.83 4.17 2 5-.83.83-3.07-.07-5-2-1.93-1.93-2.83-4.17-2-5 .83-.83 3.07.07 5 2Z"/>',
            flag:     '<path d="M4 15s1-1 4-1 5 2 8 2 4-1 4-1V3s-1 1-4 1-5-2-8-2-4 1-4 1z"/><line x1="4" x2="4" y1="22" y2="15"/>',
            money:    '<circle cx="12" cy="12" r="10"/><path d="M16 8h-6a2 2 0 1 0 0 4h4a2 2 0 1 1 0 4H8"/><path d="M12 18V6"/>',
            lock:     '<rect width="18" height="11" x="3" y="11" rx="2"/><path d="M7 11V7a5 5 0 0 1 10 0v4"/>',
            globe:    '<circle cx="12" cy="12" r="10"/><path d="M12 2a14.5 14.5 0 0 0 0 20 14.5 14.5 0 0 0 0-20"/><path d="M2 12h20"/>',
            plus:     '<path d="M5 12h14"/><path d="M12 5v14"/>'
        };
        function icon(name, size) {
            const p = ICON_PATHS[name]; if (!p) return '';
            const s = size || 16;
            return `<svg class="lic" width="${s}" height="${s}" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">${p}</svg>`;
        }
        window.icon = icon;

        const PAL_ITEMS = [
            { ico: 'home',     label: 'Início', url: '/' },
            { ico: 'practice', label: 'Practice Test (quiz)', url: '/practice' },
            { ico: 'labs',     label: 'Laboratórios', url: '/lab' },
            { ico: 'tutor',    label: 'Tutor (IA local)', url: '/tutor' },
            { ico: 'incident', label: 'Modo Incidente — quebrar e consertar', url: '/tutor?q=incidente' },
            { ico: 'exam',     label: 'Simulado de prova (Modo Exame)', url: '/tutor?q=simulado' },
            { ico: 'cloud',    label: 'Cloud (Azure AKS · multi-cloud)', url: '/cloud' },
            { ico: 'server',   label: 'On-premise (minikube)', url: '/onpremise' },
            { ico: 'argocd',   label: 'ArgoCD', url: '/argocd' },
            { ico: 'tools',    label: 'Instalações — Helm, Grafana, Ingress...', url: '/tools' },
            { ico: 'docs',     label: 'Docs — referência rápida', url: '/docs' },
        ];
        let palSel = 0;
        function openPal() {
            document.getElementById('pal-overlay').classList.add('open');
            const inp = document.getElementById('pal-input');
            inp.value = ''; palSel = 0; palFilter(); inp.focus();
        }
        function closePal() { document.getElementById('pal-overlay').classList.remove('open'); }
        function palVisible() {
            const q = document.getElementById('pal-input').value.toLowerCase();
            return PAL_ITEMS.filter(i => !q || i.label.toLowerCase().includes(q));
        }
        function palFilter() {
            const items = palVisible();
            if (palSel >= items.length) palSel = 0;
            const list = document.getElementById('pal-list');
            list.innerHTML = items.map((i, idx) =>
                `<div class="pal-item${idx === palSel ? ' sel' : ''}" onclick="location.href='${i.url}'">
                    <span class="pi-ico">${icon(i.ico, 16)}</span>${i.label}
                    ${idx === palSel ? '<span class="pi-kbd">enter</span>' : ''}</div>`).join('')
                || '<div class="pal-item">nada por aqui…</div>';
        }
        function palKey(e) {
            const items = palVisible();
            if (e.key === 'ArrowDown') { e.preventDefault(); palSel = (palSel + 1) % items.length; palFilter(); }
            else if (e.key === 'ArrowUp') { e.preventDefault(); palSel = (palSel - 1 + items.length) % items.length; palFilter(); }
            else if (e.key === 'Enter' && items[palSel]) { location.href = items[palSel].url; }
            else if (e.key === 'Escape') { closePal(); }
        }
        document.addEventListener('keydown', e => {
            if ((e.ctrlKey || e.metaKey) && e.key.toLowerCase() === 'k') { e.preventDefault(); openPal(); }
            if (e.key === 'Escape') closePal();
        });

        // ── i18n — PT/EN (termos de Kubernetes ficam SEMPRE em inglês) ──
        const I18N = {
            'nav.study':      { pt: 'estudar', en: 'study' },
            'nav.platform':   { pt: 'plataforma', en: 'platform' },
            'nav.home':       { pt: 'Início', en: 'Home' },
            'nav.labs':       { pt: 'Laboratórios', en: 'Labs' },
            'labs.title':     { pt: 'Laboratórios Práticos', en: 'Hands-on Labs' },
            'labs.sub':       { pt: 'Terminal real, validação automática e ambiente preparado para você.', en: 'Real terminal, automatic validation and a pre-provisioned environment.' },
            'labs.study':     { pt: 'Sessão de Estudo', en: 'Study Session' },
            'labs.study.d':   { pt: 'configure abaixo: hints, solution e tutor liberados', en: 'configure below: hints, solution and tutor enabled' },
            'labs.exam':      { pt: 'Modo Exame', en: 'Exam Mode' },
            'labs.exam.d':    { pt: '16 questões · 2h · sem dicas · score report', en: '16 questions · 2h · no hints · score report' },
            'labs.incident':  { pt: 'Modo Incidente', en: 'Incident Mode' },
            'labs.incident.d':{ pt: 'o cluster chega quebrado — diagnostique e conserte', en: 'the cluster arrives broken — diagnose and fix it' },
            'labs.start':     { pt: 'INICIAR SESSÃO', en: 'START SESSION' },
            'labs.env':       { pt: 'ambiente do cluster', en: 'cluster environment' },
            'labs.qty':       { pt: 'quantidade de questões', en: 'number of questions' },
            'labs.diff':      { pt: 'dificuldade', en: 'difficulty' },
            'labs.cert':      { pt: 'certificação', en: 'certification' },
            'labs.topics':    { pt: 'tópicos', en: 'topics' },
            'home.mode':      { pt: 'escolha um modo', en: 'pick a mode' },
            'nav.practice':   { pt: 'Prova Teórica', en: 'Practice Test' },
            'home.quiz.t':    { pt: 'Prova Teórica', en: 'Practice Test' },
            'home.hero':      { pt: 'Kubernetes<br><em>Certification</em><br>Practice', en: 'Kubernetes<br><em>Certification</em><br>Practice', html: true },
            'home.desc':      { pt: 'CKA · CKAD · CKS · ArgoCD — questões de teoria e labs práticos com kubectl real no seu cluster.', en: 'CKA · CKAD · CKS · ArgoCD — theory questions and hands-on labs with real kubectl on your cluster.' },
            'practice.title': { pt: 'Prova Teórica', en: 'Practice Test' },
            'practice.sub':   { pt: '// questões de teoria — configure e inicie', en: '// theory questions — configure and start' },
            'practice.start': { pt: 'INICIAR PROVA TEÓRICA', en: 'START PRACTICE TEST' },
            'home.resume':    { pt: 'Continuar de onde parou', en: 'Resume where you left off' },
            'home.quiz.d':    { pt: 'Questões de teoria e conceitos. Escolha certificação, dificuldade e quantidade.', en: 'Theory and concept questions. Pick certification, difficulty and amount.' },
            'home.lab.t':     { pt: 'Laboratório Prático', en: 'Hands-on Lab' },
            'home.lab.d':     { pt: 'Terminal real com kubectl, vim e tab completion. Sessões com validação automática.', en: 'Real terminal with kubectl, vim and tab completion. Auto-validated sessions.' },
            'home.progress':  { pt: 'seu progresso', en: 'your progress' },
            'home.checks':    { pt: 'checks feitos', en: 'checks done' },
            'home.rate':      { pt: 'taxa de acerto', en: 'success rate' },
            'home.done':      { pt: 'labs concluídos', en: 'labs completed' },
            'home.daily':     { pt: 'Revisão do dia — 5 questões dos seus pontos fracos', en: 'Daily review — 5 questions on your weak spots' },
        };
        function currentLang() {
            try { return localStorage.getItem('k8slab-lang') || 'pt'; } catch (e) { return 'pt'; }
        }
        function applyLang() {
            const l = currentLang();
            document.documentElement.lang = l === 'en' ? 'en' : 'pt-BR';
            document.querySelectorAll('[data-i18n]').forEach(el => {
                const t = I18N[el.dataset.i18n];
                if (t && t[l]) { if (t.html) el.innerHTML = t[l]; else el.textContent = t[l]; }
            });
            document.querySelectorAll('[data-lang-opt]').forEach(el =>
                el.classList.toggle('active', el.dataset.langOpt === l));
        }
        function setLang(l) {
            try { localStorage.setItem('k8slab-lang', l); } catch (e) {}
            applyLang();
            document.getElementById('theme-pop').classList.remove('open');
            window.dispatchEvent(new CustomEvent('k8slab-lang', { detail: l }));
        }
        document.addEventListener('DOMContentLoaded', applyLang);
        applyLang();

        function currentTheme() {
            return document.documentElement.getAttribute('data-theme') || 'dark';
        }
        function markActiveTheme() {
            var cur = currentTheme();
            document.querySelectorAll('[data-theme-opt]').forEach(function (el) {
                el.classList.toggle('active', el.getAttribute('data-theme-opt') === cur);
            });
        }
        function setTheme(name) {
            if (name === 'dark') document.documentElement.removeAttribute('data-theme');
            else document.documentElement.setAttribute('data-theme', name);
            try { localStorage.setItem('k8slab-theme', name); } catch (e) {}
            markActiveTheme();
            document.getElementById('theme-pop').classList.remove('open');
            // Notifica componentes interessados (ex.: terminal xterm)
            window.dispatchEvent(new CustomEvent('k8slab-theme', { detail: name }));
        }
        function toggleThemePop(ev) {
            ev.stopPropagation();
            markActiveTheme();
            document.getElementById('theme-pop').classList.toggle('open');
        }
        document.addEventListener('click', function (ev) {
            var fab = document.getElementById('theme-fab');
            if (fab && !fab.contains(ev.target)) {
                document.getElementById('theme-pop').classList.remove('open');
            }
        });

        // ── Conta logada — mostra "logado como X · sair" (só quando há login) ──
        async function loadProfile() {
            try {
                const d = await fetch('/api/profile').then(r => r.json());
                if (!d.auth) return; // uso local sem login: sem seção de conta
                // Indicador visível na sidebar (quem está logado)
                const su = document.getElementById('sb-user');
                const sn = document.getElementById('sb-user-name');
                if (su && sn) { sn.textContent = d.profile || '?'; su.style.display = 'flex'; }
                const sec = document.getElementById('profile-section');
                const box = document.getElementById('profile-box');
                if (sec && box) {
                    box.innerHTML =
                        '<span>logado como <strong style="color:var(--text);">' + (d.profile || '?') + '</strong></span>' +
                        '<a href="/logout" class="theme-opt" style="margin-left:auto;flex:0 0 auto;padding:5px 10px;">sair</a>';
                    sec.style.display = '';
                }
            } catch (e) {}
        }
        document.addEventListener('DOMContentLoaded', loadProfile);
