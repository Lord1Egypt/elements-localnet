'use strict';

/* ------------------------------------------------------------------ utils */

const $ = (selector, root = document) => root.querySelector(selector);

const esc = (value) => String(value ?? '').replace(/[&<>'"]/g, (c) => ({
  '&': '&amp;', '<': '&lt;', '>': '&gt;', "'": '&#39;', '"': '&quot;',
}[c]));

const SATS = 100000000;

function fmtSats(sats) {
  if (sats === null || sats === undefined) return null;
  const negative = sats < 0;
  const value = Math.abs(Number(sats));
  const whole = Math.floor(value / SATS);
  const frac = String(value % SATS).padStart(8, '0');
  return `${negative ? '-' : ''}${whole.toLocaleString('en-US')}.${frac}`;
}

function num(value) {
  if (value === null || value === undefined || !Number.isFinite(Number(value))) return '—';
  return Number(value).toLocaleString('en-US');
}

function fmtBytes(value) {
  if (!Number.isFinite(Number(value))) return '—';
  let n = Number(value);
  const units = ['B', 'KiB', 'MiB', 'GiB', 'TiB'];
  let i = 0;
  while (n >= 1024 && i < units.length - 1) { n /= 1024; i += 1; }
  return `${n.toFixed(i ? 1 : 0)} ${units[i]}`;
}

function fmtAge(seconds) {
  if (!Number.isFinite(Number(seconds))) return '—';
  let s = Math.max(0, Math.floor(Number(seconds)));
  if (s < 60) return `${s}s`;
  if (s < 3600) return `${Math.floor(s / 60)}m ${s % 60}s`;
  if (s < 86400) return `${Math.floor(s / 3600)}h ${Math.floor((s % 3600) / 60)}m`;
  return `${Math.floor(s / 86400)}d ${Math.floor((s % 86400) / 3600)}h`;
}

function fmtTime(unix) {
  if (!unix) return '—';
  return new Date(Number(unix) * 1000).toLocaleString();
}

const nowSeconds = () => Math.floor(Date.now() / 1000);

function short(value, head = 10, tail = 8) {
  const text = String(value ?? '');
  if (text.length <= head + tail + 1) return esc(text);
  return `${esc(text.slice(0, head))}…${esc(text.slice(-tail))}`;
}

/** Renders a truncated identifier whose full value stays reachable by title and copy. */
function idCell(value, href, options = {}) {
  if (!value) return '<span class="dim">—</span>';
  const label = short(value, options.head ?? 10, options.tail ?? 8);
  const inner = href
    ? `<a class="mono plain" href="${esc(href)}" data-link title="${esc(value)}">${label}</a>`
    : `<span class="mono" title="${esc(value)}">${label}</span>`;
  return `${inner}${copyButton(value)}`;
}

function copyButton(value) {
  return `<button type="button" class="copy" data-copy="${esc(value)}" title="Copy full value">copy</button>`;
}

function chip(text, tone = '') {
  return `<span class="chip ${tone}">${esc(text)}</span>`;
}

function metric(label, value, hint) {
  return `<div class="metric"><span class="label">${esc(label)}</span><strong>${value}</strong>${
    hint ? `<span class="hint">${esc(hint)}</span>` : ''}</div>`;
}

function detail(term, value) {
  return `<div><dt>${esc(term)}</dt><dd>${value}</dd></div>`;
}

function stateBlock(title, message, tone = '') {
  return `<div class="state ${tone}"><strong>${esc(title)}</strong>${esc(message)}</div>`;
}

function toast(message) {
  const node = $('#toast');
  node.textContent = message;
  node.hidden = false;
  clearTimeout(node.dataset.timer);
  node.dataset.timer = setTimeout(() => { node.hidden = true; }, 2200);
}

/* -------------------------------------------------------------------- api */

async function api(path) {
  const response = await fetch(path, { cache: 'no-store', headers: { Accept: 'application/json' } });
  const text = await response.text();
  let body = null;
  try { body = text ? JSON.parse(text) : null; } catch (_) { body = null; }
  if (!response.ok) {
    const message = body && body.error ? body.error.message : `Request failed with HTTP ${response.status}.`;
    const error = new Error(message);
    error.status = response.status;
    throw error;
  }
  return body;
}

/* ----------------------------------------------------------- confidential */

/** Elements outputs may hide the amount, the asset, or both. Never invent a value. */
function valueCell(entry) {
  if (entry.valueSats === null || entry.valueSats === undefined) {
    return chip('Confidential', 'conf');
  }
  return `<span class="mono num">${esc(fmtSats(entry.valueSats))}</span>`;
}

function assetCell(entry, policyAsset) {
  if (entry.asset) {
    const label = entry.asset === policyAsset
      ? `${idCell(entry.asset, `/asset/${entry.asset}`, { head: 8, tail: 6 })} ${chip('policy asset', 'info')}`
      : idCell(entry.asset, `/asset/${entry.asset}`, { head: 8, tail: 6 });
    return label;
  }
  if (entry.assetCommitment) return chip('Confidential commitment', 'conf');
  return '<span class="dim">—</span>';
}

/* ----------------------------------------------------------------- shell */

let cachedStatus = null;

function renderSyncStrip(indexer) {
  const strip = $('#sync-strip');
  if (!indexer) { strip.hidden = true; return; }
  const percent = Math.max(0, Math.min(100, Number(indexer.percentComplete || 0)));
  $('#sync-bar').style.width = `${percent}%`;
  $('#sync-percent').textContent = `${percent.toFixed(percent >= 99.95 ? 2 : 1)}%`;
  strip.classList.toggle('is-error', Boolean(indexer.lastError));

  if (indexer.lastError) {
    $('#sync-message').textContent = `Indexer retrying: ${indexer.lastError}`;
    strip.hidden = false;
    return;
  }
  if (indexer.chainHeight < 0) {
    $('#sync-message').textContent = 'Waiting for an Elements RPC node…';
    strip.hidden = false;
    return;
  }
  if (!indexer.synchronized) {
    $('#sync-message').textContent =
      `Indexing block ${num(indexer.indexedHeight)} of ${num(indexer.chainHeight)} · ${num(indexer.lagBlocks)} behind`;
    strip.hidden = false;
    return;
  }
  strip.hidden = true;
}

