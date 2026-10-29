'use strict';
const err = new URLSearchParams(location.search).get('error');
if (err) { const el = document.getElementById('error'); el.textContent = err; el.hidden = false; }
