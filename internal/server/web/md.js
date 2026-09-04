/* A small, safe Markdown renderer.
 *
 * It builds DOM nodes and sets textContent, never innerHTML. That is not a
 * stylistic preference: the text being rendered comes from a model and from
 * tool output, so anything that funnelled it through innerHTML would be an
 * injection waiting to happen. Building nodes makes that impossible by
 * construction, and costs less code than pulling in a parser plus a sanitiser.
 *
 * It covers what Claude actually emits: headings, paragraphs, fenced and inline
 * code, bold, italic, links, bullet and numbered lists, blockquotes, rules and
 * simple tables. Anything unrecognised falls through as plain text, which is the
 * right failure: unstyled but readable, never broken or dangerous.
 */
'use strict';

const MD = (() => {

  // ---- inline ----

  // Inline rules in priority order. Code comes first so that backticked text is
  // never re-scanned for emphasis — `**not bold**` must stay literal.
  const INLINE = [
    { re: /`([^`]+)`/,                       tag: 'code' },
    { re: /\*\*([^*]+)\*\*/,                 tag: 'strong' },
    { re: /__([^_]+)__/,                     tag: 'strong' },
    { re: /(?<![\w*])\*([^*\n]+)\*(?![\w*])/, tag: 'em' },
    { re: /(?<![\w_])_([^_\n]+)_(?![\w_])/,   tag: 'em' },
    { re: /~~([^~]+)~~/,                     tag: 's' },
  ];

  const LINK = /\[([^\]]+)\]\(([^)\s]+)\)/;
  const BARE = /\bhttps?:\/\/[^\s<>()]+/;

  function inline(text, into) {
    if (!text) return;

    // Links first, so their label is then processed for emphasis but their URL
    // is not.
    let m = LINK.exec(text);
    if (m) {
      inline(text.slice(0, m.index), into);
      into.append(safeLink(m[2], m[1]));
      inline(text.slice(m.index + m[0].length), into);
      return;
    }

    let best = null;
    for (const rule of INLINE) {
      const hit = rule.re.exec(text);
      if (hit && (!best || hit.index < best.hit.index)) best = { rule, hit };
    }
    if (best) {
      inline(text.slice(0, best.hit.index), into);
      const el = document.createElement(best.rule.tag);
      if (best.rule.tag === 'code') el.textContent = best.hit[1];
      else inline(best.hit[1], el);
      into.append(el);
      inline(text.slice(best.hit.index + best.hit[0].length), into);
      return;
    }

    const bare = BARE.exec(text);
    if (bare) {
      into.append(document.createTextNode(text.slice(0, bare.index)));
      into.append(safeLink(bare[0], bare[0]));
      inline(text.slice(bare.index + bare[0].length), into);
      return;
    }

    into.append(document.createTextNode(text));
  }

  // safeLink only ever produces http(s) links. A javascript: or data: URL from
  // model output must not become a clickable element.
  function safeLink(href, label) {
    const ok = /^https?:\/\//i.test(href);
    if (!ok) {
      const span = document.createElement('span');
      span.textContent = label;
      return span;
    }
    const a = document.createElement('a');
    a.href = href;
    a.textContent = label;
    a.target = '_blank';
    a.rel = 'noopener noreferrer';
    return a;
  }

  // ---- blocks ----

  function render(src) {
    const root = document.createElement('div');
    root.className = 'md';
    const lines = String(src || '').replace(/\r\n?/g, '\n').split('\n');
    let i = 0;

    const flushPara = buf => {
      if (!buf.length) return;
      const p = document.createElement('p');
      inline(buf.join('\n'), p);
      root.append(p);
      buf.length = 0;
    };

    const para = [];
    while (i < lines.length) {
      const line = lines[i];

      // fenced code
      const fence = /^\s*```+\s*([\w+-]*)\s*$/.exec(line);
      if (fence) {
        flushPara(para);
        const lang = fence[1] || '';
        const body = [];
        i++;
        while (i < lines.length && !/^\s*```+\s*$/.test(lines[i])) body.push(lines[i++]);
        i++; // closing fence
        root.append(codeBlock(body.join('\n'), lang));
        continue;
      }

      // heading
      const h = /^(#{1,6})\s+(.*)$/.exec(line);
      if (h) {
        flushPara(para);
        // Headings inside a chat bubble should not be enormous, so h1 renders
        // at the size a section title wants rather than a page title.
        const el = document.createElement('h' + Math.min(6, h[1].length + 2));
        inline(h[2], el);
        root.append(el);
        i++;
        continue;
      }

      // horizontal rule
      if (/^\s*([-*_])\s*\1\s*\1[\s\-*_]*$/.test(line)) {
        flushPara(para);
        root.append(document.createElement('hr'));
        i++;
        continue;
      }

      // blockquote
      if (/^\s*>\s?/.test(line)) {
        flushPara(para);
        const body = [];
        while (i < lines.length && /^\s*>\s?/.test(lines[i])) {
          body.push(lines[i].replace(/^\s*>\s?/, ''));
          i++;
        }
        const q = document.createElement('blockquote');
        q.append(render(body.join('\n')));
        root.append(q);
        continue;
      }

      // table: a header row followed by a separator of dashes
      if (line.includes('|') && i + 1 < lines.length && /^\s*\|?[\s:|-]+\|[\s:|-]*$/.test(lines[i + 1])) {
        flushPara(para);
        const rows = [];
        while (i < lines.length && lines[i].includes('|')) rows.push(lines[i++]);
        root.append(table(rows));
        continue;
      }

      // lists
      const li = /^(\s*)([-*+]|\d+[.)])\s+(.*)$/.exec(line);
      if (li) {
        flushPara(para);
        const ordered = /\d/.test(li[2]);
        const list = document.createElement(ordered ? 'ol' : 'ul');
        while (i < lines.length) {
          const m2 = /^(\s*)([-*+]|\d+[.)])\s+(.*)$/.exec(lines[i]);
          if (!m2) {
            // A blank line inside a list does not end it; a non-list line does.
            if (lines[i].trim() === '' && i + 1 < lines.length &&
                /^(\s*)([-*+]|\d+[.)])\s+/.test(lines[i + 1])) { i++; continue; }
            break;
          }
          const item = document.createElement('li');
          const parts = [m2[3]];
          i++;
          // Continuation lines indented under the bullet belong to it.
          while (i < lines.length && /^\s{2,}\S/.test(lines[i]) &&
                 !/^(\s*)([-*+]|\d+[.)])\s+/.test(lines[i])) {
            parts.push(lines[i].trim());
            i++;
          }
          inline(parts.join(' '), item);
          list.append(item);
        }
        root.append(list);
        continue;
      }

      if (line.trim() === '') { flushPara(para); i++; continue; }

      para.push(line);
      i++;
    }
    flushPara(para);
    return root;
  }

  function codeBlock(code, lang) {
    const wrap = document.createElement('div');
    wrap.className = 'codeblock';

    const head = document.createElement('div');
    head.className = 'codeblock-head';
    const label = document.createElement('span');
    label.textContent = lang || 'text';
    const copy = document.createElement('button');
    copy.className = 'btn ghost sm';
    copy.textContent = 'copy';
    copy.addEventListener('click', () => {
      navigator.clipboard.writeText(code);
      copy.textContent = 'copied';
      setTimeout(() => { copy.textContent = 'copy'; }, 1200);
    });
    head.append(label, copy);

    const pre = document.createElement('pre');
    const c = document.createElement('code');
    c.textContent = code;
    pre.append(c);

    wrap.append(head, pre);
    return wrap;
  }

  function table(rows) {
    const cells = r => r.replace(/^\s*\|/, '').replace(/\|\s*$/, '').split('|').map(s => s.trim());
    const wrap = document.createElement('div');
    wrap.className = 'tablewrap';
    const t = document.createElement('table');
    t.className = 'md-table';

    const head = document.createElement('tr');
    for (const h of cells(rows[0])) {
      const th = document.createElement('th');
      inline(h, th);
      head.append(th);
    }
    t.append(head);

    for (const r of rows.slice(2)) {
      const tr = document.createElement('tr');
      for (const c of cells(r)) {
        const td = document.createElement('td');
        inline(c, td);
        tr.append(td);
      }
      t.append(tr);
    }
    wrap.append(t);
    return wrap;
  }

  return { render };
})();