function renderFooter(indexer) {
  if (!indexer) return;
  $('#footer-meta').textContent =
    `Index schema v${indexer.schemaVersion} · ${fmtBytes(indexer.databaseBytes)} on disk · RPC source ${indexer.primaryRpcNode}`;
}

async function refreshStatus() {
  try {
    const body = await api('/api/v1/explorer/status');
    cachedStatus = body;
    renderSyncStrip(body.indexer);
    renderFooter(body.indexer);
  } catch (_) {
    const strip = $('#sync-strip');
    strip.hidden = false;
    strip.classList.add('is-error');
    $('#sync-message').textContent = 'The explorer service is unreachable.';
  }
}

/* ----------------------------------------------------------------- views */

async function viewOverview(view) {
  const data = await api('/api/v1/explorer/overview');
  const n = data.network;
  const e = data.economics;
  const p = data.producer;
  const ix = data.indexer;
  const c = data.chain;

  const halvingSeconds = Number(e.estimatedSecondsUntilHalving || 0);
  const producerTone = p.enabled && p.running ? 'good' : (p.enabled ? 'bad' : 'warn');
  const producerLabel = p.enabled ? (p.running ? 'running' : 'stopped') : 'disabled';

  view.innerHTML = `
    <div class="page-head">
      <div>
        <h1>Network overview</h1>
        <p>Chain <span class="mono">${esc(n.chain || '—')}</span> · ${num(data.managedNodes)} managed nodes · read-only</p>
      </div>
      <div>${chip(`producer ${producerLabel}`, producerTone)} ${
        ix.synchronized ? chip('index synchronized', 'good') : chip('index catching up', 'info')}</div>
    </div>

    <section class="section">
      <div class="grid grid-metrics">
        ${metric('Chain height', num(n.canonicalHeight), 'consensus of the managed nodes')}
        ${metric('Best block', idCell(n.canonicalBestBlockHash, `/block/${n.canonicalBestBlockHash}`), 'canonical tip')}
        ${metric('Indexed height', num(ix.indexedHeight), `${num(ix.lagBlocks)} blocks behind`)}
        ${metric('Average block interval', c.sampleBlocks > 1 ? `${c.averageBlockIntervalSeconds.toFixed(1)}s` : '—',
          c.sampleBlocks > 1 ? `last ${num(c.sampleBlocks)} indexed blocks` : 'not enough indexed blocks')}
        ${metric('Transactions per block', c.sampleBlocks > 1 ? c.averageTransactionsPerBlock.toFixed(2) : '—', 'recent sample')}
        ${metric('Mempool', `${num(n.mempoolTransactions)} tx`, fmtBytes(n.mempoolBytes))}
        ${metric('Current subsidy', `${esc(fmtSats(e.currentRewardSats))}`, `era ${num(e.currentRewardEra)}`)}
        ${metric('Next halving', `height ${num(e.nextHalvingHeight)}`,
          `${num(e.blocksUntilNextHalving)} blocks · about ${fmtAge(halvingSeconds)}`)}
        ${metric('Managed nodes', `${num(n.onlineNodes)} / ${num(n.totalNodes)}`,
          `${num(n.synchronizedNodes)} synchronized · ${num(n.divergentNodes)} divergent`)}
        ${metric('External peer connections', num(n.externalPeerConnections),
          'connections from outside the managed set; the number of distinct external nodes cannot be determined reliably')}
        ${metric('Indexed transactions', num(c.indexedTransactions), `${num(c.indexedAssets)} issued assets`)}
        ${metric('Latest block age', fmtAge(n.latestBlockAgeSeconds), fmtTime(n.latestBlockTimestamp))}
      </div>
    </section>

    <section class="split-wide section">
      <article class="panel">
        <div class="section-heading"><h2>Latest blocks</h2><a href="/blocks" data-link>All blocks</a></div>
        ${blocksTable(data.latestBlocks, { compact: true })}
      </article>
      <article class="panel">
        <div class="section-heading"><h2>Block producer</h2></div>
        <dl class="details narrow">
          ${detail('Configured', esc(p.enabled ? 'enabled' : 'disabled'))}
          ${detail('Reported state', esc(producerLabel))}
          ${detail('Interval', `${esc(p.blockInterval)}s`)}
          ${detail('Payout address', `<span class="mono">${esc(p.payoutAddressMasked || '—')}</span>`)}
          ${detail('Last produced', p.lastProducedHeight ? `height ${num(p.lastProducedHeight)}` : '—')}
          ${detail('Produced at', esc(p.lastProducedTimestamp ? new Date(p.lastProducedTimestamp).toLocaleString() : '—'))}
          ${detail('Generation warning', esc(p.generationWarning || 'none'))}
          ${detail('Last error', esc(p.lastError || 'none'))}
        </dl>
      </article>
    </section>

    <section class="section">
      <div class="section-heading"><h2>Recent transactions</h2><a href="/transactions" data-link>All transactions</a></div>
      ${transactionsTable(data.recentTransactions, ix.policyAsset)}
    </section>
  `;
}

function blocksTable(blocks, options = {}) {
  if (!blocks || blocks.length === 0) {
    return stateBlock('No indexed blocks yet', 'The indexer has not written any blocks to the explorer database.');
  }
  const rows = blocks.map((b) => `
    <tr>
      <td><a class="plain" href="/block/${b.height}" data-link><strong class="num">${num(b.height)}</strong></a></td>
      <td>${idCell(b.hash, `/block/${b.hash}`)}</td>
      <td class="num">${esc(fmtAge(nowSeconds() - b.time))}</td>
      ${options.compact ? '' : `<td class="num">${esc(fmtTime(b.time))}</td>`}
      <td class="right num">${num(b.txCount)}</td>
      ${options.compact ? '' : `<td class="right num">${esc(fmtBytes(b.size))}</td><td class="right num">${num(b.weight)}</td>`}
      <td class="right num">${esc(fmtSats(b.subsidySats))}</td>
      <td class="right num">${esc(fmtSats(b.feesSats))}</td>
    </tr>`).join('');
  return `<div class="table-wrap"><table>
    <thead><tr>
      <th>Height</th><th>Hash</th><th>Age</th>${options.compact ? '' : '<th>Timestamp</th>'}
      <th class="right">Txs</th>${options.compact ? '' : '<th class="right">Size</th><th class="right">Weight</th>'}
      <th class="right">Subsidy</th><th class="right">Fees</th>
    </tr></thead><tbody>${rows}</tbody></table></div>`;
}

