// Dependency-free release checks; rendered checks live in capture-site.mjs.
// Historical measurements remain in docs; the homepage presents user benefits.
import assert from 'node:assert/strict';
import { readFileSync, existsSync } from 'node:fs';
import { test } from 'node:test';
const root = new URL('../', import.meta.url);
const html = readFileSync(new URL('site/index.html', root), 'utf8');
const links = [...html.matchAll(/\bhref="([^"]+)"/g)].map(match => match[1]);
const release = '0.12.1';
const section = id => html.match(new RegExp(`<section[^>]+id="${id}"[\\s\\S]*?<\\/section>`))?.[0];

test('hero, downloads and sharing metadata announce the current release', () => {
  assert.ok(html.includes(`NOW AVAILABLE / VERSION ${release}`));
  assert.ok(html.includes(`LATEST RELEASE / ${release}`));
  for (const name of ['name="description"', 'property="og:description"']) {
    const metadata = html.match(new RegExp(`<meta ${name} content="([^"]+)"`))?.[1];
    assert.ok(metadata?.includes(release), name);
  }
  assert.ok(links.includes(`https://github.com/platten/playlistai/releases/tag/v${release}`));
  assert.doesNotMatch(html, /untagged-|COMING IN VERSION|awaiting publication|0\.11\.1/);
});

test('downloads retain exact published asset names and platforms', () => {
  const approved = new Set(readFileSync(new URL('build/release-assets.txt', root), 'utf8').trim().split(/\r?\n/));
  const downloads = links.filter(link => link.includes('/releases/download/'));
  assert.equal(downloads.length, 13);
  assert.equal(new Set(downloads).size, downloads.length);
  for (const link of downloads) {
    assert.ok(link.startsWith(`https://github.com/platten/playlistai/releases/download/v${release}/`));
    assert.ok(approved.has(link.split('/').at(-1)), link);
  }
  assert.match(section('download'), /Apple Silicon only; no Intel Mac build/);
  assert.match(section('download'), /Models and recommendation data download separately during setup/);
});

test('local navigation, accessibility references and assets resolve', () => {
  const ids = [...html.matchAll(/\bid="([^"]+)"/g)].map(match => match[1]);
  assert.equal(new Set(ids).size, ids.length, 'duplicate element IDs');
  const references = [...html.matchAll(/\baria-(?:labelledby|controls)="([^"]+)"/g)].flatMap(match => match[1].split(' '));
  for (const id of [...references, ...links.filter(link => link.startsWith('#')).map(link => link.slice(1))]) assert.ok(ids.includes(id), `Missing target: ${id}`);
  for (const link of links.filter(link => !/^(https:|#)/.test(link))) assert.ok(existsSync(new URL(`site/${link}`, root)), link);
  for (const match of html.matchAll(/\bsrc="([^"]+)"/g)) assert.ok(existsSync(new URL(`site/${match[1]}`, root)), match[1]);
});

test('documentation links refer to existing files', () => {
  for (const link of links) {
    const match = link.match(/\/blob\/(?:v\d+\.\d+\.\d+|main)\/([^#]+)(?:#.*)?$/);
    if (match) assert.ok(existsSync(new URL(match[1], root)), link);
  }
});

test('highlights distinguish cumulative features from the patch release', () => {
  const highlights = section('new');
  assert.match(highlights, /since v0\.11\.0/);
  for (const text of ['More of what you meant', 'Strong matches first', 'Setup does the heavy lifting', 'Pick up where you left off']) assert.ok(highlights.includes(text), text);
  for (const version of ['0.12.0', release]) {
    assert.ok(highlights.includes(`/releases/tag/v${version}`));
    assert.ok(existsSync(new URL(`docs/releases/v${version}.md`, root)));
  }
  assert.match(highlights, /No Python required/);
  assert.match(highlights, /Verified download parts can be reused on retry/);
});

test('four modes present the correct default and preserve optional analysis', () => {
  const modes = section('recommendations');
  assert.match(modes, /ENHANCED HYBRID \/ DEFAULT FOR NEW SETUPS/);
  assert.equal([...modes.matchAll(/class="mode-card"/g)].length, 4);
  for (const name of ['Enhanced Hybrid', 'AcousticBrainz first', 'CLAP first', 'Deej-AI only']) assert.ok(modes.includes(name), name);
  assert.match(modes, /Your saved mode choice is respected/);
  assert.match(modes, /does not require MERT/);
  assert.match(html, /Downloading a model does not automatically enable preview analysis or experimental suggestions/);
});

test('examples and recommendation claims explain practical limits', () => {
  assert.match(section('experience'), /AN IDEA FOR YOUR NEXT REQUEST/);
  assert.match(section('experience'), /Targets use known track lengths/);
  assert.match(html, /Essential requirements and exclusions still apply/);
  assert.match(html, /Missing evidence can mean a shorter playlist/);
  assert.match(html, /not a listening-quality study/);
  assert.match(html, /Earlier desktop version shown/);
  assert.doesNotMatch(html, /data-result=|\d+\/40<\/td>|\d+% (?:accuracy|musical quality)/);
});

test('local-first copy discloses external lookups and model licensing', () => {
  const privacy = section('privacy');
  for (const provider of ['MusicBrainz', 'Wikipedia', 'Wikidata', 'AcousticBrainz', 'Deezer', 'Discogs', 'Soundiiz']) assert.ok(privacy.includes(provider), provider);
  assert.match(privacy, /No cloud language model receives your prompt/);
  assert.match(html, /MERT includes a noncommercial restriction/);
});
