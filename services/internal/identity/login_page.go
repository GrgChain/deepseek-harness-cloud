package identity

import (
	"html/template"
	"net/http"
)

var loginTemplate = template.Must(template.New("login").Parse(`<!doctype html>
<html lang="zh-CN"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>登录 · Harness Cloud</title><link rel="stylesheet" href="/oidc/login.css"></head>
<body><main><a class="brand" href="/login"><span class="mark" aria-hidden="true">H</span> Harness Cloud</a><section aria-labelledby="login-title"><h1 id="login-title">登录账户</h1><p class="intro">登录后进入你的工作空间</p>
{{if .Error}}<p class="error" role="alert">{{.Error}}</p>{{end}}
<form method="post" action="/oidc/login"><input type="hidden" name="request" value="{{.Request}}"><input type="hidden" name="csrf" value="{{.CSRF}}">
<label for="login">邮箱</label><input id="login" name="login" type="email" autocomplete="username" required maxlength="254" autofocus>
<label for="password">密码</label><input id="password" name="password" type="password" autocomplete="current-password" required maxlength="72">
<button type="submit">登录</button></form><a class="back" href="/auth/login">重新开始登录</a></section></main></body></html>`))

func (s *AuthorizationServer) loginPage(w http.ResponseWriter, status int, request, csrf, message string) {
	// Chromium derives a form POST's Origin from the document's referrer
	// policy. no-referrer produces an opaque/null Origin even on this origin.
	// strict-origin preserves the exact origin without leaking authorize query.
	w.Header().Set("Referrer-Policy", "strict-origin")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_ = loginTemplate.Execute(w, struct{ Request, CSRF, Error string }{request, csrf, message})
}

const loginCSS = `:root{font-family:system-ui,-apple-system,"Segoe UI","Microsoft YaHei",sans-serif;color:#172033;background:#f6f8fb;color-scheme:light}*{box-sizing:border-box}body{margin:0}main{width:min(100% - 40px,420px);margin:clamp(40px,12vh,120px) auto}.brand{display:flex;align-items:center;gap:12px;color:#111c30;text-decoration:none;font-weight:700;font-size:20px;margin-bottom:32px}.mark{display:grid;place-items:center;background:#111c30;color:white;width:38px;height:38px;border-radius:10px}section{background:white;padding:32px;border:1px solid #e4e8ef;border-radius:16px;box-shadow:0 8px 28px #17203308}h1{font-size:25px;letter-spacing:-.5px;margin:0 0 8px}.intro{font-size:14px;color:#526078;margin:0 0 28px}label{display:block;font-size:14px;font-weight:600;margin:20px 0 8px}input:not([type=hidden]){width:100%;height:46px;border:1px solid #c6ceda;border-radius:8px;padding:10px 12px;font-size:16px;background:white;color:#172033}input:focus-visible,button:focus-visible,a:focus-visible{outline:3px solid #93b4fa;outline-offset:3px}button{width:100%;height:46px;margin-top:28px;border:0;border-radius:8px;background:#2563eb;color:white;font-size:16px;font-weight:600;cursor:pointer}button:hover{background:#1d4ed8}.back{display:block;text-align:center;margin-top:20px;color:#526078;font-size:13px}.error{background:#fff1f2;border:1px solid #fecdd3;padding:12px;color:#9f1239;font-size:14px;border-radius:8px}@media(max-width:480px){section{padding:24px}}@media(prefers-reduced-motion:reduce){*{scroll-behavior:auto}}`