function transactionsTable(txs, policyAsset) {
  if (!txs || txs.length === 0) {
    return stateBlock('No transactions', 'Nothing has been indexed for this view yet.');
  }
  const rows = txs.map((t) => `
    <tr>
      <td>${idCell(t.txid, `/tx/${t.txid}`, { head: 12, tail: 10 })}</td>
      <td><a class="plain num" href="/block/${t.height}" data-link>${num(t.height)}</a></td>
      <td class="num">${esc(fmtAge(nowSeconds() - t.blockTime))}</td>
      <td class="right num">${num(t.inputCount)} / ${num(t.outputCount)}</td>
      <td class="right num">${esc(fmtBytes(t.size))}</td>
      <td class="right num">${t.feeSats === null || t.feeSats === undefined ? '<span class="dim">—</span>' : esc(fmtSats(t.feeSats))}</td>
      <td>${[
        t.isCoinbase ? chip('coinbase', 'info') : '',
        t.hasIssuance ? chip('issuance', 'warn') : '',
        t.hasPegin ? chip('peg-in', 'warn') : '',
        t.hasPegout ? chip('peg-out', 'warn') : '',
      ].filter(Boolean).join(' ') || '<span class="dim">—</span>'}</td>
    </tr>`).join('');
  return `<div class="table-wrap"><table>
    <thead><tr><th>Transaction</th><th>Block</th><th>Age</th><th class="right">In / Out</th>
    <th class="right">Size</th><th class="right">Fee</th><th>Kind</th></tr></thead>
    <tbody>${rows}</tbody></table></div>`;
}

function pager(page, buildHref, cursorKey) {
  const buttons = [];
  if (cursorKey) {
    if (page.nextCursor !== undefined && page.nextCursor !== null) {
      buttons.push(`<a class="ghost" role="button" href="${esc(buildHref(page.nextCursor))}" data-link>Older →</a>`);
    }
  } else {
    if (page.offset > 0) {
      const previous = Math.max(0, page.offset - page.limit);
      buttons.push(`<a class="ghost" role="button" href="${esc(buildHref(previous))}" data-link>← Newer</a>`);
    }
    if (page.nextOffset !== undefined && page.nextOffset !== null) {
      buttons.push(`<a class="ghost" role="button" href="${esc(buildHref(page.nextOffset))}" data-link>Older →</a>`);
    }
  }
  return `<div class="pager"><span class="info">${num(page.total)} total</span>` +
    `<div class="buttons">${buttons.join('')}</div></div>`;
}

async function viewBlocks(view, params) {
  const before = params.get('before');
  const query = before ? `?limit=25&before=${encodeURIComponent(before)}` : '?limit=25';
  const data = await api(`/api/v1/blocks${query}`);
  view.innerHTML = `
    <div class="page-head"><div><h1>Blocks</h1>
      <p>${num(data.page.total)} indexed blocks. Newest first.</p></div></div>
    ${blocksTable(data.blocks)}
    ${pager(data.page, (cursor) => `/blocks?before=${cursor}`, 'before')}
  `;
}

async function viewTransactions(view) {
  const data = await api('/api/v1/transactions?limit=50');
  view.innerHTML = `
    <div class="page-head"><div><h1>Transactions</h1>
      <p>The 50 most recently indexed transactions. Open a block to page through its full list.</p></div></div>
    ${transactionsTable(data.transactions, cachedStatus?.indexer?.policyAsset)}
  `;
}

async function viewBlock(view, params, id) {
  const offset = Number(params.get('offset') || 0);
  const data = await api(`/api/v1/blocks/${encodeURIComponent(id)}?limit=25&offset=${offset}`);
  const previous = data.previousBlockHash
    ? `<a href="/block/${data.previousBlockHash}" data-link>← Block ${num(data.height - 1)}</a>` : '<span class="dim">← genesis</span>';
  const next = data.nextBlockHash
    ? `<a href="/block/${data.nextBlockHash}" data-link>Block ${num(data.height + 1)} →</a>` : '<span class="dim">chain tip →</span>';

  view.innerHTML = `
    <div class="page-head">
      <div><h1>Block ${num(data.height)}</h1>
      <p class="mono">${esc(data.hash)}${copyButton(data.hash)}</p></div>
      <div class="pager"><div class="buttons">${previous} &nbsp; ${next}</div></div>
    </div>

    <section class="panel section">
      <dl class="details">
        ${detail('Timestamp', `${esc(fmtTime(data.time))} <span class="dim">(${esc(fmtAge(nowSeconds() - data.time))} ago)</span>`)}
        ${detail('Transactions', num(data.txCount))}
        ${detail('Size', `${esc(fmtBytes(data.size))} <span class="dim">stripped ${esc(fmtBytes(data.strippedSize))}</span>`)}
        ${detail('Weight', num(data.weight))}
        ${detail('Version', `<span class="mono">${num(data.version)}</span>`)}
        ${detail('Merkle root', idCell(data.merkleRoot, null, { head: 12, tail: 10 }))}
        ${detail('Subsidy', `<span class="mono num">${esc(fmtSats(data.subsidySats))}</span>`)}
        ${detail('Explicit fees', `<span class="mono num">${esc(fmtSats(data.feesSats))}</span>`)}
        ${detail('Median time', esc(fmtTime(data.medianTime)))}
        ${detail('Signblock challenge', data.signblockChallenge ? `<span class="mono">${esc(data.signblockChallenge)}</span>` : '<span class="dim">—</span>')}
      </dl>
    </section>

    <section class="section">
      <div class="section-heading"><h2>Transactions in this block</h2></div>
      ${transactionsTable(data.transactions, cachedStatus?.indexer?.policyAsset)}
      ${pager(data.transactionPage, (o) => `/block/${id}?offset=${o}`)}
    </section>

    ${rawSection(`/api/v1/blocks/${encodeURIComponent(data.hash)}/raw`, 'Raw block JSON from the Elements node')}
  `;
}

