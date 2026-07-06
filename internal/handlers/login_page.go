package handlers

import (
	"fmt"
	"html"
)

// renderLogin monta a página de login/registro. tab = "login" | "register"
// define qual aba abre; errMsg (se houver) aparece em vermelho.
func renderLogin(errMsg, tab string) string {
	errHTML := ""
	if errMsg != "" {
		errHTML = `<div class="err">` + html.EscapeString(errMsg) + `</div>`
	}
	if tab != "register" {
		tab = "login"
	}
	return fmt.Sprintf(loginPageTmpl, tab, errHTML)
}

const loginPageTmpl = `<!DOCTYPE html>
<html lang="pt-BR"><head><meta charset="UTF-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<title>K8s Study Lab — Entrar</title>
<link rel="icon" type="image/svg+xml" href="/static/favicon.svg">
<style>
*{box-sizing:border-box}
body{margin:0;min-height:100vh;display:flex;align-items:center;justify-content:center;
background:#050510;font-family:system-ui,-apple-system,Segoe UI,Roboto,sans-serif;color:#eef0ff}
.card{width:100%%;max-width:340px;padding:32px 30px;border-radius:14px;
background:#0d0d20;border:1px solid #20204a}
.brand{font-family:ui-monospace,SFMono-Regular,Menlo,monospace;font-weight:600;color:#818cf8;font-size:15px;text-align:center;margin-bottom:4px}
.sub{font-size:12px;color:#50527a;text-align:center;margin:0 0 20px}
.tabs{display:flex;gap:6px;margin-bottom:18px}
.tab{flex:1;padding:8px;border-radius:8px;border:1px solid #20204a;background:#08081a;color:#9193b8;
font-size:12px;font-weight:700;cursor:pointer;text-align:center}
.tab.on{background:#18183a;color:#eef0ff;border-color:#5b5fef}
label{display:block;font-size:11px;color:#9193b8;margin:0 0 5px}
input{width:100%%;padding:11px 13px;border-radius:8px;margin-bottom:14px;
border:1px solid #20204a;background:#08081a;color:#eef0ff;font-size:14px;outline:none}
input:focus{border-color:#5b5fef}
button.go{width:100%%;padding:11px;border-radius:8px;border:none;cursor:pointer;font-weight:800;font-size:13px;
background:linear-gradient(135deg,#7c82f7,#5b5fef);color:#fff}
.err{color:#f87171;font-size:11px;text-align:center;margin-top:12px}
.hint{font-size:10.5px;color:#50527a;margin:-8px 0 14px}
.hide{display:none}
</style></head>
<body data-tab="%s">
<div class="card">
  <div class="brand">⎈ k8s study lab</div>
  <p class="sub">entre com a sua conta</p>
  <div class="tabs">
    <div class="tab" id="tab-login" onclick="showTab('login')">Entrar</div>
    <div class="tab" id="tab-register" onclick="showTab('register')">Criar conta</div>
  </div>

  <form id="form-login" method="POST" action="/login">
    <label>usuário</label>
    <input name="username" autocomplete="username" autofocus>
    <label>senha</label>
    <input type="password" name="password" autocomplete="current-password">
    <button class="go" type="submit">Entrar</button>
  </form>

  <form id="form-register" method="POST" action="/register" class="hide">
    <label>usuário</label>
    <input name="username" autocomplete="username">
    <label>senha</label>
    <input type="password" name="password" autocomplete="new-password">
    <label>código de convite</label>
    <input type="password" name="invite" autocomplete="off">
    <div class="hint">peça o código de convite para quem hospeda a plataforma.</div>
    <button class="go" type="submit">Criar conta e entrar</button>
  </form>

  %s
</div>
<script>
function showTab(t){
  document.getElementById('form-login').classList.toggle('hide', t!=='login');
  document.getElementById('form-register').classList.toggle('hide', t!=='register');
  document.getElementById('tab-login').classList.toggle('on', t==='login');
  document.getElementById('tab-register').classList.toggle('on', t==='register');
}
showTab(document.body.dataset.tab || 'login');
</script>
</body></html>`
