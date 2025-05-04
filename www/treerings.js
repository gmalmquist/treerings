const RE_ENDPOINT = /^(?<method>[A-Z]+)\s+(?<path>\S+)$/i;

const isNone = x => typeof x === 'undefined' || x === null;
const isSome = x => !isNone(x);
const isBlank = x => isNone(x) || `${x}`.trim().length === 0;
const isNotBlank = x => !isBlank(x);

function coalesce(...args) {
  for (const a of args) {
    if (isSome(a)) {
      return a;
    }
  }
  return null;
}

const valueOf = e => {
  return coalesce(e.value, e.innerHTML);
};

function setupForm(form) {
    const constructRequest = () => {
      const m = RE_ENDPOINT.exec(form.dataset.endpoint);
      const method = m.groups.method;
      const rawpath = m.groups.path;
      const args = {};
      const query = {};
      let body = undefined;
      for (const field of form.querySelectorAll('[data-path-arg]')) {
        args[field.dataset.pathArg] = valueOf(field);
      }
      for (const field of form.querySelectorAll('[data-query-arg]')) {
        query[field.dataset.queryArg] = valueOf(field);
      }
      for (const field of form.querySelectorAll('[data-body-arg]')) {
        const names = field.dataset.bodyArg.split(/[.]+/);
        if (isNone(body)) {
          body = {};
        }
        let target = body;
        for (let i = 0; i < names.length; i++) {
          if (i === names.length - 1) {
            target[names[i]] = valueOf(field);
            break;
          }
          target[names[i]] = {};
          target = target[names[i]];
        }
      }
      let path = '';
      let inArg = false;
      let argName = '';
      for (let i = 0; i < rawpath.length; i++) {
        const c = rawpath.charAt(i);
        if (inArg) {
          if (c == '}') {
            path = `${path}${coalesce(args[argName], '')}`;
            inArg = false;
            continue;
          }
          argName = `${argName}${c}`;
        } else {
          if (c == '{') {
            inArg = true;
            continue;
          }
          path = `${path}${c}`;
        }
      }
      let queryString = '';
      for (const key in query) {
        if (isNotBlank(query[key])) {
          queryString = `${queryString}${queryString.length === 0 ? '?' : '&'}${key}=${query[key]}`;
        }
      }
      return {
        method,
        path,
        body: isSome(body) ? JSON.stringify(body) : undefined,
        query: queryString,
      };
    };
    console.log('default request:', constructRequest());
    for (const sub of form.querySelectorAll('[data-action="submit"]')) {
      sub.addEventListener('click', () => {
        const req = constructRequest();
        fetch(`${req.path}${req.query}`, {
          method: req.method,
          body: req.body,
          credentials: 'include',
        });
      });
    }
}

function setupForms() {
  for (const form of document.getElementsByClassName('form')) {
    try { setupForm(form); } catch (e) { console.error(e); }
  }
}

function setup() {
  setupForms();
  console.log('setup complete.');
}