function rawSection(href, label) {
  return `<details class="raw-json"><summary>${esc(label)}</summary>
    <pre data-raw="${esc(href)}">Select to load…</pre></details>`;
}

async function viewTransaction(view, params, txid) {
  const t = await api(`/api/v1/transactions/${encodeURIComponent(txid)}`);
  const policyAsset = cachedStatus?.indexer?.policyAsset;

  const inputs = t.inputs.map((input) => {
    const rows = [];
    if (input.isCoinbase) {
      rows.push(`<div><span class="k">Source</span>${chip('newly generated coins', 'info')}</div>`);
    } else {
      rows.push(`<div><span class="k">Previous output</span>${
        input.prevTxid ? `${idCell(input.prevTxid, `/tx/${input.prevTxid}`)} <span class="dim">: ${num(input.prevVout)}</span>` : '<span class="dim">—</span>'}</div>`);
      if (input.prevout) {
        rows.push(`<div><span class="k">Value</span>${valueCell(input.prevout)}</div>`);
        rows.push(`<div><span class="k">Asset</span>${assetCell(input.prevout, policyAsset)}</div>`);
        rows.push(`<div><span class="k">Script address</span>${
          input.prevout.scriptAddress ? idCell(input.prevout.scriptAddress, `/address/${input.prevout.scriptAddress}`, { head: 14, tail: 8 }) : '<span class="dim">not derivable</span>'}</div>`);
      }
    }
    rows.push(`<div><span class="k">Sequence</span><span class="mono num">${num(input.sequence)}</span></div>`);
    const issuance = input.issuance ? issuanceBlock(input.issuance) : '';
    return `<article class="io-item">
      <div class="io-head"><span class="io-index">Input ${num(input.vin)}</span>
        <span>${[input.isCoinbase ? chip('coinbase', 'info') : '', input.isPegin ? chip('peg-in', 'warn') : '',
          input.issuance ? chip(input.issuance.isReissuance ? 'reissuance' : 'issuance', 'warn') : ''].filter(Boolean).join(' ')}</span></div>
      <div class="io-body">${rows.join('')}</div>${issuance}</article>`;
  }).join('');

  const outputs = t.outputs.map((output) => {
    const rows = [
      `<div><span class="k">Value</span>${valueCell(output)}</div>`,
      `<div><span class="k">Asset</span>${assetCell(output, policyAsset)}</div>`,
    ];
    if (output.isFee) {
      rows.push(`<div><span class="k">Kind</span>${chip('explicit fee output', 'info')}</div>`);
    } else {
      rows.push(`<div><span class="k">Script address</span>${
        output.scriptAddress ? idCell(output.scriptAddress, `/address/${output.scriptAddress}`, { head: 14, tail: 8 })
          : '<span class="dim">not derivable from this script</span>'}</div>`);
      rows.push(`<div><span class="k">Script type</span>${esc(output.scriptType || '—')}</div>`);
      rows.push(`<div><span class="k">Spent by</span>${
        output.spentTxid ? `${idCell(output.spentTxid, `/tx/${output.spentTxid}`)} <span class="dim">: ${num(output.spentVin)}</span>` : chip('unspent', 'good')}</div>`);
    }
    if (output.pegoutChain) {
      rows.push(`<div><span class="k">Peg-out chain</span><span class="mono">${esc(output.pegoutChain)}</span></div>`);
      rows.push(`<div><span class="k">Peg-out address</span><span class="mono">${esc(output.pegoutAddress || '—')}</span></div>`);
    }
    const advanced = [];
    if (output.assetCommitment) advanced.push(['Asset commitment', output.assetCommitment]);
    if (output.valueCommitment) advanced.push(['Value commitment', output.valueCommitment]);
    if (output.nonceCommitment) advanced.push(['Nonce commitment', output.nonceCommitment]);
    if (output.scriptHex) advanced.push(['scriptPubKey hex', output.scriptHex]);
    if (output.scriptAsm) advanced.push(['scriptPubKey asm', output.scriptAsm]);
    const advancedBlock = advanced.length === 0 ? '' : `<details class="raw-json"><summary>Commitments and script detail</summary>
      <dl class="details narrow">${advanced.map(([k, v]) => detail(k, `<span class="mono">${esc(v)}</span>${copyButton(v)}`)).join('')}</dl></details>`;
    return `<article class="io-item">
      <div class="io-head"><span class="io-index">Output ${num(output.vout)}</span>
        <span>${output.confidential ? chip('confidential', 'conf') : ''}</span></div>
      <div class="io-body">${rows.join('')}</div>${advancedBlock}</article>`;
  }).join('');

  const feeRows = Object.entries(t.explicitFees || {});
  view.innerHTML = `
    <div class="page-head">
      <div><h1>Transaction</h1><p class="mono">${esc(t.txid)}${copyButton(t.txid)}</p></div>
      <div>${t.isCoinbase ? chip('coinbase', 'info') : ''} ${
        t.confirmations > 0 ? chip(`${num(t.confirmations)} confirmations`, 'good') : chip('unconfirmed', 'warn')}</div>
    </div>

    <section class="panel section">
      <dl class="details">
        ${detail('Block', `<a href="/block/${t.height}" data-link class="num">${num(t.height)}</a>`)}
        ${detail('Block hash', idCell(t.blockHash, `/block/${t.blockHash}`))}
        ${detail('Timestamp', `${esc(fmtTime(t.blockTime))} <span class="dim">(${esc(fmtAge(nowSeconds() - t.blockTime))} ago)</span>`)}
        ${detail('Witness transaction ID', t.wtxid ? idCell(t.wtxid, null, { head: 12, tail: 10 }) : '<span class="dim">—</span>')}
        ${detail('Size / virtual size / weight', `<span class="num">${num(t.size)} B · ${num(t.vsize)} vB · ${num(t.weight)} WU</span>`)}
        ${detail('Version', `<span class="num">${num(t.version)}</span>`)}
        ${detail('Locktime', `<span class="num">${num(t.locktime)}</span>`)}
        ${detail('Explicit fees', feeRows.length === 0
          ? '<span class="dim">none</span>'
          : feeRows.map(([asset, sats]) => `<div><span class="mono num">${esc(fmtSats(sats))}</span> ${idCell(asset, `/asset/${asset}`, { head: 8, tail: 6 })}</div>`).join(''))}
      </dl>
      ${t.isCoinbase ? '<p class="notice">Coinbase outputs are spendable only after the configured coinbase maturity of 100 blocks.</p>' : ''}
      ${t.outputs.some((o) => o.confidential)
        ? '<p class="notice amber">This transaction has confidential outputs. Blinded amounts are not recoverable from public chain data and are never estimated here.</p>' : ''}
    </section>

    <section class="split section">
      <article class="panel"><h2>Inputs (${num(t.inputs.length)})</h2><div class="io-list">${inputs}</div></article>
      <article class="panel"><h2>Outputs (${num(t.outputs.length)})</h2><div class="io-list">${outputs}</div></article>
    </section>

    ${rawSection(`/api/v1/transactions/${encodeURIComponent(t.txid)}/raw`, 'Raw transaction JSON from the Elements node')}
  `;
}

