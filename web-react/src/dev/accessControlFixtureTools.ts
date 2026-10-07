// This page is served by Vite, outside the production index.html entry point.
if (import.meta.env.DEV) {
  const form = document.querySelector<HTMLFormElement>('#fixtures')
  const status = document.querySelector<HTMLElement>('#status')
  const open = () => { window.location.href = './admin/access-control' }
  const error = () => { if (status) status.textContent = 'Browser storage is unavailable. Fixtures were not activated.' }
  form?.addEventListener('submit', event => {
    event.preventDefault()
    const scenario = new FormData(form).get('scenario')
    if (!['normal', 'empty', 'error', 'catalog-missing', 'catalog-failed'].includes(String(scenario))) return
    try {
      localStorage.setItem('psp_dev_access_state', String(scenario))
      localStorage.setItem('psp_dev_fixtures', 'access')
    } catch { error(); return }
    open()
  })
  document.querySelector('#disable')?.addEventListener('click', () => {
    try { localStorage.removeItem('psp_dev_fixtures'); localStorage.removeItem('psp_dev_access_state') } catch { error(); return }
    open()
  })
}
