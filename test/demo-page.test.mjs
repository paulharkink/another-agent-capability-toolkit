import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';

const root = new URL('../docs/demo/', import.meta.url);

test('GitHub Pages demo is a standalone static page with local assets', () => {
  const html = readFileSync(new URL('index.html', root), 'utf8');
  assert.match(html, /<!doctype html>/i);
  assert.match(html, /<main[^>]*id="aact-demo"/);
  assert.match(html, /href="\.\/style\.css"/);
  assert.match(html, /src="\.\/app\.mjs"/);
  assert.doesNotMatch(html, /https?:\/\//);
});

test('stylesheet keeps the Norton Commander palette and visibly stacked overlays', () => {
  const css = readFileSync(new URL('style.css', root), 'utf8');
  assert.match(css, /#09266f/);
  assert.match(css, /\.split-dialog/);
  assert.match(css, /top:\s*[1-9]/);
  assert.match(css, /\.scroll-cue/);
  assert.match(css, /\.inactive-credential/);
});