function issuanceBlock(issuance) {
  return `<dl class="details narrow" style="margin-top:11px">
    ${detail('Asset ID', idCell(issuance.assetId, `/asset/${issuance.assetId}`))}
    ${detail('Reissuance token', issuance.tokenId ? idCell(issuance.tokenId, null) : '<span class="dim">—</span>')}
    ${detail('Entropy', issuance.entropy ? idCell(issuance.entropy, null) : '<span class="dim">—</span>')}
    ${detail('Kind', esc(issuance.isReissuance ? 'reissuance' : 'new issuance'))}
    ${detail('Issued amount', issuance.assetAmountSats === null || issuance.assetAmountSats === undefined
      ? chip('Confidential', 'conf') : `<span class="mono num">${esc(fmtSats(issuance.assetAmountSats))}</span>`)}
    ${detail('Reissuance token amount', issuance.tokenAmountSats === null || issuance.tokenAmountSats === undefined
      ? chip('Confidential', 'conf') : `<span class="mono num">${esc(fmtSats(issuance.tokenAmountSats))}</span>`)}
  </dl>`;
}

async function viewAddress(view, params, address) {
  const offset = Number(params.get('offset') || 0);
  const [data, utxoData] = await Promise.all([
    api(`/api/v1/addresses/${encodeURIComponent(address)}?limit=25&offset=${offset}`),
    api(`/api/v1/addresses/${encodeURIComponent(address)}/utxos?limit=25`),
  ]);
  const s = data.summary;
  const policyAsset = cachedStatus?.indexer?.policyAsset;

  const balances = s.explicitBalances.length === 0
    ? stateBlock('No explicit balances', 'Every indexed output paying this script has a blinded amount.')
    : `<div class="table-wrap"><table>
        <thead><tr><th>Asset</th><th class="right">Received</th><th class="right">Spent</th>
        <th class="right">Balance</th><th class="right">Outputs</th><th class="right">Unspent</th></tr></thead>
        <tbody>${s.explicitBalances.map((b) => `<tr>
          <td>${assetCell({ asset: b.assetId }, policyAsset)}</td>
          <td class="right mono num">${esc(fmtSats(b.receivedSats))}</td>
          <td class="right mono num">${esc(fmtSats(b.spentSats))}</td>
          <td class="right mono num">${esc(fmtSats(b.balanceSats))}</td>
          <td class="right num">${num(b.outputCount)}</td>
          <td class="right num">${num(b.unspentOutputs)}</td>
        </tr>`).join('')}</tbody></table></div>`;

  const utxos = utxoData.utxos.length === 0
    ? stateBlock('No unspent outputs', 'Every indexed output paying this script has been spent.')
    : `<div class="table-wrap"><table>
        <thead><tr><th>Outpoint</th><th>Block</th><th>Asset</th><th class="right">Value</th></tr></thead>
        <tbody>${utxoData.utxos.map((u) => `<tr>
          <td>${idCell(u.txid, `/tx/${u.txid}`)} <span class="dim">: ${num(u.vout)}</span></td>
          <td><a class="plain num" href="/block/${u.height}" data-link>${num(u.height)}</a></td>
          <td>${assetCell(u, policyAsset)}</td>
          <td class="right">${valueCell(u)}</td>
        </tr>`).join('')}</tbody></table></div>`;

  view.innerHTML = `
    <div class="page-head">
      <div><h1>Address</h1><p class="mono">${esc(s.address)}${copyButton(s.address)}</p></div>
      <div>${chip('script address', 'info')}</div>
    </div>

    <p class="notice amber">This is the unconfidential address derived from the output scriptPubKey.
    If the sender used a confidential address, that address is not recorded on chain and cannot be shown here.
    Amounts below cover explicit outputs only; blinded amounts are never estimated.</p>

    <section class="section">
      <div class="grid grid-metrics">
        ${metric('Transactions', num(s.transactionCount))}
        ${metric('Outputs', num(s.outputCount), `${num(s.unspentOutputs)} unspent`)}
        ${metric('Confidential outputs', num(s.confidentialOutputs), 'amount not publicly known')}
        ${metric('Explicit assets', num(s.explicitBalances.length))}
      </div>
    </section>

    <section class="section"><div class="section-heading"><h2>Explicit balances by asset</h2></div>${balances}</section>
    <section class="section"><div class="section-heading"><h2>Unspent outputs</h2>
      <span class="legend">${num(utxoData.page.total)} total</span></div>${utxos}</section>
    <section class="section"><div class="section-heading"><h2>Transaction history</h2></div>
      ${transactionsTable(data.transactions, policyAsset)}
      ${pager(data.page, (o) => `/address/${encodeURIComponent(address)}?offset=${o}`)}</section>
  `;
}

