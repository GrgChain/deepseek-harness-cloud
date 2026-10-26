(() => {
  const form = document.querySelector('form');
  const password = document.querySelector('#password');
  const toggle = document.querySelector('.password-toggle');
  const submit = document.querySelector('.submit');
  const label = document.querySelector('.submit-label');
  const status = document.querySelector('.submit-status');
  const spinner = document.querySelector('.spinner');
  const arrow = document.querySelector('.submit-arrow');
  const capsLock = document.querySelector('#caps-lock');
  toggle.hidden = false;
  toggle.addEventListener('click', () => {
    const visible = password.type === 'password';
    password.type = visible ? 'text' : 'password';
    toggle.setAttribute('aria-pressed', String(visible));
    toggle.setAttribute('aria-label', visible ? '隐藏密码' : '显示密码');
    toggle.querySelector('.eye-slash').toggleAttribute('hidden', !visible);
  });
  for (const event of ['keydown', 'keyup']) {
    password.addEventListener(event, event => { capsLock.hidden = !event.getModifierState('CapsLock'); });
  }
  password.addEventListener('blur', () => { capsLock.hidden = true; });
  form.addEventListener('submit', event => {
    if (submit.disabled) { event.preventDefault(); return; }
    submit.disabled = true;
    form.setAttribute('aria-busy', 'true');
    label.textContent = '正在登录';
    status.textContent = '正在验证账户，请稍候…';
    spinner.hidden = false;
    arrow.setAttribute('hidden', '');
  });
  window.addEventListener('pageshow', () => {
    submit.disabled = false;
    form.removeAttribute('aria-busy');
    label.textContent = '登录';
    status.textContent = '';
    spinner.hidden = true;
    arrow.removeAttribute('hidden');
    password.type = 'password';
    toggle.setAttribute('aria-pressed', 'false');
    toggle.setAttribute('aria-label', '显示密码');
    toggle.querySelector('.eye-slash').setAttribute('hidden', '');
  });
})();