async function viewAssets(view, params) {
  const offset = Number(params.get('offset') || 0);
  const data = await api(`/api/v1/assets?limit=25&offset=${offset}`);
  const body = data.assets.length === 0
    ? stateBlock('No issued assets', 'No asset issuance has been indexed on this chain yet.')
    : `<div class="table-wrap"><table>
      <thead><tr><th>Asset</th><th>Name</th><th>Issued in</th><th>Issuance</th>
      <th class="right">Known supply</th><th>Reissuance</th></tr></thead>
      <tbody>${data.assets.map((a) => `<tr>
        <td>${idCell(a.assetId, `/asset/${a.assetId}`)}</td>
        <td class="wrap">${a.metadata && a.metadata.name
          ? `${esc(a.metadata.name)} ${a.metadata.ticker ? `<span class="dim">${esc(a.metadata.ticker)}</span>` : ''}`
          : '<span class="dim">no local metadata</span>'}</td>
        <td><a class="plain num" href="/block/${a.issuanceHeight}" data-link>${num(a.issuanceHeight)}</a></td>
        <td>${a.confidentialIssuance ? chip('Confidential', 'conf') : chip('public', 'good')}</td>
        <td class="right">${a.issuedSats === null || a.issuedSats === undefined
          ? '<span class="dim">Not publicly verifiable</span>' : `<span class="mono num">${esc(fmtSats(a.issuedSats))}</span>`}</td>
        <td class="wrap"><span class="dim">${esc(a.reissuanceState)}</span></td>
      </tr>`).join('')}</tbody></table></div>`;

  view.innerHTML = `
    <div class="page-head"><div><h1>Assets</h1>
      <p>${num(data.page.total)} issued assets indexed from on-chain issuances.</p></div></div>
    <p class="notice">Names, tickers and descriptions come from a local, operator-supplied registry.
    They are descriptive labels, never consensus data.</p>
    ${body}
    ${pager(data.page, (o) => `/assets?offset=${o}`)}
  `;
}

async function viewAsset(view, params, id) {
  const data = await api(`/api/v1/assets/${encodeURIComponent(id)}?limit=25`);
  const a = data.asset;
  const meta = a.metadata;
  const txData = await api(`/api/v1/assets/${encodeURIComponent(id)}/transactions?limit=25`);

  const issuances = data.issuances.map((i) => `<tr>
    <td>${idCell(i.txid, `/tx/${i.txid}`)}</td>
    <td><a class="plain num" href="/block/${i.height}" data-link>${num(i.height)}</a></td>
    <td>${esc(i.isReissuance ? 'reissuance' : 'new issuance')}</td>
    <td class="right">${i.assetAmountSats === null || i.assetAmountSats === undefined
      ? chip('Confidential', 'conf') : `<span class="mono num">${esc(fmtSats(i.assetAmountSats))}</span>`}</td>
    <td class="right">${i.tokenAmountSats === null || i.tokenAmountSats === undefined
      ? chip('Confidential', 'conf') : `<span class="mono num">${esc(fmtSats(i.tokenAmountSats))}</span>`}</td>
  </tr>`).join('');

  view.innerHTML = `
    <div class="page-head">
      <div><h1>${meta && meta.name ? esc(meta.name) : 'Asset'}</h1><p class="mono">${esc(a.assetId)}${copyButton(a.assetId)}</p></div>
      <div>${a.confidentialIssuance ? chip('confidential issuance', 'conf') : chip('public issuance', 'good')}
        ${a.derivationVerified ? chip('identifiers verified', 'good') : chip('identifiers unverified', 'warn')}</div>
    </div>

    ${a.confidentialIssuance ? `<p class="notice amber">
      <strong>Supply: Not publicly verifiable.</strong> Issuance: Confidential.
      The issued amount is blinded on chain. Any figure reported by a wallet owner is a claim, not proof.</p>` : ''}

    <section class="panel section">
      <dl class="details">
        ${detail('Supply state', esc(a.supplyState))}
        ${detail('Known explicit supply', a.issuedSats === null || a.issuedSats === undefined
          ? '<span class="dim">Not publicly verifiable</span>' : `<span class="mono num">${esc(fmtSats(a.issuedSats))}</span>`)}
        ${detail('Reissuance', esc(a.reissuanceState))}
        ${detail('Reissuance token ID', a.reissuanceTokenId ? idCell(a.reissuanceTokenId, null) : '<span class="dim">—</span>')}
        ${detail('Entropy', a.entropy ? idCell(a.entropy, null) : '<span class="dim">—</span>')}
        ${detail('Issuance transaction', idCell(a.issuanceTxid, `/tx/${a.issuanceTxid}`))}
        ${detail('Issuance block', `<a href="/block/${a.issuanceHeight}" data-link class="num">${num(a.issuanceHeight)}</a>`)}
        ${detail('Issued at', esc(fmtTime(a.issuanceTime)))}
        ${detail('Issuance events', `${num(a.issuanceCount)} <span class="dim">(${num(a.reissuanceCount)} reissuances)</span>`)}
        ${detail('Outputs carrying this asset', num(a.outputCount))}
      </dl>
    </section>

    ${meta ? `<section class="panel section"><h2>Local registry metadata</h2>
      <p class="dim" style="font-size:.78rem;margin:6px 0 0">Descriptive only. Not consensus data.</p>
      <dl class="details">
        ${detail('Name', esc(meta.name || '—'))}
        ${detail('Ticker', esc(meta.ticker || '—'))}
        ${detail('Decimals', meta.decimals === undefined || meta.decimals === null ? '—' : num(meta.decimals))}
        ${detail('Status', esc(meta.status || '—'))}
        ${detail('Website', esc(meta.website || '—'))}
        ${detail('Logo path', esc(meta.logoPath || '—'))}
        ${detail('Description', esc(meta.description || '—'))}
      </dl></section>` : `<p class="notice">No local metadata is registered for this Asset ID.</p>`}

    <section class="section"><div class="section-heading"><h2>Issuance events</h2></div>
      <div class="table-wrap"><table>
        <thead><tr><th>Transaction</th><th>Block</th><th>Kind</th>
        <th class="right">Asset amount</th><th class="right">Token amount</th></tr></thead>
        <tbody>${issuances}</tbody></table></div></section>

    <section class="section"><div class="section-heading"><h2>Transactions with explicit outputs of this asset</h2></div>
      ${transactionsTable(txData.transactions, cachedStatus?.indexer?.policyAsset)}</section>
  `;
}

async function viewMempool(view) {
  const data = await api('/api/v1/mempool?limit=50');
  const body = data.transactions.length === 0
    ? stateBlock('The mempool is empty', 'No unconfirmed transaction is waiting on the indexing node.')
    : `<div class="table-wrap"><table>
      <thead><tr><th>Transaction</th><th>Age</th><th class="right">Virtual size</th>
      <th class="right">Weight</th><th class="right">Fee</th></tr></thead>
      <tbody>${data.transactions.map((t) => `<tr>
        <td>${idCell(t.txid, `/tx/${t.txid}`, { head: 12, tail: 10 })}</td>
        <td class="num">${t.entryTime ? esc(fmtAge(nowSeconds() - t.entryTime)) : '<span class="dim">—</span>'}</td>
        <td class="right num">${num(t.vsize)}</td>
        <td class="right num">${num(t.weight)}</td>
        <td class="right num">${t.feeSats === null || t.feeSats === undefined ? '<span class="dim">—</span>' : esc(fmtSats(t.feeSats))}</td>
      </tr>`).join('')}</tbody></table></div>`;

  view.innerHTML = `
    <div class="page-head"><div><h1>Mempool</h1>
      <p>Unconfirmed transactions seen by the managed nodes.</p></div></div>
    <section class="section"><div class="grid grid-metrics">
      ${metric('Transactions', num(data.page.total), 'indexed from the RPC source')}
      ${metric('Reported count', num(data.reportedCount), 'sum across managed nodes')}
      ${metric('Total bytes', fmtBytes(data.reportedBytes))}
      ${metric('Memory usage', fmtBytes(data.reportedUsage))}
    </div></section>
    ${body}
  `;
}

async function viewNodes(view) {
  const snapshot = await api('/api/v1/network');
  const n = snapshot.network;
  const nodes = snapshot.nodes;

  const cards = nodes.map((node) => {
    const tone = String(node.state || 'error').toLowerCase();
    return `<article class="node-card">
      <div class="node-title"><div><h3>${esc(node.id)}</h3>
        <span class="dim" style="font-size:.74rem">${esc(node.role)}${
          (node.capabilities || []).length ? ` · ${esc(node.capabilities.join(', '))}` : ''}</span></div>
        <span class="state-tag ${tone}">${esc(node.state)}</span></div>
      <dl class="details" style="margin-top:12px">
        ${detail('Block / headers', `<span class="num">${num(node.blockHeight)} / ${num(node.headerHeight)}</span>`)}
        ${detail('Best block', idCell(node.bestBlockHash, `/block/${node.bestBlockHash}`, { head: 8, tail: 6 }))}
        ${detail('Peers', `<span class="num">${num(node.peerCount)}</span> <span class="dim">${num(node.inboundPeers)} in / ${num(node.outboundPeers)} out · ${num(node.externalPeerConnections)} external</span>`)}
        ${detail('Mempool', `<span class="num">${num(node.mempoolTransactions)} tx</span> <span class="dim">${esc(fmtBytes(node.mempoolUsage))}</span>`)}
        ${detail('Verification', `${(Number(node.verificationProgress || 0) * 100).toFixed(2)}%`)}
        ${detail('RPC latency', `<span class="num">${num(node.rpcLatencyMillis)} ms</span>`)}
        ${detail('Chain data', esc(fmtBytes(node.blockchainSize)))}
        ${detail('Version', esc(node.subversion || node.version))}
      </dl>
      ${node.lastError ? `<p class="notice amber" style="margin-top:11px">${esc(node.lastError)}</p>` : ''}
    </article>`;
  }).join('');

  view.innerHTML = `
    <div class="page-head"><div><h1>Nodes</h1>
      <p>${num(n.onlineNodes)} of ${num(n.totalNodes)} managed nodes online · ${num(n.managedNodeLinks)} internal peer links ·
      ${num(n.externalPeerConnections)} external peer connections</p></div></div>
    <p class="notice">External peer connections are counted per node. The number of distinct external
    nodes behind them cannot be determined reliably, so it is not claimed.</p>
    <section class="panel section">
      <div class="section-heading"><h2>Topology</h2>
        <span class="legend"><i class="dot healthy"></i> connected <i class="dot offline"></i> unavailable</span></div>
      <div class="topology" id="topology" role="img" aria-label="Managed node peer topology"></div>
    </section>
    <section class="section"><div class="node-grid">${cards}</div></section>
  `;
  drawTopology(snapshot.topology);
}

function drawTopology(topology) {
  const nodes = topology.nodes || [];
  const columns = Math.min(5, Math.max(1, Math.ceil(Math.sqrt(nodes.length))));
  const rows = Math.ceil(nodes.length / columns) || 1;
  const width = columns * 150 + 30;
  const height = rows * 92 + 30;
  const positions = {};
  nodes.forEach((node, i) => {
    positions[node.id] = { x: 25 + (i % columns) * 150, y: 25 + Math.floor(i / columns) * 92, node };
  });
  const lines = (topology.edges || []).map((edge) => {
    const a = positions[edge.from];
    const b = positions[edge.to];
    return a && b ? `<line x1="${a.x + 55}" y1="${a.y + 24}" x2="${b.x + 55}" y2="${b.y + 24}"/>` : '';
  }).join('');
  const boxes = Object.values(positions).map(({ x, y, node }) => `
    <g class="${String(node.state || 'error').toLowerCase()}">
      <rect x="${x}" y="${y}" rx="8" width="110" height="48"/>
      <circle cx="${x + 13}" cy="${y + 15}" r="5" fill="currentColor"/>
      <text x="${x + 23}" y="${y + 19}">${esc(node.id)}</text>
      <text class="role" x="${x + 13}" y="${y + 36}">${esc(node.role)} · ${esc(node.state)}</text>
    </g>`).join('');
  const target = $('#topology');
  if (target) target.innerHTML = `<svg viewBox="0 0 ${width} ${height}" aria-hidden="true">${lines}${boxes}</svg>`;
}

async function viewSearch(view, params) {
  const query = params.get('q') || '';
  if (!query) {
    view.innerHTML = `<div class="page-head"><div><h1>Search</h1></div></div>
      ${stateBlock('Nothing to search for', 'Enter a block height, block hash, transaction ID, Asset ID, or address.')}`;
    return;
  }
  const data = await api(`/api/v1/search?q=${encodeURIComponent(query)}`);
  if (data.results.length === 1) {
    navigate(data.results[0].path, true);
    return;
  }
  const body = data.results.length === 0
    ? stateBlock('No match', 'Nothing in the explorer index matches that value. It may not be indexed yet.')
    : `<div class="table-wrap"><table><thead><tr><th>Kind</th><th>Match</th></tr></thead><tbody>
      ${data.results.map((hit) => `<tr><td>${esc(hit.label)}</td>
        <td>${idCell(hit.value, hit.path, { head: 16, tail: 12 })}</td></tr>`).join('')}
      </tbody></table></div>`;
  view.innerHTML = `
    <div class="page-head"><div><h1>Search results</h1>
      <p>${data.results.length > 1
        ? 'This value matches more than one kind of record. Choose the one you meant.'
        : `Results for <span class="mono">${esc(query)}</span>.`}</p></div></div>
    ${body}`;
}

/* ----------------------------------------------------------------- router */

const ROUTES = [
  { pattern: /^\/$/, nav: 'overview', view: viewOverview },
  { pattern: /^\/blocks\/?$/, nav: 'blocks', view: viewBlocks },
  { pattern: /^\/transactions\/?$/, nav: 'transactions', view: viewTransactions },
  { pattern: /^\/assets\/?$/, nav: 'assets', view: viewAssets },
  { pattern: /^\/mempool\/?$/, nav: 'mempool', view: viewMempool },
  { pattern: /^\/nodes\/?$/, nav: 'nodes', view: viewNodes },
  { pattern: /^\/search\/?$/, nav: null, view: viewSearch },
  { pattern: /^\/block\/([^/]+)$/, nav: 'blocks', view: viewBlock },
  { pattern: /^\/tx\/([^/]+)$/, nav: 'transactions', view: viewTransaction },
  { pattern: /^\/address\/([^/]+)$/, nav: null, view: viewAddress },
  { pattern: /^\/asset\/([^/]+)$/, nav: 'assets', view: viewAsset },
];

const AUTO_REFRESH = new Set([viewOverview, viewNodes, viewMempool]);
let refreshTimer = null;
let renderToken = 0;

function navigate(path, replace = false) {
  if (replace) window.history.replaceState({}, '', path);
  else window.history.pushState({}, '', path);
  render();
}

async function render() {
  const token = ++renderToken;
  const view = $('#view');
  const path = window.location.pathname;
  const params = new URLSearchParams(window.location.search);
  const match = ROUTES.map((route) => ({ route, result: route.pattern.exec(path) })).find((entry) => entry.result);

  document.querySelectorAll('[data-nav]').forEach((link) => {
    if (match && match.route.nav === link.dataset.nav) link.setAttribute('aria-current', 'page');
    else link.removeAttribute('aria-current');
  });
  $('#primary-nav').classList.remove('open');
  $('#nav-toggle').setAttribute('aria-expanded', 'false');

  if (!match) {
    view.innerHTML = `<div class="page-head"><div><h1>Page not found</h1></div></div>
      ${stateBlock('Unknown page', 'That address is not part of the explorer.')}`;
    view.setAttribute('aria-busy', 'false');
    return;
  }

  view.setAttribute('aria-busy', 'true');
  if (!view.dataset.path || view.dataset.path !== path + window.location.search) {
    view.innerHTML = '<p class="state state-loading">Loading…</p>';
  }
  view.dataset.path = path + window.location.search;

  try {
    const args = match.result.slice(1).map(decodeURIComponent);
    await match.route.view(view, params, ...args);
  } catch (error) {
    if (token !== renderToken) return;
    const notFound = error.status === 404;
    view.innerHTML = `<div class="page-head"><div><h1>${notFound ? 'Not found' : 'Something went wrong'}</h1></div></div>
      ${stateBlock(notFound ? 'No indexed record matches' : 'Request failed',
        `${error.message}${cachedStatus && !cachedStatus.indexer.synchronized
          ? ' The indexer is still catching up, so recent or early records may not be indexed yet.' : ''}`,
        'state-error')}`;
  } finally {
    if (token === renderToken) view.setAttribute('aria-busy', 'false');
  }

  clearInterval(refreshTimer);
  if (AUTO_REFRESH.has(match.route.view)) {
    refreshTimer = setInterval(() => {
      if (document.visibilityState === 'visible') render();
    }, 10000);
  }
}

/* --------------------------------------------------------------- bindings */

document.addEventListener('click', (event) => {
  const copy = event.target.closest('[data-copy]');
  if (copy) {
    const value = copy.dataset.copy;
    if (navigator.clipboard && navigator.clipboard.writeText) {
      navigator.clipboard.writeText(value).then(() => toast('Copied to clipboard'), () => toast('Copy was blocked'));
    } else {
      toast('Clipboard is unavailable in this browser');
    }
    event.preventDefault();
    return;
  }
  const link = event.target.closest('a[data-link]');
  if (link && link.origin === window.location.origin && !event.metaKey && !event.ctrlKey && event.button === 0) {
    event.preventDefault();
    navigate(link.getAttribute('href'));
  }
});

document.addEventListener('toggle', (event) => {
  const pre = event.target.matches('details.raw-json') ? event.target.querySelector('pre[data-raw]') : null;
  if (!pre || !event.target.open || pre.dataset.loaded === '1') return;
  pre.dataset.loaded = '1';
  pre.textContent = 'Loading…';
  fetch(pre.dataset.raw, { cache: 'no-store' })
    .then((response) => (response.ok ? response.json() : Promise.reject(new Error(`HTTP ${response.status}`))))
    .then((body) => { pre.textContent = JSON.stringify(body, null, 2); })
    .catch((error) => { pre.textContent = `Could not load the raw JSON: ${error.message}`; pre.dataset.loaded = '0'; });
}, true);

$('#search-form').addEventListener('submit', (event) => {
  event.preventDefault();
  const query = $('#search-input').value.trim();
  if (query) navigate(`/search?q=${encodeURIComponent(query)}`);
});

$('#nav-toggle').addEventListener('click', () => {
  const nav = $('#primary-nav');
  const open = nav.classList.toggle('open');
  $('#nav-toggle').setAttribute('aria-expanded', String(open));
});

window.addEventListener('popstate', render);

(async function start() {
  try {
    const snapshot = await api('/api/v1/network');
    if (snapshot.network.networkName) {
      $('#brand-name').textContent = snapshot.network.networkName;
      document.title = `${snapshot.network.networkName} explorer`;
    }
  } catch (_) {
    // The shell still renders; the view below reports the failure.
  }
  await refreshStatus();
  await render();
  setInterval(refreshStatus, 5000);
}());
